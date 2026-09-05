//go:build integration

package server_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestUnifiedHandlerRejectsUnknownAPIHost(t *testing.T) {
	srv, _ := newTestSiteServer(t)
	for _, tc := range []struct {
		host   string
		status int
	}{
		{"api.droplydoc.com", http.StatusUnauthorized},
		{"API.DROPLYDOC.COM:8080", http.StatusUnauthorized},
		{"attacker.example", http.StatusNotFound},
		{"alice.droplydoc.com", http.StatusNotFound},
	} {
		t.Run(tc.host, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://"+tc.host+"/subdomains", nil)
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)
			if rr.Code != tc.status {
				t.Fatalf("status %d, want %d", rr.Code, tc.status)
			}
		})
	}
}

func TestHTTPLoginCookieAndRedirectBoundary(t *testing.T) {
	srv, dir := newTestSiteServer(t)
	_, password := setupProtectedSite(t, srv, dir)
	for _, tc := range []struct {
		name, path string
		status     int
	}{
		{"local path", "/docs/hello.txt", 302},
		{"external URL", "https://other.example/docs/hello.txt", 400},
		{"network path", "//other.example/docs/hello.txt", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{"password": {password}, "host": {"alice.droplydoc.com"}, "redirect": {tc.path}}
			req := httptest.NewRequest("POST", "http://alice.droplydoc.com/_droply/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)
			if rr.Code != tc.status {
				t.Fatalf("got %d want %d", rr.Code, tc.status)
			}
			if tc.status == 302 {
				cookies := rr.Result().Cookies()
				if len(cookies) != 1 || cookies[0].Secure {
					t.Fatal("HTTP mode cannot use its login cookie")
				}
			}
		})
	}
}

func TestDirectRequestCannotSpoofWhitelistedIP(t *testing.T) {
	srv, dir := newTestSiteServer(t)
	token, _ := setupProtectedSite(t, srv, dir)
	request := httptest.NewRequest("PUT", "/subdomains/alice/projects/docs/access", strings.NewReader(`{"allowed_ips":["10.1.2.3"]}`))
	request.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, request)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	for _, tc := range []struct {
		name, remote, xff string
		trusted           []string
		status            int
	}{
		{"direct spoof", "192.0.2.1:1234", "10.1.2.3", nil, 403},
		{"trusted proxy", "127.0.0.1:1234", "10.1.2.3", []string{"127.0.0.1/32"}, 200},
		{"forged leftmost", "127.0.0.1:1234", "10.1.2.3, 192.0.2.1", []string{"127.0.0.1/32"}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := srv.SetTrustedProxies(tc.trusted); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "http://alice.droplydoc.com/docs/hello.txt", nil)
			req.RemoteAddr = tc.remote
			req.Header.Set("X-Forwarded-For", tc.xff)
			req.Header.Set("X-Real-IP", "10.1.2.3")
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)
			if rr.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rr.Code, tc.status, rr.Body.String())
			}
		})
	}
}
