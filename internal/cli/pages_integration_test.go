//go:build integration

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestCIProjectTokensPreviewAndPromote(t *testing.T) {
	run, _, content := deploymentCommandFixture(t)
	apiURL := LoadConfig().APIURL
	tokens := map[string]string{}
	for _, scope := range []string{"preview", "production"} {
		output, err := run("project-token", "create", "--scope", scope, "--name", "ci-"+scope)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatal(err)
		}
		if result.Token == "" {
			t.Fatalf("missing raw token: %s", output)
		}
		tokens[scope] = result.Token
	}
	if err := os.Remove(configPath()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DROPLY_API_URL", apiURL)
	t.Setenv("DROPLY_TOKEN", tokens["preview"])
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("preview-C"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := run("deploy", source, "--preview", "--branch", "feature/ci", "--commit", "0123456", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var deployment struct {
		Version     int    `json:"version"`
		URL         string `json:"url"`
		Environment string `json:"environment"`
	}
	if err := json.Unmarshal([]byte(output), &deployment); err != nil {
		t.Fatal(err)
	}
	if deployment.Version != 3 || deployment.URL == "" || deployment.Environment != "preview" {
		t.Fatalf("deployment=%s", output)
	}
	if got := content(); got != "B" {
		t.Fatalf("preview changed production: %s", got)
	}
	if output, err := run("deployment", "promote", strconv.Itoa(deployment.Version), "--json"); err == nil || output != "" {
		t.Fatalf("preview credential promoted: %q %v", output, err)
	}
	if output, err := run("deploy", source, "--json"); err == nil || output != "" {
		t.Fatalf("preview credential deployed production: %q %v", output, err)
	}
	t.Setenv("DROPLY_TOKEN", tokens["production"])
	if output, err := run("deployment", "promote", "3", "--json"); err != nil || !json.Valid([]byte(output)) {
		t.Fatalf("promotion: %q %v", output, err)
	}
	if got := content(); got != "preview-C" {
		t.Fatalf("promotion content: %s", got)
	}
	if output, err := run("deployment", "rollback", "2", "--json"); err != nil || !json.Valid([]byte(output)) {
		t.Fatalf("rollback: %q %v", output, err)
	}
	if got := content(); got != "B" {
		t.Fatalf("rollback content: %s", got)
	}
	if output, err := run("deployment", "events", "--json"); err != nil || !json.Valid([]byte(output)) {
		t.Fatalf("events: %q %v", output, err)
	}
	if _, err := os.Stat(configPath()); !os.IsNotExist(err) {
		t.Fatalf("CI wrote configuration: %v", err)
	}
}
