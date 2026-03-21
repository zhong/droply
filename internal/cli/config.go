package cli

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config holds the global CLI configuration.
type Config struct {
	APIURL string `toml:"api_url"`
	Token  string `toml:"token"`
}

// ProjectConfig holds the per-project configuration read from .droply.toml.
type ProjectConfig struct {
	Subdomain string `toml:"subdomain"`
	Project   string `toml:"project"`
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
		APIURL: "https://api.droply.dev",
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
		return nil, err
	}
	return &pc, nil
}
