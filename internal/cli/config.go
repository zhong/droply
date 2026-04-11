package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config holds the global CLI configuration.
type Config struct {
	APIURL string `toml:"api_url"`
	Token  string `toml:"token"`
}

// ProjectConfig holds the per-project configuration read from .droply.toml.
type ProjectConfig struct {
	Subdomain    string   `toml:"subdomain"`
	Project      string   `toml:"project"`
	ExcludePaths []string `toml:"exclude_paths"`
	ExcludeFiles []string `toml:"exclude_files"`
}

// configDir returns the path to the droply config directory.
func configDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".config/droply"
	}
	return filepath.Join(home, ".config", "droply")
}

// configPath returns the path to the global config file.
func configPath() string {
	return filepath.Join(configDir(), "config.toml")
}

// LoadConfig reads the global config file. If the file does not exist,
// it returns a Config with the default API URL.
func LoadConfig() *Config {
	cfg := &Config{
		APIURL: "https://api.droplydoc.com",
	}
	p := configPath()
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return cfg
	}
	if _, err := toml.DecodeFile(p, cfg); err != nil {
		return cfg
	}
	return cfg
}

// SaveConfig writes cfg to the global config file.
func SaveConfig(cfg *Config) error {
	dir := configDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(configPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

// LoadProjectConfig reads .droply.toml from the current working directory.
func LoadProjectConfig() (*ProjectConfig, error) {
	var pc ProjectConfig
	_, err := toml.DecodeFile(".droply.toml", &pc)
	if err != nil {
		return nil, formatProjectConfigError(err)
	}
	return &pc, nil
}

func loadOptionalProjectConfig() (*ProjectConfig, error) {
	pc, err := LoadProjectConfig()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return pc, nil
}

func formatProjectConfigError(err error) error {
	if err == nil {
		return nil
	}
	if os.IsNotExist(err) {
		return err
	}

	if hint := projectConfigHint(err.Error()); hint != "" {
		return fmt.Errorf("invalid .droply.toml: %s (%w)", hint, err)
	}

	return fmt.Errorf("invalid .droply.toml: %w", err)
}

func projectConfigHint(errMsg string) string {
	switch {
	case strings.Contains(errMsg, `last key "exclude_paths"`) && strings.Contains(errMsg, "destination has type slice"):
		return `exclude_paths must be an array, e.g. exclude_paths = ["research"]`
	case strings.Contains(errMsg, `last key "exclude_files"`) && strings.Contains(errMsg, "destination has type slice"):
		return `exclude_files must be an array, e.g. exclude_files = ["secret.txt"]`
	default:
		return ""
	}
}
