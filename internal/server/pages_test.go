package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPagesDeploymentLifecycle(t *testing.T) {
	srv, _ := newDeployTestServer(t)
	token := registerAndGetToken(t, srv, "pages@example.test", "password123")
	createSubdomain(t, srv, token, "pages")
	base := "/subdomains/pages/projects/site"
	upload := func(query string, files map[string]string, status int) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, buildDeployRequest(t, base+"/deploy"+query, createTestTarGz(t, files), token))
		if w.Code != status {
			t.Fatalf("upload: %d %s", w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, base+path, bytes.NewBufferString(body))
		r.Host = "api.droplydoc.com"
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
	site := func(raw, path string, code int, body string) *httptest.ResponseRecorder {
		t.Helper()
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("GET", path, nil)
		r.Host = u.Host
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code != code || (body != "" && w.Body.String() != body) {
			t.Fatalf("site %s%s: %d %q", raw, path, w.Code, w.Body.String())
		}
		return w
	}
	production := upload("", map[string]string{"index.html": "A", "assets/main.css": "root asset"}, 200)
	prod := production["project_url"].(string)
	site(prod, "/assets/main.css", 200, "root asset")
	site("https://pages.droplydoc.com", "/site/", 200, "A")
	preview := upload("?environment=preview&branch=Feature%2FA&commit=abc123", map[string]string{"index.html": "B", "_headers": "/*\n  Cache-Control: public, max-age=3600\n"}, 200)
	prev, branch := preview["preview_url"].(string), preview["branch_url"].(string)
	if preview["environment"] != "preview" || preview["commit"] != "abc123" {
		t.Fatal(preview)
	}
	if w := site(prev, "/", 200, "B"); !strings.Contains(w.Header().Get("X-Robots-Tag"), "noindex") {
		t.Fatal("preview lacks noindex")
	}
	site(branch, "/", 200, "B")
	for _, raw := range []string{prev, branch} {
		u, _ := url.Parse(raw)
		decorated := "https://" + strings.ToUpper(u.Host) + ".:443"
		if w := site(decorated, "/", 200, "B"); !strings.Contains(w.Header().Get("X-Robots-Tag"), "noindex") {
			t.Fatal("normalized preview lost noindex")
		}
	}

	site(prod, "/", 200, "A")
	upload("?environment=preview&branch=Feature%2FA", map[string]string{"index.html": "BAD", "_redirects": "invalid rule"}, 400)
	site(branch, "/", 200, "B")
	site(prod, "/", 200, "A")
	if w := request("POST", "/rollback/2", ""); w.Code != 409 {
		t.Fatalf("unpublished preview rollback: %d", w.Code)
	}
	for range 2 {
		if w := request("POST", "/promote/2", ""); w.Code != 200 {
			t.Fatalf("promote: %s", w.Body.String())
		}
	}
	site(prod, "/", 200, "B")
	site(prev, "/", 200, "B")
	w := request("GET", "/events", "")
	var events []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &events); err != nil || len(events) != 1 {
		t.Fatalf("events %s", w.Body.String())
	}
	if w := request("POST", "/rollback/1", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	site(prod, "/", 200, "A")
	if w := request("PUT", "/access", `{"allowed_ips":["203.0.113.0/24"]}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, host := range []string{prod, prev, branch} {
		w := site(host, "/", 403, "")
		if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("access denial cacheable")
		}
	}

	u, _ := url.Parse(prev)
	authorized := httptest.NewRequest("GET", "/", nil)
	authorized.Host = u.Host
	authorized.RemoteAddr = "203.0.113.5:1234"
	allowed := httptest.NewRecorder()
	srv.NewSiteHandler().ServeHTTP(allowed, authorized)
	if allowed.Code != 200 || allowed.Body.String() != "B" || !strings.Contains(allowed.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("private site headers overrode platform policy: %d %v", allowed.Code, allowed.Header())
	}
	if w := site(prev, "/", 403, ""); !strings.Contains(w.Header().Get("X-Robots-Tag"), "noindex") {
		t.Fatal("preview auth denial lacks noindex")
	}
	if w := request("DELETE", "/access", ""); w.Code != 200 && w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	// Move the branch alias, then cleanup may revoke only the old immutable URL.
	next := upload("?environment=preview&branch=Feature%2FA", map[string]string{"index.html": "C"}, 200)
	site(branch, "/", 200, "C")
	site(prev, "/", 200, "B")
	if w := request("POST", "/cleanup?keep=0&days=0", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	site(prev, "/", 404, "")
	site(next["preview_url"].(string), "/", 200, "C")
	site(prod, "/", 200, "A")
}

func TestPagesStaticConfigurationPublication(t *testing.T) {
	srv, _ := newDeployTestServer(t)
	token := registerAndGetToken(t, srv, "static@example.test", "password123")
	createSubdomain(t, srv, token, "static")
	files := map[string]string{"index.html": "SPA", "404.html": "missing", "docs.html": "docs", "_droply.toml": "[site]\nmode = \"spa\"\n", "_redirects": "/old /docs 302\n"}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, buildDeployRequest(t, "/subdomains/static/projects/site/deploy", createTestTarGz(t, files), token))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	u, _ := url.Parse(out["url"].(string))
	for _, tc := range []struct {
		path string
		code int
		body string
	}{{"/docs", 200, "docs"}, {"/client/route", 200, "SPA"}, {"/_headers", 404, ""}, {"/_droply.toml", 404, ""}} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		r.Host = u.Host
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		srv.NewSiteHandler().ServeHTTP(w, r)
		if w.Code != tc.code || (tc.body != "" && w.Body.String() != tc.body) {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "/site/old", nil)
	r.Host = "static.droplydoc.com"
	w = httptest.NewRecorder()
	srv.NewSiteHandler().ServeHTTP(w, r)
	if w.Code != 302 || w.Header().Get("Location") != "/site/docs" {
		t.Fatalf("legacy redirect mount: %d %s", w.Code, w.Header().Get("Location"))
	}
}
