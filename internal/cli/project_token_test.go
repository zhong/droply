//go:build integration

package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhong/droply/internal/model"
)

func TestProjectTokenCommandRealAPI(t *testing.T) {
	run, selectUser, _ := deploymentCommandFixture(t)
	output, err := run("project-token", "create", "--name", "github-actions")
	if err != nil {
		t.Fatal(err)
	}
	var issued struct {
		model.ProjectToken
		Token string `json:"token"`
	}
	if err = json.Unmarshal([]byte(output), &issued); err != nil {
		t.Fatal(err)
	}
	if issued.Token == "" || len(issued.Scopes) != 1 || issued.Scopes[0] != "preview" {
		t.Fatalf("create output: %s", output)
	}
	listed, err := run("project-token", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(listed, issued.Token) || strings.Contains(listed, "digest") {
		t.Fatal("list disclosed secret")
	}
	var metadata []model.ProjectToken
	if err = json.Unmarshal([]byte(listed), &metadata); err != nil || len(metadata) != 1 || metadata[0].ID != issued.ID {
		t.Fatalf("metadata: %s %v", listed, err)
	}
	// Drive the real CLI upload using the issued credential, without changing the API.
	owner := *mustLoadConfig(t)
	if err = SaveFullConfig(&Config{CurrentContext: "integration", Contexts: map[string]Context{"integration": {APIURL: owner.APIURL, Token: issued.Token}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = run("deployment", "list", "--json"); err != nil {
		t.Fatalf("scoped history: %v", err)
	}
	if _, err = run("project-token", "list"); err == nil {
		t.Fatal("project token could list credentials")
	}
	source := t.TempDir()
	if err = os.WriteFile(filepath.Join(source, "index.html"), []byte("scoped preview"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = run("deploy", source); err == nil {
		t.Fatal("preview credential published production")
	}
	if _, err = run("deploy", source, "--preview", "--json"); err != nil {
		t.Fatalf("preview credential cannot upload preview: %v", err)
	}
	selectUser("stranger")
	if _, err = run("project-token", "create"); err == nil {
		t.Fatal("other owner created project token")
	}
	selectUser("owner")
	if _, err = run("project-token", "revoke", fmt.Sprint(issued.ID)); err != nil {
		t.Fatal(err)
	}
	var response any
	if err = NewAPIClient(&Context{APIURL: owner.APIURL, Token: issued.Token}).doJSONContext(t.Context(), http.MethodGet, "/subdomains/alice/projects/site/deployments", nil, &response); err == nil {
		t.Fatal("revoked credential still authenticates")
	}
	for _, args := range [][]string{{"project-token", "create", "--scope", "admin"}, {"project-token", "create", "--expires-in", "0s"}, {"project-token", "create", "--expires-in", "9000h"}, {"project-token", "revoke", "-1"}} {
		if _, err = run(args...); err == nil {
			t.Fatalf("invalid command accepted: %v", args)
		}
	}
}
