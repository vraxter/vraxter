// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package security

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vraxter/vraxter/internal/utils"
)

const (
	// DefaultHostExecTimeout is the default execution timeout for guarded host commands.
	DefaultHostExecTimeout = 15 * time.Second
	// MaxHostExecOutputBytes caps stdout/stderr per stream at 2 MB to prevent memory exhaustion.
	MaxHostExecOutputBytes = 2 * 1024 * 1024
)

var (
	ErrHostExecDisabled      = errors.New("host command execution ($) is disabled by default; enable explicitly via --enable-host-exec or local settings.json")
	ErrShellBlocked          = errors.New("shell and script interpreter binaries are strictly forbidden")
	ErrInjectionDetected     = errors.New("forbidden shell metacharacters detected in command or arguments")
	ErrCommandNotAllowlisted = errors.New("command is not present in host_commands.json allowlist")
	ErrInvalidBinaryPath     = errors.New("allowlisted binary path must be an existing regular file at an absolute path")
	ErrBinaryHashMismatch    = errors.New("binary SHA-256 integrity verification failed")
	ErrNetworkTargetBlocked  = errors.New("network destination argument blocked by isolation perimeter")
	ErrApprovalRequired      = errors.New("explicit human-in-the-loop (HITL) approval is required")
)

// HostCommandTier classifies allowlisted host binaries by operational risk.
type HostCommandTier string

const (
	HostCommandTierReadOnly HostCommandTier = "read_only"
	HostCommandTierElevated HostCommandTier = "elevated"
)

// HostCommandSpec describes a single allowlisted binary in host_commands.json.
type HostCommandSpec struct {
	Path            string          `json:"path"`
	SHA256          string          `json:"sha256"`
	Tier            HostCommandTier `json:"tier,omitempty"`
	NetworkArgCheck *bool           `json:"network_arg_check,omitempty"`
	TimeoutSec      int             `json:"timeout_sec,omitempty"`
}

// HostCommandsManifest represents ~/.config/vraxter/host_commands.json.
type HostCommandsManifest struct {
	Commands map[string]HostCommandSpec `json:"commands"`
}

// HostExecOptions configures a HostExecGuard instance.
type HostExecOptions struct {
	Enabled        bool
	CommandsFile   string
	DefaultTimeout time.Duration
	MaxOutputBytes int
	IsolationMode  string
	WhitelistedIPs []string
}

// PreparedHostCommand holds a validated, integrity-checked command ready for execution or HITL gating.
type PreparedHostCommand struct {
	Command           string
	ResolvedPath      string
	ExpectedSHA256    string
	Args              []string
	Tier              HostCommandTier
	RequiresApproval  bool
	OriginatedFromLLM bool
	Timeout           time.Duration
}

// HostExecResult holds bounded output captured from a guarded host command execution.
type HostExecResult struct {
	Stdout    string
	Stderr    string
	Truncated bool
	ExitCode  int
}

// CombinedOutput returns stdout and stderr merged cleanly for display.
func (r *HostExecResult) CombinedOutput() string {
	if r == nil {
		return ""
	}
	var out string
	if r.Stdout != "" && r.Stderr != "" {
		out = r.Stdout + "\n" + r.Stderr
	} else if r.Stdout != "" {
		out = r.Stdout
	} else {
		out = r.Stderr
	}
	if r.Truncated {
		out += "\n...[output truncated at 2MB limit]"
	}
	return out
}

// LimitedBuffer is an io.Writer that retains at most limit bytes in memory without erroring on overflow.
type LimitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

// NewLimitedBuffer creates a LimitedBuffer with the specified byte cap.
func NewLimitedBuffer(limit int) *LimitedBuffer {
	if limit <= 0 {
		limit = MaxHostExecOutputBytes
	}
	return &LimitedBuffer{limit: limit}
}

func (b *LimitedBuffer) Write(p []byte) (int, error) {
	total := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buf.Write(p[:remaining])
			b.truncated = true
		} else {
			_, _ = b.buf.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return total, nil
}

func (b *LimitedBuffer) String() string { return b.buf.String() }
func (b *LimitedBuffer) Bytes() []byte  { return b.buf.Bytes() }
func (b *LimitedBuffer) Truncated() bool { return b.truncated }

// HostExecGuard enforces zero-trust execution rules on direct host commands ($).
type HostExecGuard struct {
	mu             sync.RWMutex
	enabled        bool
	commandsFile   string
	defaultTimeout time.Duration
	maxOutputBytes int
	isolationMode  string
	whitelistedIPs []string
}

// NewHostExecGuard constructs a HostExecGuard. If options are empty, it fails closed (disabled + airgap).
func NewHostExecGuard(opts HostExecOptions) *HostExecGuard {
	timeout := opts.DefaultTimeout
	if timeout <= 0 {
		timeout = DefaultHostExecTimeout
	}
	maxOut := opts.MaxOutputBytes
	if maxOut <= 0 {
		maxOut = MaxHostExecOutputBytes
	}
	mode := strings.TrimSpace(opts.IsolationMode)
	if mode == "" {
		mode = "airgap"
	}
	wl := make([]string, len(opts.WhitelistedIPs))
	copy(wl, opts.WhitelistedIPs)

	return &HostExecGuard{
		enabled:        opts.Enabled,
		commandsFile:   opts.CommandsFile,
		defaultTimeout: timeout,
		maxOutputBytes: maxOut,
		isolationMode:  mode,
		whitelistedIPs: wl,
	}
}

// IsEnabled reports whether guarded host execution is enabled.
func (g *HostExecGuard) IsEnabled() bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.enabled
}

// UpdateNetworkPolicy updates the isolation mode and IP whitelist used to validate network command targets.
func (g *HostExecGuard) UpdateNetworkPolicy(isolationMode string, whitelistedIPs []string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if strings.TrimSpace(isolationMode) != "" {
		g.isolationMode = isolationMode
	}
	g.whitelistedIPs = make([]string, len(whitelistedIPs))
	copy(g.whitelistedIPs, whitelistedIPs)
}

var blockedShellBinaries = map[string]bool{
	"sh":          true,
	"bash":        true,
	"zsh":         true,
	"dash":        true,
	"ash":         true,
	"csh":         true,
	"tcsh":        true,
	"ksh":         true,
	"fish":        true,
	"rbash":       true,
	"cmd":         true,
	"command.com": true,
	"powershell":  true,
	"pwsh":        true,
	"wsl":         true,
	"wslhost":     true,
	"wscript":     true,
	"cscript":     true,
	"mshta":       true,
	"rundll32":    true,
}

var knownReadOnlyCommands = map[string]bool{
	"ping":       true,
	"ping6":      true,
	"traceroute": true,
	"traceroute6": true,
	"tracert":    true,
	"tracepath":  true,
	"ss":         true,
	"netstat":    true,
	"uname":      true,
	"uptime":     true,
	"df":         true,
	"free":       true,
	"ps":         true,
	"whoami":     true,
	"hostname":   true,
	"id":         true,
	"date":       true,
	"ls":         true,
	"ip":         true,
}

var alwaysElevatedCommands = map[string]bool{
	"nmap":      true,
	"masscan":   true,
	"tcpdump":   true,
	"tshark":    true,
	"iptables":  true,
	"ip6tables": true,
	"nft":       true,
	"ufw":       true,
	"systemctl": true,
	"service":   true,
	"kill":      true,
	"pkill":     true,
	"killall":   true,
	"mount":     true,
	"umount":    true,
	"chmod":     true,
	"chown":     true,
	"reboot":    true,
	"shutdown":  true,
}

var networkCommands = map[string]bool{
	"ping":        true,
	"ping6":       true,
	"traceroute":  true,
	"traceroute6": true,
	"tracert":     true,
	"tracepath":   true,
	"ss":          true,
	"nmap":        true,
	"curl":        true,
	"wget":        true,
	"dig":         true,
	"nslookup":    true,
	"host":        true,
	"nc":          true,
	"ncat":        true,
	"netcat":      true,
	"socat":       true,
	"telnet":      true,
	"ssh":         true,
	"scp":         true,
	"sftp":        true,
	"ftp":         true,
	"rsync":       true,
	"mtr":         true,
	"arp":         true,
	"arping":      true,
	"fping":       true,
	"hping3":      true,
	"masscan":     true,
	"iperf":       true,
	"iperf3":      true,
	"whois":       true,
}

func normalizedBinaryBase(nameOrPath string) string {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(nameOrPath)))
	base = strings.TrimSuffix(base, ".exe")
	base = strings.TrimSuffix(base, ".bat")
	base = strings.TrimSuffix(base, ".cmd")
	return base
}

// IsBlockedShell reports whether a command name or binary path refers to a forbidden shell/interpreter.
func IsBlockedShell(nameOrPath string) bool {
	rawBase := strings.ToLower(strings.TrimSpace(filepath.Base(nameOrPath)))
	if blockedShellBinaries[rawBase] {
		return true
	}
	return blockedShellBinaries[normalizedBinaryBase(nameOrPath)]
}

// ContainsShellMetacharacters reports whether s contains shell command-chaining or expansion syntax.
func ContainsShellMetacharacters(s string) bool {
	if strings.ContainsAny(s, ";|&`><\n\r\x00") {
		return true
	}
	if strings.Contains(s, "$(") || strings.Contains(s, "${") {
		return true
	}
	return false
}

// ComputeFileSHA256 hashes a regular file on disk and returns its lowercase 64-char hex digest.
func ComputeFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Prepare validates a host command invocation against all Zero-Trust policies without executing it.
func (g *HostExecGuard) Prepare(cmdName string, args []string, originatedFromLLM bool) (*PreparedHostCommand, error) {
	if g == nil {
		return nil, ErrHostExecDisabled
	}

	g.mu.RLock()
	enabled := g.enabled
	commandsFile := g.commandsFile
	defaultTimeout := g.defaultTimeout
	isolationMode := g.isolationMode
	whitelistedIPs := make([]string, len(g.whitelistedIPs))
	copy(whitelistedIPs, g.whitelistedIPs)
	g.mu.RUnlock()

	if !enabled {
		return nil, ErrHostExecDisabled
	}

	cmdName = strings.TrimSpace(cmdName)
	if cmdName == "" {
		return nil, fmt.Errorf("%w: empty command", ErrCommandNotAllowlisted)
	}

	// 1. Reject shell metacharacters across command and every argument
	if ContainsShellMetacharacters(cmdName) {
		return nil, fmt.Errorf("%w: command '%s'", ErrInjectionDetected, cmdName)
	}
	for _, a := range args {
		if ContainsShellMetacharacters(a) {
			return nil, fmt.Errorf("%w: argument %q", ErrInjectionDetected, a)
		}
	}

	// 2. Reject shells immediately on requested command name
	if IsBlockedShell(cmdName) {
		return nil, fmt.Errorf("%w: '%s'", ErrShellBlocked, cmdName)
	}

	// 3. Load allowlist manifest from disk (fail-closed)
	manifest, err := loadHostCommandsManifest(commandsFile)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCommandNotAllowlisted, err)
	}

	spec, ok := manifest.Commands[cmdName]
	if !ok {
		return nil, fmt.Errorf("%w: '%s'", ErrCommandNotAllowlisted, cmdName)
	}

	// 4. Validate binary path and block shells even if aliased in host_commands.json
	binPath := strings.TrimSpace(spec.Path)
	if binPath == "" || !filepath.IsAbs(binPath) {
		return nil, fmt.Errorf("%w: '%s'", ErrInvalidBinaryPath, spec.Path)
	}
	if IsBlockedShell(binPath) {
		return nil, fmt.Errorf("%w: '%s'", ErrShellBlocked, binPath)
	}

	realPath, err := filepath.EvalSymlinks(binPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBinaryPath, err)
	}
	if IsBlockedShell(realPath) {
		return nil, fmt.Errorf("%w: resolved path '%s'", ErrShellBlocked, realPath)
	}

	info, err := os.Stat(realPath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: '%s'", ErrInvalidBinaryPath, realPath)
	}

	// 5. Verify on-disk SHA-256 hash against allowlist
	expectedHex := strings.ToLower(strings.TrimSpace(spec.SHA256))
	if len(expectedHex) != 64 {
		return nil, fmt.Errorf("%w: invalid sha256 entry for '%s'", ErrBinaryHashMismatch, cmdName)
	}
	if _, err := hex.DecodeString(expectedHex); err != nil {
		return nil, fmt.Errorf("%w: non-hex sha256 entry for '%s'", ErrBinaryHashMismatch, cmdName)
	}

	actualHex, err := ComputeFileSHA256(realPath)
	if err != nil {
		return nil, fmt.Errorf("%w: could not hash '%s': %v", ErrBinaryHashMismatch, realPath, err)
	}
	if subtle.ConstantTimeCompare([]byte(actualHex), []byte(expectedHex)) != 1 {
		return nil, fmt.Errorf("%w: expected %s, got %s", ErrBinaryHashMismatch, expectedHex, actualHex)
	}

	// 6. Validate destination IPs/hosts in arguments of network commands against isolation perimeter
	if shouldCheckNetworkArgs(cmdName, realPath, spec) {
		if err := validateNetworkCommandArgs(cmdName, args, isolationMode, whitelistedIPs); err != nil {
			return nil, err
		}
	}

	// 7. Classify tier and determine HITL approval requirement
	tier := classifyCommandTier(cmdName, realPath, spec.Tier)
	requiresApproval := tier == HostCommandTierElevated || originatedFromLLM

	timeout := defaultTimeout
	if spec.TimeoutSec > 0 {
		timeout = time.Duration(spec.TimeoutSec) * time.Second
	}

	clonedArgs := make([]string, len(args))
	copy(clonedArgs, args)

	return &PreparedHostCommand{
		Command:           cmdName,
		ResolvedPath:      realPath,
		ExpectedSHA256:    expectedHex,
		Args:              clonedArgs,
		Tier:              tier,
		RequiresApproval:  requiresApproval,
		OriginatedFromLLM: originatedFromLLM,
		Timeout:           timeout,
	}, nil
}

// ExecutePrepared re-verifies on-disk binary SHA-256 and executes the prepared command with
// minimal sanitized environment, strict timeout, and 2 MB output cap.
func (g *HostExecGuard) ExecutePrepared(ctx context.Context, plan *PreparedHostCommand) (*HostExecResult, error) {
	if g == nil || !g.IsEnabled() {
		return nil, ErrHostExecDisabled
	}
	if plan == nil {
		return nil, errors.New("nil prepared host command")
	}

	// Re-verify hash immediately before exec to minimize TOCTOU window (especially after HITL pause)
	actualHex, err := ComputeFileSHA256(plan.ResolvedPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBinaryHashMismatch, err)
	}
	if subtle.ConstantTimeCompare([]byte(actualHex), []byte(plan.ExpectedSHA256)) != 1 {
		return nil, fmt.Errorf("%w: binary modified before execution (expected %s, got %s)", ErrBinaryHashMismatch, plan.ExpectedSHA256, actualHex)
	}

	g.mu.RLock()
	maxOut := g.maxOutputBytes
	g.mu.RUnlock()

	timeout := plan.Timeout
	if timeout <= 0 {
		timeout = DefaultHostExecTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, plan.ResolvedPath, plan.Args...)
	cmd.Env = minimalHostEnv()

	stdoutBuf := NewLimitedBuffer(maxOut)
	stderrBuf := NewLimitedBuffer(maxOut)
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	runErr := cmd.Run()

	res := &HostExecResult{
		Stdout:    stdoutBuf.String(),
		Stderr:    stderrBuf.String(),
		Truncated: stdoutBuf.Truncated() || stderrBuf.Truncated(),
	}

	if runErr != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return res, fmt.Errorf("host command '%s' timed out after %v", plan.Command, timeout)
		}
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		}
		return res, runErr
	}

	return res, nil
}

// Execute performs validation, enforces HITL approval gating, and executes the host command.
func (g *HostExecGuard) Execute(ctx context.Context, cmdName string, args []string, originatedFromLLM bool, hitlApproved bool) (*HostExecResult, error) {
	plan, err := g.Prepare(cmdName, args, originatedFromLLM)
	if err != nil {
		return nil, err
	}
	if plan.RequiresApproval && !hitlApproved {
		return nil, fmt.Errorf("%w: command '%s' (tier=%s, from_llm=%v)", ErrApprovalRequired, plan.Command, plan.Tier, originatedFromLLM)
	}
	return g.ExecutePrepared(ctx, plan)
}

func loadHostCommandsManifest(path string) (*HostCommandsManifest, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("host_commands.json path is not configured")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	var m HostCommandsManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid JSON in %s: %w", path, err)
	}
	if m.Commands == nil {
		m.Commands = map[string]HostCommandSpec{}
	}
	return &m, nil
}

func classifyCommandTier(cmdName, realPath string, declared HostCommandTier) HostCommandTier {
	nameBase := normalizedBinaryBase(cmdName)
	pathBase := normalizedBinaryBase(realPath)

	if alwaysElevatedCommands[nameBase] || alwaysElevatedCommands[pathBase] {
		return HostCommandTierElevated
	}

	switch HostCommandTier(strings.ToLower(strings.TrimSpace(string(declared)))) {
	case HostCommandTierReadOnly:
		return HostCommandTierReadOnly
	case HostCommandTierElevated:
		return HostCommandTierElevated
	case "":
		if knownReadOnlyCommands[nameBase] || knownReadOnlyCommands[pathBase] {
			return HostCommandTierReadOnly
		}
		return HostCommandTierElevated
	default:
		// Fail-safe: unknown tier values require HITL elevation
		return HostCommandTierElevated
	}
}

func shouldCheckNetworkArgs(cmdName, realPath string, spec HostCommandSpec) bool {
	if spec.NetworkArgCheck != nil {
		return *spec.NetworkArgCheck
	}
	return networkCommands[normalizedBinaryBase(cmdName)] || networkCommands[normalizedBinaryBase(realPath)]
}

func minimalHostEnv() []string {
	if runtime.GOOS == "windows" {
		// Windows system binaries (e.g., ping.exe / Winsock) may require SYSTEMROOT to load DLLs.
		// No daemon keys, tokens, PATH overrides, or user environment variables are passed.
		if sysRoot := os.Getenv("SYSTEMROOT"); sysRoot != "" {
			return []string{"SYSTEMROOT=" + sysRoot}
		}
	}
	return []string{}
}

// flagsTakingNonTargetValue lists flags whose immediately following argument is a numeric count,
// timeout, port list, interface name, or local output filename (not a destination host).
var flagsTakingNonTargetValue = map[string]bool{
	"-c": true, "-n": true, "-i": true, "-w": true, "-W": true,
	"-t": true, "-l": true, "-s": true, "-m": true, "-p": true,
	"-q": true, "-T": true, "-I": true, "-e": true, "-f": true,
	"-h": true, "-z": true,
	"--count": true, "--interval": true, "--timeout": true,
	"--top-ports": true, "--max-retries": true, "--min-rate": true,
	"--max-rate": true, "--host-timeout": true, "--port": true,
	"-oN": true, "-oX": true, "-oG": true, "-oA": true,
}

// flagsDisallowedInIsolatedMode lists flags that could load external targets from a file or proxy.
var flagsDisallowedInIsolatedMode = map[string]bool{
	"-iL": true, "--input-list": true, "-iR": true, "--proxies": true,
	"-x": true, "--proxy": true, "-K": true, "--config": true,
}

func validateNetworkCommandArgs(cmdName string, args []string, isolationMode string, whitelistedIPs []string) error {
	isolated := isolationMode == "airgap" || isolationMode == "strict_local" || isolationMode == "local_lan"
	skipNext := false

	for _, rawArg := range args {
		arg := strings.TrimSpace(rawArg)
		if arg == "" {
			continue
		}

		if skipNext {
			skipNext = false
			// Even for a flag value, if it explicitly looks like a URL, validate it.
			if strings.Contains(arg, "://") {
				if err := validateNetworkTarget(arg, isolationMode, whitelistedIPs); err != nil {
					return err
				}
			}
			continue
		}

		if strings.HasPrefix(arg, "-") {
			// Handle --flag=value syntax
			if eqIdx := strings.Index(arg, "="); eqIdx != -1 {
				flagName := arg[:eqIdx]
				flagVal := arg[eqIdx+1:]
				if isolated && flagsDisallowedInIsolatedMode[flagName] {
					return fmt.Errorf("%w: flag '%s' is forbidden in %s mode", ErrNetworkTargetBlocked, flagName, isolationMode)
				}
				if !flagsTakingNonTargetValue[flagName] && flagVal != "" {
					if looksLikeNetworkTarget(flagVal) {
						if err := validateNetworkTarget(flagVal, isolationMode, whitelistedIPs); err != nil {
							return err
						}
					}
				}
				continue
			}

			if isolated && flagsDisallowedInIsolatedMode[arg] {
				return fmt.Errorf("%w: flag '%s' is forbidden in %s mode", ErrNetworkTargetBlocked, arg, isolationMode)
			}
			if flagsTakingNonTargetValue[arg] {
				skipNext = true
			}
			continue
		}

		// For ss/netstat filter expressions (e.g. "state", "established", "sport", "=", ":80"),
		// skip obvious non-host keywords unless they contain an IP/CIDR/hostname.
		base := normalizedBinaryBase(cmdName)
		if (base == "ss" || base == "netstat") && !looksLikeNetworkTarget(arg) {
			continue
		}

		if err := validateNetworkTarget(arg, isolationMode, whitelistedIPs); err != nil {
			return err
		}
	}

	return nil
}

func looksLikeNetworkTarget(val string) bool {
	if strings.Contains(val, "://") {
		return true
	}
	if net.ParseIP(val) != nil {
		return true
	}
	if _, _, err := net.ParseCIDR(val); err == nil {
		return true
	}
	if strings.Contains(val, ".") || strings.Contains(val, ":") {
		// Filter out pure port specs like ":80"
		if strings.HasPrefix(val, ":") {
			return false
		}
		return true
	}
	return false
}

func validateNetworkTarget(target string, isolationMode string, whitelistedIPs []string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}

	// Support comma-separated target lists (e.g. nmap "192.168.1.1,8.8.8.8")
	if strings.Contains(target, ",") && !strings.Contains(target, "://") {
		for _, part := range strings.Split(target, ",") {
			if err := validateNetworkTarget(part, isolationMode, whitelistedIPs); err != nil {
				return err
			}
		}
		return nil
	}

	// Strip user@ prefix (e.g. ssh user@host)
	if !strings.Contains(target, "://") {
		if atIdx := strings.LastIndex(target, "@"); atIdx != -1 {
			target = target[atIdx+1:]
		}
	}

	// 1. Full URL (http://, https://, etc.)
	if strings.Contains(target, "://") {
		u, err := url.Parse(target)
		if err != nil || u.Hostname() == "" {
			return fmt.Errorf("%w: invalid target URL '%s'", ErrNetworkTargetBlocked, target)
		}
		allowed, err := utils.IsAllowedNetworkWithMode(target, isolationMode, whitelistedIPs)
		if err != nil || !allowed {
			return fmt.Errorf("%w: target '%s' (%v)", ErrNetworkTargetBlocked, target, err)
		}
		return nil
	}

	// 2. CIDR notation (e.g. 192.168.1.0/24 or 8.8.8.0/24)
	if ip, ipNet, err := net.ParseCIDR(target); err == nil {
		ones, bits := ipNet.Mask.Size()
		if ones == 0 {
			return fmt.Errorf("%w: unrestricted CIDR '%s' blocked in %s mode", ErrNetworkTargetBlocked, target, isolationMode)
		}
		// Validate both the first and last IP of the CIDR block so wide ranges (e.g. 10.0.0.0/4) cannot span into WAN.
		firstIP := ip.Mask(ipNet.Mask)
		lastIP := make(net.IP, len(firstIP))
		for i := range firstIP {
			lastIP[i] = firstIP[i] | ^ipNet.Mask[i]
		}
		if bits == 32 || bits == 128 {
			if ok, err := utils.IsAllowedHost(firstIP.String(), isolationMode, whitelistedIPs); !ok || err != nil {
				return fmt.Errorf("%w: CIDR '%s' (%v)", ErrNetworkTargetBlocked, target, err)
			}
			if ok, err := utils.IsAllowedHost(lastIP.String(), isolationMode, whitelistedIPs); !ok || err != nil {
				return fmt.Errorf("%w: CIDR '%s' spans disallowed IP %s (%v)", ErrNetworkTargetBlocked, target, lastIP.String(), err)
			}
		}
		return nil
	}

	// 3. Reject integer / hex / octal IP obfuscation in isolated modes (e.g. 0x08080808, 134744072)
	isolated := isolationMode == "airgap" || isolationMode == "strict_local" || isolationMode == "local_lan"
	if isolated && isNumericIPObfuscation(target) {
		return fmt.Errorf("%w: obfuscated numeric IP target '%s' blocked in %s mode", ErrNetworkTargetBlocked, target, isolationMode)
	}

	// 4. Nmap octet range (e.g. 8.8.8.1-10 or 192.168.1.1-50)
	if strings.Count(target, ".") == 3 && strings.Contains(target, "-") {
		octets := strings.Split(target, ".")
		var firstOctets, lastOctets [4]string
		validRange := true
		for i, oct := range octets {
			if strings.Contains(oct, "-") {
				bounds := strings.SplitN(oct, "-", 2)
				a, errA := strconv.Atoi(bounds[0])
				b, errB := strconv.Atoi(bounds[1])
				if errA != nil || errB != nil || a < 0 || a > 255 || b < 0 || b > 255 {
					validRange = false
					break
				}
				firstOctets[i] = strconv.Itoa(a)
				lastOctets[i] = strconv.Itoa(b)
			} else {
				firstOctets[i] = oct
				lastOctets[i] = oct
			}
		}
		if validRange {
			startIP := strings.Join(firstOctets[:], ".")
			endIP := strings.Join(lastOctets[:], ".")
			if ok, err := utils.IsAllowedHost(startIP, isolationMode, whitelistedIPs); !ok || err != nil {
				return fmt.Errorf("%w: range '%s' (%v)", ErrNetworkTargetBlocked, target, err)
			}
			if ok, err := utils.IsAllowedHost(endIP, isolationMode, whitelistedIPs); !ok || err != nil {
				return fmt.Errorf("%w: range '%s' (%v)", ErrNetworkTargetBlocked, target, err)
			}
			return nil
		}
	}

	// 5. Standard IP literal, host:port, or hostname/FQDN
	allowed, err := utils.IsAllowedNetworkWithMode(target, isolationMode, whitelistedIPs)
	if err != nil || !allowed {
		return fmt.Errorf("%w: target '%s' (%v)", ErrNetworkTargetBlocked, target, err)
	}
	return nil
}

func isNumericIPObfuscation(s string) bool {
	if strings.HasPrefix(strings.ToLower(s), "0x") {
		_, err := strconv.ParseUint(s[2:], 16, 64)
		return err == nil
	}
	// Pure integer (e.g., 134744072 = 8.8.8.8)
	if _, err := strconv.ParseUint(s, 10, 64); err == nil {
		return true
	}
	// Octal dotted IP (e.g. 010.010.010.010)
	if strings.Count(s, ".") == 3 {
		for _, part := range strings.Split(s, ".") {
			if len(part) > 1 && strings.HasPrefix(part, "0") {
				return true
			}
		}
	}
	return false
}
