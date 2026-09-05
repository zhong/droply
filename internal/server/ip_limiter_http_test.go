package server_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginSurfacesHaveIndependentQuotasAndProxyTrust(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(fmt.Sprintf("trusted=%t", trusted), func(t *testing.T) {
			srv := newTestServer(t)
			if trusted {
				if err := srv.SetTrustedProxies([]string{"10.0.0.0/8"}); err != nil {
					t.Fatal(err)
				}
			}
			visitor := srv.NewSiteHandler()
			attempt := func(account bool, forwarded string) *httptest.ResponseRecorder {
				path, body := "/_droply/login", "%"
				if account {
					path, body = "/auth/login", "{"
				}
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				req.Host = "site.droplydoc.com"
				req.RemoteAddr = "10.0.0.1:1234"
				req.Header.Set("X-Forwarded-For", forwarded)
				req.Header.Set("X-Real-IP", forwarded)
				if !account {
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				}
				response := httptest.NewRecorder()
				if account {
					srv.ServeHTTP(response, req)
				} else {
					visitor.ServeHTTP(response, req)
				}
				return response
			}
			// Each surface receives ten requests from the same effective IP. Forged
			// forwarding values cannot create new buckets; a trusted proxy can.
			for i := range 11 {
				forwarded := "198.51.100.1"
				if !trusted {
					forwarded = fmt.Sprintf("198.51.100.%d", i+1)
				}
				for _, account := range []bool{true, false} {
					response := attempt(account, forwarded)
					want := 400
					if i == 10 {
						want = 429
					}
					if response.Code != want {
						t.Fatalf("account=%t attempt=%d status=%d want=%d", account, i, response.Code, want)
					}
					if i == 10 {
						if account {
							if response.Header().Get("Retry-After") != "6" || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), "too many authentication attempts") {
								t.Fatal("account throttle response changed")
							}
						} else if response.Header().Get("Retry-After") != "" || response.Header().Get("Cache-Control") != "private, no-store" || response.Body.String() != "Too Many Requests\n" {
							t.Fatal("visitor throttle response changed")
						}
					}
				}
			}
			for _, account := range []bool{true, false} {
				response := attempt(account, "203.0.113.20")
				want := 429
				if trusted {
					want = 400
				}
				if response.Code != want {
					t.Fatalf("new forwarding IP account=%t status=%d want=%d", account, response.Code, want)
				}
			}
		})
	}
}
