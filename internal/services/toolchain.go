package services

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"sync"

	"github.com/patagonicrune/vraxter/internal/utils"
)

// ToolchainManager handles the hermetic installation and detection of SDKs (Go, Rust)
type ToolchainManager struct {
	sdkDir string

	// Concurrency control to prevent multiple setups of the same language
	mu          sync.Mutex
	isSettingUp map[string]bool
}

func NewToolchainManager(sdkDir string) *ToolchainManager {
	m := &ToolchainManager{
		sdkDir:      sdkDir,
		isSettingUp: make(map[string]bool),
	}

	// Lazy setup: SDKs will be downloaded on-demand when a Coder needs them.
	return m
}

// ProactiveSetup checks for missing SDKs and starts downloading them if needed
func (m *ToolchainManager) ProactiveSetup() {
	langs := []string{"go", "rust"}
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

// GetGoPath returns the path to the hermetic go binary or the system one as fallback
func (m *ToolchainManager) GetGoPath() string {
	hermeticGo := filepath.Join(m.sdkDir, "go", "bin", "go")
	if runtime.GOOS == "windows" {
		hermeticGo += ".exe"
	}

	if _, err := os.Stat(hermeticGo); err == nil {
		return hermeticGo
	}

	// Fallback to system go
	path, _ := exec.LookPath("go")
	return path
}

// GetRustcPath returns the path to the hermetic rustc binary
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

// IsReady checks if the required tools for a language are available
func (m *ToolchainManager) IsReady(lang string) bool {
	switch lang {
	case "go":
		return m.GetGoPath() != ""
	case "rust":
		return m.GetRustcPath() != ""
	default:
		return false
	}
}

// SetupSDK downloads and extracts the SDK for the specified language
func (m *ToolchainManager) SetupSDK(lang string) error {
	m.mu.Lock()
	if m.isSettingUp[lang] {
		m.mu.Unlock()
		return nil // Already in progress
	}
	m.isSettingUp[lang] = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.isSettingUp[lang] = false
		m.mu.Unlock()
	}()

	os.MkdirAll(m.sdkDir, 0755)
	dest := filepath.Join(m.sdkDir, lang)

	switch lang {
	case "go":
		url := m.getGoURL()
		if url == "" {
			return fmt.Errorf("unsupported platform for Go: %s/%s", runtime.GOOS, runtime.GOARCH)
		}

		tmpFile := filepath.Join(os.TempDir(), "vraxter-go-sdk.tar.gz")
		if runtime.GOOS == "windows" {
			tmpFile = filepath.Join(os.TempDir(), "vraxter-go-sdk.zip")
		}

		fmt.Printf("📥 Downloading Go SDK for %s/%s...\n", runtime.GOOS, runtime.GOARCH)
		if err := utils.DownloadFile(tmpFile, url); err != nil {
			return err
		}

		fmt.Printf("📦 Extracting Go SDK...\n")
		os.MkdirAll(dest, 0755)
		var err error
		if runtime.GOOS == "windows" {
			err = utils.ExtractZip(tmpFile, dest)
		} else {
			err = utils.ExtractTarGz(tmpFile, dest)
		}
		if err != nil {
			return err
		}

		utils.CleanupFolder(dest)
		os.Remove(tmpFile)
		return nil

	case "rust":
		url := m.getRustURL()
		if url == "" {
			return fmt.Errorf("unsupported platform for Rust automatic setup: %s/%s", runtime.GOOS, runtime.GOARCH)
		}

		tmpFile := filepath.Join(os.TempDir(), "vraxter-rust-sdk.tar.gz")
		fmt.Printf("📥 Downloading Rust SDK (Standalone) for %s/%s...\n", runtime.GOOS, runtime.GOARCH)
		if err := utils.DownloadFile(tmpFile, url); err != nil {
			fmt.Println("Error downloading Rust SDK: ", err)
			return err
		}

		fmt.Printf("📦 Extracting Rust SDK...\n")
		os.MkdirAll(dest, 0755)
		if err := utils.ExtractTarGz(tmpFile, dest); err != nil {
			return err
		}

		utils.CleanupFolder(dest)
		os.Remove(tmpFile)
		return nil

	default:
		return fmt.Errorf("unsupported language: %s", lang)
	}
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
	default:
		return ""
	}

	return fmt.Sprintf("https://static.rust-lang.org/dist/rust-%s-%s.tar.gz", version, target)
}
