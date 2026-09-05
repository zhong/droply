package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/zhong/droply/internal/model"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

	srv := server.New(st, sitesDir, "droplydoc.com", []byte("test-hmac-key-for-testing-1234"))
	token := registerAndGetToken(t, srv, "alice@example.com", "password123")

	// Create subdomain "alice".
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
	req.Host = "api.droplydoc.com"
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
	req.Host = "api.droplydoc.com"
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
	req.Host = "api.droplydoc.com"
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete domain: expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestDomainValidation(t *testing.T) {
	srv, token := setupDomainTest(t)
	for _, domain := range []string{"api.droplydoc.com", "alice.droplydoc.com", "droplydoc.com", "*.example.com", "https://example.com", "localhost", "127.0.0.1", "bad..example.com", "example.com:443"} {
		body, _ := json.Marshal(map[string]string{"domain": domain})
		req := httptest.NewRequest(http.MethodPost, "/subdomains/alice/projects/blog/domains", bytes.NewReader(body))
		req.Host = "api.droplydoc.com"
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%q status=%d want 400", domain, rr.Code)
		}
	}
}

type txtResolver struct {
	records []string
	err     error
	name    string
}

func (d *txtResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	d.name = name
	return d.records, d.err
}

func TestDomainOwnershipLifecycle(t *testing.T) {
	srv, token := setupDomainTest(t)
	dns := &txtResolver{}
	srv.SetDNSResolver(dns)
	api := func(method, path string, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "api.droplydoc.com"
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		return rr
	}
	base := "/subdomains/alice/projects/blog/domains"
	rr := api("POST", base, `{"domain":" BLOG.Example.COM. "}`)
	if rr.Code != 201 {
		t.Fatalf("create: %d %s", rr.Code, rr.Body)
	}
	var binding model.CustomDomain
	if err := json.Unmarshal(rr.Body.Bytes(), &binding); err != nil {
		t.Fatal(err)
	}
	if binding.Domain != "blog.example.com" || binding.Status != "pending" || binding.VerificationToken == "" {
		t.Fatalf("binding: %+v", binding)
	}
	if rr := api("POST", base, `{"domain":"blog.example.com"}`); rr.Code != 409 {
		t.Fatalf("duplicate: %d", rr.Code)
	}
	check := func(want int, allowed bool) {
		t.Helper()
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = "blog.example.com"
		rr := httptest.NewRecorder()
		srv.NewSiteHandler().ServeHTTP(rr, req)
		if rr.Code != want || srv.AllowedTLSHost(req.Host) != allowed {
			t.Fatalf("host: %d allowed=%v", rr.Code, srv.AllowedTLSHost(req.Host))
		}
	}
	check(404, false)
	dns.records = []string{"wrong-challenge"}
	rr = api("POST", base+"/blog.example.com/verify", "")
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"verified":false`) {
		t.Fatalf("wrong TXT: %d %s", rr.Code, rr.Body)
	}
	if dns.name != binding.VerificationRecord {
		t.Fatalf("lookup %q", dns.name)
	}
	dns.err = errors.New("DNS unavailable")
	if rr := api("POST", base+"/blog.example.com/verify", ""); rr.Code != 502 {
		t.Fatalf("DNS failure: %d", rr.Code)
	}
	check(404, false)
	dns.err = nil
	dns.records = []string{binding.VerificationToken}
	rr = api("POST", base+"/BLOG.EXAMPLE.COM/verify", "")
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"verified":true`) {
		t.Fatalf("verify: %d %s", rr.Code, rr.Body)
	}
	check(200, true)
	if rr := api("DELETE", base+"/blog.example.com", ""); rr.Code != 204 {
		t.Fatalf("delete: %d", rr.Code)
	}
	check(404, false)
	rr = api("POST", base, `{"domain":"blog.example.com"}`)
	var rebound model.CustomDomain
	if err := json.Unmarshal(rr.Body.Bytes(), &rebound); err != nil {
		t.Fatal(err)
	}
	if rebound.VerificationToken == binding.VerificationToken {
		t.Fatal("rebinding reused old proof")
	}
	dns.records = []string{rebound.VerificationToken}
	api("POST", base+"/blog.example.com/verify", "")
	check(200, true)
	if rr := api("DELETE", "/subdomains/alice/projects/blog", ""); rr.Code != 204 {
		t.Fatalf("delete project: %d", rr.Code)
	}
	check(404, false)
}

func TestUnifiedSiteAccessAndStatistics(t *testing.T) {
	srv, token := setupDomainTest(t)
	api := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "api.droplydoc.com"
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		return rr
	}
	base := "/subdomains/alice/projects/blog"
	rr := api("POST", base+"/domains", `{"domain":"blog.example.com"}`)
	var binding model.CustomDomain
	if err := json.Unmarshal(rr.Body.Bytes(), &binding); err != nil {
		t.Fatal(err)
	}
	srv.SetDNSResolver(&txtResolver{records: []string{binding.VerificationToken}})
	if rr := api("POST", base+"/domains/blog.example.com/verify", ""); rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	handler := srv.NewSiteHandler()
	request := func(host, path, peer string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		req.Host = host
		req.RemoteAddr = peer
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}
	hosts := []struct{ host, path string }{{"alice.droplydoc.com", "/blog/"}, {"blog.example.com", "/"}}
	for _, h := range hosts {
		rr := request(h.host, h.path, "192.0.2.1:1234", nil)
		if rr.Code != 200 || !strings.Contains(rr.Body.String(), "<body>blog") {
			t.Fatalf("public %s: %d %s", h.host, rr.Code, rr.Body)
		}
	}
	for _, access := range []string{`{"allowed_ips":["10.0.0.0/8"]}`, `{"password":"private-password"}`, `{"wework_enabled":true}`} {
		if rr := api("PUT", base+"/access", access); rr.Code != 200 {
			t.Fatal(rr.Body.String())
		}
		for _, h := range hosts {
			rr := request(h.host, h.path, "192.0.2.1:1234", nil)
			if strings.Contains(rr.Body.String(), "<body>blog") || rr.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatalf("protected %s: %d %s", h.host, rr.Code, rr.Body)
			}
			if strings.Contains(access, "allowed_ips") {
				if rr.Code != 403 {
					t.Fatalf("IP denial: %d", rr.Code)
				}
				rr = request(h.host, h.path, "10.1.2.3:1234", nil)
				if rr.Code != 200 || rr.Header().Get("Cache-Control") != "private, no-store" {
					t.Fatalf("IP allow: %d", rr.Code)
				}
			}
			if strings.Contains(access, "password") {
				form := url.Values{"password": {"private-password"}, "redirect": {h.path}, "host": {h.host}}
				req := httptest.NewRequest("POST", "/_droply/login", strings.NewReader(form.Encode()))
				req.Host = h.host
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				login := httptest.NewRecorder()
				handler.ServeHTTP(login, req)
				cookies := login.Result().Cookies()
				if login.Code != 302 || len(cookies) == 0 {
					t.Fatalf("login: %d %s", login.Code, login.Body)
				}
				rr = request(h.host, h.path, "192.0.2.1:1234", cookies[0])
				if rr.Code != 200 || !strings.Contains(rr.Body.String(), "<body>blog") || rr.Header().Get("Cache-Control") != "private, no-store" {
					t.Fatalf("private content: %d %s", rr.Code, rr.Body)
				}
			}
		}
	}
	srv.StartAnalytics()
	srv.ShutdownAnalytics()
	rr = api("GET", base+"/stats", "")
	var stats struct {
		TotalPV int `json:"total_pv"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.TotalPV != 6 {
		t.Fatalf("both aliases should record successful visits only: %d %s", stats.TotalPV, rr.Body)
	}
}

func TestDomainAuthorizationSurvivesRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "droply.db")
	sites := t.TempDir()
	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	user, err := st.CreateUser("restart@example.com", "hash", "token")
	if err != nil {
		t.Fatal(err)
	}
	sub, err := st.CreateSubdomain(user.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateProject(sub.ID, "blog")
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"verified.example.com", "pending.example.com"} {
		if _, err := st.CreateCustomDomain(project.ID, domain); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.VerifyCustomDomain("verified.example.com"); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(sites, "alice", "blog")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("restart content"), 0644); err != nil {
		t.Fatal(err)
	}
	check := func(st *store.SQLiteStore) {
		t.Helper()
		srv := server.New(st, sites, "droplydoc.com", []byte("test-key"))
		for _, tc := range []struct {
			host    string
			status  int
			allowed bool
		}{{"verified.example.com", 200, true}, {"pending.example.com", 404, false}, {"unknown.example.com", 404, false}} {
			req := httptest.NewRequest("GET", "/", nil)
			req.Host = tc.host
			rr := httptest.NewRecorder()
			srv.NewSiteHandler().ServeHTTP(rr, req)
			if rr.Code != tc.status || srv.AllowedTLSHost(tc.host) != tc.allowed {
				t.Fatalf("%s response=%d allowed=%v", tc.host, rr.Code, srv.AllowedTLSHost(tc.host))
			}
		}
	}
	check(st)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	check(st)
	if err := st.DeleteProject(sub.ID, project.Name); err != nil {
		t.Fatal(err)
	}
	srv := server.New(st, sites, "droplydoc.com", []byte("test-key"))
	for _, target := range []struct{ host, path string }{{"alice.droplydoc.com", "/blog/"}, {"alice.droplydoc.com", "/blog"}, {"verified.example.com", "/"}} {
		req := httptest.NewRequest("GET", target.path, nil)
		req.Host = target.host
		rr := httptest.NewRecorder()
		srv.NewSiteHandler().ServeHTTP(rr, req)
		if rr.Code != 404 {
			t.Fatalf("deleted project with leftover files: %s%s got %d", target.host, target.path, rr.Code)
		}
	}
}
