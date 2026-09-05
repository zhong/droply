package cli

import (
	"bytes"
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
func LoadConfig() (*Context, error) {
	full, err := LoadFullConfig()
	if err != nil {
		return nil, err
	}
	name, err := resolveActiveContextName(full)
	if err != nil {
		return nil, err
	}
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
	return &ctx, nil
}

// LoadFullConfig returns the entire config (all contexts). Used by `droply context` commands.
func LoadFullConfig() (*Config, error) {
	cfg := &Config{
		Contexts: make(map[string]Context),
	}
	p := configPath()
	if _, err := toml.DecodeFile(p, cfg); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config %s: %w", p, err)
		}
		cfg.CurrentContext = defaultContextName
		cfg.Contexts[defaultContextName] = Context{APIURL: defaultAPIURL}
		return cfg, nil
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
	return cfg, nil
}

// resolveActiveContextName decides which context the next command should use.
// Priority (highest to lowest):
//  1. --context flag (SetActiveContext)
//  2. .droply.toml context field in current working directory
//  3. current_context from global config
func resolveActiveContextName(cfg *Config) (string, error) {
	name := activeContextOverride
	if name == "" {
		pc, err := loadOptionalProjectConfig()
		if err != nil {
			return "", err
		}
		if pc != nil {
			name = pc.Context
		}
	}
	if name == "" {
		name = cfg.CurrentContext
	}
	if _, ok := cfg.Contexts[name]; !ok {
		return "", fmt.Errorf("context %q not found; use 'droply context add' to create it", name)
	}
	return name, nil
}

// SaveActiveContext writes the given Context back to the active context slot.
func SaveActiveContext(ctx *Context) error {
	full, err := LoadFullConfig()
	if err != nil {
		return err
	}
	name, err := resolveActiveContextName(full)
	if err != nil {
		return err
	}
	full.Contexts[name] = *ctx
	if _, ok := full.Contexts[full.CurrentContext]; !ok {
		full.CurrentContext = name
	}
	return SaveFullConfig(full)
}

// SaveFullConfig writes the entire config to disk.
func SaveFullConfig(cfg *Config) error {
	// Encode before touching disk, without changing the caller's migration fields.
	saved := *cfg
	saved.LegacyAPIURL = ""
	saved.LegacyToken = ""
	var data bytes.Buffer
	if err := toml.NewEncoder(&data).Encode(saved); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	dir := configDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	f, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(data.Bytes()); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync config: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(f.Name(), configPath()); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// ActiveContextName returns the selected context, or an error if it cannot be loaded.
func ActiveContextName() (string, error) {
	full, err := LoadFullConfig()
	if err != nil {
		return "", err
	}
	return resolveActiveContextName(full)
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
