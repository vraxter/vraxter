// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package utils

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// safeArchiveEntryRelPath validates an archive entry name and returns a clean,
// relative path strictly contained inside destAbs, rejecting ZipSlip/TarSlip attacks.
func safeArchiveEntryRelPath(destAbs, entryName string) (string, error) {
	if strings.ContainsRune(entryName, '\x00') {
		return "", fmt.Errorf("archive entry %q contains null byte", entryName)
	}
	if strings.TrimSpace(entryName) == "" {
		return "", fmt.Errorf("archive entry has empty name")
	}

	// Reject Windows drive letters (e.g. C:\evil or C:/evil) and leading slashes
	if len(entryName) >= 2 && entryName[1] == ':' {
		return "", fmt.Errorf("archive entry %q uses absolute drive path (ZipSlip/TarSlip blocked)", entryName)
	}
	if strings.HasPrefix(entryName, "/") || strings.HasPrefix(entryName, "\\") || filepath.IsAbs(entryName) {
		return "", fmt.Errorf("archive entry %q uses absolute path (ZipSlip/TarSlip blocked)", entryName)
	}

	// Normalize separators and inspect raw segments for traversal
	unixNorm := strings.ReplaceAll(entryName, "\\", "/")
	for _, seg := range strings.Split(unixNorm, "/") {
		if seg == ".." {
			return "", fmt.Errorf("archive entry %q attempts path traversal (ZipSlip/TarSlip blocked)", entryName)
		}
	}

	cleaned := path.Clean(unixNorm)
	if cleaned == "." {
		return ".", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("archive entry %q escapes extraction root (ZipSlip/TarSlip blocked)", entryName)
	}

	// Double-check against destAbs with filepath.Clean + filepath.Rel
	osRel := filepath.FromSlash(cleaned)
	targetAbs := filepath.Clean(filepath.Join(destAbs, osRel))
	rel, err := filepath.Rel(destAbs, targetAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("archive entry %q escapes destination %q (ZipSlip/TarSlip blocked)", entryName, destAbs)
	}

	return cleaned, nil
}

func mkdirAllInRoot(root *os.Root, relDir string) error {
	if relDir == "" || relDir == "." {
		return nil
	}
	parts := strings.Split(path.Clean(strings.ReplaceAll(relDir, "\\", "/")), "/")
	cur := ""
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if cur == "" {
			cur = part
		} else {
			cur = cur + "/" + part
		}
		if err := root.Mkdir(cur, 0755); err != nil && !errors.Is(err, os.ErrExist) {
			if st, statErr := root.Stat(cur); statErr == nil && st.IsDir() {
				continue
			}
			return err
		}
	}
	return nil
}

// ExtractTarGz extracts a .tar.gz file into dest with strict TarSlip and os.Root confinement.
func ExtractTarGz(src, dest string) error {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	destAbs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	destAbs = filepath.Clean(destAbs)

	root, err := os.OpenRoot(destAbs)
	if err != nil {
		return fmt.Errorf("failed to open extraction root: %w", err)
	}
	defer root.Close()

	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		relPath, err := safeArchiveEntryRelPath(destAbs, header.Name)
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := mkdirAllInRoot(root, relPath); err != nil {
				return err
			}
		case tar.TypeReg:
			if relPath == "." {
				return fmt.Errorf("invalid regular file tar entry %q", header.Name)
			}
			parent := path.Dir(relPath)
			if err := mkdirAllInRoot(root, parent); err != nil {
				return err
			}
			perm := os.FileMode(header.Mode).Perm()
			if perm == 0 {
				perm = 0600
			}
			outFile, err := root.OpenFile(relPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return err
			}
			if err := outFile.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			// Reject links escaping extraction root (TarSlip via symlink/hardlink)
			linkTarget := header.Linkname
			if filepath.IsAbs(linkTarget) || strings.HasPrefix(linkTarget, "/") || strings.HasPrefix(linkTarget, "\\") {
				return fmt.Errorf("tar link entry %q points to absolute path %q (TarSlip blocked)", header.Name, linkTarget)
			}
			resolved := path.Clean(path.Join(path.Dir(relPath), strings.ReplaceAll(linkTarget, "\\", "/")))
			if resolved == ".." || strings.HasPrefix(resolved, "../") {
				return fmt.Errorf("tar link entry %q escapes extraction root via %q (TarSlip blocked)", header.Name, linkTarget)
			}
			// Do not materialize symlinks/hardlinks outside regular safe extraction
		}
	}
	return nil
}

// ExtractZip extracts a .zip file into dest with strict ZipSlip and os.Root confinement.
func ExtractZip(src, dest string) error {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	destAbs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	destAbs = filepath.Clean(destAbs)

	root, err := os.OpenRoot(destAbs)
	if err != nil {
		return fmt.Errorf("failed to open extraction root: %w", err)
	}
	defer root.Close()

	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		relPath, err := safeArchiveEntryRelPath(destAbs, f.Name)
		if err != nil {
			return err
		}

		// Block symlink entries inside zip archives
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("zip entry %q is a symlink (ZipSlip blocked)", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := mkdirAllInRoot(root, relPath); err != nil {
				return err
			}
			continue
		}

		if relPath == "." {
			return fmt.Errorf("invalid file zip entry %q", f.Name)
		}

		parent := path.Dir(relPath)
		if err := mkdirAllInRoot(root, parent); err != nil {
			return err
		}

		perm := f.Mode().Perm()
		if perm == 0 {
			perm = 0600
		}
		dstFile, err := root.OpenFile(relPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
		if err != nil {
			return err
		}

		srcFile, err := f.Open()
		if err != nil {
			dstFile.Close()
			return err
		}

		if _, err := io.Copy(dstFile, srcFile); err != nil {
			srcFile.Close()
			dstFile.Close()
			return err
		}

		srcFile.Close()
		if err := dstFile.Close(); err != nil {
			return err
		}
	}
	return nil
}

// ExtractTarXz extracts a .tar.xz file using the system's tar command.
// This requires 'tar' (with xz support) to be installed on the host.
func ExtractTarXz(src, dest string) error {
	cmd := exec.Command("tar", "-xf", src, "-C", dest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tar -xf failed: %v, output: %s", err, string(out))
	}
	return nil
}

// CleanupFolder removes the top-level folder if it's the only child (common in Go/Rust tarballs)
// Example: extracted go1.22/bin/... but we want it in bin/...
func CleanupFolder(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return nil
	}

	innerDir := filepath.Join(dir, entries[0].Name())
	tempDir := dir + "_temp"

	if err := os.Rename(innerDir, tempDir); err != nil {
		return err
	}

	os.RemoveAll(dir)
	return os.Rename(tempDir, dir)
}
