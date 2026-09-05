//go:build integration

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhong/droply/internal/model"
	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
)

// Exercise Cobra and the real HTTP/API/site/storage chain. Only DNS dialing is
// redirected to the local listener; the API and site retain their actual Hosts.
func deploymentCommandFixture(t *testing.T) (func(...string) (string, error), func(string), func() string) {
	t.Helper()
	withTempHome(t)
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "droply.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	app := server.New(st, t.TempDir(), "droply.test", []byte("test-hmac"))
	if err := app.PrepareDeployments(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.ShutdownAnalytics)
	listener := httptest.NewServer(app.Handler())
	t.Cleanup(listener.Close)
	dialer := &net.Dialer{}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if strings.HasSuffix(host, ".droply.test") {
			address = listener.Listener.Addr().String()
		}
		return dialer.DialContext(ctx, network, address)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous; transport.CloseIdleConnections() })
	anonymous := NewAPIClient(&Context{APIURL: "http://api.droply.test"})
	tokens := map[string]string{}
	for _, name := range []string{"owner", "stranger"} {
		var response struct {
			Token string `json:"api_token"`
		}
		if err := anonymous.doJSON("POST", "/auth/register", map[string]string{"email": name + "@example.com", "password": "test-password"}, &response); err != nil {
			t.Fatal(err)
		}
		tokens[name] = response.Token
	}
	selectUser := func(name string) {
		t.Helper()
		if err := SaveFullConfig(&Config{CurrentContext: "integration", Contexts: map[string]Context{"integration": {APIURL: "http://api.droply.test", Token: tokens[name]}}}); err != nil {
			t.Fatal(err)
		}
	}
	selectUser("owner")
	if err := NewAPIClient(LoadConfig()).doJSON("POST", "/subdomains", map[string]string{"name": "alice"}, nil); err != nil {
		t.Fatal(err)
	}
	projectDir := t.TempDir()
	t.Chdir(projectDir)
	if err := os.WriteFile(filepath.Join(projectDir, ".droply.toml"), []byte("subdomain = \"alice\"\nproject = \"site\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		cmd := NewRootCmd("test")
		var output, diagnostics bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&diagnostics)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return output.String(), err
	}
	for _, content := range []string{"A", "B"} {
		source := t.TempDir()
		if err := os.WriteFile(filepath.Join(source, "index.html"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := run("deploy", source); err != nil {
			t.Fatal(err)
		}
	}
	content := func() string {
		t.Helper()
		response, err := http.Get("http://alice.droply.test/site/")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 {
			t.Fatalf("site: %d %s", response.StatusCode, data)
		}
		return string(data)
	}
	return run, selectUser, content
}

func TestDeploymentCommandRollbackAndHistory(t *testing.T) {
	run, selectUser, content := deploymentCommandFixture(t)
	if got := content(); got != "B" {
		t.Fatalf("initial content=%q", got)
	}
	output, err := run("deployment", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var list []model.Deployment
	if err := json.Unmarshal([]byte(output), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !list[0].Available || !list[0].Production || list[0].Version != 2 {
		t.Fatalf("history=%+v", list)
	}
	for round := range 2 {
		output, err = run("deployment", "rollback", "1", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Deployment model.Deployment `json:"deployment"`
			Changed    bool             `json:"changed"`
		}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatal(err)
		}
		if result.Deployment.Version != 1 || !result.Deployment.Production || result.Changed != (round == 0) {
			t.Fatalf("rollback=%+v", result)
		}
		if got := content(); got != "A" {
			t.Fatalf("rollback content=%q", got)
		}
	}
	output, err = run("deployment", "list")
	if err != nil || !strings.Contains(output, "PRODUCTION") || !strings.Contains(output, "available") {
		t.Fatalf("table=%q %v", output, err)
	}
	selectUser("stranger")
	if _, err := run("deployment", "rollback", "2"); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("nonowner=%v", err)
	}
	selectUser("unknown")
	if _, err := run("deployment", "list", "--json"); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("unauthorized=%v", err)
	}
	selectUser("owner")
	if _, err := run("deployment", "rollback", "999"); err == nil {
		t.Fatal("unknown version succeeded")
	}
	for _, args := range [][]string{{"deployment", "rollback", "0"}, {"deployment", "rollback", "invalid"}, {"deployment", "rollback"}, {"deployment", "list", "extra"}, {"deployment", "cleanup", "--keep=-1"}, {"deployment", "cleanup", "--days=-1"}} {
		if _, err := run(args...); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	if got := content(); got != "A" {
		t.Fatalf("denied rollback changed content=%q", got)
	}
}

func TestDeploymentCleanupCommandRequiresApply(t *testing.T) {
	run, _, content := deploymentCommandFixture(t)
	type cleanupResult struct {
		DryRun     bool `json:"dry_run"`
		Candidates []struct {
			Version int   `json:"version"`
			Bytes   int64 `json:"bytes"`
		} `json:"candidates"`
		Deleted []int `json:"deleted_versions"`
	}
	// Explicit zero retention is forwarded, while the default command omits it.
	output, err := run("deployment", "cleanup", "--keep", "0", "--days", "0")
	if err != nil {
		t.Fatal(err)
	}
	var preview cleanupResult
	if err := json.Unmarshal([]byte(output), &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.DryRun || len(preview.Candidates) != 1 || preview.Candidates[0].Version != 1 || len(preview.Deleted) != 0 {
		t.Fatalf("preview=%s", output)
	}
	// A preview must leave the artifact available for an actual rollback.
	if _, err := run("deployment", "rollback", "1", "--json"); err != nil {
		t.Fatal(err)
	}
	if got := content(); got != "A" {
		t.Fatalf("preview deleted old artifact: %q", got)
	}
	output, err = run("deployment", "cleanup", "--keep=0", "--days=0", "--apply")
	if err != nil {
		t.Fatal(err)
	}
	var applied cleanupResult
	if err := json.Unmarshal([]byte(output), &applied); err != nil {
		t.Fatal(err)
	}
	if applied.DryRun || len(applied.Deleted) != 1 || applied.Deleted[0] != 2 {
		t.Fatalf("applied=%s", output)
	}
	if _, err := run("deployment", "rollback", "2", "--json"); err == nil {
		t.Fatal("cleaned artifact rollback succeeded")
	}
	output, err = run("deployment", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var versions []model.Deployment
	if err := json.Unmarshal([]byte(output), &versions); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Version != 2 || versions[0].Available || versions[0].ArtifactState != "deleted" {
		t.Fatalf("cleaned history lost: %s", output)
	}
	if got := content(); got != "A" {
		t.Fatalf("cleanup changed production: %q", got)
	}
}

func TestDeploymentCleanupOmitsUnspecifiedRetention(t *testing.T) {
	withTempHome(t)
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.RawQuery != "" {
			t.Errorf("default preview request: %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"dry_run":true,"used_bytes":20,"reclaimable_bytes":0,"candidates":[],"deleted_versions":[],"errors":[]}`)
	}))
	defer api.Close()
	if err := SaveFullConfig(&Config{CurrentContext: "test", Contexts: map[string]Context{"test": {APIURL: api.URL, Token: "token"}}}); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd("test")
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"deployment", "cleanup", "--sub", "alice", "--project", "site"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !json.Valid(output.Bytes()) {
		t.Fatalf("result=%s calls=%d", output.String(), calls)
	}
}

func TestDeploymentCleanupPartialFailureKeepsJSON(t *testing.T) {
	withTempHome(t)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"dry_run":false,"used_bytes":20,"reclaimable_bytes":10,"candidates":[{"version":1,"bytes":10}],"deleted_versions":[],"errors":["artifact removal failed"]}`)
	}))
	defer api.Close()
	if err := SaveFullConfig(&Config{CurrentContext: "test", Contexts: map[string]Context{"test": {APIURL: api.URL, Token: "token"}}}); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd("test")
	var output, diagnostics bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&diagnostics)
	cmd.SetArgs([]string{"deployment", "cleanup", "--sub", "alice", "--project", "site", "--apply"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("partial cleanup must return failure")
	}
	if !json.Valid(output.Bytes()) || !strings.Contains(output.String(), "artifact removal failed") {
		t.Fatalf("failure output corrupted: %s", output.String())
	}
}
