// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package skills

import (
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
	"github.com/tetratelabs/wazero/experimental/sysfs"
	wazerosys "github.com/tetratelabs/wazero/sys"
	"github.com/vraxter/vraxter/pkg/types"
)

// quotaFS wraps a Wazero experimental sys.FS mount to enforce skill-manifest/1
// filesystem policies at the WASI syscall layer:
//   - read-only vs read-write access
//   - path confinement (blocks ".." escapes, symlinks, and hard links)
//   - allow_extensions allowlist
//   - max_file_mb individual file byte ceiling
//   - max_total_mb cumulative mount byte ceiling
//   - max_files regular file count ceiling
//   - allow_delete enforcement across Unlink, Rmdir, destructive Rename, O_TRUNC, and Truncate
type quotaFS struct {
	base          experimentalsys.FS
	hostDir       string
	guestPath     string
	readOnly      bool
	allowDelete   bool
	allowedExts   map[string]struct{}
	maxFileBytes  int64
	maxTotalBytes int64
	maxFiles      int

	mu        sync.Mutex
	violation string
}

func mbToBytes(mb float64) int64 {
	if mb <= 0 {
		return 0
	}
	b := int64(math.Floor(mb * 1024 * 1024))
	if b < 1 {
		return 1
	}
	return b
}

func newQuotaFS(hostDir string, mount types.MountSpec) *quotaFS {
	readOnly := strings.EqualFold(strings.TrimSpace(mount.Mode), "ro")
	var base experimentalsys.FS = sysfs.DirFS(hostDir)
	if readOnly {
		base = &sysfs.ReadFS{FS: base}
	}

	var exts map[string]struct{}
	if len(mount.AllowExtensions) > 0 {
		exts = make(map[string]struct{}, len(mount.AllowExtensions))
		for _, ext := range mount.AllowExtensions {
			e := strings.ToLower(strings.TrimSpace(ext))
			if e == "" {
				continue
			}
			if !strings.HasPrefix(e, ".") {
				e = "." + e
			}
			exts[e] = struct{}{}
		}
	}

	return &quotaFS{
		base:          base,
		hostDir:       filepath.Clean(hostDir),
		guestPath:     path.Clean(mount.Guest),
		readOnly:      readOnly,
		allowDelete:   mount.AllowDelete,
		allowedExts:   exts,
		maxFileBytes:  mbToBytes(mount.MaxFileMB),
		maxTotalBytes: mbToBytes(mount.MaxTotalMB),
		maxFiles:      mount.MaxFiles,
	}
}

func (q *quotaFS) recordViolationLocked(reason string) {
	if q.violation == "" {
		q.violation = reason
	}
}

func (q *quotaFS) Violation() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.violation
}

func (q *quotaFS) validateRelPath(p string) experimentalsys.Errno {
	if p == "" || p == "." {
		return 0
	}
	if strings.ContainsRune(p, '\x00') || strings.ContainsRune(p, '\\') {
		q.recordViolationLocked(fmt.Sprintf("forbidden path characters in %q", p))
		return experimentalsys.EPERM
	}
	if strings.HasPrefix(p, "/") {
		q.recordViolationLocked(fmt.Sprintf("absolute path %q not allowed inside mount", p))
		return experimentalsys.EPERM
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			q.recordViolationLocked(fmt.Sprintf("path traversal ('..') blocked in %q", p))
			return experimentalsys.EPERM
		}
	}
	return 0
}

func (q *quotaFS) isExtensionAllowed(p string) bool {
	if len(q.allowedExts) == 0 {
		return true
	}
	ext := strings.ToLower(path.Ext(strings.TrimSpace(p)))
	_, ok := q.allowedExts[ext]
	return ok
}

func isWriteOflag(flag experimentalsys.Oflag) bool {
	// Low 2 bits encode mutually exclusive O_RDONLY (0), O_RDWR (1), O_WRONLY (2).
	accessMode := flag & 3
	if accessMode == experimentalsys.O_WRONLY || accessMode == experimentalsys.O_RDWR {
		return true
	}
	return (flag & (experimentalsys.O_CREAT | experimentalsys.O_TRUNC | experimentalsys.O_APPEND)) != 0
}

func (q *quotaFS) scanHostDirLocked() (fileCount int, totalBytes int64) {
	_ = filepath.WalkDir(q.hostDir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Type().IsRegular() {
			fileCount++
			if info, infoErr := d.Info(); infoErr == nil {
				totalBytes += info.Size()
			}
		}
		return nil
	})
	return fileCount, totalBytes
}

func (q *quotaFS) OpenFile(p string, flag experimentalsys.Oflag, perm fs.FileMode) (experimentalsys.File, experimentalsys.Errno) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if errno := q.validateRelPath(p); errno != 0 {
		return nil, errno
	}

	writeRequested := isWriteOflag(flag)
	if q.readOnly && writeRequested {
		q.recordViolationLocked(fmt.Sprintf("write open blocked on read-only mount for %q", p))
		return nil, experimentalsys.EROFS
	}

	isRoot := p == "" || p == "."
	st, statErr := q.base.Stat(p)
	exists := statErr == 0
	isDir := isRoot || (flag&experimentalsys.O_DIRECTORY) != 0 || (exists && st.Mode.IsDir())

	if !isDir {
		if !q.isExtensionAllowed(p) {
			ext := strings.ToLower(path.Ext(p))
			q.recordViolationLocked(fmt.Sprintf("file extension %q for %q is not in allow_extensions", ext, p))
			return nil, experimentalsys.EACCES
		}

		// Block destructive O_TRUNC on an existing non-empty file when allow_delete is false.
		if (flag&experimentalsys.O_TRUNC) != 0 && exists && st.Size > 0 && !q.allowDelete {
			q.recordViolationLocked(fmt.Sprintf("destructive truncate on %q blocked (allow_delete is false)", p))
			return nil, experimentalsys.EPERM
		}

		// Enforce max_files quota before creating a new regular file.
		if (flag&experimentalsys.O_CREAT) != 0 && !exists && q.maxFiles > 0 {
			count, _ := q.scanHostDirLocked()
			if count >= q.maxFiles {
				q.recordViolationLocked(fmt.Sprintf("max_files quota (%d) exceeded creating %q", q.maxFiles, p))
				return nil, experimentalsys.EPERM
			}
		}
	}

	f, errno := q.base.OpenFile(p, flag, perm)
	if errno != 0 {
		return nil, errno
	}

	// Ensure newly created files on host keep restrictive 0600 permissions where supported.
	if !isDir && (flag&experimentalsys.O_CREAT) != 0 && !exists {
		_ = os.Chmod(filepath.Join(q.hostDir, filepath.FromSlash(p)), 0600)
	}

	return &quotaFile{
		File:     f,
		qfs:      q,
		relPath:  p,
		readOnly: q.readOnly || !writeRequested,
	}, 0
}

func (q *quotaFS) Lstat(p string) (wazerosys.Stat_t, experimentalsys.Errno) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if errno := q.validateRelPath(p); errno != 0 {
		return wazerosys.Stat_t{}, errno
	}
	return q.base.Lstat(p)
}

func (q *quotaFS) Stat(p string) (wazerosys.Stat_t, experimentalsys.Errno) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if errno := q.validateRelPath(p); errno != 0 {
		return wazerosys.Stat_t{}, errno
	}
	return q.base.Stat(p)
}

func (q *quotaFS) Mkdir(p string, perm fs.FileMode) experimentalsys.Errno {
	q.mu.Lock()
	defer q.mu.Unlock()
	if errno := q.validateRelPath(p); errno != 0 {
		return errno
	}
	if q.readOnly {
		q.recordViolationLocked(fmt.Sprintf("mkdir %q blocked on read-only mount", p))
		return experimentalsys.EROFS
	}
	return q.base.Mkdir(p, perm)
}

func (q *quotaFS) Chmod(p string, perm fs.FileMode) experimentalsys.Errno {
	q.mu.Lock()
	defer q.mu.Unlock()
	if errno := q.validateRelPath(p); errno != 0 {
		return errno
	}
	if q.readOnly {
		q.recordViolationLocked(fmt.Sprintf("chmod %q blocked on read-only mount", p))
		return experimentalsys.EROFS
	}
	return q.base.Chmod(p, perm)
}

func (q *quotaFS) Rename(from, to string) experimentalsys.Errno {
	q.mu.Lock()
	defer q.mu.Unlock()
	if errno := q.validateRelPath(from); errno != 0 {
		return errno
	}
	if errno := q.validateRelPath(to); errno != 0 {
		return errno
	}
	if q.readOnly {
		q.recordViolationLocked(fmt.Sprintf("rename %q -> %q blocked on read-only mount", from, to))
		return experimentalsys.EROFS
	}
	if !q.isExtensionAllowed(to) {
		ext := strings.ToLower(path.Ext(to))
		q.recordViolationLocked(fmt.Sprintf("rename target extension %q for %q is not in allow_extensions", ext, to))
		return experimentalsys.EACCES
	}
	if !q.allowDelete {
		if _, stErr := q.base.Stat(to); stErr == 0 {
			q.recordViolationLocked(fmt.Sprintf("destructive rename overwriting %q blocked (allow_delete is false)", to))
			return experimentalsys.EPERM
		}
	}
	return q.base.Rename(from, to)
}

func (q *quotaFS) Rmdir(p string) experimentalsys.Errno {
	q.mu.Lock()
	defer q.mu.Unlock()
	if errno := q.validateRelPath(p); errno != 0 {
		return errno
	}
	if q.readOnly {
		q.recordViolationLocked(fmt.Sprintf("rmdir %q blocked on read-only mount", p))
		return experimentalsys.EROFS
	}
	if !q.allowDelete {
		q.recordViolationLocked(fmt.Sprintf("rmdir %q blocked (allow_delete is false)", p))
		return experimentalsys.EPERM
	}
	return q.base.Rmdir(p)
}

func (q *quotaFS) Unlink(p string) experimentalsys.Errno {
	q.mu.Lock()
	defer q.mu.Unlock()
	if errno := q.validateRelPath(p); errno != 0 {
		return errno
	}
	if q.readOnly {
		q.recordViolationLocked(fmt.Sprintf("unlink %q blocked on read-only mount", p))
		return experimentalsys.EROFS
	}
	if !q.allowDelete {
		q.recordViolationLocked(fmt.Sprintf("unlink %q blocked (allow_delete is false)", p))
		return experimentalsys.EPERM
	}
	return q.base.Unlink(p)
}

func (q *quotaFS) Link(oldPath, newPath string) experimentalsys.Errno {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recordViolationLocked(fmt.Sprintf("hard link %q -> %q is forbidden inside sandbox mount", oldPath, newPath))
	return experimentalsys.EPERM
}

func (q *quotaFS) Symlink(oldPath, linkName string) experimentalsys.Errno {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recordViolationLocked(fmt.Sprintf("symlink %q -> %q is forbidden inside sandbox mount", oldPath, linkName))
	return experimentalsys.EPERM
}

func (q *quotaFS) Readlink(p string) (string, experimentalsys.Errno) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if errno := q.validateRelPath(p); errno != 0 {
		return "", errno
	}
	return q.base.Readlink(p)
}

func (q *quotaFS) Utimens(p string, atim, mtim int64) experimentalsys.Errno {
	q.mu.Lock()
	defer q.mu.Unlock()
	if errno := q.validateRelPath(p); errno != 0 {
		return errno
	}
	if q.readOnly {
		q.recordViolationLocked(fmt.Sprintf("utimens %q blocked on read-only mount", p))
		return experimentalsys.EROFS
	}
	return q.base.Utimens(p, atim, mtim)
}

// quotaFile wraps an open experimentalsys.File to enforce max_file_mb, max_total_mb,
// and allow_delete on Write, Pwrite, and Truncate syscalls.
type quotaFile struct {
	experimentalsys.File
	qfs      *quotaFS
	relPath  string
	readOnly bool
}

func (qf *quotaFile) checkWriteGrowthLocked(projectedSize, currentSize int64) experimentalsys.Errno {
	if qf.qfs.maxFileBytes > 0 && projectedSize > qf.qfs.maxFileBytes {
		qf.qfs.recordViolationLocked(fmt.Sprintf("max_file_mb quota (%d bytes) exceeded writing %q (projected %d bytes)", qf.qfs.maxFileBytes, qf.relPath, projectedSize))
		return experimentalsys.EPERM
	}
	delta := projectedSize - currentSize
	if delta > 0 && qf.qfs.maxTotalBytes > 0 {
		_, totalBytes := qf.qfs.scanHostDirLocked()
		if totalBytes+delta > qf.qfs.maxTotalBytes {
			qf.qfs.recordViolationLocked(fmt.Sprintf("max_total_mb quota (%d bytes) exceeded writing %q (projected %d bytes)", qf.qfs.maxTotalBytes, qf.relPath, totalBytes+delta))
			return experimentalsys.EPERM
		}
	}
	return 0
}

func (qf *quotaFile) Write(buf []byte) (int, experimentalsys.Errno) {
	qf.qfs.mu.Lock()
	defer qf.qfs.mu.Unlock()

	if qf.readOnly {
		qf.qfs.recordViolationLocked(fmt.Sprintf("write to read-only handle %q blocked", qf.relPath))
		return 0, experimentalsys.EBADF
	}

	st, errno := qf.File.Stat()
	if errno != 0 {
		return 0, errno
	}
	curSize := st.Size
	writeStart := curSize
	if !qf.File.IsAppend() {
		if pos, seekErr := qf.File.Seek(0, io.SeekCurrent); seekErr == 0 {
			writeStart = pos
		}
	}

	projected := curSize
	if end := writeStart + int64(len(buf)); end > projected {
		projected = end
	}
	if errno := qf.checkWriteGrowthLocked(projected, curSize); errno != 0 {
		return 0, errno
	}

	return qf.File.Write(buf)
}

func (qf *quotaFile) Pwrite(buf []byte, off int64) (int, experimentalsys.Errno) {
	qf.qfs.mu.Lock()
	defer qf.qfs.mu.Unlock()

	if qf.readOnly {
		qf.qfs.recordViolationLocked(fmt.Sprintf("pwrite to read-only handle %q blocked", qf.relPath))
		return 0, experimentalsys.EBADF
	}
	if off < 0 {
		return 0, experimentalsys.EINVAL
	}

	st, errno := qf.File.Stat()
	if errno != 0 {
		return 0, errno
	}
	curSize := st.Size
	projected := curSize
	if end := off + int64(len(buf)); end > projected {
		projected = end
	}
	if errno := qf.checkWriteGrowthLocked(projected, curSize); errno != 0 {
		return 0, errno
	}

	return qf.File.Pwrite(buf, off)
}

func (qf *quotaFile) Truncate(size int64) experimentalsys.Errno {
	qf.qfs.mu.Lock()
	defer qf.qfs.mu.Unlock()

	if qf.readOnly {
		qf.qfs.recordViolationLocked(fmt.Sprintf("truncate on read-only handle %q blocked", qf.relPath))
		return experimentalsys.EBADF
	}
	if size < 0 {
		return experimentalsys.EINVAL
	}

	st, errno := qf.File.Stat()
	if errno != 0 {
		return errno
	}
	curSize := st.Size
	if !qf.qfs.allowDelete && size < curSize {
		qf.qfs.recordViolationLocked(fmt.Sprintf("destructive truncate (%d -> %d bytes) on %q blocked (allow_delete is false)", curSize, size, qf.relPath))
		return experimentalsys.EPERM
	}
	if size > curSize {
		if errno := qf.checkWriteGrowthLocked(size, curSize); errno != 0 {
			return errno
		}
	}

	return qf.File.Truncate(size)
}
