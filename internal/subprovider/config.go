package subprovider

import (
	"fmt"
	"os"
	"time"
)

// ---------- Config loading ----------

// SubtitleConfig holds the subtitle provider settings from config.json.
type SubtitleConfig struct {
	Enabled           bool     `json:"enabled"`
	APIKey            string   `json:"api_key"`  // OpenSubtitles REST API key
	BaseURL           string   `json:"base_url"` // OpenSubtitles base URL (optional)
	User              string   `json:"user"`     // OpenSubtitles username (optional)
	Password          string   `json:"password"` // OpenSubtitles password (optional)
	Preferred         []string `json:"preferred_languages"`
	MaxResults        int      `json:"max_results"`
	OpenSubtitlesUser string   // legacy alias
	OpenSubtitlesPass string   // legacy alias
	// FUSEMountPath is read from env var, not config.json
	FUSEMountPath string
}

// EngineConfig is used internally by the Engine.
type EngineConfig struct {
	FUSEMountPath         string
	PreferredLanguages    []LanguageTag
	OpenSubtitlesKey      string
	OpenSubtitlesBaseURL  string
	OpenSubtitlesUser     string
	OpenSubtitlesPass     string
	MaxResultsPerProvider int
	SubDBTimeout          time.Duration
	OSDownloadTimeout     time.Duration
}

// LoadSubtitleConfig reads subtitle config from config.json and env vars.
// The FUSE mount path comes from TIRAMISU_FUSE_MOUNT_PATH env var (D5).
func LoadSubtitleConfig() (SubtitleConfig, error) {
	cfg := SubtitleConfig{
		Preferred:  []string{"por", "multi", "eng"},
		MaxResults: 5,
	}

	// Read FUSE mount path from env var (D5)
	fuseMountPath := os.Getenv("TIRAMISU_FUSE_MOUNT_PATH")
	if fuseMountPath == "" {
		fuseMountPath = os.Getenv("TIRAMISU_MOUNT_PATH")
	}
	if fuseMountPath == "" {
		return cfg, fmt.Errorf("subprovider: TIRAMISU_FUSE_MOUNT_PATH (or TIRAMISU_MOUNT_PATH) env var is not set")
	}

	cfg.FUSEMountPath = fuseMountPath

	return cfg, nil
}

// Validate returns an error if subtitle is enabled but OpenSubtitles API key is missing (D2).
func (sc SubtitleConfig) Validate() error {
	if sc.Enabled && sc.APIKey == "" {
		return fmt.Errorf("subtitle.enabled=true requires subtitle.api_key (OpenSubtitles REST API key)")
	}
	return nil
}

// ToEngineConfig converts SubtitleConfig to EngineConfig for the subprovider engine.
func (sc SubtitleConfig) ToEngineConfig() EngineConfig {
	languages := make([]LanguageTag, len(sc.Preferred))
	for i, lang := range sc.Preferred {
		languages[i] = LanguageTag(lang)
	}

	return EngineConfig{
		FUSEMountPath:         sc.FUSEMountPath,
		PreferredLanguages:    languages,
		OpenSubtitlesKey:      sc.APIKey,
		OpenSubtitlesBaseURL:  sc.BaseURL,
		OpenSubtitlesUser:     sc.User,
		OpenSubtitlesPass:     sc.Password,
		MaxResultsPerProvider: sc.MaxResults,
		SubDBTimeout:          5 * time.Second,
		OSDownloadTimeout:     15 * time.Second,
	}
}

// getDefaultConfig returns a config with sensible defaults.
func getDefaultConfig() EngineConfig {
	return EngineConfig{
		PreferredLanguages:    []LanguageTag{LangPortuguese, LangMulti, LangEnglish},
		MaxResultsPerProvider: 5,
		SubDBTimeout:          5 * time.Second,
		OSDownloadTimeout:     15 * time.Second,
		OpenSubtitlesBaseURL:  "https://api.opensubtitles.com",
	}
}
