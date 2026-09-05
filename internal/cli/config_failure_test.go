package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestConfigErrorsStopCommands(t *testing.T) {
	for _, fault := range []string{"invalid TOML", "unreadable file", "directory", "unknown flag", "unknown project", "unknown current", "invalid project", "empty file"} {
		t.Run(fault, func(t *testing.T) {
			withTempHome(t)
			t.Chdir(t.TempDir())
			var requests atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				io.WriteString(w, "[]")
			}))
			defer api.Close()
			t.Setenv("DROPLY_API_URL", api.URL)
			t.Setenv("DROPLY_TOKEN", "secret")
			if err := SaveFullConfig(&Config{CurrentContext: "saved", Contexts: map[string]Context{"saved": {APIURL: api.URL}}}); err != nil {
				t.Fatal(err)
			}
			args := []string{"projects"}
			switch fault {
			case "invalid TOML", "empty file":
				contents := ""
				if fault == "invalid TOML" {
					contents = "[broken"
				}
				if err := os.WriteFile(configPath(), []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			case "unreadable file":
				if err := os.Chmod(configPath(), 0000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(configPath(), 0600) })
				if _, err := os.ReadFile(configPath()); err == nil {
					t.Skip("process can read files without permission")
				}
			case "directory":
				if err := os.Remove(configPath()); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(configPath(), 0700); err != nil {
					t.Fatal(err)
				}
			case "unknown flag":
				args = append(args, "--context", "missing")
			case "unknown project", "invalid project":
				contents := "context = \"missing\""
				if fault == "invalid project" {
					contents = "[broken"
				}
				if err := os.WriteFile(".droply.toml", []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			case "unknown current":
				if err := SaveFullConfig(&Config{CurrentContext: "missing", Contexts: map[string]Context{"saved": {APIURL: api.URL}}}); err != nil {
					t.Fatal(err)
				}
			}
			cmd := NewRootCmd("test")
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err == nil {
				t.Fatal("invalid config accepted")
			}
			if requests.Load() != 0 {
				t.Fatalf("sent %d requests despite invalid configuration", requests.Load())
			}
		})
	}
}

func TestConfigErrorsReachLocalAndLoginCommands(t *testing.T) {
	withTempHome(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(configDir(), 0700); err != nil {
		t.Fatal(err)
	}
	broken := []byte("[broken")
	if err := os.WriteFile(configPath(), broken, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"whoami"}, {"logout"}, {"login"}, {"register"}, {"context", "list"}, {"context", "show"}, {"context", "add", "other", "--api-url", "https://example.test"}, {"context", "use", "default"}, {"context", "remove", "default"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cmd := NewRootCmd("test")
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "read config") {
				t.Fatalf("error = %v", err)
			}
			after, err := os.ReadFile(configPath())
			if err != nil || !bytes.Equal(after, broken) {
				t.Fatalf("config changed: %q, %v", after, err)
			}
		})
	}
}

func TestSaveFullConfigFailurePreservesOriginal(t *testing.T) {
	withTempHome(t)
	original := &Config{CurrentContext: "saved", Contexts: map[string]Context{"saved": {Token: "keep"}}}
	if err := SaveFullConfig(original); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(configPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configDir(), 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(configDir(), 0700) })
	probe, err := os.CreateTemp(configDir(), "permission-probe")
	if err == nil {
		probe.Close()
		os.Remove(probe.Name())
		t.Skip("process can write directories without permission")
	}
	if err := SaveFullConfig(&Config{CurrentContext: "changed"}); err == nil {
		t.Fatal("expected save failure")
	}
	after, err := os.ReadFile(configPath())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("original config changed: %q, %v", after, err)
	}
}

func TestSaveFullConfigAtomicReplacement(t *testing.T) {
	withTempHome(t)
	if err := SaveFullConfig(&Config{CurrentContext: "old"}); err != nil {
		t.Fatal(err)
	}
	old, err := os.Open(configPath())
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	before, err := io.ReadAll(old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	saved := &Config{CurrentContext: "new", LegacyToken: "legacy", Contexts: map[string]Context{"new": {Token: "new"}}}
	if err := SaveFullConfig(saved); err != nil {
		t.Fatal(err)
	}
	after, err := io.ReadAll(old)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("existing inode was modified: %q, %v", after, err)
	}
	if got := mustLoadFullConfig(t); got.CurrentContext != "new" {
		t.Fatalf("replacement not published: %+v", got)
	}
	if saved.LegacyToken != "legacy" {
		t.Fatal("save modified caller's config")
	}
	info, err := os.Stat(configPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
	files, err := filepath.Glob(filepath.Join(configDir(), ".config-*.toml"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary files left: %v %v", files, err)
	}
}

func TestLoginUnknownContextRequiresExplicitAPIURL(t *testing.T) {
	withTempHome(t)
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"login", "--context", "missing"}, {"register", "--context", "missing"}} {
		cmd := NewRootCmd("test")
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), `context "missing" not found`) {
			t.Fatalf("unknown login context: %v", err)
		}
	}
	cmd := newLoginCmd()
	if err := cmd.ParseFlags([]string{"--context", "new", "--api-url", "https://new.test"}); err != nil {
		t.Fatal(err)
	}
	name, ctx, err := resolveLoginTarget(cmd)
	if err != nil || name != "new" || ctx.APIURL != "https://new.test" {
		t.Fatalf("explicit new target: %q %+v %v", name, ctx, err)
	}
}
