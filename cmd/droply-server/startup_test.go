//go:build integration

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Invalid configuration must fail before creating persistent state.
func TestStartupValidationOrderAndFailureCleanup(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		failure string
	}{
		{"base domain", []string{"--domain", "invalid"}, "invalid base domain"},
		{"proxy", []string{"--trusted-proxies", "invalid"}, "trusted proxy"},
		{"WeCom", []string{"--wework-corp-id", "corp"}, "all four WeCom"},
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
				if !os.IsNotExist(err) {
					t.Fatalf("%s: invalid config created persistent state: %v", file, err)
				}
			}
		})
	}
}
