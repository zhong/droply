package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const defaultContextName = "default"
const defaultAPIURL = "https://api.droplydoc.com"

// Context represents a single droply server connection (API URL + auth token).
type Context struct {
	APIURL string `toml:"api_url"`
	Token  string `toml:"token"`
}

// Config holds the global CLI configuration with multiple named contexts.
type Config struct {
	CurrentContext string             `toml:"current_context"`
	Contexts       map[string]Context `toml:"contexts"`

	// Legacy fields preserved for migration. These are read from the file
	// when present and migrated into Contexts["default"] on first access.
	LegacyAPIURL string `toml:"api_url,omitempty"`
	LegacyToken  string `toml:"token,omitempty"`
}

// activeContextOverride, set by --context flag, takes precedence over CurrentContext.
// Empty means use CurrentContext from disk.
var activeContextOverride string

// SetActiveContext sets a one-shot context override (used by the --context flag).
func SetActiveContext(name string) {
	activeContextOverride = name
}

// ProjectConfig holds the per-project configuration read from .droply.toml.
type ProjectConfig struct {
	Context      string   `toml:"context"`
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

// LoadConfig reads the global config file and returns the active Context
// (resolved from --context flag, .droply.toml context, or current_context).
//
// To preserve backward compatibility with existing single-server installs,
// the legacy top-level fields (api_url, token) are silently migrated into
// contexts.default on the next SaveConfig.
//
// If the config file does not exist, returns a Context pointing at the
// default public API with no token.
func LoadConfig() *Context {
	full := loadFullConfig()
	name := resolveActiveContextName(full)
	ctx := full.Contexts[name]
	if value, ok := os.LookupEnv("DROPLY_API_URL"); ok {
		ctx.APIURL = value
	}
	if value, ok := os.LookupEnv("DROPLY_TOKEN"); ok {
		ctx.Token = value
	}
	if ctx.APIURL == "" {
		ctx.APIURL = defaultAPIURL
	}
	return &ctx
}

// LoadFullConfig returns the entire config (all contexts). Used by `droply context` commands.
func LoadFullConfig() *Config {
	return loadFullConfig()
}

func loadFullConfig() *Config {
	cfg := &Config{
		Contexts: make(map[string]Context),
	}
	p := configPath()
	if _, err := os.Stat(p); os.IsNotExist(err) {
		// Brand-new install: seed a default context.
		cfg.CurrentContext = defaultContextName
		cfg.Contexts[defaultContextName] = Context{APIURL: defaultAPIURL}
		return cfg
	}
	if _, err := toml.DecodeFile(p, cfg); err != nil {
		// Corrupt or unparseable config: fall back to empty default rather than crash.
		cfg.CurrentContext = defaultContextName
		cfg.Contexts[defaultContextName] = Context{APIURL: defaultAPIURL}
		return cfg
	}
	if cfg.Contexts == nil {
		cfg.Contexts = make(map[string]Context)
	}
	// Migrate legacy top-level fields into "default" context (silent migration).
	if cfg.LegacyAPIURL != "" || cfg.LegacyToken != "" {
		existing := cfg.Contexts[defaultContextName]
		if existing.APIURL == "" {
			existing.APIURL = cfg.LegacyAPIURL
		}
		if existing.Token == "" {
			existing.Token = cfg.LegacyToken
		}
		if existing.APIURL == "" {
			existing.APIURL = defaultAPIURL
		}
		cfg.Contexts[defaultContextName] = existing
		cfg.LegacyAPIURL = ""
		cfg.LegacyToken = ""
		if cfg.CurrentContext == "" {
			cfg.CurrentContext = defaultContextName
		}
	}
	if cfg.CurrentContext == "" {
		cfg.CurrentContext = defaultContextName
	}
	if _, ok := cfg.Contexts[cfg.CurrentContext]; !ok && len(cfg.Contexts) == 0 {
		cfg.Contexts[defaultContextName] = Context{APIURL: defaultAPIURL}
	}
	return cfg
}

// resolveActiveContextName decides which context the next command should use.
// Priority (highest to lowest):
//  1. --context flag (SetActiveContext)
//  2. .droply.toml context field in current working directory
//  3. current_context from global config
func resolveActiveContextName(cfg *Config) string {
	if activeContextOverride != "" {
		return activeContextOverride
	}
	if pc, _ := loadOptionalProjectConfig(); pc != nil && pc.Context != "" {
		return pc.Context
	}
	if cfg.CurrentContext != "" {
		return cfg.CurrentContext
	}
	return defaultContextName
}

// SaveActiveContext writes the given Context back to the active context slot.
func SaveActiveContext(ctx *Context) error {
	full := loadFullConfig()
	name := resolveActiveContextName(full)
	full.Contexts[name] = *ctx
	if _, ok := full.Contexts[full.CurrentContext]; !ok {
		full.CurrentContext = name
	}
	return SaveFullConfig(full)
}

// SaveFullConfig writes the entire config to disk.
func SaveFullConfig(cfg *Config) error {
	dir := configDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Clear legacy fields before writing so they don't reappear.
	cfg.LegacyAPIURL = ""
	cfg.LegacyToken = ""
	f, err := os.OpenFile(configPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

// ActiveContextName returns the name of the context that LoadConfig will use,
// considering flag, project config, and saved current_context.
func ActiveContextName() string {
	return resolveActiveContextName(loadFullConfig())
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
