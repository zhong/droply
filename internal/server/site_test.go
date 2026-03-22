package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
)

func newTestSiteServer(t *testing.T) (*server.Server, string) {
	t.Helper()
	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	sitesDir := t.TempDir()
	srv := server.New(st, sitesDir, "droplydoc.com", nil, []byte("test-hmac-secret-key-1234567890"), "localhost:8081")
	return srv, sitesDir
}

// deployProject creates a subdomain (if needed) and deploys a project with files via the API.
// Returns after the project exists in both the DB and on disk.
func deployProject(t *testing.T, srv *server.Server, token, subName, projName string, files map[string]string) {
	t.Helper()
	archive := createTestTarGz(t, files)
	req := buildDeployRequest(t, "/subdomains/"+subName+"/projects/"+projName+"/deploy", archive, token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("deploy %s/%s: expected 200, got %d: %s", subName, projName, rr.Code, rr.Body.String())
	}
}

// setupProtectedSite registers a user, creates a subdomain, deploys a project with files,
// and sets a password access rule. Returns the API token and the plain-text password.
func setupProtectedSite(t *testing.T, srv *server.Server, sitesDir string) (token, password string) {
	t.Helper()
	token = registerAndGetToken(t, srv, "siteuser@example.com", "userpassword123")

	// Create subdomain via API.
	body, _ := json.Marshal(map[string]string{"name": "alice"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// Deploy project with a file (creates project in DB + on disk).
	deployProject(t, srv, token, "alice", "docs", map[string]string{
		"hello.txt": "Hello Droply",
	})

	// Set password access rule.
	accessBody, _ := json.Marshal(map[string]interface{}{
		"auto_password": true,
	})
	req = httptest.NewRequest(http.MethodPut, "/subdomains/alice/projects/docs/access", bytes.NewReader(accessBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set access: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode access response: %v", err)
	}
	password = resp["generated_password"].(string)
	return token, password
}

func TestSiteHandlerNoRule(t *testing.T) {
	srv, _ := newTestSiteServer(t)
	token := registerAndGetToken(t, srv, "norule@example.com", "password123")

	// Create subdomain.
	body, _ := json.Marshal(map[string]string{"name": "bob"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// Deploy project with files.
	deployProject(t, srv, token, "bob", "site", map[string]string{
		"hello.txt": "Public Content",
	})

	// Request via site handler.
	siteHandler := srv.NewSiteHandler()
	req = httptest.NewRequest(http.MethodGet, "/site/hello.txt", nil)
	req.Host = "bob.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Public Content") {
		t.Fatalf("expected body to contain 'Public Content', got %q", rr.Body.String())
	}
}

func TestSiteHandlerTrailingSlashRedirect(t *testing.T) {
	srv, _ := newTestSiteServer(t)
	token := registerAndGetToken(t, srv, "carol@example.com", "password123")

	body, _ := json.Marshal(map[string]string{"name": "carol"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	deployProject(t, srv, token, "carol", "site", map[string]string{
		"index.html": "<html><link rel='stylesheet' href='style.css'></html>",
		"style.css":  "body{}",
	})

	siteHandler := srv.NewSiteHandler()

	// Request /site (no trailing slash) should redirect to /site/
	req = httptest.NewRequest(http.MethodGet, "/site", nil)
	req.Host = "carol.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301 redirect, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if loc != "/site/" {
		t.Fatalf("expected redirect to /site/, got %s", loc)
	}

	// Request /site/ (with trailing slash) should serve index.html
	req = httptest.NewRequest(http.MethodGet, "/site/", nil)
	req.Host = "carol.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSiteHandlerPasswordRequired(t *testing.T) {
	srv, sitesDir := newTestSiteServer(t)
	setupProtectedSite(t, srv, sitesDir)

	siteHandler := srv.NewSiteHandler()
	req := httptest.NewRequest(http.MethodGet, "/docs/hello.txt", nil)
	req.Host = "alice.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (login page), got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "_droply/login") {
		t.Fatalf("expected login page with _droply/login, got %q", rr.Body.String())
	}
}

func TestSiteHandlerLoginSuccess(t *testing.T) {
	srv, sitesDir := newTestSiteServer(t)
	_, password := setupProtectedSite(t, srv, sitesDir)

	siteHandler := srv.NewSiteHandler()

	// POST login.
	form := url.Values{}
	form.Set("password", password)
	form.Set("redirect", "/docs/hello.txt")
	form.Set("host", "alice.droplydoc.com")
	req := httptest.NewRequest(http.MethodPost, "/_droply/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "alice.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", rr.Code, rr.Body.String())
	}

	// Extract Set-Cookie header.
	cookies := rr.Result().Cookies()
	var accessCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "_droply_access" {
			accessCookie = c
			break
		}
	}
	if accessCookie == nil {
		t.Fatal("expected _droply_access cookie to be set")
	}

	// Use cookie to access the protected site.
	req = httptest.NewRequest(http.MethodGet, "/docs/hello.txt", nil)
	req.Host = "alice.droplydoc.com"
	req.AddCookie(accessCookie)
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Hello Droply") {
		t.Fatalf("expected body to contain 'Hello Droply', got %q", rr.Body.String())
	}
}

func TestSiteHandlerLoginWrongPassword(t *testing.T) {
	srv, sitesDir := newTestSiteServer(t)
	setupProtectedSite(t, srv, sitesDir)

	siteHandler := srv.NewSiteHandler()

	form := url.Values{}
	form.Set("password", "wrongpassword1234")
	form.Set("redirect", "/docs/hello.txt")
	form.Set("host", "alice.droplydoc.com")
	req := httptest.NewRequest(http.MethodPost, "/_droply/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "alice.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (login page with error), got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Incorrect password") {
		t.Fatalf("expected 'Incorrect password' in body, got %q", rr.Body.String())
	}
}

func TestSiteHandlerIPBlocked(t *testing.T) {
	srv, _ := newTestSiteServer(t)
	token := registerAndGetToken(t, srv, "ipblock@example.com", "password123")

	// Create subdomain.
	body, _ := json.Marshal(map[string]string{"name": "ipsite"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// Deploy project.
	deployProject(t, srv, token, "ipsite", "proj", map[string]string{
		"hello.txt": "IP Protected",
	})

	// Set IP-only access rule (only allow 10.0.0.1).
	accessBody, _ := json.Marshal(map[string]interface{}{
		"allowed_ips": []string{"10.0.0.1"},
	})
	req = httptest.NewRequest(http.MethodPut, "/subdomains/ipsite/projects/proj/access", bytes.NewReader(accessBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set access: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Request from a different IP.
	siteHandler := srv.NewSiteHandler()
	req = httptest.NewRequest(http.MethodGet, "/proj/hello.txt", nil)
	req.Host = "ipsite.droplydoc.com"
	req.RemoteAddr = "192.168.1.1:12345"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSiteHandlerIPAllowed(t *testing.T) {
	srv, _ := newTestSiteServer(t)
	token := registerAndGetToken(t, srv, "ipallow@example.com", "password123")

	// Create subdomain.
	body, _ := json.Marshal(map[string]string{"name": "ipallow"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// Deploy project.
	deployProject(t, srv, token, "ipallow", "proj", map[string]string{
		"hello.txt": "Allowed Content",
	})

	// Set IP-only access rule.
	accessBody, _ := json.Marshal(map[string]interface{}{
		"allowed_ips": []string{"10.0.0.0/8"},
	})
	req = httptest.NewRequest(http.MethodPut, "/subdomains/ipallow/projects/proj/access", bytes.NewReader(accessBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set access: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Request from an allowed IP.
	siteHandler := srv.NewSiteHandler()
	req = httptest.NewRequest(http.MethodGet, "/proj/hello.txt", nil)
	req.Host = "ipallow.droplydoc.com"
	req.RemoteAddr = "10.1.2.3:12345"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Allowed Content") {
		t.Fatalf("expected 'Allowed Content', got %q", rr.Body.String())
	}
}

func TestSiteHandlerRateLimiting(t *testing.T) {
	srv, sitesDir := newTestSiteServer(t)
	setupProtectedSite(t, srv, sitesDir)

	siteHandler := srv.NewSiteHandler()

	// Make 11 login attempts; the 11th should be rate limited.
	var lastCode int
	for i := 0; i < 11; i++ {
		form := url.Values{}
		form.Set("password", "wrongpassword1234")
		form.Set("redirect", "/docs/hello.txt")
		form.Set("host", "alice.droplydoc.com")
		req := httptest.NewRequest(http.MethodPost, "/_droply/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Host = "alice.droplydoc.com"
		req.RemoteAddr = "1.2.3.4:9999"
		rr := httptest.NewRecorder()
		siteHandler.ServeHTTP(rr, req)
		lastCode = rr.Code
	}

	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 11th attempt, got %d", lastCode)
	}
}

func TestSiteHandlerCustomDomain(t *testing.T) {
	srv, _ := newTestSiteServer(t)

	siteHandler := srv.NewSiteHandler()

	// Request with an unknown host should get 404.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "unknown.example.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}
