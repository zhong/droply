package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

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
			resp, _ := json.Marshal(map[string]any{
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
	req.Host = "api.droplydoc.com"
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
	accessReq := map[string]any{
		"wework_enabled": true,
	}
	if allowedUsers != nil {
		accessReq["allowed_wework_users"] = allowedUsers
	}
	accessBody, _ := json.Marshal(accessReq)
	req = httptest.NewRequest(http.MethodPut, "/subdomains/wesite/projects/app/access", bytes.NewReader(accessBody))
	req.Host = "api.droplydoc.com"
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
	srv := server.New(st, sitesDir, "droplydoc.com", []byte("test-hmac-secret-key-1234567890"))

	weClient := wework.NewClient(wework.Config{
		CorpID:             "corp-test",
		AgentID:            "1000",
		Secret:             "sec",
		RedirectURI:        "https://api.droplydoc.com/_droply/wework/callback",
		AuthorizeURL:       mockAPIURL + "/wwlogin/sso/login",
		MobileAuthorizeURL: mockAPIURL + "/connect/oauth2/authorize",
		APIBaseURL:         mockAPIURL,
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
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login: %d", rr.Code)
	}
	var loginResp map[string]any
	json.NewDecoder(rr.Body).Decode(&loginResp)
	weToken := loginResp["api_token"].(string)

	req = httptest.NewRequest(http.MethodGet, "/subdomains/wesite/projects/app/access", nil)
	req.Host = "api.droplydoc.com"
	req.Header.Set("Authorization", "Bearer "+weToken)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get access: %d %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["wework_enabled"] != true {
		t.Errorf("expected wework_enabled=true, got %v", resp["wework_enabled"])
	}
	users := resp["allowed_wework_users"].([]any)
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
	if !strings.Contains(loc, "/wwlogin/sso/login") {
		t.Errorf("expected redirect to SSO login URL, got %q", loc)
	}
	if !strings.Contains(loc, "login_type=CorpApp") {
		t.Errorf("expected login_type=CorpApp in redirect, got %q", loc)
	}
	if !strings.Contains(loc, "state=") {
		t.Errorf("expected state in redirect URL, got %q", loc)
	}
}

func TestWeWorkCallbackSuccess(t *testing.T) {
	mockAPI := newWeWorkMockAPI(t, map[string]string{
		"valid-code": "alice",
	})
	defer mockAPI.Close()
	srv, _ := newSiteServerWithWeWork(t, mockAPI.URL)
	setupWeWorkSite(t, srv, []string{"alice", "bob"})

	siteHandler := srv

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
	// Attach the "recently failed" marker so the handler renders the login
	// page instead of auto-redirecting. (Without the marker, a WeCom-only
	// rule would 302 straight to OAuth — covered by TestWeWorkAutoRedirect.)
	req.AddCookie(&http.Cookie{Name: "_droply_wework_attempted", Value: "1"})
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

	// 1. Hit a protected page with the "attempted" marker so we get the
	//    login page (instead of an auto-redirect, which TestWeWorkAutoRedirect covers).
	req := httptest.NewRequest(http.MethodGet, "/app/", nil)
	req.Host = "wesite.droplydoc.com"
	req.AddCookie(&http.Cookie{Name: "_droply_wework_attempted", Value: "1"})
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
	if !strings.Contains(rr.Header().Get("Location"), "/wwlogin/sso/login") {
		t.Errorf("expected redirect to WeCom SSO login URL, got %q", rr.Header().Get("Location"))
	}
}

// TestWeWorkCallbackCrossSubdomainRedirect verifies the fix for a 404 that
// occurred in production (login.example.com/vpn/) when:
//   - User starts login on its.example.com (state stored with this host)
//   - WeCom redirects callback to a different host (login.example.com)
//   - Old code redirected to stateData.Redirect "/vpn/" → resolved against
//     the callback host → "https://login.example.com/vpn/" → 404
//
// Fix: when callback host != state host, redirect to absolute URL on the
// original host, and set the cookie Domain to the parent so the redirected
// request can read the session.
func TestWeWorkCallbackCrossSubdomainRedirect(t *testing.T) {
	mockAPI := newWeWorkMockAPI(t, map[string]string{"code-1": "alice"})
	defer mockAPI.Close()
	srv, _ := newSiteServerWithWeWork(t, mockAPI.URL)
	setupWeWorkSite(t, srv, []string{"alice"})

	siteHandler := srv

	// Step 1: User starts login on the original subdomain "wesite".
	req := httptest.NewRequest(http.MethodGet,
		"/_droply/wework/auth?redirect=/app/&host=wesite.droplydoc.com", nil)
	req.Host = "wesite.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	state := extractQueryParam(rr.Header().Get("Location"), "state")
	if state == "" {
		t.Fatal("no state from auth handler")
	}

	// Step 2: WeCom redirects callback to a central API host.
	req = httptest.NewRequest(http.MethodGet,
		"/_droply/wework/callback?code=code-1&state="+state, nil)
	req.Host = "api.droplydoc.com" // intentionally different from state host
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302 after callback, got %d: %s", rr.Code, rr.Body.String())
	}

	// The redirect must be an absolute URL pointing at the ORIGINAL host,
	// not a relative path that would resolve against the callback host.
	loc := rr.Header().Get("Location")
	if loc != "https://wesite.droplydoc.com/app/" {
		t.Errorf("expected absolute redirect to original host, got %q", loc)
	}

	// The cookie must be scoped to the parent domain so the original host
	// can read it after the redirect.
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
	if cookie.Domain != "droplydoc.com" {
		t.Errorf("expected cookie Domain=droplydoc.com (parent), got %q", cookie.Domain)
	}
}

// TestWeWorkCallbackSameHostNoDomainAttribute verifies that when the callback
// arrives on the same host the user started on, the cookie is set host-only
// (no Domain attribute) — the safer default that doesn't leak the session
// to sibling subdomains.
func TestWeWorkCallbackSameHostNoDomainAttribute(t *testing.T) {
	mockAPI := newWeWorkMockAPI(t, map[string]string{"code-1": "alice"})
	defer mockAPI.Close()
	srv, _ := newSiteServerWithWeWork(t, mockAPI.URL)
	setupWeWorkSite(t, srv, []string{"alice"})

	siteHandler := srv.NewSiteHandler()

	req := httptest.NewRequest(http.MethodGet,
		"/_droply/wework/auth?redirect=/app/&host=wesite.droplydoc.com", nil)
	req.Host = "wesite.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	state := extractQueryParam(rr.Header().Get("Location"), "state")

	// Callback on the SAME host as the original request.
	req = httptest.NewRequest(http.MethodGet,
		"/_droply/wework/callback?code=code-1&state="+state, nil)
	req.Host = "wesite.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rr.Code)
	}
	// Same-host case: relative redirect is fine.
	if loc := rr.Header().Get("Location"); loc != "/app/" {
		t.Errorf("expected relative redirect /app/, got %q", loc)
	}

	for _, c := range rr.Result().Cookies() {
		if c.Name == "_droply_access" {
			if c.Domain != "" {
				t.Errorf("expected host-only cookie when no host hop, got Domain=%q", c.Domain)
			}
			return
		}
	}
	t.Fatal("expected _droply_access cookie")
}

func TestCookieParentDomain(t *testing.T) {
	cases := []struct {
		callback, origin, base, want string
	}{
		{"login.example.com", "its.example.com", "example.com", "example.com"},
		{"its.example.com", "its.example.com", "example.com", ""},
		{"login.example.com", "", "example.com", ""},
		{"login.example.com", "evil.com", "example.com", ""},
		{"login.example.com", "its.example.com", "", ""},
		{"LOGIN.example.com", "its.example.com", "example.com", "example.com"},
	}
	for _, c := range cases {
		got := server.CookieParentDomainForTest(c.callback, c.origin, c.base)
		if got != c.want {
			t.Errorf("CookieParentDomainForTest(%q,%q,%q) = %q, want %q",
				c.callback, c.origin, c.base, got, c.want)
		}
	}
}

// TestWeWorkAuthDispatchByUserAgent verifies the UA-based routing fix:
//   - Desktop / regular mobile browsers → SSO login (PC QR code page)
//   - WeCom in-app browser ("wxwork/") → snsapi_base mobile OAuth (silent)
//
// Regression for the case where users opening the link inside WeCom got the
// QR code page instead of being silently signed in.
func TestWeWorkAuthDispatchByUserAgent(t *testing.T) {
	mockAPI := newWeWorkMockAPI(t, nil)
	defer mockAPI.Close()

	cases := []struct {
		name           string
		userAgent      string
		wantInLocation string
	}{
		{
			name:           "desktop chrome",
			userAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
			wantInLocation: "/wwlogin/sso/login",
		},
		{
			name:           "wecom ios",
			userAgent:      "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) wxwork/5.0.10 MicroMessenger/7.0.1",
			wantInLocation: "/connect/oauth2/authorize",
		},
		{
			name:           "wecom android",
			userAgent:      "Mozilla/5.0 (Linux; Android 13) wxwork/4.1.36 MicroMessenger/8.0",
			wantInLocation: "/connect/oauth2/authorize",
		},
		{
			name:           "plain wechat (not wecom) → still PC QR",
			userAgent:      "Mozilla/5.0 (iPhone) MicroMessenger/8.0",
			wantInLocation: "/wwlogin/sso/login",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newSiteServerWithWeWork(t, mockAPI.URL)
			setupWeWorkSite(t, srv, []string{"alice"})

			siteHandler := srv.NewSiteHandler()
			req := httptest.NewRequest(http.MethodGet,
				"/_droply/wework/auth?redirect=/app/&host=wesite.droplydoc.com", nil)
			req.Host = "wesite.droplydoc.com"
			req.Header.Set("User-Agent", tc.userAgent)
			rr := httptest.NewRecorder()
			siteHandler.ServeHTTP(rr, req)

			if rr.Code != http.StatusFound {
				t.Fatalf("expected 302, got %d", rr.Code)
			}
			loc := rr.Header().Get("Location")
			if !strings.Contains(loc, tc.wantInLocation) {
				t.Errorf("UA %q: expected redirect to contain %q, got %q",
					tc.userAgent, tc.wantInLocation, loc)
			}
		})
	}
}

// TestWeWorkAutoRedirect verifies the auto-redirect-to-OAuth optimization
// for WeCom-only access rules: a first hit on a protected page goes
// straight to /_droply/wework/auth (no manual "Login" click required).
func TestWeWorkAutoRedirect(t *testing.T) {
	mockAPI := newWeWorkMockAPI(t, nil)
	defer mockAPI.Close()
	srv, _ := newSiteServerWithWeWork(t, mockAPI.URL)
	setupWeWorkSite(t, srv, nil) // wework-only, no password

	siteHandler := srv.NewSiteHandler()
	req := httptest.NewRequest(http.MethodGet, "/app/", nil)
	req.Host = "wesite.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302 auto-redirect, got %d:\n%s", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "/_droply/wework/auth?") {
		t.Errorf("expected redirect to wework auth, got %q", loc)
	}
	// Original path should be preserved for callback to redirect back to.
	if !strings.Contains(loc, url.QueryEscape("/app/")) {
		t.Errorf("expected /app/ to be preserved in redirect param, got %q", loc)
	}
}

// TestWeWorkNoAutoRedirectWhenPasswordAlsoEnabled verifies that when a rule
// has both WeCom and password, the login page is shown (so users can pick),
// not auto-redirected to WeCom OAuth.
func TestWeWorkNoAutoRedirectWhenPasswordAlsoEnabled(t *testing.T) {
	mockAPI := newWeWorkMockAPI(t, nil)
	defer mockAPI.Close()
	srv, sitesDir := newSiteServerWithWeWork(t, mockAPI.URL)

	// Set up a rule with BOTH password and WeCom.
	token := registerAndGetToken(t, srv, "both@example.com", "password123")
	body, _ := json.Marshal(map[string]string{"name": "wesite"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: %d %s", rr.Code, rr.Body.String())
	}
	deployProject(t, srv, token, "wesite", "app", map[string]string{"index.html": "ok"})
	accessBody, _ := json.Marshal(map[string]any{
		"auto_password":  true,
		"wework_enabled": true,
	})
	req = httptest.NewRequest(http.MethodPut, "/subdomains/wesite/projects/app/access", bytes.NewReader(accessBody))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set access: %d %s", rr.Code, rr.Body.String())
	}
	_ = sitesDir

	siteHandler := srv.NewSiteHandler()
	req = httptest.NewRequest(http.MethodGet, "/app/", nil)
	req.Host = "wesite.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 login page when password also enabled, got %d", rr.Code)
	}
	body2 := rr.Body.String()
	if !strings.Contains(body2, "Enter password") {
		t.Errorf("expected password input in login page, body=%q", body2)
	}
	if !strings.Contains(body2, "Login with WeCom") {
		t.Errorf("expected WeCom button in login page, body=%q", body2)
	}
}

// TestWeWorkNoAutoRedirectAfterRecentFailure verifies that after an OAuth
// attempt fails (marker cookie set), the next protected page hit renders the
// login page instead of looping back into OAuth.
func TestWeWorkNoAutoRedirectAfterRecentFailure(t *testing.T) {
	mockAPI := newWeWorkMockAPI(t, nil)
	defer mockAPI.Close()
	srv, _ := newSiteServerWithWeWork(t, mockAPI.URL)
	setupWeWorkSite(t, srv, nil)

	siteHandler := srv.NewSiteHandler()
	req := httptest.NewRequest(http.MethodGet, "/app/", nil)
	req.Host = "wesite.droplydoc.com"
	// Simulate a recent failure by attaching the marker cookie.
	req.AddCookie(&http.Cookie{Name: "_droply_wework_attempted", Value: "1"})
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 login page after recent failure, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Login with WeCom") {
		t.Errorf("expected login page with WeCom button, got body=%q", rr.Body.String())
	}
}

// TestWeWorkAutoRedirectDisabledWhenServerNotConfigured verifies that without
// a WeCom-configured server (s.wework == nil), the login page is shown
// instead of an auto-redirect (which would 503 immediately).
func TestWeWorkAutoRedirectDisabledWhenServerNotConfigured(t *testing.T) {
	srv, _ := newTestSiteServer(t) // no WeCom configured
	setupWeWorkSite(t, srv, nil)

	siteHandler := srv.NewSiteHandler()
	req := httptest.NewRequest(http.MethodGet, "/app/", nil)
	req.Host = "wesite.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 login page when WeCom not configured, got %d", rr.Code)
	}
	body := rr.Body.String()
	// Login page should NOT show the WeCom button when server is unconfigured.
	if strings.Contains(body, "Login with WeCom") {
		t.Errorf("WeCom button should not be present when server unconfigured, body=%q", body)
	}
}

// extractQueryParam pulls a query parameter out of a URL string. Empty if not found.
func extractQueryParam(rawURL, key string) string {
	_, query, found := strings.Cut(rawURL, "?")
	if !found {
		return ""
	}
	// Trim fragment if present.
	q, _, _ := strings.Cut(query, "#")
	for pair := range strings.SplitSeq(q, "&") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 && kv[0] == key {
			return kv[1]
		}
	}
	return ""
}

func TestWeWorkCallbackUpstreamFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"access_token":"private-token","userid":"alice"}`))
	}))
	defer upstream.Close()
	srv, _ := newSiteServerWithWeWork(t, upstream.URL)
	setupWeWorkSite(t, srv, []string{"alice"})
	handler := srv.NewSiteHandler()
	req := httptest.NewRequest(http.MethodGet, "/_droply/wework/auth?redirect=/app/&host=wesite.droplydoc.com", nil)
	req.Host = "wesite.droplydoc.com"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	state := extractQueryParam(rr.Header().Get("Location"), "state")
	req = httptest.NewRequest(http.MethodGet, "/_droply/wework/callback?code=private-code&state="+state, nil)
	req.Host = "wesite.droplydoc.com"
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("callback status = %d: %s", rr.Code, rr.Body.String())
	}
	failed := false
	for _, cookie := range rr.Result().Cookies() {
		if cookie.Name == "_droply_access" {
			t.Fatal("upstream failure issued access cookie")
		}
		if cookie.Name == "_droply_wework_attempted" {
			failed = true
		}
	}
	if !failed {
		t.Fatal("missing failure marker")
	}
	if strings.Contains(rr.Body.String(), "private-") {
		t.Fatal("response leaked credential")
	}
}

func TestWeWorkCallbackPropagatesCancellation(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer upstream.Close()
	srv, _ := newSiteServerWithWeWork(t, upstream.URL)
	setupWeWorkSite(t, srv, []string{"alice"})
	handler := srv.NewSiteHandler()
	req := httptest.NewRequest(http.MethodGet, "/_droply/wework/auth?redirect=/app/&host=wesite.droplydoc.com", nil)
	req.Host = "wesite.droplydoc.com"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	state := extractQueryParam(rr.Header().Get("Location"), "state")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req = httptest.NewRequestWithContext(ctx, http.MethodGet, "/_droply/wework/callback?code=private-code&state="+state, nil)
	req.Host = "wesite.droplydoc.com"
	rr = httptest.NewRecorder()
	done := make(chan struct{})
	go func() { handler.ServeHTTP(rr, req); close(done) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("callback did not reach upstream")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled callback did not return")
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream not canceled")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("canceled callback status = %d", rr.Code)
	}
	for _, cookie := range rr.Result().Cookies() {
		if cookie.Name == "_droply_access" {
			t.Fatal("canceled callback issued access cookie")
		}
	}
}
