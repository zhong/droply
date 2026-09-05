package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandRequestCancellation(t *testing.T) {
	for _, command := range []string{"projects", "deploy"} {
		t.Run(command, func(t *testing.T) {
			withTempHome(t)
			t.Chdir(t.TempDir())
			started := make(chan struct{})
			released := make(chan struct{})
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				close(started)
				<-r.Context().Done()
				close(released)
			}))
			defer api.Close()
			t.Setenv("DROPLY_API_URL", api.URL)
			args := []string{command}
			if command == "deploy" {
				if err := os.WriteFile("index.html", []byte("hello"), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, ".", "--sub", "alice", "--project", "site", "--json")
			}
			cmd := NewRootCmd("test")
			var out, diagnostics bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&diagnostics)
			cmd.SetArgs(args)
			cmd.SilenceUsage = true
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			finished := make(chan error, 1)
			go func() { finished <- cmd.ExecuteContext(ctx) }()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("request did not start")
			}
			cancel()
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("command did not cancel")
			}
			select {
			case <-released:
			case <-time.After(5 * time.Second):
				t.Fatal("server request did not cancel")
			}
			if out.Len() != 0 {
				t.Fatalf("stdout = %q", out.String())
			}
		})
	}
}

func TestJSONAndUploadResponseErrors(t *testing.T) {
	for _, tc := range []struct {
		name, response, failure string
		status                  int
	}{
		{"API error", `{"error":"denied"}`, "API error: denied", 403},
		{"plain error", `offline`, "API error: status 503", 503},
		{"invalid JSON", `{`, "decode response:", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.response) }))
			defer api.Close()
			client := NewAPIClient(&Context{APIURL: api.URL})
			file := filepath.Join(t.TempDir(), "upload")
			if err := os.WriteFile(file, []byte("data"), 0600); err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			jsonErr := client.doJSONContext(t.Context(), "GET", "/", nil, &result)
			_, uploadErr := client.uploadFileContext(t.Context(), "/", file)
			for _, err := range []error{jsonErr, uploadErr} {
				if err == nil || !strings.HasPrefix(err.Error(), tc.failure) {
					t.Fatalf("error = %v", err)
				}
			}
		})
	}
}
