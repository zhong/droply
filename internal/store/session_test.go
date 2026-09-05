package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConsoleSessionDigestExpiryAndRevocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	user, err := s.CreateUser("session@test", "hash", "long-lived-token")
	if err != nil {
		t.Fatal(err)
	}
	session, raw, err := s.CreateConsoleSession(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 52 || session.UserID != user.ID || time.Until(session.ExpiresAt) > ConsoleSessionLifetime {
		t.Fatal("invalid session")
	}
	var digest string
	if err = s.db.QueryRow(`SELECT digest FROM console_sessions`).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	if digest == raw || strings.Contains(digest, raw) || len(digest) != 64 {
		t.Fatal("session not hashed")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetConsoleSession(t.Context(), raw); err != nil {
		t.Fatal("session lost on restart", err)
	}
	if _, err = s.db.Exec(`UPDATE console_sessions SET expires_at=?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetConsoleSession(t.Context(), raw); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("expired session accepted", err)
	}
	_, raw, err = s.CreateConsoleSession(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeConsoleSession(t.Context(), raw); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetConsoleSession(t.Context(), raw); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("revoked session accepted", err)
	}
}
