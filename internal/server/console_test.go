//go:build integration

package server_test

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func consoleFixture(t *testing.T) (*server.Server, *store.SQLiteStore) {
	t.Helper()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, err := bcrypt.GenerateFromPassword([]byte("console-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := st.CreateUser("owner@console.test", string(hash), "owner-long-lived-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateUser("viewer@console.test", string(hash), "viewer-long-lived-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateUser("empty@console.test", string(hash), "empty-long-lived-secret"); err != nil {
		t.Fatal(err)
	}
	sub, err := st.CreateSubdomain(owner.ID, "team")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(sub.ID, "site")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateProject(sub.ID, "secret"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.PutProjectMember(t.Context(), p.ID, "viewer@console.test", "viewer"); err != nil {
		t.Fatal(err)
	}
	srv := server.New(st, t.TempDir(), "console.localhost", []byte("console-test-key"))
	t.Cleanup(srv.ShutdownAnalytics)
	for _, env := range []string{"production", "preview"} {
		w := httptest.NewRecorder()
		request := buildDeployRequest(t, "/subdomains/team/projects/site/deploy?environment="+env, createTestTarGz(t, map[string]string{"index.html": env}), "owner-long-lived-secret")
		request.Host = "api.console.localhost"
		srv.ServeHTTP(w, request)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	return srv, st
}
func consoleRequest(srv *server.Server, method, path, origin, csrf, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://api.console.localhost"+path, strings.NewReader(body))
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("Origin", origin)
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w
}
func TestConsoleSecureSessionAndPermissions(t *testing.T) {
	srv, _ := consoleFixture(t)
	unauthenticated := httptest.NewRecorder()
	srv.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "http://api.console.localhost/projects", nil))
	if unauthenticated.Code != http.StatusUnauthorized || !strings.Contains(unauthenticated.Body.String(), `"error":"unauthorized"`) {
		t.Fatalf("unauthenticated API contract changed: %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}
	body := `{"email":"viewer@console.test","password":"console-password"}`
	if w := consoleRequest(srv, "POST", "/console/login", "https://evil.console.localhost", "", body, nil); w.Code != 403 {
		t.Fatal("cross-origin login", w.Code)
	}
	w := consoleRequest(srv, "POST", "/console/login", "https://api.console.localhost", "", body, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "long-lived") || strings.Contains(w.Body.String(), "api_token") {
		t.Fatal("login disclosed API credential")
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("session cookie missing")
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Domain != "" || cookie.Path != "/" {
		t.Fatalf("unsafe cookie %+v", cookie)
	}
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if w = consoleRequest(srv, "GET", "/projects", "", "", "", cookie); w.Code != 200 || strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), "site") {
		t.Fatal("project permission list", w.Code, w.Body.String())
	}
	if w = consoleRequest(srv, "POST", "/subdomains/team/projects/site/rollback/1", "https://api.console.localhost", session.CSRF, "", cookie); w.Code != 403 {
		t.Fatal("viewer mutation accepted", w.Code)
	}
	if w = consoleRequest(srv, "POST", "/console/logout", "https://api.console.localhost", "", "", cookie); w.Code != 403 {
		t.Fatal("missing CSRF accepted", w.Code)
	}
	if w = consoleRequest(srv, "POST", "/console/logout", "https://evil.console.localhost", session.CSRF, "", cookie); w.Code != 403 {
		t.Fatal("cross-origin logout", w.Code)
	}
	if w = consoleRequest(srv, "POST", "/console/logout", "https://api.console.localhost", session.CSRF, "", cookie); w.Code != 204 {
		t.Fatal("logout", w.Code, w.Body.String())
	}
	if w = consoleRequest(srv, "GET", "/projects", "", "", "", cookie); w.Code != 401 {
		t.Fatal("revoked session accepted", w.Code)
	}
	r := httptest.NewRequest("POST", "http://api.console.localhost/console/login", strings.NewReader(body))
	r.Header.Set("Origin", "https://api.console.localhost")
	r.Header.Set("X-Forwarded-Proto", "https")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("untrusted TLS header accepted", w.Code)
	}
}
func TestDefaultHandlerKeepsConsoleAndVisitorSessionsSeparate(t *testing.T) {
	srv, st := consoleFixture(t)
	sub, err := st.GetSubdomainByName("team")
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.GetProject(sub.ID, "site")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("visitor-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutAccessRule(t.Context(), store.AccessRuleInput{SubdomainID: sub.ID, ProjectID: &project.ID, PasswordHash: string(hash), SessionTTL: 3600}); err != nil {
		t.Fatal(err)
	}
	login := consoleRequest(srv, "POST", "/console/login", "https://api.console.localhost", "", `{"email":"viewer@console.test","password":"console-password"}`, nil)
	if login.Code != 200 {
		t.Fatal(login.Code, login.Body.String())
	}
	consoleCookie := login.Result().Cookies()[0]
	listener := httptest.NewTLSServer(srv)
	defer listener.Close()
	client := listener.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request := func(method, host, path, body string, cookie *http.Cookie) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, listener.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		return response
	}
	host := project.HostLabel + ".console.localhost"
	response := request("GET", host, "/", "", consoleCookie)
	content, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !strings.Contains(string(content), "_droply/login") {
		t.Fatalf("console session did not receive visitor login: %d %s", response.StatusCode, content)
	}
	response = request("POST", host, "/_droply/login", "password=visitor-password&host="+host+"&redirect=/", nil)
	if response.StatusCode != 302 || len(response.Cookies()) != 1 {
		t.Fatalf("visitor login: %d", response.StatusCode)
	}
	visitorCookie := response.Cookies()[0]
	response = request("GET", host, "/", "", visitorCookie)
	content, err = io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || string(content) != "production" {
		t.Fatalf("visitor site: %d %s %v", response.StatusCode, content, err)
	}
	response = request("GET", "api.console.localhost", "/projects", "", visitorCookie)
	if response.StatusCode != 401 {
		t.Fatalf("visitor session authorized management: %d", response.StatusCode)
	}
}

func TestConsoleBrowser(t *testing.T) {
	if os.Getenv("DROPLY_CONSOLE_BROWSER") != "1" {
		t.Skip("set DROPLY_CONSOLE_BROWSER=1 and DROPLY_PLAYWRIGHT_PATH to run real Chromium acceptance")
	}
	srv, _ := consoleFixture(t)
	listener := httptest.NewTLSServer(srv)
	defer listener.Close()
	script, err := filepath.Abs("../../scripts/console_test.mjs")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.TrimPrefix(listener.URL, "https://127.0.0.1:")
	cmd := exec.CommandContext(t.Context(), "node", script, "https://api.console.localhost:"+port)
	screenshot := os.Getenv("DROPLY_CONSOLE_SCREENSHOT")
	if screenshot == "" {
		screenshot = filepath.Join(t.TempDir(), "console.png")
	}
	cmd.Env = append(os.Environ(), "DROPLY_CONSOLE_SCREENSHOT="+screenshot)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser: %v\n%s", err, output)
	}
	t.Log(string(output))
}
