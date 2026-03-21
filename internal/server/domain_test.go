package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
)

// setupDomainTest creates a server with a real sites dir, registers a user, creates subdomain
// "alice", and deploys a minimal tar.gz to create project "blog". Returns (srv, token).
func setupDomainTest(t *testing.T) (*server.Server, string) {
	t.Helper()
	sitesDir := t.TempDir()
	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	srv := server.New(st, sitesDir, "droplydoc.com", nil)
	token := registerAndGetToken(t, srv, "alice@example.com", "password123")

	// Create subdomain "alice".
	body, _ := json.Marshal(map[string]string{"name": "alice"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// Deploy a minimal tar.gz to create project "blog".
	archive := createTestTarGz(t, map[string]string{
		"index.html": "<html><body>blog</body></html>",
	})
	req = buildDeployRequest(t, "/subdomains/alice/projects/blog/deploy", archive, token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("deploy blog: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	return srv, token
}

func TestCustomDomainCRUD(t *testing.T) {
	srv, token := setupDomainTest(t)

	// Add custom domain "blog.alice.com".
	body, _ := json.Marshal(map[string]string{"domain": "blog.alice.com"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains/alice/projects/blog/domains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create domain: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var created map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&created); err != nil {
		t.Fatalf("decode create domain response: %v", err)
	}
	if verified, ok := created["verified"].(bool); !ok || verified {
		t.Fatalf("expected verified=false, got %v", created["verified"])
	}
	if cnameTarget, ok := created["cname_target"].(string); !ok || cnameTarget == "" {
		t.Fatalf("expected non-empty cname_target, got %v", created["cname_target"])
	}

	// List custom domains — expect 1.
	req = httptest.NewRequest(http.MethodGet, "/subdomains/alice/projects/blog/domains", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list domains: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var domains []map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&domains); err != nil {
		t.Fatalf("decode list domains response: %v", err)
	}
	if len(domains) != 1 {
		t.Fatalf("expected 1 domain, got %d", len(domains))
	}

	// Delete the custom domain.
	req = httptest.NewRequest(http.MethodDelete, "/subdomains/alice/projects/blog/domains/blog.alice.com", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete domain: expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
}
