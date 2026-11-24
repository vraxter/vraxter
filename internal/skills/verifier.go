// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package skills

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/vraxter/vraxter/pkg/types"
)

// importCapabilityClass classifies an allowed WASM host import into the manifest
// capability required for a module to legally reference it.
type importCapabilityClass int

const (
	// importClassCore covers baseline WASI preview1 runtime and stdio functions emitted
	// unconditionally by standard Go/Rust/Zig toolchains (stdout/stderr, stdin, args,
	// allocator/scheduler). Runtime isolation of env, wall/monotonic clocks, and entropy
	// is enforced separately by Wazero ModuleConfig (empty env, synthetic zero clock/rand
	// unless explicitly enabled by manifest capabilities).
	importClassCore importCapabilityClass = iota + 1

	// importClassFSRead covers WASI path/directory lookup syscalls that require at least
	// one declared filesystem mount (ro or rw) in the skill manifest.
	importClassFSRead

	// importClassFSWrite covers WASI filesystem mutation and deletion syscalls that require
	// at least one writable (rw) filesystem mount in the skill manifest.
	importClassFSWrite

	// importClassNetworkHTTP covers the Vraxter host HTTP bridge (vrax_sandbox.http_get),
	// requiring declared network capability in the skill manifest.
	importClassNetworkHTTP

	// importClassRawSocket covers WASI preview1 socket syscalls, which require both
	// network capability and explicit allow_raw_sockets=true in the V1 manifest.
	importClassRawSocket
)

// wasiAllowedImports is the strict fail-closed allowlist of WASI preview1 host imports
// recognized by the Vraxter ABI. Any import outside this table is unconditionally rejected.
var wasiAllowedImports = map[string]importCapabilityClass{
	// Core process, scheduling, args, and stdio baseline
	"proc_exit":           importClassCore,
	"sched_yield":         importClassCore,
	"poll_oneoff":         importClassCore,
	"args_get":            importClassCore,
	"args_sizes_get":      importClassCore,
	"environ_get":         importClassCore,
	"environ_sizes_get":   importClassCore,
	"clock_time_get":      importClassCore,
	"clock_res_get":       importClassCore,
	"random_get":          importClassCore,
	"fd_read":             importClassCore,
	"fd_write":            importClassCore,
	"fd_close":            importClassCore,
	"fd_seek":             importClassCore,
	"fd_tell":             importClassCore,
	"fd_pread":            importClassCore,
	"fd_filestat_get":     importClassCore,
	"fd_fdstat_get":       importClassCore,
	"fd_fdstat_set_flags": importClassCore,
	"fd_fdstat_set_rights": importClassCore,
	"fd_prestat_get":      importClassCore,
	"fd_prestat_dir_name": importClassCore,
	"fd_advise":           importClassCore,

	// Filesystem read / directory inspection (requires any declared FS mount)
	"path_open":         importClassFSRead,
	"path_filestat_get": importClassFSRead,
	"path_readlink":     importClassFSRead,
	"fd_readdir":        importClassFSRead,

	// Filesystem write / creation / deletion / mutation (requires at least one rw mount)
	"path_create_directory": importClassFSWrite,
	"path_remove_directory": importClassFSWrite,
	"path_unlink_file":      importClassFSWrite,
	"path_rename":           importClassFSWrite,
	"path_link":             importClassFSWrite,
	"path_symlink":          importClassFSWrite,
	"path_filestat_set_times": importClassFSWrite,
	"fd_pwrite":             importClassFSWrite,
	"fd_filestat_set_size":  importClassFSWrite,
	"fd_filestat_set_times": importClassFSWrite,
	"fd_allocate":           importClassFSWrite,
	"fd_sync":               importClassFSWrite,
	"fd_datasync":           importClassFSWrite,
	"fd_renumber":           importClassFSWrite,

	// Raw WASI sockets (requires network enabled AND allow_raw_sockets=true)
	"sock_accept":   importClassRawSocket,
	"sock_recv":     importClassRawSocket,
	"sock_send":     importClassRawSocket,
	"sock_shutdown": importClassRawSocket,
}

// vraxSandboxAllowedImports is the strict fail-closed allowlist for the Vraxter host module.
var vraxSandboxAllowedImports = map[string]importCapabilityClass{
	"http_get": importClassNetworkHTTP,
}

func manifestHasNetwork(manifest types.SkillManifest) bool {
	if manifest.ManifestV1 != nil && manifest.ManifestV1.Capabilities != nil && manifest.ManifestV1.Capabilities.Network != nil {
		mode := strings.ToLower(strings.TrimSpace(manifest.ManifestV1.Capabilities.Network.Mode))
		if mode != "" && mode != "none" {
			return true
		}
	}
	for _, perm := range manifest.Permissions {
		p := strings.ToLower(strings.TrimSpace(perm))
		if p == "network" || strings.HasPrefix(p, "network:") || strings.HasPrefix(p, "net:") {
			return true
		}
	}
	return false
}

func manifestAllowsRawSockets(manifest types.SkillManifest) bool {
	if !manifestHasNetwork(manifest) {
		return false
	}
	if manifest.ManifestV1 != nil && manifest.ManifestV1.Capabilities != nil && manifest.ManifestV1.Capabilities.Network != nil {
		return manifest.ManifestV1.Capabilities.Network.AllowRawSockets
	}
	return false
}

func manifestFSCapabilities(manifest types.SkillManifest) (hasAnyFS bool, hasWriteFS bool) {
	if manifest.ManifestV1 != nil && manifest.ManifestV1.Capabilities != nil && manifest.ManifestV1.Capabilities.FS != nil {
		for _, m := range manifest.ManifestV1.Capabilities.FS.Mounts {
			hasAnyFS = true
			if strings.EqualFold(strings.TrimSpace(m.Mode), "rw") {
				hasWriteFS = true
			}
		}
	}
	for _, perm := range manifest.Permissions {
		low := strings.ToLower(strings.TrimSpace(perm))
		if strings.HasPrefix(low, "fs_read:") || strings.HasPrefix(low, "fs:read:") ||
			strings.HasPrefix(low, "filesystem:read:") || strings.HasPrefix(low, "fs_ro:") ||
			strings.HasPrefix(low, "fs:ro:") {
			hasAnyFS = true
		}
		if strings.HasPrefix(low, "fs_write:") || strings.HasPrefix(low, "fs:write:") ||
			strings.HasPrefix(low, "filesystem:write:") || strings.HasPrefix(low, "fs_rw:") ||
			strings.HasPrefix(low, "fs:rw:") {
			hasAnyFS = true
			hasWriteFS = true
		}
	}
	return hasAnyFS, hasWriteFS
}

// VerifyCompiledModule statically inspects a compiled WebAssembly module's imported
// functions and imported memories against the capabilities declared in manifest.
// It enforces a fail-closed zero-over-privilege policy prior to installation or instantiation.
func VerifyCompiledModule(compiled wazero.CompiledModule, manifest types.SkillManifest) error {
	if compiled == nil {
		return fmt.Errorf("VRAXTER SHIELD: cannot verify nil compiled WASM module")
	}

	// Reject imported/shared linear memories to prevent host memory aliasing.
	if importedMems := compiled.ImportedMemories(); len(importedMems) > 0 {
		return fmt.Errorf("VRAXTER SHIELD: skill '%s' imports external linear memory (%d found), which is forbidden", manifest.ID, len(importedMems))
	}

	hasNetwork := manifestHasNetwork(manifest)
	allowsRawSockets := manifestAllowsRawSockets(manifest)
	hasAnyFS, hasWriteFS := manifestFSCapabilities(manifest)

	for _, fn := range compiled.ImportedFunctions() {
		modName, fnName, isImport := fn.Import()
		if !isImport {
			continue
		}

		var class importCapabilityClass
		var allowed bool

		switch modName {
		case "wasi_snapshot_preview1":
			class, allowed = wasiAllowedImports[fnName]
		case "vrax_sandbox":
			class, allowed = vraxSandboxAllowedImports[fnName]
		default:
			return fmt.Errorf("VRAXTER SHIELD: static verification failed for skill '%s': unauthorized WASM import '%s.%s' (unknown host module)", manifest.ID, modName, fnName)
		}

		if !allowed {
			return fmt.Errorf("VRAXTER SHIELD: static verification failed for skill '%s': unrecognized or disallowed WASM import '%s.%s'", manifest.ID, modName, fnName)
		}

		switch class {
		case importClassCore:
			// Permitted baseline runtime/stdio import.
		case importClassFSRead:
			if !hasAnyFS {
				return fmt.Errorf("VRAXTER SHIELD: static verification failed for skill '%s': WASM imports filesystem function '%s.%s' but manifest declares no capabilities.fs mounts", manifest.ID, modName, fnName)
			}
		case importClassFSWrite:
			if !hasWriteFS {
				return fmt.Errorf("VRAXTER SHIELD: static verification failed for skill '%s': WASM imports filesystem mutation/deletion function '%s.%s' but manifest declares no writable (rw) capabilities.fs mount", manifest.ID, modName, fnName)
			}
		case importClassNetworkHTTP:
			if !hasNetwork {
				return fmt.Errorf("VRAXTER SHIELD: static verification failed for skill '%s': WASM imports network function '%s.%s' but manifest does not enable capabilities.network", manifest.ID, modName, fnName)
			}
		case importClassRawSocket:
			if !allowsRawSockets {
				return fmt.Errorf("VRAXTER SHIELD: static verification failed for skill '%s': WASM imports raw socket function '%s.%s' without network.allow_raw_sockets enabled", manifest.ID, modName, fnName)
			}
		default:
			return fmt.Errorf("VRAXTER SHIELD: static verification failed for skill '%s': unhandled import class for '%s.%s'", manifest.ID, modName, fnName)
		}
	}

	return nil
}

// VerifyWASMBytes decodes and compiles wasmBytes in an isolated Wazero runtime and runs
// VerifyCompiledModule against manifest without instantiating or executing any guest code.
func VerifyWASMBytes(ctx context.Context, wasmBytes []byte, manifest types.SkillManifest) error {
	if int64(len(wasmBytes)) > MaxWASMBinaryBytes {
		return fmt.Errorf("VRAXTER SHIELD: WASM binary exceeds maximum allowed size (%d bytes)", MaxWASMBinaryBytes)
	}
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true))
	defer rt.Close(ctx)

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		return fmt.Errorf("VRAXTER SHIELD: invalid WebAssembly bytecode for skill '%s': %w", manifest.ID, err)
	}
	defer compiled.Close(ctx)

	return VerifyCompiledModule(compiled, manifest)
}

// VerifyWASMFile reads a .wasm file under the MaxWASMBinaryBytes ceiling and verifies its
// imported functions against the manifest's declared capabilities.
func VerifyWASMFile(ctx context.Context, wasmPath string, manifest types.SkillManifest) error {
	f, err := os.Open(wasmPath)
	if err != nil {
		return fmt.Errorf("VRAXTER SHIELD: failed to open WASM binary '%s': %w", wasmPath, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("VRAXTER SHIELD: failed to stat WASM binary '%s': %w", wasmPath, err)
	}
	if fi.Size() > MaxWASMBinaryBytes {
		return fmt.Errorf("VRAXTER SHIELD: WASM binary '%s' exceeds maximum allowed size (%d > %d bytes)", wasmPath, fi.Size(), MaxWASMBinaryBytes)
	}

	wasmBytes, err := io.ReadAll(io.LimitReader(f, MaxWASMBinaryBytes+1))
	if err != nil {
		return fmt.Errorf("VRAXTER SHIELD: failed to read WASM binary '%s': %w", wasmPath, err)
	}
	if int64(len(wasmBytes)) > MaxWASMBinaryBytes {
		return fmt.Errorf("VRAXTER SHIELD: WASM binary '%s' exceeds maximum allowed size (%d bytes)", wasmPath, MaxWASMBinaryBytes)
	}

	return VerifyWASMBytes(ctx, wasmBytes, manifest)
}
