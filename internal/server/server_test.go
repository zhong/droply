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

// newTestServer creates a Server backed by an in-memory SQLite store for testing.
func newTestServer(t *testing.T) *server.Server {
	t.Helper()
	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return server.New(st, "/tmp/sites", "droplydoc.com", []byte("test-hmac-key-for-testing-1234"))
}

// registerAndGetToken registers a new user and returns their API token.
// This helper is intended to be used by subsequent test files in this package.
func registerAndGetToken(t *testing.T, srv http.Handler, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("registerAndGetToken: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("registerAndGetToken: decode response: %v", err)
	}
	token, ok := resp["api_token"]
	if !ok || token == "" {
		t.Fatalf("registerAndGetToken: no api_token in response")
	}
	return token
}

func TestRegister(t *testing.T) {
	srv := newTestServer(t)

	body, _ := json.Marshal(map[string]string{
		"email":    "alice@example.com",
		"password": "secret123",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	token, ok := resp["api_token"]
	if !ok || token == "" {
		t.Fatal("expected non-empty api_token in response")
	}
	if len(token) < 4 || token[:3] != "dp_" {
		t.Fatalf("unexpected token format: %q", token)
	}
}

func TestRegisterDuplicate(t *testing.T) {
	srv := newTestServer(t)

	// Register once
	registerAndGetToken(t, srv, "bob@example.com", "password1")

	// Register again with same email
	body, _ := json.Marshal(map[string]string{
		"email":    "bob@example.com",
		"password": "password2",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestLogin(t *testing.T) {
	srv := newTestServer(t)
	registerAndGetToken(t, srv, "carol@example.com", "mypassword")

	body, _ := json.Marshal(map[string]string{
		"email":    "carol@example.com",
		"password": "mypassword",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	token, ok := resp["api_token"]
	if !ok || token == "" {
		t.Fatal("expected non-empty api_token in response")
	}
}

func TestLoginWrongPassword(t *testing.T) {
	srv := newTestServer(t)
	registerAndGetToken(t, srv, "dave@example.com", "correctpassword")

	body, _ := json.Marshal(map[string]string{
		"email":    "dave@example.com",
		"password": "wrongpassword",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}
