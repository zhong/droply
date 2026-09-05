package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectTokenPersistenceAndRevocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	project := deploymentProject(t, s)
	u, err := s.GetUserByToken("token")
	if err != nil {
		t.Fatal(err)
	}
	token, raw, err := s.CreateProjectToken(t.Context(), project, u.ID, "CI", nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, ProjectTokenPrefix) || len(token.Scopes) != 1 || token.Scopes[0] != "preview" || time.Until(token.ExpiresAt) < 29*24*time.Hour {
		t.Fatalf("unsafe defaults: %+v", token)
	}
	var digest string
	if err = s.db.QueryRow(`SELECT digest FROM project_tokens WHERE id=?`, token.ID).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	if digest == raw || len(digest) != 64 || digest != tokenDigest(raw) {
		t.Fatal("credential was not stored as SHA256 digest")
	}
	if _, _, err = s.CreateProjectToken(t.Context(), project, u.ID+1, "wrong owner", nil, time.Time{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-owner create: %v", err)
	}
	if err = s.RevokeProjectToken(t.Context(), project, u.ID+1, token.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-owner revoke: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := s.AuthenticateProjectToken(t.Context(), raw)
	if err != nil || authenticated.ID != token.ID || authenticated.OwnerID != u.ID {
		t.Fatalf("restart authentication: %+v %v", authenticated, err)
	}
	listed, err := s.ListProjectTokens(t.Context(), project, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), raw) || strings.Contains(string(encoded), digest) || strings.Contains(string(encoded), "digest") {
		t.Fatal("list leaks credential material")
	}
	if err = s.RevokeProjectToken(t.Context(), project, u.ID, token.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeProjectToken(t.Context(), project, u.ID, token.ID); err != nil {
		t.Fatal("revoke should be idempotent", err)
	}
	if _, err = s.AuthenticateProjectToken(t.Context(), raw); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("revoked accepted: %v", err)
	}
}

func TestProjectTokenExpirationScopesAndProjectDeletion(t *testing.T) {
	s := newTestStore(t)
	project := deploymentProject(t, s)
	u, err := s.GetUserByToken("token")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		scopes []string
		expiry time.Time
	}{{[]string{"admin"}, time.Time{}}, {nil, time.Now().Add(-time.Hour)}, {nil, time.Now().Add(366 * 24 * time.Hour)}} {
		if _, _, err = s.CreateProjectToken(t.Context(), project, u.ID, "ci", test.scopes, test.expiry); !errors.Is(err, ErrProjectTokenInput) {
			t.Fatalf("invalid token options accepted: %v", err)
		}
	}
	token, raw, err := s.CreateProjectToken(t.Context(), project, u.ID, "ci", []string{"production", "preview", "production"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(token.Scopes) != 2 {
		t.Fatal("scopes not normalized")
	}
	if _, err = s.db.Exec(`UPDATE project_tokens SET expires_at=? WHERE id=?`, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateProjectToken(t.Context(), raw); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expired token accepted: %v", err)
	}
	_, raw, err = s.CreateProjectToken(t.Context(), project, u.ID, "ci", nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.GetSubdomainByName("deploy")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteProject(sub.ID, "site"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateProjectToken(t.Context(), raw); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted project token accepted: %v", err)
	}
}
