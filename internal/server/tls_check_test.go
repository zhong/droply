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

func TestTLSCheckBaseDomain(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/_droply/tls-check?domain=droplydoc.com", nil)
	req.Host = "api.droplydoc.com"
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for base domain, got %d", rr.Code)
	}
}

func TestTLSCheckAPIHost(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/_droply/tls-check?domain=api.droplydoc.com", nil)
	req.Host = "api.droplydoc.com"
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for api.{base}, got %d", rr.Code)
	}
}

func TestTLSCheckExistingSubdomain(t *testing.T) {
	srv := newTestServer(t)
	token := registerAndGetToken(t, srv, "tls@example.com", "password123")

	// Register subdomain "alice".
	body, _ := json.Marshal(map[string]string{"name": "alice"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: %d %s", rr.Code, rr.Body.String())
	}

	// alice.droplydoc.com should be allowed.
	req = httptest.NewRequest(http.MethodGet, "/_droply/tls-check?domain=alice.droplydoc.com", nil)
	req.Host = "api.droplydoc.com"
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for registered subdomain, got %d", rr.Code)
	}
}

func TestTLSCheckUnknownSubdomain(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/_droply/tls-check?domain=ghost.droplydoc.com", nil)
	req.Host = "api.droplydoc.com"
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 for unknown subdomain, got %d", rr.Code)
	}
}

func TestTLSCheckNestedNotAllowed(t *testing.T) {
	srv := newTestServer(t)

	// Two-label subdomain like foo.bar.droplydoc.com should be rejected
	// (subdomains are single-label only).
	req := httptest.NewRequest(http.MethodGet, "/_droply/tls-check?domain=foo.bar.droplydoc.com", nil)
	req.Host = "api.droplydoc.com"
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 for nested subdomain, got %d", rr.Code)
	}
}

func TestTLSCheckUnknownDomain(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/_droply/tls-check?domain=evil.com", nil)
	req.Host = "api.droplydoc.com"
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 for unknown domain, got %d", rr.Code)
	}
}

func TestTLSCheckMissingDomain(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/_droply/tls-check", nil)
	req.Host = "api.droplydoc.com"
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing domain, got %d", rr.Code)
	}
}

func TestTLSCheckVerifiedCustomDomain(t *testing.T) {
	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv := server.New(st, "/tmp/sites", "droplydoc.com", []byte("test-hmac-key-1234567890abcdef"))

	token := registerAndGetToken(t, srv, "cd@example.com", "password123")

	// Setup: create subdomain + project.
	body, _ := json.Marshal(map[string]string{"name": "cdsite"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: %d", rr.Code)
	}

	// Deploy creates the project.
	deployProject(t, srv, token, "cdsite", "app", map[string]string{"index.html": "ok"})

	// Add custom domain.
	body, _ = json.Marshal(map[string]string{"domain": "blog.example.com"})
	req = httptest.NewRequest(http.MethodPost, "/subdomains/cdsite/projects/app/domains", bytes.NewReader(body))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("add custom domain: %d %s", rr.Code, rr.Body.String())
	}

	// Unverified → should be rejected.
	req = httptest.NewRequest(http.MethodGet, "/_droply/tls-check?domain=blog.example.com", nil)
	req.Host = "api.droplydoc.com"
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 for unverified custom domain, got %d", rr.Code)
	}

	// Verify the domain at the store level (bypasses the DNS check in the API).
	if err := st.VerifyCustomDomain("blog.example.com"); err != nil {
		t.Fatalf("VerifyCustomDomain: %v", err)
	}

	// Now should be accepted.
	req = httptest.NewRequest(http.MethodGet, "/_droply/tls-check?domain=blog.example.com", nil)
	req.Host = "api.droplydoc.com"
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for verified custom domain, got %d", rr.Code)
	}
}

func TestTLSCheckCaseInsensitive(t *testing.T) {
	srv := newTestServer(t)
	token := registerAndGetToken(t, srv, "case@example.com", "password123")

	body, _ := json.Marshal(map[string]string{"name": "casesite"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/_droply/tls-check?domain=CaseSite.DroplyDoc.com", nil)
	req.Host = "api.droplydoc.com"
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for upper-case domain, got %d", rr.Code)
	}
}

func TestTLSCheckStripsPort(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/_droply/tls-check?domain=api.droplydoc.com:443", nil)
	req.Host = "api.droplydoc.com"
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 with port stripped, got %d", rr.Code)
	}
}
