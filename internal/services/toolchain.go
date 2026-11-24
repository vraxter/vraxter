// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package services

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/vraxter/vraxter/internal/utils"
)

// defaultSDKChecksums maps verified official SDK archive filenames to their upstream SHA-256 digests.
// Unverified archives (e.g., TinyGo, Rust, WASI SDK, or non-x86_64 Zig builds) are intentionally omitted
// so SetupSDK fails closed unless an operator explicitly pins verified SHA-256 digests via
// <sdkDir>/checksums.json or SetSDKChecksum.
var defaultSDKChecksums = map[string]string{
	// Go 1.22.5 — verified against official go.dev release checksums
	"go1.22.5.linux-amd64.tar.gz":  "904b924d435eaea086515bc63235b192ea441bd8c9b198c507e85009e6e4c7f0",
	"go1.22.5.linux-arm64.tar.gz":  "8d21325bfcf431be3660527c1a39d3d9ad71535fabdf5041c826e44e31642b5a",
	"go1.22.5.darwin-amd64.tar.gz": "95d9933cdcf45f211243c42c7705c37353cccd99f27eb4d8e2d1bf2f4165cb50",
	"go1.22.5.darwin-arm64.tar.gz": "4cd1bcb05be03cecb77bccd765785d5ff69d79adf4dd49790471d00c06b41133",
	"go1.22.5.windows-amd64.zip":   "59968438b8d90f108fd240d4d2f95b037e59716995f7409e0a322dcb996e9f42",
	"go1.22.5.windows-arm64.zip":   "6717d5841162aa8c05f932eb74a643f1310b8a88f80f0830e86d194289734bbf",

	// Zig 0.13.0 — verified upstream x86_64 release checksums
	"zig-linux-x86_64-0.13.0.tar.xz": "d45312e61ebcc48032b77bc4cf7fd6915c11fa16e4aad116b66c9468211230ea",
	"zig-windows-x86_64-0.13.0.zip":  "d859994725ef9402381e557c60bb57497215682e355204d754ee3df75ee3c158",
}

type ToolchainManager struct {
	sdkDir   string
	isAirgap bool

	mu           sync.RWMutex
	isSettingUp  map[string]bool
	checksums    map[string]string
	urlOverrides map[string]string
}

func NewToolchainManager(sdkDir string, isAirgap ...bool) *ToolchainManager {
	airgap := false
	if len(isAirgap) > 0 {
		airgap = isAirgap[0]
	}
	checksums := make(map[string]string, len(defaultSDKChecksums))
	for k, v := range defaultSDKChecksums {
		checksums[k] = v
	}
	m := &ToolchainManager{
		sdkDir:       sdkDir,
		isAirgap:     airgap,
		isSettingUp:  make(map[string]bool),
		checksums:    checksums,
		urlOverrides: make(map[string]string),
	}

	return m
}

func (m *ToolchainManager) SetAirgap(airgap bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.isAirgap = airgap
}

// IsAirgap returns whether SDK downloads are disabled by airgap policy (thread-safe).
func (m *ToolchainManager) IsAirgap() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.isAirgap
}

// SetSDKChecksum registers or overrides an expected SHA-256 checksum for an SDK key
// (archive filename, full download URL, or language identifier).
func (m *ToolchainManager) SetSDKChecksum(key, sha256Hex string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.checksums == nil {
		m.checksums = make(map[string]string)
	}
	m.checksums[strings.TrimSpace(key)] = strings.ToLower(strings.TrimSpace(sha256Hex))
}

// ClearSDKChecksums removes all registered in-memory SDK checksums (used to test fail-closed behavior).
func (m *ToolchainManager) ClearSDKChecksums() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checksums = make(map[string]string)
}

// SetSDKURLOverride overrides the download URL for a specific SDK language (e.g., local mirror or test server).
func (m *ToolchainManager) SetSDKURLOverride(lang, rawURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.urlOverrides == nil {
		m.urlOverrides = make(map[string]string)
	}
	m.urlOverrides[strings.ToLower(strings.TrimSpace(lang))] = strings.TrimSpace(rawURL)
}

// ResolveSDKChecksum resolves and validates the expected SHA-256 checksum for an SDK download.
// If no valid SHA-256 checksum is found, it fails closed with an explicit error.
func (m *ToolchainManager) ResolveSDKChecksum(lang, rawURL string) (string, error) {
	archiveBase := archiveFilenameFromURL(rawURL)

	m.mu.RLock()
	candidates := []string{
		m.checksums[rawURL],
		m.checksums[archiveBase],
		m.checksums[lang],
	}
	sdkDir := m.sdkDir
	m.mu.RUnlock()

	// Also allow local <sdkDir>/checksums.json if present on disk.
	if sdkDir != "" {
		checksumsPath := filepath.Join(sdkDir, "checksums.json")
		if raw, err := os.ReadFile(checksumsPath); err == nil {
			var fileMap map[string]string
			if err := json.Unmarshal(raw, &fileMap); err == nil {
				candidates = append(candidates,
					fileMap[rawURL],
					fileMap[archiveBase],
					fileMap[lang],
				)
			}
		}
	}

	for _, c := range candidates {
		if strings.TrimSpace(c) == "" {
			continue
		}
		normalized, err := utils.NormalizeAndValidateSHA256(c)
		if err != nil {
			return "", fmt.Errorf("VRAXTER SHIELD: invalid SHA-256 checksum configured for SDK %s (%s): %w", lang, archiveBase, err)
		}
		return normalized, nil
	}

	return "", fmt.Errorf("VRAXTER SHIELD: missing official SHA-256 checksum for SDK %s (%s); refusing unverified download", lang, archiveBase)
}

func archiveFilenameFromURL(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Path != "" {
		base := path.Base(u.Path)
		if base != "." && base != "/" {
			return base
		}
	}
	return filepath.Base(rawURL)
}

func (m *ToolchainManager) ProactiveSetup() {
	if m.IsAirgap() {
		return // Do not attempt background downloads in airgap mode
	}
	langs := []string{"tinygo", "go", "rust", "zig"}
	for _, lang := range langs {
		if !m.IsReady(lang) {
			fmt.Printf("🚀 Proactive Toolchain: %s is missing. Starting background prep...\n", lang)
			go func(l string) {
				if err := m.SetupSDK(l); err != nil {
					fmt.Printf("⚠️ Proactive Toolchain error (%s): %v\n", l, err)
				} else {
					fmt.Printf("✅ Proactive Toolchain: %s is now ready!\n", l)
				}
			}(lang)
		}
	}
}

func (m *ToolchainManager) GetGoPath() string {
	hermeticGo := filepath.Join(m.sdkDir, "go", "bin", "go")
	if runtime.GOOS == "windows" {
		hermeticGo += ".exe"
	}

	if _, err := os.Stat(hermeticGo); err == nil {
		return hermeticGo
	}

	path, _ := exec.LookPath("go")
	return path
}

func (m *ToolchainManager) GetTinyGoPath() string {
	hermeticTinyGo := filepath.Join(m.sdkDir, "tinygo", "bin", "tinygo")
	if runtime.GOOS == "windows" {
		hermeticTinyGo += ".exe"
	}

	if _, err := os.Stat(hermeticTinyGo); err == nil {
		return hermeticTinyGo
	}

	path, _ := exec.LookPath("tinygo")
	return path
}

func (m *ToolchainManager) GetRustcPath() string {
	hermeticRustc := filepath.Join(m.sdkDir, "rust", "bin", "rustc")
	if runtime.GOOS == "windows" {
		hermeticRustc += ".exe"
	}

	if _, err := os.Stat(hermeticRustc); err == nil {
		return hermeticRustc
	}

	path, _ := exec.LookPath("rustc")
	return path
}

func (m *ToolchainManager) GetZigPath() string {
	hermeticZig := filepath.Join(m.sdkDir, "zig", "zig")
	if runtime.GOOS == "windows" {
		hermeticZig += ".exe"
	}

	if _, err := os.Stat(hermeticZig); err == nil {
		return hermeticZig
	}

	path, _ := exec.LookPath("zig")
	return path
}

func (m *ToolchainManager) GetWasiSDKPath() string {
	hermeticClang := filepath.Join(m.sdkDir, "wasi-sdk", "bin", "clang")
	if runtime.GOOS == "windows" {
		hermeticClang += ".exe"
	}

	if _, err := os.Stat(hermeticClang); err == nil {
		return hermeticClang
	}
	return ""
}

func (m *ToolchainManager) IsReady(lang string) bool {
	switch lang {
	case "go":
		return m.GetGoPath() != ""
	case "tinygo":
		return m.GetTinyGoPath() != ""
	case "rust":
		return m.GetRustcPath() != ""
	case "zig":
		return m.GetZigPath() != ""
	case "wasi-sdk":
		return m.GetWasiSDKPath() != ""
	default:
		return false
	}
}

func (m *ToolchainManager) SetupSDK(lang string) error {
	lang = strings.ToLower(strings.TrimSpace(lang))

	m.mu.Lock()
	if m.isAirgap {
		m.mu.Unlock()
		return fmt.Errorf("SDK download blocked: Vraxter is running in airgap mode. Please pre-populate SDK in %s", m.sdkDir)
	}
	if m.isSettingUp[lang] {
		m.mu.Unlock()
		return nil
	}
	m.isSettingUp[lang] = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.isSettingUp[lang] = false
		m.mu.Unlock()
	}()

	var rawURL string
	switch lang {
	case "go":
		rawURL = m.resolveURL("go", m.getGoURL())
	case "tinygo":
		rawURL = m.resolveURL("tinygo", m.getTinyGoURL())
	case "rust":
		rawURL = m.resolveURL("rust", m.getRustURL())
	case "zig":
		rawURL = m.resolveURL("zig", m.getZigURL())
	case "wasi-sdk":
		rawURL = m.resolveURL("wasi-sdk", m.getWasiSDKURL())
	default:
		return fmt.Errorf("unsupported language: %s", lang)
	}

	if rawURL == "" {
		return fmt.Errorf("unsupported platform for %s automatic setup: %s/%s", lang, runtime.GOOS, runtime.GOARCH)
	}

	// Mandatory fail-closed SHA-256 lookup before starting network download.
	expectedSHA256, err := m.ResolveSDKChecksum(lang, rawURL)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(m.sdkDir, 0700); err != nil {
		return fmt.Errorf("failed to create SDK directory %s: %w", m.sdkDir, err)
	}

	archiveExt := detectArchiveExt(rawURL, lang)
	tmpHandle, err := os.CreateTemp(m.sdkDir, fmt.Sprintf(".vraxter-%s-sdk-*%s", lang, archiveExt))
	if err != nil {
		return fmt.Errorf("failed to allocate temporary SDK archive: %w", err)
	}
	tmpFile := tmpHandle.Name()
	_ = tmpHandle.Close()
	_ = os.Remove(tmpFile)
	defer os.Remove(tmpFile)

	fmt.Printf("📥 Downloading %s SDK for %s/%s (expected SHA-256: %s)...\n", lang, runtime.GOOS, runtime.GOARCH, expectedSHA256[:16])
	if err := utils.DownloadFileVerified(tmpFile, rawURL, expectedSHA256); err != nil {
		_ = os.Remove(tmpFile)
		return err
	}

	dest := filepath.Join(m.sdkDir, lang)
	fmt.Printf("📦 Extracting verified %s SDK...\n", lang)
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}

	var extractErr error
	switch archiveExt {
	case ".zip":
		extractErr = utils.ExtractZip(tmpFile, dest)
	case ".tar.xz":
		extractErr = utils.ExtractTarXz(tmpFile, dest)
	default:
		extractErr = utils.ExtractTarGz(tmpFile, dest)
	}
	if extractErr != nil {
		_ = os.RemoveAll(dest)
		return extractErr
	}

	utils.CleanupFolder(dest)
	return nil
}

func (m *ToolchainManager) resolveURL(lang, defaultURL string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if override, ok := m.urlOverrides[lang]; ok && override != "" {
		return override
	}
	return defaultURL
}

func detectArchiveExt(rawURL, lang string) string {
	lower := strings.ToLower(archiveFilenameFromURL(rawURL))
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return ".zip"
	case strings.HasSuffix(lower, ".tar.xz"):
		return ".tar.xz"
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return ".tar.gz"
	}
	if lang == "zig" && runtime.GOOS != "windows" {
		return ".tar.xz"
	}
	if runtime.GOOS == "windows" && (lang == "go" || lang == "tinygo" || lang == "zig") {
		return ".zip"
	}
	return ".tar.gz"
}

func (m *ToolchainManager) getGoURL() string {
	const version = "1.22.5"
	var osName, ext string

	switch runtime.GOOS {
	case "linux":
		osName, ext = "linux", "tar.gz"
	case "darwin":
		osName, ext = "darwin", "tar.gz"
	case "windows":
		osName, ext = "windows", "zip"
	default:
		return ""
	}

	arch := runtime.GOARCH
	return fmt.Sprintf("https://go.dev/dl/go%s.%s-%s.%s", version, osName, arch, ext)
}

func (m *ToolchainManager) getTinyGoURL() string {
	const version = "0.32.0"
	var osName, arch, ext string

	switch runtime.GOOS {
	case "linux":
		osName, ext = "linux", "tar.gz"
	case "darwin":
		osName, ext = "darwin", "tar.gz"
	case "windows":
		osName, ext = "windows", "zip"
	default:
		return ""
	}

	if runtime.GOARCH == "amd64" {
		arch = "amd64"
	} else if runtime.GOARCH == "arm64" {
		arch = "arm64"
	} else {
		return ""
	}

	return fmt.Sprintf("https://github.com/tinygo-org/tinygo/releases/download/v%s/tinygo%s.%s-%s.%s", version, version, osName, arch, ext)
}

func (m *ToolchainManager) getRustURL() string {
	const version = "1.80.0"
	var target string

	switch runtime.GOOS {
	case "linux":
		if runtime.GOARCH == "amd64" {
			target = "x86_64-unknown-linux-gnu"
		} else if runtime.GOARCH == "arm64" {
			target = "aarch64-unknown-linux-gnu"
		}
	case "darwin":
		if runtime.GOARCH == "amd64" {
			target = "x86_64-apple-darwin"
		} else if runtime.GOARCH == "arm64" {
			target = "aarch64-apple-darwin"
		}
	case "windows":
		if runtime.GOARCH == "amd64" {
			target = "x86_64-pc-windows-msvc"
		} else if runtime.GOARCH == "arm64" {
			target = "aarch64-pc-windows-msvc"
		}
	default:
		return ""
	}
	if target == "" {
		return ""
	}

	return fmt.Sprintf("https://static.rust-lang.org/dist/rust-%s-%s.tar.gz", version, target)
}

func (m *ToolchainManager) getZigURL() string {
	const version = "0.13.0"
	var osName, arch, ext string

	switch runtime.GOOS {
	case "linux":
		osName, ext = "linux", "tar.xz"
	case "darwin":
		osName, ext = "macos", "tar.xz"
	case "windows":
		osName, ext = "windows", "zip"
	default:
		return ""
	}

	if runtime.GOARCH == "amd64" {
		arch = "x86_64"
	} else if runtime.GOARCH == "arm64" {
		arch = "aarch64"
	} else {
		return ""
	}

	return fmt.Sprintf("https://ziglang.org/download/%s/zig-%s-%s-%s.%s", version, osName, arch, version, ext)
}

func (m *ToolchainManager) getWasiSDKURL() string {
	const version = "24.0"
	const major = "24"
	var osName, arch string

	switch runtime.GOOS {
	case "linux":
		osName = "linux"
	case "darwin":
		osName = "macos"
	case "windows":
		osName = "windows"
	default:
		return ""
	}

	switch runtime.GOARCH {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "arm64"
	default:
		return ""
	}

	return fmt.Sprintf("https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-%s/wasi-sdk-%s-%s-%s.tar.gz", major, version, arch, osName)
}
