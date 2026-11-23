// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package security

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// buildTestHelperBinary compiles a tiny pure-Go executable into dir for deterministic cross-platform testing.
func buildTestHelperBinary(t *testing.T, dir, name string) (string, string) {
	t.Helper()
	srcDir := filepath.Join(dir, "src_"+name)
	if err := os.MkdirAll(srcDir, 0700); err != nil {
		t.Fatalf("mkdir helper src: %v", err)
	}
	const src = `package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--dump-env":
			for _, kv := range os.Environ() {
				fmt.Println(kv)
			}
			return
		case "--flood":
			chunk := strings.Repeat("A", 64*1024)
			for i := 0; i < 48; i++ { // 3 MB > 2 MB cap
				os.Stdout.WriteString(chunk)
			}
			return
		case "--sleep":
			time.Sleep(5 * time.Second)
			return
		}
	}
	fmt.Printf("OK:%s", strings.Join(os.Args[1:], ","))
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module helper_"+name+"\n\ngo 1.24\n"), 0600); err != nil {
		t.Fatalf("write helper go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(src), 0600); err != nil {
		t.Fatalf("write helper main.go: %v", err)
	}
	binName := name
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(dir, binName)
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = srcDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build test helper binary: %v\n%s", err, string(out))
	}
	hash, err := ComputeFileSHA256(binPath)
	if err != nil {
		t.Fatalf("ComputeFileSHA256 failed: %v", err)
	}
	return binPath, hash
}

func writeHostCommandsManifest(t *testing.T, dir string, manifest HostCommandsManifest) string {
	t.Helper()
	p := filepath.Join(dir, "host_commands.json")
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal host_commands.json: %v", err)
	}
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatalf("write host_commands.json: %v", err)
	}
	return p
}

func TestHostExecGuard_DisabledByDefault(t *testing.T) {
	guard := NewHostExecGuard(HostExecOptions{})
	if guard.IsEnabled() {
		t.Fatal("expected HostExecGuard to be disabled by default")
	}

	_, err := guard.Execute(context.Background(), "ping", []string{"127.0.0.1"}, false, false)
	if !errors.Is(err, ErrHostExecDisabled) {
		t.Fatalf("expected ErrHostExecDisabled, got: %v", err)
	}
}

func TestHostExecGuard_AllowlistAndSHA256Integrity(t *testing.T) {
	dir := t.TempDir()
	binPath, binHash := buildTestHelperBinary(t, dir, "probe")
	boolFalse := false
	manifestPath := writeHostCommandsManifest(t, dir, HostCommandsManifest{
		Commands: map[string]HostCommandSpec{
			"probe": {
				Path:            binPath,
				SHA256:          binHash,
				Tier:            HostCommandTierReadOnly,
				NetworkArgCheck: &boolFalse,
				TimeoutSec:      10,
			},
			"relpath": {
				Path:   "relative/bin",
				SHA256: binHash,
				Tier:   HostCommandTierReadOnly,
			},
		},
	})

	guard := NewHostExecGuard(HostExecOptions{
		Enabled:       true,
		CommandsFile:  manifestPath,
		IsolationMode: "airgap",
	})

	// 1. Binary outside host_commands.json is rejected
	if _, err := guard.Execute(context.Background(), "unlisted-tool", nil, false, false); !errors.Is(err, ErrCommandNotAllowlisted) {
		t.Fatalf("expected ErrCommandNotAllowlisted, got: %v", err)
	}

	// 2. Relative path in host_commands.json is rejected
	if _, err := guard.Execute(context.Background(), "relpath", nil, false, false); !errors.Is(err, ErrInvalidBinaryPath) {
		t.Fatalf("expected ErrInvalidBinaryPath, got: %v", err)
	}

	// 3. Valid allowlisted read_only binary executes cleanly
	res, err := guard.Execute(context.Background(), "probe", []string{"status"}, false, false)
	if err != nil {
		t.Fatalf("expected valid allowlisted binary to succeed, got: %v", err)
	}
	if res.Stdout != "OK:status" {
		t.Fatalf("expected 'OK:status', got %q", res.Stdout)
	}

	// 4. Tamper with binary on disk -> SHA-256 verification MUST reject
	f, err := os.OpenFile(binPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("failed to tamper binary: %v", err)
	}
	_, _ = f.Write([]byte("TAMPERED"))
	_ = f.Close()

	if _, err := guard.Execute(context.Background(), "probe", []string{"status"}, false, false); !errors.Is(err, ErrBinaryHashMismatch) {
		t.Fatalf("expected ErrBinaryHashMismatch after binary tamper, got: %v", err)
	}
}

func TestHostExecGuard_BlocksShellsEvenIfAllowlisted(t *testing.T) {
	dir := t.TempDir()
	// Create dummy files named like shells to prove they are blocked even if present in host_commands.json
	shellNames := []string{"sh", "bash", "cmd.exe", "powershell.exe", "pwsh"}
	cmds := make(map[string]HostCommandSpec)
	for _, s := range shellNames {
		p := filepath.Join(dir, s)
		_ = os.WriteFile(p, []byte("fake-shell"), 0700)
		h, _ := ComputeFileSHA256(p)
		cmds[s] = HostCommandSpec{Path: p, SHA256: h, Tier: HostCommandTierReadOnly}
		cmds["alias_"+s] = HostCommandSpec{Path: p, SHA256: h, Tier: HostCommandTierReadOnly}
	}
	manifestPath := writeHostCommandsManifest(t, dir, HostCommandsManifest{Commands: cmds})

	guard := NewHostExecGuard(HostExecOptions{
		Enabled:       true,
		CommandsFile:  manifestPath,
		IsolationMode: "airgap",
	})

	for _, s := range shellNames {
		if _, err := guard.Prepare(s, []string{"-c", "id"}, false); !errors.Is(err, ErrShellBlocked) {
			t.Errorf("expected shell %q to be blocked with ErrShellBlocked, got: %v", s, err)
		}
		if _, err := guard.Prepare("alias_"+s, []string{"-c", "id"}, false); !errors.Is(err, ErrShellBlocked) {
			t.Errorf("expected aliased shell %q to be blocked with ErrShellBlocked, got: %v", "alias_"+s, err)
		}
	}
}

func TestHostExecGuard_BlocksCommandInjectionMetacharacters(t *testing.T) {
	dir := t.TempDir()
	binPath, binHash := buildTestHelperBinary(t, dir, "ping")
	manifestPath := writeHostCommandsManifest(t, dir, HostCommandsManifest{
		Commands: map[string]HostCommandSpec{
			"ping": {Path: binPath, SHA256: binHash, Tier: HostCommandTierReadOnly},
		},
	})
	guard := NewHostExecGuard(HostExecOptions{
		Enabled:       true,
		CommandsFile:  manifestPath,
		IsolationMode: "airgap",
	})

	payloads := [][]string{
		{"127.0.0.1;id"},
		{"127.0.0.1", ";", "whoami"},
		{"127.0.0.1|cat", "/etc/passwd"},
		{"127.0.0.1", "&&", "whoami"},
		{"`id`"},
		{"$(whoami)"},
		{"${HOME}"},
		{"127.0.0.1", ">", "/tmp/out"},
		{"127.0.0.1\nid"},
	}

	for _, args := range payloads {
		if _, err := guard.Prepare("ping", args, false); !errors.Is(err, ErrInjectionDetected) {
			t.Errorf("expected ErrInjectionDetected for args %v, got: %v", args, err)
		}
	}
}

func TestHostExecGuard_EnforcesNetworkPerimeterInAirgapAndLocalLAN(t *testing.T) {
	dir := t.TempDir()
	binPath, binHash := buildTestHelperBinary(t, dir, "netbin")
	manifestPath := writeHostCommandsManifest(t, dir, HostCommandsManifest{
		Commands: map[string]HostCommandSpec{
			"ping":       {Path: binPath, SHA256: binHash, Tier: HostCommandTierReadOnly},
			"traceroute": {Path: binPath, SHA256: binHash, Tier: HostCommandTierReadOnly},
			"nmap":       {Path: binPath, SHA256: binHash, Tier: HostCommandTierElevated},
		},
	})

	for _, mode := range []string{"airgap", "local_lan"} {
		guard := NewHostExecGuard(HostExecOptions{
			Enabled:       true,
			CommandsFile:  manifestPath,
			IsolationMode: mode,
		})

		blockedCases := []struct {
			cmd  string
			args []string
		}{
			{"ping", []string{"-c", "1", "8.8.8.8"}},
			{"ping", []string{"google.com"}},
			{"traceroute", []string{"api.openai.com"}},
			{"nmap", []string{"-p", "80,443", "1.1.1.1"}},
			{"nmap", []string{"8.8.8.0/24"}},
			{"nmap", []string{"0.0.0.0/0"}},
			{"nmap", []string{"10.0.0.0/4"}}, // wide CIDR spanning public IPs
			{"nmap", []string{"8.8.8.1-10"}},
			{"nmap", []string{"192.168.1.1,8.8.8.8"}},
			{"nmap", []string{"-iL", "targets.txt"}},
			{"ping", []string{"134744072"}},    // integer obfuscated 8.8.8.8
			{"ping", []string{"0x08080808"}},   // hex obfuscated 8.8.8.8
			{"ping", []string{"https://evil.example.com/probe"}},
		}

		for _, tc := range blockedCases {
			if _, err := guard.Prepare(tc.cmd, tc.args, false); !errors.Is(err, ErrNetworkTargetBlocked) {
				t.Errorf("[%s] expected ErrNetworkTargetBlocked for %s %v, got: %v", mode, tc.cmd, tc.args, err)
			}
		}

		allowedCases := []struct {
			cmd  string
			args []string
		}{
			{"ping", []string{"-c", "1", "127.0.0.1"}},
			{"ping", []string{"localhost"}},
			{"ping", []string{"192.168.1.10"}},
			{"nmap", []string{"-p", "80,443", "192.168.1.0/24"}},
		}

		for _, tc := range allowedCases {
			if _, err := guard.Prepare(tc.cmd, tc.args, false); err != nil {
				t.Errorf("[%s] expected local target %s %v to be allowed, got: %v", mode, tc.cmd, tc.args, err)
			}
		}
	}
}

func TestHostExecGuard_TierClassificationAndHITLApproval(t *testing.T) {
	dir := t.TempDir()
	binPath, binHash := buildTestHelperBinary(t, dir, "tool")
	manifestPath := writeHostCommandsManifest(t, dir, HostCommandsManifest{
		Commands: map[string]HostCommandSpec{
			"ping": {Path: binPath, SHA256: binHash, Tier: HostCommandTierReadOnly},
			"nmap": {Path: binPath, SHA256: binHash, Tier: HostCommandTierReadOnly}, // even if mislabeled read_only, nmap MUST stay elevated!
		},
	})

	guard := NewHostExecGuard(HostExecOptions{
		Enabled:       true,
		CommandsFile:  manifestPath,
		IsolationMode: "airgap",
	})

	// 1. Direct human read_only command -> no HITL required
	planReadOnly, err := guard.Prepare("ping", []string{"127.0.0.1"}, false)
	if err != nil {
		t.Fatalf("Prepare ping failed: %v", err)
	}
	if planReadOnly.Tier != HostCommandTierReadOnly || planReadOnly.RequiresApproval {
		t.Fatalf("expected human ping to be read_only with RequiresApproval=false, got tier=%s req=%v", planReadOnly.Tier, planReadOnly.RequiresApproval)
	}

	// 2. Direct human elevated command (nmap) -> HITL mandatory
	planElevated, err := guard.Prepare("nmap", []string{"127.0.0.1"}, false)
	if err != nil {
		t.Fatalf("Prepare nmap failed: %v", err)
	}
	if planElevated.Tier != HostCommandTierElevated || !planElevated.RequiresApproval {
		t.Fatalf("expected nmap to be elevated with RequiresApproval=true, got tier=%s req=%v", planElevated.Tier, planElevated.RequiresApproval)
	}
	if _, err := guard.Execute(context.Background(), "nmap", []string{"127.0.0.1"}, false, false); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired for unapproved elevated nmap, got: %v", err)
	}

	// 3. LLM/specialist-originated read_only command -> HITL ALWAYS mandatory
	planFromLLM, err := guard.Prepare("ping", []string{"127.0.0.1"}, true)
	if err != nil {
		t.Fatalf("Prepare LLM ping failed: %v", err)
	}
	if !planFromLLM.RequiresApproval {
		t.Fatalf("expected LLM-originated read_only command to ALWAYS require HITL approval")
	}
	if _, err := guard.Execute(context.Background(), "ping", []string{"127.0.0.1"}, true, false); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired for unapproved LLM ping, got: %v", err)
	}

	// 4. Explicit HITL approval allows execution
	res, err := guard.Execute(context.Background(), "nmap", []string{"127.0.0.1"}, false, true)
	if err != nil {
		t.Fatalf("expected HITL-approved nmap to succeed, got: %v", err)
	}
	if res.Stdout != "OK:127.0.0.1" {
		t.Fatalf("unexpected output: %q", res.Stdout)
	}
}

func TestHostExecGuard_SanitizedEnvOutputCapAndTimeout(t *testing.T) {
	dir := t.TempDir()
	binPath, binHash := buildTestHelperBinary(t, dir, "probe")
	boolFalse := false
	manifestPath := writeHostCommandsManifest(t, dir, HostCommandsManifest{
		Commands: map[string]HostCommandSpec{
			"probe": {
				Path:            binPath,
				SHA256:          binHash,
				Tier:            HostCommandTierReadOnly,
				NetworkArgCheck: &boolFalse,
				TimeoutSec:      1,
			},
		},
	})

	t.Setenv("VRAXTER_DAEMON_SECRET_LEAK_CHECK", "super-secret-value")

	guard := NewHostExecGuard(HostExecOptions{
		Enabled:        true,
		CommandsFile:   manifestPath,
		DefaultTimeout: 1 * time.Second,
		IsolationMode:  "airgap",
	})

	// 1. Verify daemon environment secrets are NOT leaked to child process
	resEnv, err := guard.Execute(context.Background(), "probe", []string{"--dump-env"}, false, false)
	if err != nil {
		t.Fatalf("dump-env failed: %v", err)
	}
	if strings.Contains(resEnv.Stdout, "VRAXTER_DAEMON_SECRET_LEAK_CHECK") || strings.Contains(resEnv.Stdout, "super-secret-value") {
		t.Fatalf("daemon environment variable leaked into host command child process:\n%s", resEnv.Stdout)
	}

	// 2. Verify 2 MB stdout cap truncates safely
	resFlood, err := guard.Execute(context.Background(), "probe", []string{"--flood"}, false, false)
	if err != nil {
		t.Fatalf("flood test failed: %v", err)
	}
	if !resFlood.Truncated {
		t.Fatal("expected >2MB stdout to be marked truncated")
	}
	if len(resFlood.Stdout) != MaxHostExecOutputBytes {
		t.Fatalf("expected stdout capped at exactly %d bytes, got %d", MaxHostExecOutputBytes, len(resFlood.Stdout))
	}

	// 3. Verify strict timeout terminates long-running command
	_, err = guard.Execute(context.Background(), "probe", []string{"--sleep"}, false, false)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
}
