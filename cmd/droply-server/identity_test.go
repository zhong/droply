package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhong/droply/internal/hosting"
	"github.com/zhong/droply/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func TestLocalAdministratorInitialization(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(secret, []byte("initial-password\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	args := []string{"init-admin", "--data-dir", dir, "--email", "admin@example.com", "--password-file", secret}
	handled, err := runIdentityCommand(t.Context(), args, &out)
	if !handled || err != nil {
		t.Fatal(handled, err)
	}
	if strings.Contains(out.String(), "initial-password") || strings.Contains(out.String(), "dp_") {
		t.Fatal("credential exposed")
	}
	st, err := store.NewSQLiteStore(filepath.Join(dir, "droply.db"))
	if err != nil {
		t.Fatal(err)
	}
	user, err := st.GetUserByEmail("admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !user.IsAdmin || bcrypt.CompareHashAndPassword([]byte(user.Password), []byte("initial-password")) != nil {
		t.Fatal("administrator not initialized")
	}
	st.Close()
	if _, err := runIdentityCommand(t.Context(), args, &out); err == nil {
		t.Fatal("bootstrap repeated")
	}
	lock, err := hosting.LockDataDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := runIdentityCommand(t.Context(), []string{"claim-admin", "--data-dir", dir, "--email", "admin@example.com"}, &out); err == nil {
		t.Fatal("active data directory accepted")
	}
}

func TestLocalAdministratorLegacyClaim(t *testing.T) {
	dir := t.TempDir()
	st, err := store.NewSQLiteStore(filepath.Join(dir, "droply.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser("legacy@example.com", "hash", "legacy-token"); err != nil {
		t.Fatal(err)
	}
	st.Close()
	var out bytes.Buffer
	if _, err := runIdentityCommand(t.Context(), []string{"claim-admin", "--data-dir", dir, "--email", "legacy@example.com"}, &out); err != nil {
		t.Fatal(err)
	}
	st, err = store.NewSQLiteStore(filepath.Join(dir, "droply.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	user, err := st.GetUserByToken("legacy-token")
	if err != nil || !user.IsAdmin {
		t.Fatal("legacy token or administrator lost", err)
	}
}
