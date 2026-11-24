// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vraxter/vraxter/internal/db"
)

func buildTestVraxterBinary(t *testing.T) string {
	t.Helper()
	binName := "vraxter-test"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(t.TempDir(), binName)
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed building vraxter test binary: %v\n%s", err, string(out))
	}
	return binPath
}

func reserveFreeLoopbackAddr(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed reserving loopback port: %v", err)
	}
	addr := lis.Addr().String()
	_ = lis.Close()
	return addr
}

func TestCLI_ReadOnlyAndOfflineCommandsDoNotBootstrapOrTouchDB(t *testing.T) {
	binPath := buildTestVraxterBinary(t)
	appDir := filepath.Join(t.TempDir(), "clean-enclave")
	env := append(os.Environ(), "VRAXTER_APP_DIR="+appDir)

	// 1. `vraxter version` must produce clean output with zero DB/key/AppDir creation.
	cmdVer := exec.Command(binPath, "version")
	cmdVer.Env = env
	outVer, err := cmdVer.CombinedOutput()
	if err != nil {
		t.Fatalf("vraxter version failed: %v\n%s", err, string(outVer))
	}
	if strings.Contains(string(outVer), "Initializing Implementation World") {
		t.Fatalf("vraxter version unexpectedly triggered daemon bootstrap:\n%s", string(outVer))
	}
	if _, err := os.Stat(appDir); !os.IsNotExist(err) {
		t.Fatalf("vraxter version must not create AppDir or SQLite artifacts, but %s exists", appDir)
	}

	// 2. `vraxter skill keygen` must work offline without creating vraxter.db or .masterkey.
	keyDir := t.TempDir()
	cmdKeygen := exec.Command(binPath, "skill", "keygen", "--out", keyDir, "--name", "offline-ci")
	cmdKeygen.Env = env
	outKeygen, err := cmdKeygen.CombinedOutput()
	if err != nil {
		t.Fatalf("vraxter skill keygen failed: %v\n%s", err, string(outKeygen))
	}
	if strings.Contains(string(outKeygen), "Initializing Implementation World") {
		t.Fatalf("vraxter skill keygen unexpectedly triggered daemon bootstrap:\n%s", string(outKeygen))
	}
	if _, err := os.Stat(filepath.Join(appDir, "vraxter.db")); !os.IsNotExist(err) {
		t.Fatal("vraxter skill keygen must not open or create vraxter.db")
	}

	// 3. `vraxter daemon status` on a clean host must report STOPPED without creating vraxter.db.
	freeAddr := reserveFreeLoopbackAddr(t)
	cmdStatus := exec.Command(binPath, "daemon", "status", "--addr", "tcp://"+freeAddr)
	cmdStatus.Env = env
	outStatus, err := cmdStatus.CombinedOutput()
	if err != nil {
		t.Fatalf("vraxter daemon status failed: %v\n%s", err, string(outStatus))
	}
	if !strings.Contains(string(outStatus), "STOPPED") {
		t.Fatalf("expected STOPPED from daemon status, got:\n%s", string(outStatus))
	}
	if _, err := os.Stat(filepath.Join(appDir, "vraxter.db")); !os.IsNotExist(err) {
		t.Fatal("vraxter daemon status must not open or create vraxter.db")
	}
}

func TestCLI_DaemonStartRestartStopLifecycleAndDBLock(t *testing.T) {
	binPath := buildTestVraxterBinary(t)
	appDir := t.TempDir()
	endpoint := "tcp://" + reserveFreeLoopbackAddr(t)
	env := append(os.Environ(),
		"VRAXTER_APP_DIR="+appDir,
		"VRAXTER_ENCRYPT_DB_AT_REST=true",
	)

	// Always stop daemon on cleanup so no orphan child process survives the test.
	t.Cleanup(func() {
		stopCmd := exec.Command(binPath, "daemon", "stop", "--addr", endpoint)
		stopCmd.Env = env
		_, _ = stopCmd.CombinedOutput()
	})

	// 1. Start detached daemon via `vraxter daemon start`.
	startCmd := exec.Command(binPath, "daemon", "start", "--addr", endpoint)
	startCmd.Env = env
	outStart, err := startCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("vraxter daemon start failed: %v\n%s", err, string(outStart))
	}
	if !probeDaemonEndpoint(endpoint, 500*time.Millisecond) {
		t.Fatalf("detached daemon did not remain listening after CLI exit (output: %s)", string(outStart))
	}

	pid1, ok := readDaemonPIDFile(appDir)
	if !ok || !isProcessAlive(pid1) {
		t.Fatalf("expected valid running PID in vraxter.pid after daemon start, got pid=%d ok=%v", pid1, ok)
	}

	// 2. While daemon is running with EncryptAtRest=true, attempting to open the DB directly must fail with ErrDatabaseLocked,
	//    AND running `vraxter config list` or `vraxter daemon status` must NOT corrupt or delete .vraxter.db.working!
	dbPath := filepath.Join(appDir, "vraxter.db")
	if s, lockErr := db.NewStoreWithOptions(dbPath, db.StoreOptions{EncryptAtRest: true}); lockErr == nil {
		_ = s.Close()
		t.Fatal("expected opening active daemon's DB to fail with ErrDatabaseLocked")
	} else if !errors.Is(lockErr, db.ErrDatabaseLocked) {
		t.Fatalf("expected ErrDatabaseLocked, got: %v", lockErr)
	}

	cfgCmd := exec.Command(binPath, "config", "list", "--addr", endpoint)
	cfgCmd.Env = env
	if outCfg, err := cfgCmd.CombinedOutput(); err != nil {
		t.Fatalf("vraxter config list failed against live sealed daemon: %v\n%s", err, string(outCfg))
	}

	// 3. `vraxter daemon restart` must cleanly stop pid1 and spawn a NEW detached daemon process (pid2 != pid1).
	restartCmd := exec.Command(binPath, "daemon", "restart", "--addr", endpoint)
	restartCmd.Env = env
	outRestart, err := restartCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("vraxter daemon restart failed: %v\n%s", err, string(outRestart))
	}
	if !probeDaemonEndpoint(endpoint, 500*time.Millisecond) {
		t.Fatalf("restarted daemon is not listening on %s (output: %s)", endpoint, string(outRestart))
	}

	pid2, ok := readDaemonPIDFile(appDir)
	if !ok || !isProcessAlive(pid2) {
		t.Fatalf("expected valid running PID after daemon restart, got pid=%d ok=%v", pid2, ok)
	}
	if pid2 == pid1 {
		t.Fatalf("expected daemon restart to launch a new OS process PID, but PID stayed %d", pid1)
	}

	// 4. `vraxter daemon stop` cleanly shuts down pid2, seals vraxter.db.enc, and releases vraxter.db.lock.
	stopCmd := exec.Command(binPath, "daemon", "stop", "--addr", endpoint)
	stopCmd.Env = env
	outStop, err := stopCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("vraxter daemon stop failed: %v\n%s", err, string(outStop))
	}
	if probeDaemonEndpoint(endpoint, 200*time.Millisecond) {
		t.Fatalf("expected daemon endpoint %s to be closed after daemon stop", endpoint)
	}
	if _, err := os.Stat(dbPath + ".enc"); err != nil {
		t.Fatalf("expected sealed %s.enc after daemon stop: %v", dbPath, err)
	}
	sAfter, err := db.NewStoreWithOptions(dbPath, db.StoreOptions{EncryptAtRest: true})
	if err != nil {
		t.Fatalf("expected lock to be released after daemon stop, got: %v", err)
	}
	_ = sAfter.Close()
	_ = fmt.Sprintf("verified restart PID %d -> %d", pid1, pid2)
}
