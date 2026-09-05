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
	srv := server.New(st, sitesDir, "droplydoc.com", []byte("test-hmac-secret-key-1234567890"))
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
	req.Host = "api.droplydoc.com"
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
	req.Host = "api.droplydoc.com"
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
	req.Host = "api.droplydoc.com"
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
	req.Host = "api.droplydoc.com"
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
	req.Host = "api.droplydoc.com"
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
	req.Host = "api.droplydoc.com"
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
	req.Host = "api.droplydoc.com"
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
	req.Host = "api.droplydoc.com"
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

	// Burst concurrently: sequential bcrypt checks can cross the refill interval
	// under the race detector, legitimately allowing the eleventh request.
	codes := make(chan int, 11)
	start := make(chan struct{})
	for i := 0; i < 11; i++ {
		go func() {
			<-start
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
			codes <- rr.Code
		}()
	}
	close(start)
	limited := 0
	for range 11 {
		if code := <-codes; code == http.StatusTooManyRequests {
			limited++
		} else if code != http.StatusOK {
			t.Errorf("unexpected login status: %d", code)
		}
	}
	if limited != 1 {
		t.Fatalf("expected one rate-limited request in burst, got %d", limited)
	}
}

func TestSiteHandlerDeletedSubdomainReturns404(t *testing.T) {
	srv, _ := newTestSiteServer(t)
	token := registerAndGetToken(t, srv, "deltest@example.com", "password123")

	// Create subdomain and deploy a project.
	body, _ := json.Marshal(map[string]string{"name": "delme"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	deployProject(t, srv, token, "delme", "site", map[string]string{
		"hello.txt": "Should Not Be Served After Delete",
	})

	siteHandler := srv.NewSiteHandler()

	// Verify it's accessible before deletion.
	req = httptest.NewRequest(http.MethodGet, "/site/hello.txt", nil)
	req.Host = "delme.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("before delete: expected 200, got %d", rr.Code)
	}

	// Delete the subdomain via API.
	req = httptest.NewRequest(http.MethodDelete, "/subdomains/delme", nil)
	req.Host = "api.droplydoc.com"
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete subdomain: expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	// After deletion, the site should return 404.
	req = httptest.NewRequest(http.MethodGet, "/site/hello.txt", nil)
	req.Host = "delme.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("after delete: expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSiteHandlerDeletedProjectReturns404(t *testing.T) {
	srv, _ := newTestSiteServer(t)
	token := registerAndGetToken(t, srv, "projdel@example.com", "password123")

	// Create subdomain and deploy two projects.
	body, _ := json.Marshal(map[string]string{"name": "keeper"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d", rr.Code)
	}

	deployProject(t, srv, token, "keeper", "alive", map[string]string{
		"hello.txt": "Still Here",
	})
	deployProject(t, srv, token, "keeper", "doomed", map[string]string{
		"hello.txt": "Gone Soon",
	})

	siteHandler := srv.NewSiteHandler()

	// Delete project "doomed" via API.
	req = httptest.NewRequest(http.MethodDelete, "/subdomains/keeper/projects/doomed", nil)
	req.Host = "api.droplydoc.com"
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete project: expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	// Deleted project should return 404.
	req = httptest.NewRequest(http.MethodGet, "/doomed/hello.txt", nil)
	req.Host = "keeper.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("deleted project: expected 404, got %d: %s", rr.Code, rr.Body.String())
	}

	// Other project should still be accessible.
	req = httptest.NewRequest(http.MethodGet, "/alive/hello.txt", nil)
	req.Host = "keeper.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("surviving project: expected 200, got %d: %s", rr.Code, rr.Body.String())
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

func TestSiteHandlerCookieInvalidAfterPasswordChange(t *testing.T) {
	srv, sitesDir := newTestSiteServer(t)
	token, password := setupProtectedSite(t, srv, sitesDir)

	siteHandler := srv.NewSiteHandler()

	// Step 1: Login with original password to get a cookie.
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
		t.Fatalf("login: expected 302, got %d", rr.Code)
	}

	var oldCookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == "_droply_access" {
			oldCookie = c
			break
		}
	}
	if oldCookie == nil {
		t.Fatal("expected _droply_access cookie after login")
	}

	// Step 2: Verify old cookie works.
	req = httptest.NewRequest(http.MethodGet, "/docs/hello.txt", nil)
	req.Host = "alice.droplydoc.com"
	req.AddCookie(oldCookie)
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Hello Droply") {
		t.Fatalf("old cookie should work before password change, got %d", rr.Code)
	}

	// Step 3: Change the password via API.
	accessBody, _ := json.Marshal(map[string]interface{}{
		"password": "newpassword12345",
	})
	req = httptest.NewRequest(http.MethodPut, "/subdomains/alice/projects/docs/access", bytes.NewReader(accessBody))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("update password: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Step 4: Old cookie should now be rejected.
	req = httptest.NewRequest(http.MethodGet, "/docs/hello.txt", nil)
	req.Host = "alice.droplydoc.com"
	req.AddCookie(oldCookie)
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), "_droply/login") {
		t.Fatalf("old cookie should be rejected after password change, got %d: %s", rr.Code, rr.Body.String())
	}

	// Step 5: Login with new password should work.
	form = url.Values{}
	form.Set("password", "newpassword12345")
	form.Set("redirect", "/docs/hello.txt")
	form.Set("host", "alice.droplydoc.com")
	req = httptest.NewRequest(http.MethodPost, "/_droply/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "alice.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("login with new password: expected 302, got %d", rr.Code)
	}
}

func TestSiteHandlerSubdomainCookieWithProjectRule(t *testing.T) {
	srv, sitesDir := newTestSiteServer(t)
	token := registerAndGetToken(t, srv, "subtest@example.com", "password123")

	// Create subdomain.
	body, _ := json.Marshal(map[string]string{"name": "bob"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d", rr.Code)
	}

	// Deploy project.
	deployProject(t, srv, token, "bob", "app", map[string]string{
		"page.html": "Hello App",
	})

	// Set SUBDOMAIN-level password rule.
	accessBody, _ := json.Marshal(map[string]interface{}{
		"password": "subdomainpass1",
	})
	req = httptest.NewRequest(http.MethodPut, "/subdomains/bob/access", bytes.NewReader(accessBody))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set subdomain access: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// ALSO set PROJECT-level password rule (different password).
	accessBody, _ = json.Marshal(map[string]interface{}{
		"password": "projectpass12",
	})
	req = httptest.NewRequest(http.MethodPut, "/subdomains/bob/projects/app/access", bytes.NewReader(accessBody))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set project access: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	siteHandler := srv.NewSiteHandler()

	// Login with SUBDOMAIN password at a second project (no project-level rule)
	// to get a subdomain-scoped cookie.
	deployProject(t, srv, token, "bob", "other", map[string]string{
		"page.html": "Other Page",
	})

	form := url.Values{}
	form.Set("password", "subdomainpass1")
	form.Set("redirect", "/other/page.html")
	form.Set("host", "bob.droplydoc.com")
	req = httptest.NewRequest(http.MethodPost, "/_droply/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "bob.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("subdomain login: expected 302, got %d: %s", rr.Code, rr.Body.String())
	}

	var subCookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == "_droply_access" {
			subCookie = c
			break
		}
	}
	if subCookie == nil {
		t.Fatal("expected subdomain-level cookie")
	}

	// Use subdomain-scoped cookie to access the project "app" (which has its own project-level rule).
	// A project rule overrides the subdomain session.
	req = httptest.NewRequest(http.MethodGet, "/app/page.html", nil)
	req.Host = "bob.droplydoc.com"
	req.AddCookie(subCookie)
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), "_droply/login") {
		t.Fatalf("subdomain cookie must not bypass project rule, got %d: %s", rr.Code, rr.Body.String())
	}

	// Now change the SUBDOMAIN password — subdomain-scoped cookie should be invalidated.
	accessBody, _ = json.Marshal(map[string]interface{}{
		"password": "newsubpass123",
	})
	req = httptest.NewRequest(http.MethodPut, "/subdomains/bob/access", bytes.NewReader(accessBody))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("update subdomain password: expected 200, got %d", rr.Code)
	}

	// Old subdomain cookie should now be rejected.
	req = httptest.NewRequest(http.MethodGet, "/app/page.html", nil)
	req.Host = "bob.droplydoc.com"
	req.AddCookie(subCookie)
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), "_droply/login") {
		t.Fatalf("old subdomain cookie should be rejected after password change, got %d: %s", rr.Code, rr.Body.String())
	}
	_ = sitesDir
}
