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

type ToolchainManager struct {
	sdkDir string

	mu          sync.Mutex
	isSettingUp map[string]bool
}

func NewToolchainManager(sdkDir string) *ToolchainManager {
	m := &ToolchainManager{
		sdkDir:      sdkDir,
		isSettingUp: make(map[string]bool),
	}

	return m
}

func (m *ToolchainManager) ProactiveSetup() {
	langs := []string{"tinygo", "go", "rust"}
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

func (m *ToolchainManager) IsReady(lang string) bool {
	switch lang {
	case "go":
		return m.GetGoPath() != ""
	case "tinygo":
		return m.GetTinyGoPath() != ""
	case "rust":
		return m.GetRustcPath() != ""
	default:
		return false
	}
}

func (m *ToolchainManager) SetupSDK(lang string) error {
	m.mu.Lock()
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

	case "tinygo":
		url := m.getTinyGoURL()
		if url == "" {
			return fmt.Errorf("unsupported platform for TinyGo: %s/%s", runtime.GOOS, runtime.GOARCH)
		}

		tmpFile := filepath.Join(os.TempDir(), "vraxter-tinygo-sdk.tar.gz")
		if runtime.GOOS == "windows" {
			tmpFile = filepath.Join(os.TempDir(), "vraxter-tinygo-sdk.zip")
		}

		fmt.Printf("📥 Downloading TinyGo SDK for %s/%s...\n", runtime.GOOS, runtime.GOARCH)
		if err := utils.DownloadFile(tmpFile, url); err != nil {
			return err
		}

		fmt.Printf("📦 Extracting TinyGo SDK...\n")
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
	default:
		return ""
	}

	return fmt.Sprintf("https://static.rust-lang.org/dist/rust-%s-%s.tar.gz", version, target)
}
