package staticweb_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhong/droply/internal/staticweb"
)

func fixture(t *testing.T, files map[string]string) *staticweb.Site {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	site, err := staticweb.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return site
}
func request(site *staticweb.Site, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	site.ServeHTTP(w, r, staticweb.Options{Path: r.URL.Path, ETagSeed: "version-one"})
	return w
}
func TestCleanHTMLAndCustom404(t *testing.T) {
	site := fixture(t, map[string]string{"about.html": "about", "404.html": "missing", "folder/index.html": "folder"})
	if w := request(site, "/about"); w.Code != 200 || w.Body.String() != "about" {
		t.Fatalf("clean HTML: %d %s", w.Code, w.Body.String())
	}
	if w := request(site, "/absent.js"); w.Code != 404 || w.Body.String() != "missing" {
		t.Fatalf("404: %d %s", w.Code, w.Body.String())
	}
	if w := request(site, "/folder"); w.Code != 301 || w.Header().Get("Location") != "/folder/" {
		t.Fatalf("directory: %d %v", w.Code, w.Header())
	}
}

func TestSPAOnlyFallsBackForHTMLNavigation(t *testing.T) {
	site := fixture(t, map[string]string{"_droply.toml": "[site]\nmode=\"spa\"", "index.html": "app", "404.html": "not found", ".well-known/security.txt": "contact"})
	for _, test := range []struct {
		path, accept, method string
		status               int
		body                 string
	}{
		{"/deep/page", "text/html", "GET", 200, "app"},
		{"/deep/page", "", "GET", 200, "app"},
		{"/deep/page", "text/html", "HEAD", 200, ""},
		{"/deep/page", "application/json", "GET", 404, "not found"},
		{"/deep/page", "text/html;q=0, */*;q=1", "GET", 404, "not found"},
		{"/missing.js", "text/html", "GET", 404, "not found"},
		{"/.well-known/missing", "text/html", "GET", 404, "not found"},
		{"/.well-known/security.txt", "*/*", "GET", 200, "contact"},
		{"/deep/page", "text/html", "POST", 405, "method not allowed\n"},
	} {
		t.Run(test.path+test.accept+test.method, func(t *testing.T) {
			r := httptest.NewRequest(test.method, test.path, nil)
			r.Header.Set("Accept", test.accept)
			w := httptest.NewRecorder()
			site.ServeHTTP(w, r, staticweb.Options{Path: test.path})
			if w.Code != test.status || w.Body.String() != test.body {
				t.Fatalf("%d %q", w.Code, w.Body.String())
			}
		})
	}
}

func TestHiddenPathsAndDirectoryListingDisabled(t *testing.T) {
	site := fixture(t, map[string]string{"_headers": "/*\n  X-Test: allowed", "_redirects": "/old /new 302", "_droply.toml": "[site]\nmode=\"static\"", ".secret": "hidden", "nested/.private": "hidden", "manifest.json": "private", "_droply/login": "platform", ".well-known/acme-challenge/token": "managed", "folder/file.txt": "file"})
	for _, path := range []string{"/_headers", "/_redirects", "/_droply.toml", "/.secret", "/nested/.private", "/manifest.json", "/_droply/login", "/.well-known/acme-challenge/token", "/folder/", "/folder", "/nested/../.secret"} {
		if w := request(site, path); w.Code != 404 {
			t.Fatalf("%s exposed: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestVersionedHeadersCacheAndConditionalRanges(t *testing.T) {
	site := fixture(t, map[string]string{"index.html": "html", "app.0123abcd.js": "abcdefghij", "other.css": "style", "_headers": "/*\n  X-Site: broad\n  Cache-Control: public, max-age=20\n  Vary: Accept-Encoding\n/app.0123abcd.js\n  X-Site: exact"})
	serve := func(path, method, seed string, headers map[string]string, private, preview bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		site.ServeHTTP(w, r, staticweb.Options{Path: path, ETagSeed: seed, Private: private, Preview: preview})
		return w
	}
	html := serve("/", "GET", "a", nil, false, false)
	if html.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal(html.Header())
	}
	first := serve("/app.0123abcd.js", "GET", "a", nil, false, false)
	if first.Header().Get("X-Site") != "exact" || first.Header().Get("Cache-Control") != "public, max-age=20" {
		t.Fatal(first.Header())
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	if w := serve("/app.0123abcd.js", "GET", "a", map[string]string{"If-None-Match": etag}, false, false); w.Code != 304 || w.Body.Len() != 0 {
		t.Fatalf("conditional: %d %s", w.Code, w.Body.String())
	}
	if w := serve("/app.0123abcd.js", "GET", "b", map[string]string{"If-None-Match": etag, "If-Modified-Since": "Wed, 31 Dec 2099 23:59:59 GMT"}, false, false); w.Code != 200 || w.Body.String() != "abcdefghij" {
		t.Fatal(w)
	}
	if w := serve("/app.0123abcd.js", "GET", "a", map[string]string{"Range": "bytes=2-4", "If-Range": etag}, false, false); w.Code != 206 || w.Body.String() != "cde" {
		t.Fatalf("range: %d %s", w.Code, w.Body.String())
	}
	if w := serve("/app.0123abcd.js", "GET", "b", map[string]string{"Range": "bytes=2-4", "If-Range": etag}, false, false); w.Code != 200 || w.Body.String() != "abcdefghij" {
		t.Fatal(w)
	}
	if w := serve("/app.0123abcd.js", "HEAD", "a", nil, false, false); w.Body.Len() != 0 || w.Header().Get("Content-Length") != "10" {
		t.Fatal(w)
	}
	for _, headers := range []map[string]string{nil, {"If-None-Match": etag}, {"Range": "bytes=100-200"}} {
		w := serve("/app.0123abcd.js", "GET", "a", headers, true, true)
		if w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(strings.Join(w.Header().Values("Vary"), ","), "Cookie") || !strings.Contains(w.Header().Get("X-Robots-Tag"), "noindex") {
			t.Fatalf("private constraints lost: %d %v", w.Code, w.Header())
		}
	}
	defaults := fixture(t, map[string]string{"app.0123abcd.js": "js", "other.css": "css", "index.html": "html"})
	if w := request(defaults, "/app.0123abcd.js"); w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatal(w.Header())
	}
	if w := request(defaults, "/other.css"); w.Header().Get("Cache-Control") != "public, max-age=0, must-revalidate" {
		t.Fatal(w.Header())
	}
}

func TestRedirectQueriesPrefixesAndTerminalRewrites(t *testing.T) {
	site := fixture(t, map[string]string{"index.html": "app", "new.html": "rewritten", "folder/index.html": "folder", "_redirects": "/old/* /new/:splat 301\n/query /new?fixed=1 302\n/clear /new? 303\n/external https://example.org 307\n/rewrite /new.html 200\n/chain /rewrite 200"})
	for _, test := range []struct {
		input, target string
		status        int
	}{
		{"/old/path?q=1", "/blog/new/path?q=1", 301}, {"/query?q=1", "/blog/new?fixed=1", 302}, {"/clear?q=1", "/blog/new?", 303}, {"/external?q=1", "https://example.org/?q=1", 307}, {"/folder?q=1", "/blog/folder/?q=1", 301},
	} {
		r := httptest.NewRequest("GET", "/blog"+test.input, nil)
		w := httptest.NewRecorder()
		site.ServeHTTP(w, r, staticweb.Options{Path: strings.Split(test.input, "?")[0], Prefix: "/blog"})
		if w.Code != test.status || w.Header().Get("Location") != test.target {
			t.Fatalf("%s: %d %s", test.input, w.Code, w.Header().Get("Location"))
		}
	}
	if w := request(site, "/rewrite"); w.Code != 200 || w.Body.String() != "rewritten" {
		t.Fatal(w)
	}
	if w := request(site, "/chain"); w.Code != 404 {
		t.Fatal("rewrites unexpectedly recurse", w)
	}
	spa := fixture(t, map[string]string{"index.html": "app", "_redirects": "/* /index.html 200"})
	if w := request(spa, "/deep"); w.Code != 200 || w.Body.String() != "app" {
		t.Fatal(w)
	}
	if w := request(spa, "/missing.js"); w.Code != 404 {
		t.Fatal("catch-all returned HTML for JS", w)
	}
}

func TestInvalidDeploymentRulesFailValidation(t *testing.T) {
	for _, test := range []struct{ name, file, data string }{
		{"unknown config", "_droply.toml", "[site]\nmode=\"spa\"\nunknown=true"},
		{"invalid mode", "_droply.toml", "[site]\nmode=\"dynamic\""},
		{"cookie header", "_headers", "/*\n  Set-Cookie: bypass=1"},
		{"location header", "_headers", "/*\n  Location: /elsewhere"},
		{"ETag header", "_headers", "/*\n  ETag: bad"},
		{"auth header", "_headers", "/*\n  WWW-Authenticate: Basic"},
		{"hop-by-hop", "_headers", "/*\n  Connection: keep-alive"},
		{"empty block", "_headers", "/*"},
		{"condition syntax", "_redirects", "/a /b 302 Country=US"},
		{"force syntax", "_redirects", "/a /b 200!"},
		{"proxy rewrite", "_redirects", "/a https://example.org/b 200"},
		{"unsafe path", "_redirects", "/a /../outside 200"},
		{"hidden target", "_redirects", "/a /.env 200"},
		{"auth target", "_redirects", "/a /_droply/login 200"},
		{"protocol relative", "_redirects", "/a //evil.example/b 302"},
		{"source placeholder", "_redirects", "/a/:id /b 302"},
		{"query splat", "_redirects", "/a/* /b?q=:splat 302"},
		{"simple loop", "_redirects", "/a /b 302\n/b /a 301"},
		{"growing loop", "_redirects", "/a/* /a/more/:splat 302"},
		{"rewrite loop", "_redirects", "/a /b 200\n/b /a 200"},
		{"long line", "_headers", "/" + strings.Repeat("a", 2049)},
		{"huge config", "_headers", strings.Repeat("#", 65537)},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, test.file), []byte(test.data), 0644); err != nil {
				t.Fatal(err)
			}
			if err := staticweb.Validate(dir); err == nil {
				t.Fatal("invalid rule accepted")
			}
		})
	}
}

func TestCustom404RetainsStatusAndPrivateGuards(t *testing.T) {
	site := fixture(t, map[string]string{"404.html": "missing", "_headers": "/*\n  Cache-Control: public, max-age=31536000\n  X-Robots-Tag: index"})
	r := httptest.NewRequest("HEAD", "/missing", nil)
	r.Header.Set("If-None-Match", "*")
	r.Header.Set("Range", "bytes=0-2")
	w := httptest.NewRecorder()
	site.ServeHTTP(w, r, staticweb.Options{Path: "/missing", Private: true, Preview: true})
	if w.Code != http.StatusNotFound || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "7" || w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(w.Header().Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("bad protected404: %d %v", w.Code, w.Header())
	}
}

func TestDirectoryCanonicalizationCannotCreateRedirectLoop(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "folder", "index.html"), []byte("folder"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "_redirects"), []byte("/folder/ /folder 301"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := staticweb.Validate(dir); err == nil {
		t.Fatal("implicit directory redirect loop accepted")
	}
	site := fixture(t, map[string]string{"folder/index.html": "folder", "_redirects": "/alias /folder 200"})
	if w := request(site, "/alias"); w.Code != 200 || w.Body.String() != "folder" {
		t.Fatal("directory rewrite redirected instead of serving", w)
	}
}
