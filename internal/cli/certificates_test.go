//go:build integration

package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCertificateCommandHTTP(t *testing.T) {
	for _, test := range []struct {
		name, body string
		code       int
		wantError  string
	}{
		{"pending", `{"domain":"site.example.com","state":"pending"}`, 200, ""},
		{"ready", `{"domain":"site.example.com","state":"ready","expires_at":"2026-12-01T00:00:00Z"}`, 200, ""},
		{"renewal failure", `{"domain":"site.example.com","state":"error","expires_at":"2026-12-01T00:00:00Z","last_error":"dns_cleanup_failed","retry_at":"2026-09-06T00:00:00Z"}`, 200, ""},
		{"external", `{"domain":"site.example.com","state":"externally-managed"}`, 200, ""},
		{"forbidden", `{"error":"forbidden"}`, 403, "forbidden"},
		{"unauthorized", `{"error":"unauthorized"}`, 401, "unauthorized"},
	} {
		t.Run(test.name, func(t *testing.T) {
			withTempHome(t)
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/certificates/site.example.com" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer dp_test" {
					t.Error("missing active context token")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.code)
				_, _ = w.Write([]byte(test.body))
			}))
			defer srv.Close()
			if err := SaveFullConfig(&Config{CurrentContext: "integration", Contexts: map[string]Context{"integration": {APIURL: srv.URL, Token: "dp_test"}}}); err != nil {
				t.Fatal(err)
			}
			cmd := NewRootCmd("test")
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs([]string{"certificate", "site.example.com"})
			err := cmd.Execute()
			if calls != 1 {
				t.Fatalf("HTTP requests: %d", calls)
			}
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got, want map[string]any
			if err = json.Unmarshal(output.Bytes(), &got); err != nil {
				t.Fatalf("output is not JSON: %s", output.String())
			}
			if err = json.Unmarshal([]byte(test.body), &want); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(want) {
				t.Fatalf("fields lost: %+v", got)
			}
			for key, value := range want {
				if got[key] != value {
					t.Fatalf("%s = %v, want %v", key, got[key], value)
				}
			}
		})
	}
}
