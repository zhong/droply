//go:build integration

package main

import (
	"context"
	"database/sql"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zhong/droply/internal/store"
)

func preflightOldData(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "droply.db"))
	if err != nil {
		t.Fatal(err)
	}
	// A fixed pre-admin users table, with SQLite user_version left at zero.
	_, err = db.Exec(`CREATE TABLE users (
 id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT NOT NULL UNIQUE,
 password TEXT NOT NULL, api_token TEXT NOT NULL UNIQUE,
 created_at DATETIME NOT NULL DEFAULT '2026-01-01 00:00:00');
 INSERT INTO users(email,password,api_token) VALUES('old@example.test','hash','old-token');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sites", "old", "project")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "index.html"), []byte("preserve legacy bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}
func preflightSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		snapshot[relative] = info.Mode().String()
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot[relative] += string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
func clearPreflightEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"DROPLY_WEWORK_CORP_ID", "DROPLY_WEWORK_AGENT_ID", "DROPLY_WEWORK_SECRET", "DROPLY_WEWORK_REDIRECT_URI", "DROPLY_CLOUDFLARE_API_TOKEN"} {
		t.Setenv(name, "")
	}
}
func TestPreflightPreservesOldDatabaseAndArtifacts(t *testing.T) {
	clearPreflightEnvironment(t)
	emptyToken := filepath.Join(t.TempDir(), "empty-token")
	if err := os.WriteFile(emptyToken, []byte(" \n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		args    []string
		failure string
	}{
		{"proxy", []string{"--trusted-proxies", "invalid"}, "trusted proxy"},
		{"wecom incomplete", []string{"--wework-corp-id", "corp"}, "all four WeCom"},
		{"wecom URL", []string{"--wework-corp-id", "corp", "--wework-agent-id", "agent", "--wework-secret", "secret", "--wework-redirect-uri", "invalid"}, "callback URL"},
		{"cloudflare missing", []string{"--tls-mode", "cloudflare"}, "requires a token"},
		{"cloudflare unreadable", []string{"--tls-mode", "cloudflare", "--cloudflare-token-file", emptyToken + "-missing"}, "cannot read"},
		{"cloudflare empty", []string{"--tls-mode", "cloudflare", "--cloudflare-token-file", emptyToken}, "requires a token"},
		{"ACME URL", []string{"--tls-mode", "auto", "--acme-ca", ":invalid"}, "ACME directory URL"},
		{"ACME port", []string{"--tls-mode", "auto", "--acme-ca", "https://ca.test:99999/directory"}, "ACME directory URL"},
		{"certificate directory", []string{"--tls-mode", "auto", "--cert-dir", emptyToken}, "not a directory"},
		{"certificate parent", []string{"--tls-mode", "auto", "--cert-dir", filepath.Join(emptyToken, "child")}, "certificate directory"},
		{"manual material", []string{"--tls-mode", "manual", "--tls-cert", emptyToken}, "load manual TLS"},
		{"deployment limit", []string{"--deploy-max-files", "-1"}, "must not be negative"},
		{"retention overflow", []string{"--deployment-retain-days", "100001"}, "must not exceed"},
		{"listener address", []string{"--addr", "invalid"}, "invalid listen address"},
		{"legacy cannot replace primary", []string{"--addr", "", "--site-addr", "127.0.0.1:0"}, "at least one listener"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := preflightOldData(t)
			before := preflightSnapshot(t, dir)
			args := append([]string{"--data-dir", dir, "--domain", "example.test", "--addr", "127.0.0.1:0"}, test.args...)
			err := run(t.Context(), args)
			if err == nil || !strings.Contains(err.Error(), test.failure) {
				t.Fatalf("error: %v", err)
			}
			if !reflect.DeepEqual(before, preflightSnapshot(t, dir)) {
				t.Fatal("invalid config changed old data, schema bytes, permissions or directory entries")
			}
		})
	}
}
func TestValidPreflightStillUpgradesOldData(t *testing.T) {
	clearPreflightEnvironment(t)
	dir := preflightOldData(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	addr := availableAddress(t)
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"--data-dir", dir, "--domain", "example.test", "--addr", addr}) }()
	waitForListener(t, addr, done)
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "api.example.test"
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status=%d", response.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "droply.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != store.SchemaVersion {
		t.Fatalf("schema=%d", version)
	}
	var admin int
	if err := db.QueryRow("SELECT is_admin FROM users WHERE email='old@example.test'").Scan(&admin); err != nil {
		t.Fatal(err)
	}
}
