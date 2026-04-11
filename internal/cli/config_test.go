package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadProjectConfigIncludesExclusions(t *testing.T) {
	tmpDir := t.TempDir()

	config := `subdomain = "alice"
project = "blog"
exclude_paths = ["dist/private", "public/secret.txt"]
exclude_files = ["draft.html", "robots-local.txt"]
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".droply.toml"), []byte(config), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	pc, err := LoadProjectConfig()
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}

	if pc.Subdomain != "alice" {
		t.Fatalf("expected subdomain alice, got %q", pc.Subdomain)
	}
	if pc.Project != "blog" {
		t.Fatalf("expected project blog, got %q", pc.Project)
	}

	expectedPaths := []string{"dist/private", "public/secret.txt"}
	if !reflect.DeepEqual(pc.ExcludePaths, expectedPaths) {
		t.Fatalf("expected exclude_paths %v, got %v", expectedPaths, pc.ExcludePaths)
	}

	expectedFiles := []string{"draft.html", "robots-local.txt"}
	if !reflect.DeepEqual(pc.ExcludeFiles, expectedFiles) {
		t.Fatalf("expected exclude_files %v, got %v", expectedFiles, pc.ExcludeFiles)
	}
}

func TestLoadProjectConfigReturnsHelpfulErrorForInvalidExcludePaths(t *testing.T) {
	tmpDir := t.TempDir()

	config := `subdomain = "para-infra"
project = "network"
exclude_paths = "research/"
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".droply.toml"), []byte(config), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	_, err = LoadProjectConfig()
	if err == nil {
		t.Fatal("expected LoadProjectConfig error")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "invalid .droply.toml") {
		t.Fatalf("expected invalid config prefix, got %q", errMsg)
	}
	if !strings.Contains(errMsg, `exclude_paths must be an array, e.g. exclude_paths = ["research"]`) {
		t.Fatalf("expected helpful exclude_paths hint, got %q", errMsg)
	}
	if !strings.Contains(errMsg, `last key "exclude_paths"`) {
		t.Fatalf("expected original TOML error details, got %q", errMsg)
	}
}
