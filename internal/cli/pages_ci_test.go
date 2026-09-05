package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCIEnvironmentPrecedenceWithoutConfigWrites(t *testing.T) {
	withTempHome(t)
	t.Chdir(t.TempDir())
	cfg := &Config{CurrentContext: "saved", Contexts: map[string]Context{
		"saved":   {APIURL: "https://saved.test", Token: "saved"},
		"project": {APIURL: "https://project.test", Token: "project"},
		"flag":    {APIURL: "https://flag.test", Token: "flag"},
	}}
	if err := SaveFullConfig(cfg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(configPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".droply.toml", []byte("context = \"project\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := LoadConfig().Token; got != "project" {
		t.Fatal(got)
	}
	SetActiveContext("flag")
	if got := LoadConfig().Token; got != "flag" {
		t.Fatal(got)
	}
	t.Setenv("DROPLY_API_URL", "https://ci.test")
	t.Setenv("DROPLY_TOKEN", "ci-secret")
	got := LoadConfig()
	if got.APIURL != "https://ci.test" || got.Token != "ci-secret" {
		t.Fatalf("environment did not override context")
	}
	after, err := os.ReadFile(configPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("environment credentials persisted")
	}
	cmd := NewRootCmd("test")
	cmd.SetArgs([]string{"version"})
	cmd.SetOut(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if activeContextOverride != "" {
		t.Fatal("flag leaked into next invocation")
	}
}

func TestCIDeployJSONAndNoReplay(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusForbidden, http.StatusTemporaryRedirect, 0} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			withTempHome(t)
			t.Chdir(t.TempDir())
			source := t.TempDir()
			if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("CI"), 0600); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" || r.URL.Path != "/subdomains/alice/projects/site/deploy" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer ci-secret" {
					t.Error("missing CI auth")
				}
				if r.URL.Query().Get("environment") != "preview" || r.URL.Query().Get("branch") != "feature/中文" || r.URL.Query().Get("commit") != "abc123" {
					t.Errorf("metadata=%s", r.URL)
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
				}
				if r.MultipartForm != nil {
					defer r.MultipartForm.RemoveAll()
				}
				switch status {
				case http.StatusOK:
					io.WriteString(w, `{"deployment_id":3,"version":3,"environment":"preview","url":"https://d-example.test/"}`)
				case 0:
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					connection.Close()
				default:
					w.Header().Set("Location", "/replayed")
					w.WriteHeader(status)
					io.WriteString(w, `{"error":"denied","version":3}`)
				}
			}))
			defer api.Close()
			t.Setenv("DROPLY_API_URL", api.URL)
			t.Setenv("DROPLY_TOKEN", "ci-secret")
			cmd := NewRootCmd("test")
			var output, diagnostics bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&diagnostics)
			cmd.SetArgs([]string{"deploy", source, "--sub", "alice", "--project", "site", "--preview", "--branch", "feature/中文", "--commit", "abc123", "--json"})
			err := cmd.Execute()
			if status == http.StatusOK {
				if err != nil || !json.Valid(output.Bytes()) {
					t.Fatalf("output=%q err=%v", output.String(), err)
				}
			} else if err == nil || output.Len() != 0 {
				t.Fatalf("failure output=%q err=%v", output.String(), err)
			}
			if calls.Load() != 1 {
				t.Fatalf("unsafe replay: %d calls", calls.Load())
			}
			if strings.Contains(diagnostics.String(), "ci-secret") {
				t.Fatal("secret in diagnostics")
			}
			if _, err := os.Stat(configPath()); !os.IsNotExist(err) {
				t.Fatalf("CI wrote global config: %v", err)
			}
		})
	}
}
