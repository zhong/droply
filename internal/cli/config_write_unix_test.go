//go:build darwin || linux

package cli

import (
	"bytes"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A subprocess limits file writes without changing the test runner's limits.
func TestSaveFullConfigDiskWriteFailure(t *testing.T) {
	if os.Getenv("DROPLY_TEST_CONFIG_WRITE_LIMIT") == "1" {
		signal.Ignore(syscall.SIGXFSZ)
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 128, Max: 128}); err != nil {
			t.Fatal(err)
		}
		err := SaveFullConfig(&Config{Contexts: map[string]Context{"new": {Token: strings.Repeat("x", 1024)}}})
		if err == nil || !strings.Contains(err.Error(), "write config") {
			t.Fatalf("expected disk write failure: %v", err)
		}
		return
	}
	withTempHome(t)
	if err := SaveFullConfig(&Config{CurrentContext: "original"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(configPath())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSaveFullConfigDiskWriteFailure$")
	cmd.Env = append(os.Environ(), "DROPLY_TEST_CONFIG_WRITE_LIMIT=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("write-limited subprocess: %s %v", output, err)
	}
	after, err := os.ReadFile(configPath())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("original truncated: %q %v", after, err)
	}
	files, err := filepath.Glob(filepath.Join(configDir(), ".config-*.toml"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary files left: %v %v", files, err)
	}
}
