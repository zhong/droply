//go:build integration

package server_test

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhong/droply/internal/certificates"
)

func TestCertificateStatusAuthorizationAndFailure(t *testing.T) {
	srv := newTestServer(t)
	owner := registerAndGetToken(t, srv, "cert-owner@example.com", "password123")
	stranger := registerAndGetToken(t, srv, "cert-stranger@example.com", "password123")
	createSubdomain(t, srv, owner, "certsite")
	host := "certsite.droplydoc.com"
	request := func(token, domain string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/certificates/"+domain, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
	for _, test := range []struct {
		name, token, domain string
		code                int
	}{
		{"unauthenticated", "", host, 401}, {"platform unauthenticated", "", "api.droplydoc.com", 401}, {"base unauthenticated", "", "droplydoc.com", 401}, {"invalid token", "invalid", host, 401}, {"non-owner", stranger, host, 403}, {"unknown domain", owner, "missing.droplydoc.com", 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := request(test.token, test.domain)
			if w.Code != test.code {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "expires_at") {
				t.Fatal("certificate detail leaked")
			}
		})
	}
	state := func(domain, want string) map[string]any {
		t.Helper()
		w := request(owner, domain)
		if w.Code != 200 {
			t.Fatalf("got %d: %s", w.Code, w.Body.String())
		}
		var response map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response["state"] != want || response["domain"] != strings.ToLower(strings.TrimSuffix(domain, ".")) {
			t.Fatalf("bad status: %+v", response)
		}
		return response
	}
	state("CERTSITE.DROPLYDOC.COM.", "externally-managed")
	ca := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "sensitive-provider-token", 500) }))
	defer ca.Close()
	manager, err := certificates.New(certificates.Config{Directory: t.TempDir(), CAURL: ca.URL, HTTPClient: ca.Client(), Allowed: srv.AllowedTLSHost})
	if err != nil {
		t.Fatal(err)
	}
	srv.SetCertificates(manager)
	state(host, "pending")
	for _, platform := range []string{"api.droplydoc.com", "droplydoc.com"} {
		state(platform, "pending")
		if _, err := manager.GetCertificate(&tls.ClientHelloInfo{ServerName: platform}); err == nil {
			t.Fatal("CA failure unexpectedly succeeded")
		}
		response := state(platform, "error")
		if response["last_error"] != "acme_issuance_failed" {
			t.Fatalf("platform failure missing: %+v", response)
		}
		w := request(stranger, platform)
		if w.Code != 200 || strings.Contains(w.Body.String(), "sensitive-provider-token") {
			t.Fatalf("authenticated platform status: %d %s", w.Code, w.Body)
		}
	}

	if _, err = manager.GetCertificate(&tls.ClientHelloInfo{ServerName: host}); err == nil {
		t.Fatal("CA failure unexpectedly succeeded")
	}
	response := state(host, "error")
	if response["last_error"] != "acme_issuance_failed" || response["retry_at"] == nil {
		t.Fatalf("missing controlled failure: %+v", response)
	}
	raw, _ := json.Marshal(response)
	if strings.Contains(string(raw), "sensitive-provider-token") {
		t.Fatal("CA secret leaked")
	}
	if w := request(stranger, host); w.Code != 403 {
		t.Fatalf("failure status bypassed ownership: %d", w.Code)
	}
}
