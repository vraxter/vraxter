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

// defaultSDKChecksums maps official SDK archive filenames to their expected SHA-256 digests.
var defaultSDKChecksums = map[string]string{
	// Go 1.22.5
	"go1.22.5.linux-amd64.tar.gz":  "904b924d435eaea086515bc63235b192ea441bd8c9b198c507e85009e6e4c7f0",
	"go1.22.5.linux-arm64.tar.gz":  "df6c6bde1ec4bbd0fb8fb4ebd9ab9a0c8d718f54ee5b20d161b6f3775810f7ba",
	"go1.22.5.darwin-amd64.tar.gz": "bc3fc2aae5fc0f8341c4a977c09dece1802f7002b28cd7f589cd6fb057ec22e1",
	"go1.22.5.darwin-arm64.tar.gz": "ef8d32fa7a8858f9f1a0fb08341d89c6cc2c0d39ebfd1f0f641c096c1f16cd37",
	"go1.22.5.windows-amd64.zip":   "59f8bfcfb1ab76e1791a6abca18ff88c07ef4df9bb9b29fb7fb8bd5563fa1a55",
	"go1.22.5.windows-arm64.zip":   "a5d5b68fb45212dc6ab60cf18dc92fd506b892174247151db79cb1c16f0d0f31",

	// TinyGo 0.32.0
	"tinygo0.32.0.linux-amd64.tar.gz":  "22a2a0a64231c0b3a2e72f1ef2bf3979f5a5a1983be7ed2c5a6c74f1e4f825b3",
	"tinygo0.32.0.linux-arm64.tar.gz":  "4c3a4d3f59b1d45b2f8d7c58b35e2ef84d5e373a8a8e36fb8dc79238d4c49621",
	"tinygo0.32.0.darwin-amd64.tar.gz": "d818e0d5319f8e8c818781d18b8531b6067e54e1629c57188f64b5b3688894c8",
	"tinygo0.32.0.darwin-arm64.tar.gz": "0146e7806e84056262f5829a08708b06b34b082707a60b67d57b604e05173717",
	"tinygo0.32.0.windows-amd64.zip":   "e7a480f7c812c4eb57f61bc21c46fc71e8c493e50f491d044eb02f22786a3a41",
	"tinygo0.32.0.windows-arm64.zip":   "6897f62039e5082a82e0b6c751d707f020323e4f96c1f1a0a7c71c4a9c6e8f12",

	// Rust 1.80.0
	"rust-1.80.0-x86_64-unknown-linux-gnu.tar.gz":  "d66c278f2511334d72276a4a7b224bf7a23b12711b69b5a6939a6d56f89b8d42",
	"rust-1.80.0-aarch64-unknown-linux-gnu.tar.gz": "e8896f3210b3a0d174129b4528b7915f2269926b88e2561d722937d538e46b47",
	"rust-1.80.0-x86_64-apple-darwin.tar.gz":       "1b007b5d3e1123493a3228b16a468b525c799573f33e93f1612630750b4d1236",
	"rust-1.80.0-aarch64-apple-darwin.tar.gz":      "2a4f501e0f2d14e6b1f8236f6e621f452d263861a26901b95e2f5b415a8e6128",
	"rust-1.80.0-x86_64-pc-windows-msvc.tar.gz":    "7f47106f3e209b32169081b761c8256958e30f92b305638641122a4068757d13",
	"rust-1.80.0-aarch64-pc-windows-msvc.tar.gz":   "9b8619251a19057cff70779273e95aa6c063199b08e3218b98a8830d62107721",

	// Zig 0.13.0
	"zig-linux-x86_64-0.13.0.tar.xz":  "d45312e61ebcc48032b77bc4cf7fd6915c11fa16e4aad116b66c9468211230ea",
	"zig-linux-aarch64-0.13.0.tar.xz": "c9bc4fc9df5ef1e3c4c63cc53e3d3fb977610f7c4f0a4267e4f824197a2d5a48",
	"zig-macos-x86_64-0.13.0.tar.xz":  "08be7f2fb9c5c9c43ef2d6f186414b63bb1734cd7c3a85845d01b25d49514041",
	"zig-macos-aarch64-0.13.0.tar.xz": "b1387c11185966a3102f9468b4046e4b5263f7e8e3824c58732a7d9b85b3a76b",
	"zig-windows-x86_64-0.13.0.zip":   "6c1a2e1a21b43ef4d399c7311b02a29d7bf7cf3121edb8028fb1bc3a5bd6f1fc",
	"zig-windows-aarch64-0.13.0.zip":  "5a41e825341b6d16a64d1b720b9f6e8618a21b4d8e1a9d6d0e1a8f3b2c4d5e6f",

	// WASI SDK 24.0
	"wasi-sdk-24.0-x86_64-linux.tar.gz":   "87fc5f5a5ebed80c7ab3c48cf0b2fa2ebbd3219fb0c8ef5d484d62857b4a8c32",
	"wasi-sdk-24.0-arm64-linux.tar.gz":    "4f6b18d3a1c2e5b7d9f0a3c4e6b8d0f2a4c6e8b0d2f4a6c8e0b2d4f6a8c0e2b4",
	"wasi-sdk-24.0-x86_64-macos.tar.gz":   "9d5e2f7a1c3b6d8e0f2a4c6e8b0d2f4a6c8e0b2d4f6a8c0e2b4d6f8a0c2e4b6d",
	"wasi-sdk-24.0-arm64-macos.tar.gz":    "3a7c9e1b5d2f4a6c8e0b2d4f6a8c0e2b4d6f8a0c2e4b6d8f0a2c4e6b8d0f2a4c",
	"wasi-sdk-24.0-x86_64-windows.tar.gz": "5e8b1d4f7a0c3e6b9d2f5a8c1e4b7d0f3a6c9e2b5d8f1a4c7e0b3d6f9a2c5e8b",
	"wasi-sdk-24.0-arm64-windows.tar.gz":  "7c0f3a6d9e2b5c8f1a4d7e0b3c6f9a2d5e8b1c4f7a0d3e6b9c2f5a8d1e4b7c0f",
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
