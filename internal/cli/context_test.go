package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// withTempHome sets HOME to a temp dir for the duration of the test,
// so config files don't pollute the real user home.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// Reset state between tests.
	activeContextOverride = ""
	return dir
}

func TestLoadConfigBrandNew(t *testing.T) {
	withTempHome(t)

	ctx := LoadConfig()
	if ctx.APIURL != defaultAPIURL {
		t.Errorf("expected default API URL, got %q", ctx.APIURL)
	}
	if ctx.Token != "" {
		t.Errorf("expected empty token, got %q", ctx.Token)
	}
}

func TestLoadConfigMigratesLegacy(t *testing.T) {
	home := withTempHome(t)

	// Write a legacy-format config.
	cfgDir := filepath.Join(home, ".config", "droply")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := "api_url = \"https://api.example.com\"\ntoken = \"dp_legacy_token\"\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(legacy), 0600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	// LoadConfig should silently migrate into contexts.default.
	ctx := LoadConfig()
	if ctx.APIURL != "https://api.example.com" {
		t.Errorf("APIURL: got %q want %q", ctx.APIURL, "https://api.example.com")
	}
	if ctx.Token != "dp_legacy_token" {
		t.Errorf("Token: got %q want %q", ctx.Token, "dp_legacy_token")
	}

	// Re-read the config file directly and verify it no longer has legacy fields.
	full := LoadFullConfig()
	if _, ok := full.Contexts[defaultContextName]; !ok {
		t.Error("expected default context after migration")
	}
	if full.Contexts[defaultContextName].APIURL != "https://api.example.com" {
		t.Errorf("default context APIURL: got %q", full.Contexts[defaultContextName].APIURL)
	}
	if full.CurrentContext != defaultContextName {
		t.Errorf("CurrentContext: got %q want %q", full.CurrentContext, defaultContextName)
	}

	// Read raw bytes to verify legacy fields are gone.
	data, err := os.ReadFile(filepath.Join(cfgDir, "config.toml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)
	if containsTopLevel(content, "api_url =") {
		t.Errorf("expected top-level api_url to be removed after migration, got:\n%s", content)
	}
}

func TestSaveAndLoadMultipleContexts(t *testing.T) {
	withTempHome(t)

	full := &Config{
		CurrentContext: "paratera",
		Contexts: map[string]Context{
			defaultContextName: {APIURL: "https://api.droplydoc.com", Token: "dp_default"},
			"paratera":         {APIURL: "https://api.docs.paratera.co", Token: "dp_para"},
		},
	}
	if err := SaveFullConfig(full); err != nil {
		t.Fatalf("SaveFullConfig: %v", err)
	}

	got := LoadFullConfig()
	if got.CurrentContext != "paratera" {
		t.Errorf("CurrentContext: got %q", got.CurrentContext)
	}
	if got.Contexts["paratera"].Token != "dp_para" {
		t.Errorf("paratera token mismatch: %q", got.Contexts["paratera"].Token)
	}
	if got.Contexts[defaultContextName].APIURL != "https://api.droplydoc.com" {
		t.Errorf("default APIURL mismatch")
	}

	// LoadConfig should return the active context (paratera).
	ctx := LoadConfig()
	if ctx.APIURL != "https://api.docs.paratera.co" || ctx.Token != "dp_para" {
		t.Errorf("active context wrong: APIURL=%q Token=%q", ctx.APIURL, ctx.Token)
	}
}

func TestActiveContextOverride(t *testing.T) {
	withTempHome(t)

	full := &Config{
		CurrentContext: "default",
		Contexts: map[string]Context{
			"default":  {APIURL: "https://api.droplydoc.com", Token: "dp_default"},
			"paratera": {APIURL: "https://api.docs.paratera.co", Token: "dp_para"},
		},
	}
	if err := SaveFullConfig(full); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Without override: default wins.
	if got := LoadConfig().Token; got != "dp_default" {
		t.Errorf("without override: got %q want dp_default", got)
	}

	// With override: paratera wins.
	SetActiveContext("paratera")
	defer SetActiveContext("")
	if got := LoadConfig().Token; got != "dp_para" {
		t.Errorf("with override: got %q want dp_para", got)
	}
}

func TestActiveContextOverrideUnknown(t *testing.T) {
	withTempHome(t)

	full := &Config{
		CurrentContext: "default",
		Contexts: map[string]Context{
			"default": {APIURL: "https://api.droplydoc.com", Token: "dp_default"},
		},
	}
	if err := SaveFullConfig(full); err != nil {
		t.Fatalf("save: %v", err)
	}

	SetActiveContext("nonexistent")
	defer SetActiveContext("")

	ctx := LoadConfig()
	// Falls back to default API URL with no token.
	if ctx.APIURL != defaultAPIURL {
		t.Errorf("expected fallback APIURL, got %q", ctx.APIURL)
	}
	if ctx.Token != "" {
		t.Errorf("expected empty token for unknown context, got %q", ctx.Token)
	}
}

func TestProjectConfigContextOverride(t *testing.T) {
	withTempHome(t)

	// Save two contexts.
	full := &Config{
		CurrentContext: "default",
		Contexts: map[string]Context{
			"default":  {APIURL: "https://api.droplydoc.com", Token: "dp_default"},
			"paratera": {APIURL: "https://api.docs.paratera.co", Token: "dp_para"},
		},
	}
	if err := SaveFullConfig(full); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Create a project dir with .droply.toml pointing at paratera.
	projDir := t.TempDir()
	projConfig := `context = "paratera"
subdomain = "alice"
project = "blog"
`
	if err := os.WriteFile(filepath.Join(projDir, ".droply.toml"), []byte(projConfig), 0644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	origDir, _ := os.Getwd()
	if err := os.Chdir(projDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	// LoadConfig should resolve to paratera even though current_context is default.
	ctx := LoadConfig()
	if ctx.Token != "dp_para" {
		t.Errorf("expected paratera token, got %q (APIURL=%q)", ctx.Token, ctx.APIURL)
	}
}

func TestExplicitOverrideBeatsProjectConfig(t *testing.T) {
	withTempHome(t)

	full := &Config{
		CurrentContext: "default",
		Contexts: map[string]Context{
			"default":  {APIURL: "https://api.droplydoc.com", Token: "dp_default"},
			"paratera": {APIURL: "https://api.docs.paratera.co", Token: "dp_para"},
			"corp":     {APIURL: "https://api.corp.local", Token: "dp_corp"},
		},
	}
	if err := SaveFullConfig(full); err != nil {
		t.Fatalf("save: %v", err)
	}

	projDir := t.TempDir()
	projConfig := `context = "paratera"
`
	if err := os.WriteFile(filepath.Join(projDir, ".droply.toml"), []byte(projConfig), 0644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	origDir, _ := os.Getwd()
	if err := os.Chdir(projDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	// --context corp should override project config's paratera.
	SetActiveContext("corp")
	defer SetActiveContext("")

	if got := LoadConfig().Token; got != "dp_corp" {
		t.Errorf("expected corp token, got %q", got)
	}
}

func TestDeriveContextName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://api.droplydoc.com", "droplydoc"},
		{"https://api.docs.paratera.co", "paratera"},
		{"http://api.example.com/path", "example"},
		{"https://localhost", "localhost"},
		{"", "custom"},
	}
	for _, c := range cases {
		if got := deriveContextName(c.in); got != c.want {
			t.Errorf("deriveContextName(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

// containsTopLevel checks if a TOML string contains a key as a top-level entry
// (not nested inside a [table]).
func containsTopLevel(content, prefix string) bool {
	inTable := false
	for _, line := range splitLines(content) {
		trimmed := trimLeftSpace(line)
		if len(trimmed) > 0 && trimmed[0] == '[' {
			inTable = true
			continue
		}
		if !inTable && len(trimmed) >= len(prefix) && trimmed[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func trimLeftSpace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[i:]
}
