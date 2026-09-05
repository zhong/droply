//go:build integration

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestMemberCommandsRealProjectPermissions(t *testing.T) {
	run, selectUser, content := deploymentCommandFixture(t)
	output, err := run("member", "set", "stranger@example.com", "--role", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	var member struct {
		UserID int64 `json:"user_id"`
	}
	if err := json.Unmarshal([]byte(output), &member); err != nil {
		t.Fatal(err)
	}
	selectUser("stranger")
	output, err = run("projects")
	if err != nil {
		t.Fatal(err)
	}
	var projects []struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal([]byte(output), &projects); err != nil || len(projects) != 1 || projects[0].Role != "viewer" {
		t.Fatalf("projects=%s err=%v", output, err)
	}
	if _, err := run("deployment", "list", "--json"); err != nil {
		t.Fatal(err)
	}
	if out, err := run("deployment", "rollback", "1", "--json"); err == nil || out != "" {
		t.Fatal("viewer rolled back")
	}
	selectUser("owner")
	if _, err := run("member", "set", "stranger@example.com", "--role", "deployer"); err != nil {
		t.Fatal(err)
	}
	selectUser("stranger")
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("member publish"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run("deploy", source, "--json"); err != nil {
		t.Fatal(err)
	}
	if got := content(); got != "member publish" {
		t.Fatal(got)
	}
	output, err = run("project-token", "create", "--scope", "production")
	if err != nil {
		t.Fatal(err)
	}
	var token struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(output), &token); err != nil {
		t.Fatal(err)
	}
	if token.Token == "" {
		t.Fatal("deployer token missing")
	}
	selectUser("owner")
	if _, err := run("member", "remove", strconv.FormatInt(member.UserID, 10)); err != nil {
		t.Fatal(err)
	}
	selectUser("stranger")
	if out, err := run("deployment", "list", "--json"); err == nil || out != "" {
		t.Fatal("removed member kept access")
	}
	t.Setenv("DROPLY_TOKEN", token.Token)
	if out, err := run("deployment", "rollback", "1", "--json"); err == nil || out != "" {
		t.Fatal("removed member token kept access")
	}
}
