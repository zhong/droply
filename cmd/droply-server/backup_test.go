package main

import (
	"bytes"
	"github.com/zhong/droply/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupCommandRoundTrip(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewSQLiteStore(filepath.Join(source, "droply.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "hmac.key"), bytes.Repeat([]byte{1}, 32), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "service.env")
	if err = os.WriteFile(config, []byte("--domain example.test"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "backup.tar.gz")
	handled, err := runBackupCommand([]string{"backup", "--data-dir", source, "--output", archive, "--config", config})
	if !handled || err != nil {
		t.Fatalf("backup: %v %v", handled, err)
	}
	target := filepath.Join(root, "restored")
	handled, err = runBackupCommand([]string{"restore", "--input", archive, "--data-dir", target})
	if !handled || err != nil {
		t.Fatalf("restore: %v %v", handled, err)
	}
	if _, err = os.Stat(filepath.Join(target, "droply.db")); err != nil {
		t.Fatal(err)
	}
}
func TestBackupCommandArguments(t *testing.T) {
	for _, args := range [][]string{{"backup"}, {"restore"}, {"backup", "--unknown"}, {"restore", "--max-files", "0"}, {"backup", "extra"}} {
		if handled, err := runBackupCommand(args); !handled || err == nil {
			t.Fatalf("%v accepted: %v %v", args, handled, err)
		}
	}
	if handled, err := runBackupCommand([]string{"--domain", "example.test"}); handled || err != nil {
		t.Fatal("intercepted serving")
	}
}
