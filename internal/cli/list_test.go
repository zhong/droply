package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectListEntrypoints(t *testing.T) {
	for _, prefix := range [][]string{{"list"}, {"project", "list"}} {
		for _, tc := range []struct {
			name, response, output, failure string
			status                          int
		}{
			{"populated", `[{"name":"one"},{"name":"two"}]`, "alice/one\nalice/two\n", "", 200},
			{"empty", `[]`, "No projects found in subdomain \"alice\".\n", "", 200},
			{"API error", `{"error":"denied"}`, "", "API error: denied", 403},
			{"plain error", `offline`, "", "API error: status 503", 503},
		} {
			t.Run(strings.Join(prefix, " ")+"/"+tc.name, func(t *testing.T) {
				withTempHome(t)
				t.Chdir(t.TempDir())
				api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" || r.URL.Path != "/subdomains/alice/projects" {
						t.Errorf("request = %s %s", r.Method, r.URL.Path)
					}
					w.WriteHeader(tc.status)
					io.WriteString(w, tc.response)
				}))
				defer api.Close()
				t.Setenv("DROPLY_API_URL", api.URL)
				cmd := NewRootCmd("test")
				var out, diagnostics bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&diagnostics)
				cmd.SilenceUsage = true
				cmd.SetArgs(append(append([]string{}, prefix...), "--sub", "alice"))
				err := cmd.Execute()
				if tc.failure == "" && err != nil || tc.failure != "" && (err == nil || err.Error() != tc.failure) {
					t.Fatalf("error = %v", err)
				}
				if out.String() != tc.output {
					t.Fatalf("stdout = %q", out.String())
				}
				if tc.failure == "" && diagnostics.Len() != 0 || tc.failure != "" && !strings.Contains(diagnostics.String(), tc.failure) {
					t.Fatalf("stderr = %q", diagnostics.String())
				}
			})
		}
	}
}
