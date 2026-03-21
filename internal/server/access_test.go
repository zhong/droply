package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func createSubdomain(t *testing.T, srv http.Handler, token, name string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("createSubdomain: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSetSubdomainAccess(t *testing.T) {
	srv := newTestServer(t)
	token := registerAndGetToken(t, srv, "access@example.com", "password123")
	createSubdomain(t, srv, token, "mysite")

	body, _ := json.Marshal(map[string]interface{}{
		"auto_password": true,
	})
	req := httptest.NewRequest(http.MethodPut, "/subdomains/mysite/access", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	gp, ok := resp["generated_password"].(string)
	if !ok || len(gp) != 16 {
		t.Fatalf("expected 16-char generated_password, got %q", gp)
	}

	if !resp["has_password"].(bool) {
		t.Fatal("expected has_password to be true")
	}
}

func TestGetSubdomainAccess(t *testing.T) {
	srv := newTestServer(t)
	token := registerAndGetToken(t, srv, "getaccess@example.com", "password123")
	createSubdomain(t, srv, token, "getsite")

	// Set access first.
	body, _ := json.Marshal(map[string]interface{}{
		"auto_password": true,
	})
	req := httptest.NewRequest(http.MethodPut, "/subdomains/getsite/access", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set access: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// GET access.
	req = httptest.NewRequest(http.MethodGet, "/subdomains/getsite/access", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// GET should not contain generated_password.
	if _, ok := resp["generated_password"]; ok {
		t.Fatal("GET should not return generated_password")
	}

	if !resp["has_password"].(bool) {
		t.Fatal("expected has_password to be true")
	}
}

func TestDeleteSubdomainAccess(t *testing.T) {
	srv := newTestServer(t)
	token := registerAndGetToken(t, srv, "delaccess@example.com", "password123")
	createSubdomain(t, srv, token, "delsite")

	// Set access.
	body, _ := json.Marshal(map[string]interface{}{
		"auto_password": true,
	})
	req := httptest.NewRequest(http.MethodPut, "/subdomains/delsite/access", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set access: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Delete access.
	req = httptest.NewRequest(http.MethodDelete, "/subdomains/delsite/access", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	// GET should now return 404.
	req = httptest.NewRequest(http.MethodGet, "/subdomains/delsite/access", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSetAccessForbiddenForNonOwner(t *testing.T) {
	srv := newTestServer(t)
	ownerToken := registerAndGetToken(t, srv, "owner@example.com", "password123")
	otherToken := registerAndGetToken(t, srv, "other@example.com", "password123")
	createSubdomain(t, srv, ownerToken, "ownersite")

	body, _ := json.Marshal(map[string]interface{}{
		"auto_password": true,
	})
	req := httptest.NewRequest(http.MethodPut, "/subdomains/ownersite/access", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+otherToken)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSetAccessValidation(t *testing.T) {
	srv := newTestServer(t)
	token := registerAndGetToken(t, srv, "valid@example.com", "password123")
	createSubdomain(t, srv, token, "validsite")

	// Empty rule should fail.
	body, _ := json.Marshal(map[string]interface{}{})
	req := httptest.NewRequest(http.MethodPut, "/subdomains/validsite/access", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty rule: expected 400, got %d: %s", rr.Code, rr.Body.String())
	}

	// Short password should fail.
	body, _ = json.Marshal(map[string]interface{}{
		"password": "short",
	})
	req = httptest.NewRequest(http.MethodPut, "/subdomains/validsite/access", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("short password: expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}
