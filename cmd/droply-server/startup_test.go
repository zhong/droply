//go:build integration

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhong/droply/internal/hosting"
)

// This structural step retains the existing validation order. The follow-up
// preflight change will intentionally move the late failures before data writes.
func TestStartupValidationOrderAndFailureCleanup(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		failure    string
		dataOpened bool
	}{
		{"base domain", []string{"--domain", "invalid"}, "invalid base domain", false},
		{"proxy", []string{"--trusted-proxies", "invalid"}, "trusted proxy", true},
		{"WeCom", []string{"--wework-corp-id", "corp"}, "all four WeCom", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DROPLY_WEWORK_CORP_ID", "")
			t.Setenv("DROPLY_WEWORK_AGENT_ID", "")
			t.Setenv("DROPLY_WEWORK_SECRET", "")
			t.Setenv("DROPLY_WEWORK_REDIRECT_URI", "")
			data := filepath.Join(t.TempDir(), "data")
			args := append([]string{"--data-dir", data, "--domain", "example.test", "--addr", "127.0.0.1:0"}, tc.args...)
			if err := run(t.Context(), args); err == nil || !strings.Contains(err.Error(), tc.failure) {
				t.Fatalf("startup error = %v", err)
			}
			for _, file := range []string{"droply.db", "hmac.key"} {
				_, err := os.Stat(filepath.Join(data, file))
				if tc.dataOpened && err != nil || !tc.dataOpened && !os.IsNotExist(err) {
					t.Fatalf("%s: expected dataOpened=%t, stat=%v", file, tc.dataOpened, err)
				}
			}
			if tc.dataOpened {
				lock, err := hosting.LockDataDirectory(data)
				if err != nil {
					t.Fatalf("failed startup leaked data lock: %v", err)
				}
				if err := lock.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
