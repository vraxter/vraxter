package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config holds global daemon data.
type Config struct {
	AppDir    string
	DBPath    string
	KeyPath          string
	SkillsDir         string
	SDKDir            string
	EnableGoogleHome  bool
	SpatialConfigPath string
	Implementation       string
	PrivacyPolicy        string
	HubURL               string
	RequireSkillApproval bool
	WhitelistedIPs       []string
}

type Settings struct {
	Implementation       string `json:"implementation"`
	PrivacyPolicy        string `json:"privacy_policy"`
	HubURL               string `json:"hub_url"`
	RequireSkillApproval bool   `json:"require_skill_approval"`
	WhitelistedIPs       []string `json:"whitelisted_ips"`
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

	// 3. Load from settings.json if exists
	settingsPath := filepath.Join(appDir, "settings.json")
	var impl = "custom"
	var privacy = "ask"
	var hub = "https://hub.vraxter.com"
	var requireSkillApproval = false
	var whitelistedIPs []string

	if data, err := os.ReadFile(settingsPath); err == nil {
		var s Settings
		if json.Unmarshal(data, &s) == nil {
			if s.Implementation != "" {
				impl = s.Implementation
			}
			if s.PrivacyPolicy != "" {
				privacy = s.PrivacyPolicy
			}
			if s.HubURL != "" {
				hub = s.HubURL
			}
			requireSkillApproval = s.RequireSkillApproval
			if len(s.WhitelistedIPs) > 0 {
				whitelistedIPs = s.WhitelistedIPs
			}
		}
	}
	
	// Override with env var if explicitly set
	if envImpl := os.Getenv("VRAXTER_IMPLEMENTATION"); envImpl != "" {
		impl = envImpl
	}
	if envPrivacy := os.Getenv("VRAXTER_PRIVACY_POLICY"); envPrivacy != "" {
		privacy = envPrivacy
	}
	if envHub := os.Getenv("VRAXTER_HUB_URL"); envHub != "" {
		hub = envHub
	}
	if envReqApp := os.Getenv("VRAXTER_REQUIRE_SKILL_APPROVAL"); envReqApp == "true" {
		requireSkillApproval = true
	}

	return Config{
		AppDir:    appDir,
		DBPath:    filepath.Join(appDir, "vraxter.db"),
		KeyPath:          filepath.Join(appDir, ".masterkey"),
		SkillsDir:         skillsDir,
		SDKDir:            sdkDir,
		EnableGoogleHome:  os.Getenv("VRAXTER_GOOGLE_HOME") == "true",
		SpatialConfigPath: filepath.Join(appDir, "spatial.json"),
		Implementation:       impl,
		PrivacyPolicy:        privacy,
		HubURL:               hub,
		RequireSkillApproval: requireSkillApproval,
		WhitelistedIPs:       whitelistedIPs,
	}
}

// SaveSettings writes minimal settings back to the filesystem
func (c *Config) SaveSettings() error {
	s := Settings{
		Implementation:       c.Implementation,
		PrivacyPolicy:        c.PrivacyPolicy,
		HubURL:               c.HubURL,
		RequireSkillApproval: c.RequireSkillApproval,
		WhitelistedIPs:       c.WhitelistedIPs,
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(filepath.Join(c.AppDir, "settings.json"), data, 0600)
}
