package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubdomainCRUD(t *testing.T) {
	srv := newTestServer(t)
	token := registerAndGetToken(t, srv, "alice@example.com", "password123")

	// Create subdomain
	body, _ := json.Marshal(map[string]string{"name": "alice"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// List subdomains
	req = httptest.NewRequest(http.MethodGet, "/subdomains", nil)
	req.Host = "api.droplydoc.com"
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list subdomains: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var subs []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&subs); err != nil {
		t.Fatalf("list subdomains: decode response: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("list subdomains: expected 1, got %d", len(subs))
	}
	if subs[0]["name"] != "alice" {
		t.Fatalf("list subdomains: expected name=alice, got %v", subs[0]["name"])
	}

	// Delete subdomain
	req = httptest.NewRequest(http.MethodDelete, "/subdomains/alice", nil)
	req.Host = "api.droplydoc.com"
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete subdomain: expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSubdomainInvalidName(t *testing.T) {
	srv := newTestServer(t)
	token := registerAndGetToken(t, srv, "bob@example.com", "password123")

	body, _ := json.Marshal(map[string]string{"name": "INVALID!"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSubdomainUnauthorized(t *testing.T) {
	srv := newTestServer(t)

	body, _ := json.Marshal(map[string]string{"name": "mysite"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}
