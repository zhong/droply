//go:build integration

package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRollbackRetainsVersionsAndReportsRepeatedRequest(t *testing.T) {
	srv, _ := newDeployTestServer(t)
	token := registerAndGetToken(t, srv, "rollback@example.test", "password123")
	createSubdomain(t, srv, token, "rollback")
	base := "/subdomains/rollback/projects/site"
	for _, content := range []string{"A", "B"} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, buildDeployRequest(t, base+"/deploy", createTestTarGz(t, map[string]string{"index.html": content}), token))
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	for round := range 2 {
		r := httptest.NewRequest("POST", base+"/rollback/1", nil)
		r.Host = "api.droplydoc.com"
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("rollback: %d %s", w.Code, w.Body.String())
		}
		var result struct {
			Changed bool `json:"changed"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Changed != (round == 0) {
			t.Fatal("repeat rollback did not report unchanged")
		}
	}
	if w := siteResponse(srv.NewSiteHandler(), "rollback.droplydoc.com", "/site/"); w.Code != 200 || w.Body.String() != "A" {
		t.Fatalf("rollback content: %d %s", w.Code, w.Body.String())
	}
}

func TestRollbackCustomDomainKeepsCurrentAccessRules(t *testing.T) {
	f := newRecoveryFixture(t)
	for _, body := range []string{"A", "B"} {
		if w := f.upload(t, body); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	d := f.history(t)[0]
	if _, err := f.st.CreateCustomDomain(d.ProjectID, "rollback.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.VerifyCustomDomain("rollback.example.test"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("PUT", "/subdomains/recovery/projects/site/access", bytes.NewBufferString(`{"allowed_ips":["203.0.113.0/24"]}`))
	r.Host = "api.droplydoc.com"
	r.Header.Set("Authorization", "Bearer "+f.token)
	w := httptest.NewRecorder()
	f.srv.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := rollbackRequest(f, 1); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for host, path := range map[string]string{"recovery.droplydoc.com": "/site/", "rollback.example.test": "/"} {
		if w := siteResponse(f.srv.NewSiteHandler(), host, path); w.Code != 403 {
			t.Fatalf("current rule bypassed on %s: %d", host, w.Code)
		}
		r := httptest.NewRequest("GET", path, nil)
		r.Host = host
		r.RemoteAddr = "203.0.113.5:4321"
		w := httptest.NewRecorder()
		f.srv.NewSiteHandler().ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != "A" {
			t.Fatalf("authorized rollback on %s: %d %q", host, w.Code, w.Body.String())
		}
	}
}

func siteResponse(srv http.Handler, host, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Host = host
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w
}

func TestDeployRejectsCorruptionWithoutChangingProduction(t *testing.T) {
	srv, _ := newDeployTestServer(t)
	token := registerAndGetToken(t, srv, "atomic@example.test", "password123")
	createSubdomain(t, srv, token, "atomic")
	upload := func(archive *bytes.Buffer) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, buildDeployRequest(t, "/subdomains/atomic/projects/site/deploy", archive, token))
		return w
	}
	if w := upload(createTestTarGz(t, map[string]string{"index.html": "old", "removed.txt": "old file"})); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	bad := createTestTarGz(t, map[string]string{"index.html": "partial new"}).Bytes()
	bad[len(bad)-8] ^= 0xff // corrupt gzip checksum after an otherwise valid tar
	if w := upload(bytes.NewBuffer(bad)); w.Code < 400 {
		t.Fatalf("corrupt upload reported success: %d %s", w.Code, w.Body.String())
	}
	if w := siteResponse(srv.NewSiteHandler(), "atomic.droplydoc.com", "/site/"); w.Code != 200 || w.Body.String() != "old" {
		t.Fatalf("failed upload changed production: %d %s", w.Code, w.Body.String())
	}
	if w := upload(createTestTarGz(t, map[string]string{"index.html": "new"})); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := siteResponse(srv.NewSiteHandler(), "atomic.droplydoc.com", "/site/removed.txt"); w.Code != 404 {
		t.Fatalf("new deployment retained old file: %d", w.Code)
	}
}

func TestDeploymentValidatorsChangeOnPublishAndRollback(t *testing.T) {
	f := newRecoveryFixture(t)
	if w := f.upload(t, "A"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	a := siteResponse(f.srv.NewSiteHandler(), "recovery.droplydoc.com", "/site/")
	if a.Header().Get("ETag") == "" {
		t.Fatal("missing immutable deployment validator")
	}
	if w := f.upload(t, "B"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	r := httptest.NewRequest("GET", "/site/", nil)
	r.Host = "recovery.droplydoc.com"
	r.Header.Set("If-None-Match", a.Header().Get("ETag"))
	r.Header.Set("If-Modified-Since", a.Header().Get("Last-Modified"))
	b := httptest.NewRecorder()
	f.srv.NewSiteHandler().ServeHTTP(b, r)
	if b.Code != 200 || b.Body.String() != "B" || b.Header().Get("ETag") == a.Header().Get("ETag") {
		t.Fatalf("stale conditional response: %d %s", b.Code, b.Body.String())
	}
	if w := rollbackRequest(f, 1); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	r.Header.Set("If-None-Match", b.Header().Get("ETag"))
	w := httptest.NewRecorder()
	f.srv.NewSiteHandler().ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "A" || w.Header().Get("ETag") != a.Header().Get("ETag") {
		t.Fatalf("rollback validator: %d %s", w.Code, w.Body.String())
	}
	r.Header.Set("If-None-Match", a.Header().Get("ETag"))
	w = httptest.NewRecorder()
	f.srv.NewSiteHandler().ServeHTTP(w, r)
	if w.Code != 304 {
		t.Fatalf("same artifact should revalidate: %d", w.Code)
	}
}
