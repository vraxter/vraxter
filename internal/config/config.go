package config

import (
	"os"
	"path/filepath"
)

// Config holds global daemon data.
type Config struct {
	AppDir    string
	DBPath    string
	KeyPath   string
	SkillsDir string
	SDKDir    string
}

// Load loads the minimal configuration from environment or system defaults
func Load() Config {
	// 1. Priority: Explicit environment variable (useful for mobile apps)
	appDir := os.Getenv("VRAXTER_APP_DIR")

	if appDir == "" {
		// 2. Standard: Use OS specific configuration directory
		configDir, err := os.UserConfigDir()
		if err != nil {
			home, _ := os.UserHomeDir()
			appDir = filepath.Join(home, ".vraxter")
		} else {
			appDir = filepath.Join(configDir, "vraxter")
		}
	}

	skillsDir := filepath.Join(appDir, "skills")
	sdkDir := filepath.Join(appDir, "sdk")

	// Ensure the directories exist with restricted permissions (0700)
	_ = os.MkdirAll(appDir, 0700)
	_ = os.MkdirAll(skillsDir, 0700)
	_ = os.MkdirAll(sdkDir, 0700)

	return Config{
		AppDir:    appDir,
		DBPath:    filepath.Join(appDir, "vraxter.db"),
		KeyPath:   filepath.Join(appDir, ".masterkey"),
		SkillsDir: skillsDir,
		SDKDir:    sdkDir,
	}
}
