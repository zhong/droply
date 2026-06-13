package server_test

import (
	"bytes"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
	"github.com/zhong/droply/internal/wework"
)

// newWeWorkMockAPI returns an httptest.Server that simulates the WeWork OAuth API.
// userIDByCode maps OAuth codes to user IDs the API will return.
func newWeWorkMockAPI(t *testing.T, userIDByCode map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			w.Write([]byte(`{"errcode":0,"access_token":"mock-token"}`))
		case "/cgi-bin/auth/getuserinfo":
			code := r.URL.Query().Get("code")
			uid, ok := userIDByCode[code]
			if !ok {
				w.Write([]byte(`{"errcode":40029,"errmsg":"invalid code"}`))
				return
			}
			resp, _ := json.Marshal(map[string]interface{}{
				"errcode": 0,
				"userid":  uid,
			})
			w.Write(resp)
		default:
			t.Errorf("unexpected wework mock path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// setupWeWorkSite registers a user, creates subdomain "wesite", deploys "app", and applies an
// access rule with WeWork enabled. allowedUsers may be nil (any corp member).
func setupWeWorkSite(t *testing.T, srv *server.Server, allowedUsers []string) string {
	t.Helper()
	token := registerAndGetToken(t, srv, "we@example.com", "password123")

	// Create subdomain.
	body, _ := json.Marshal(map[string]string{"name": "wesite"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: %d %s", rr.Code, rr.Body.String())
	}

	// Deploy project.
	deployProject(t, srv, token, "wesite", "app", map[string]string{
		"index.html": "Hello WeCom",
	})

	// Set WeWork access rule.
	accessReq := map[string]interface{}{
		"wework_enabled": true,
	}
	if allowedUsers != nil {
		accessReq["allowed_wework_users"] = allowedUsers
	}
	accessBody, _ := json.Marshal(accessReq)
	req = httptest.NewRequest(http.MethodPut, "/subdomains/wesite/projects/app/access", bytes.NewReader(accessBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set wework access: %d %s", rr.Code, rr.Body.String())
	}
	return token
}

// newSiteServerWithWeWork sets up a site server with a mocked WeWork client.
func newSiteServerWithWeWork(t *testing.T, mockAPIURL string) (*server.Server, string) {
	t.Helper()
	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	sitesDir := t.TempDir()
	srv := server.New(st, sitesDir, "droplydoc.com", nil, []byte("test-hmac-secret-key-1234567890"), "localhost:8081")

	weClient := wework.NewClient(wework.Config{
		CorpID:       "corp-test",
		AgentID:     "1000",
		Secret:       "sec",
		RedirectURI:  "https://api.droplydoc.com/_droply/wework/callback",
		AuthorizeURL: mockAPIURL + "/connect/oauth2/authorize",
		APIBaseURL:   mockAPIURL,
	})
	srv.SetWeWork(weClient)
	return srv, sitesDir
}

func TestWeWorkAccessRuleSet(t *testing.T) {
	srv, _ := newTestSiteServer(t)
	setupWeWorkSite(t, srv, []string{"alice", "bob"})

	// GET access should reflect WeWork enabled.
	token := registerAndGetToken(t, srv, "verify@example.com", "password123")
	// Re-fetch as the original user. Easier: query as the original token from setupWeWorkSite.
	// Quick path: read directly from API as we@example.com via a fresh login.
	loginBody, _ := json.Marshal(map[string]string{
		"email":    "we@example.com",
		"password": "password123",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login: %d", rr.Code)
	}
	var loginResp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&loginResp)
	weToken := loginResp["api_token"].(string)

	req = httptest.NewRequest(http.MethodGet, "/subdomains/wesite/projects/app/access", nil)
	req.Header.Set("Authorization", "Bearer "+weToken)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get access: %d %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["wework_enabled"] != true {
		t.Errorf("expected wework_enabled=true, got %v", resp["wework_enabled"])
	}
	users := resp["allowed_wework_users"].([]interface{})
	if len(users) != 2 {
		t.Errorf("expected 2 allowed users, got %d", len(users))
	}
	_ = token
}

func TestWeWorkAuthRedirectsToAuthorizeURL(t *testing.T) {
	mockAPI := newWeWorkMockAPI(t, nil)
	defer mockAPI.Close()
	srv, _ := newSiteServerWithWeWork(t, mockAPI.URL)
	setupWeWorkSite(t, srv, []string{"alice"})

	siteHandler := srv.NewSiteHandler()

	req := httptest.NewRequest(http.MethodGet, "/_droply/wework/auth?redirect=/app/&host=wesite.droplydoc.com", nil)
	req.Host = "wesite.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "/connect/oauth2/authorize") {
		t.Errorf("expected redirect to authorize URL, got %q", loc)
	}
	if !strings.Contains(loc, "state=") {
		t.Errorf("expected state in redirect URL, got %q", loc)
	}
	if !strings.Contains(loc, "#wechat_redirect") {
		t.Errorf("expected wechat_redirect fragment, got %q", loc)
	}
}

func TestWeWorkCallbackSuccess(t *testing.T) {
	mockAPI := newWeWorkMockAPI(t, map[string]string{
		"valid-code": "alice",
	})
	defer mockAPI.Close()
	srv, _ := newSiteServerWithWeWork(t, mockAPI.URL)
	setupWeWorkSite(t, srv, []string{"alice", "bob"})

	siteHandler := srv.NewSiteHandler()

	// First, get a valid state token via /auth.
	req := httptest.NewRequest(http.MethodGet, "/_droply/wework/auth?redirect=/app/&host=wesite.droplydoc.com", nil)
	req.Host = "wesite.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("auth: %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	state := extractQueryParam(loc, "state")
	if state == "" {
		t.Fatal("no state in auth redirect")
	}

	// Hit callback.
	req = httptest.NewRequest(http.MethodGet, "/_droply/wework/callback?code=valid-code&state="+state, nil)
	req.Host = "wesite.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect after callback, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Location") != "/app/" {
		t.Errorf("expected redirect to /app/, got %q", rr.Header().Get("Location"))
	}

	// Verify cookie was set.
	var cookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == "_droply_access" {
			cookie = c
			break
		}
	}
	if cookie == nil {
		t.Fatal("expected _droply_access cookie")
	}
	if !strings.HasPrefix(cookie.Value, "v2:") {
		t.Errorf("expected v2 cookie, got %q", cookie.Value)
	}

	// Use cookie to access protected site.
	req = httptest.NewRequest(http.MethodGet, "/app/", nil)
	req.Host = "wesite.droplydoc.com"
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid cookie, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Hello WeCom") {
		t.Errorf("expected Hello WeCom in body, got %q", rr.Body.String())
	}
}

func TestWeWorkCallbackUserNotAllowed(t *testing.T) {
	mockAPI := newWeWorkMockAPI(t, map[string]string{
		"intruder-code": "eve",
	})
	defer mockAPI.Close()
	srv, _ := newSiteServerWithWeWork(t, mockAPI.URL)
	setupWeWorkSite(t, srv, []string{"alice", "bob"})

	siteHandler := srv.NewSiteHandler()

	// Get a valid state.
	req := httptest.NewRequest(http.MethodGet, "/_droply/wework/auth?redirect=/app/&host=wesite.droplydoc.com", nil)
	req.Host = "wesite.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	state := extractQueryParam(rr.Header().Get("Location"), "state")

	// Callback with valid code but user not in allow-list.
	req = httptest.NewRequest(http.MethodGet, "/_droply/wework/callback?code=intruder-code&state="+state, nil)
	req.Host = "wesite.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for disallowed user, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestWeWorkCallbackInvalidState(t *testing.T) {
	mockAPI := newWeWorkMockAPI(t, map[string]string{"valid": "alice"})
	defer mockAPI.Close()
	srv, _ := newSiteServerWithWeWork(t, mockAPI.URL)
	setupWeWorkSite(t, srv, []string{"alice"})

	siteHandler := srv.NewSiteHandler()

	req := httptest.NewRequest(http.MethodGet, "/_droply/wework/callback?code=valid&state=fake-state", nil)
	req.Host = "wesite.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid state, got %d", rr.Code)
	}
}

func TestWeWorkCallbackStateSingleUse(t *testing.T) {
	mockAPI := newWeWorkMockAPI(t, map[string]string{"valid": "alice"})
	defer mockAPI.Close()
	srv, _ := newSiteServerWithWeWork(t, mockAPI.URL)
	setupWeWorkSite(t, srv, []string{"alice"})

	siteHandler := srv.NewSiteHandler()

	// Get state.
	req := httptest.NewRequest(http.MethodGet, "/_droply/wework/auth?redirect=/app/&host=wesite.droplydoc.com", nil)
	req.Host = "wesite.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	state := extractQueryParam(rr.Header().Get("Location"), "state")

	// First use: success.
	req = httptest.NewRequest(http.MethodGet, "/_droply/wework/callback?code=valid&state="+state, nil)
	req.Host = "wesite.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("first use should succeed: %d", rr.Code)
	}

	// Second use: should fail (state consumed).
	req = httptest.NewRequest(http.MethodGet, "/_droply/wework/callback?code=valid&state="+state, nil)
	req.Host = "wesite.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected state reuse to fail with 400, got %d", rr.Code)
	}
}

func TestWeWorkLoginPageShowsButton(t *testing.T) {
	mockAPI := newWeWorkMockAPI(t, nil)
	defer mockAPI.Close()
	srv, _ := newSiteServerWithWeWork(t, mockAPI.URL)
	setupWeWorkSite(t, srv, nil) // any corp member

	siteHandler := srv.NewSiteHandler()
	req := httptest.NewRequest(http.MethodGet, "/app/index.html", nil)
	req.Host = "wesite.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (login page), got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Login with WeCom") {
		t.Errorf("expected WeCom button in login page, body=%q", body)
	}
	if !strings.Contains(body, "/_droply/wework/auth") {
		t.Errorf("expected auth link in login page, body=%q", body)
	}
	if strings.Contains(body, "Enter password") {
		t.Errorf("did not expect password input when only WeWork enabled, body=%q", body)
	}
}

func TestWeWorkAuthHandlerNotConfigured(t *testing.T) {
	srv, _ := newTestSiteServer(t) // no WeWork
	setupWeWorkSite(t, srv, []string{"alice"})

	siteHandler := srv.NewSiteHandler()
	req := httptest.NewRequest(http.MethodGet, "/_droply/wework/auth?redirect=/app/&host=wesite.droplydoc.com", nil)
	req.Host = "wesite.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when WeWork not configured, got %d", rr.Code)
	}
}

// TestWeWorkLoginPageButtonHrefIsClickable is a regression test for a bug where
// the login page's "Login with WeCom" button href was double-URL-encoded:
// renderLoginPage called url.QueryEscape() on the redirect path, and then
// html/template did URL escaping again on the attribute value. Clicking the
// resulting link yielded ?redirect=%252Fvpn%252F, which the auth handler then
// parsed as project name "%2Fvpn%2F" → 404.
func TestWeWorkLoginPageButtonHrefIsClickable(t *testing.T) {
	mockAPI := newWeWorkMockAPI(t, nil)
	defer mockAPI.Close()
	srv, _ := newSiteServerWithWeWork(t, mockAPI.URL)
	setupWeWorkSite(t, srv, nil)

	siteHandler := srv.NewSiteHandler()

	// 1. Hit a protected page → login page rendered.
	req := httptest.NewRequest(http.MethodGet, "/app/", nil)
	req.Host = "wesite.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 login page, got %d", rr.Code)
	}

	// 2. Extract the WeCom link href from the rendered HTML.
	hrefRe := regexp.MustCompile(`href="(/_droply/wework/auth\?[^"]+)"`)
	m := hrefRe.FindStringSubmatch(rr.Body.String())
	if m == nil {
		t.Fatalf("did not find wework auth href in login page body:\n%s", rr.Body.String())
	}
	// HTML-decode the href to get the URL as the browser would see it.
	href := html.UnescapeString(m[1])

	// Sanity-check: redirect query param should decode to a path with a leading slash,
	// not to a double-encoded value like "%2Fapp%2F".
	u, err := url.Parse(href)
	if err != nil {
		t.Fatalf("parse href %q: %v", href, err)
	}
	gotRedirect := u.Query().Get("redirect")
	if !strings.HasPrefix(gotRedirect, "/") {
		t.Errorf("redirect param %q is not a clean path; likely double-encoded", gotRedirect)
	}
	if strings.Contains(gotRedirect, "%2F") || strings.Contains(gotRedirect, "%25") {
		t.Errorf("redirect param %q still contains literal percent-escapes — double encoding bug", gotRedirect)
	}

	// 3. Follow the link → auth handler should redirect to WeCom (302), not 404.
	req = httptest.NewRequest(http.MethodGet, href, nil)
	req.Host = "wesite.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect to WeCom OAuth after clicking login button, got %d:\n%s\n(href was %q)",
			rr.Code, rr.Body.String(), href)
	}
	if !strings.Contains(rr.Header().Get("Location"), "/connect/oauth2/authorize") {
		t.Errorf("expected redirect to WeCom authorize URL, got %q", rr.Header().Get("Location"))
	}
}

// extractQueryParam pulls a query parameter out of a URL string. Empty if not found.
func extractQueryParam(rawURL, key string) string {
	idx := strings.Index(rawURL, "?")
	if idx < 0 {
		return ""
	}
	// Trim fragment if present.
	q := rawURL[idx+1:]
	if hash := strings.Index(q, "#"); hash >= 0 {
		q = q[:hash]
	}
	for _, pair := range strings.Split(q, "&") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 && kv[0] == key {
			return kv[1]
		}
	}
	return ""
}
