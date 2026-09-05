//go:build integration

package server_test

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSiteRequestPrivatePreviewAndRangeMatrix(t *testing.T) {
	f := newRecoveryFixture(t)
	files := map[string]string{"index.html": "index", "asset.txt": "0123456789", "_headers": "/*\n  Cache-Control: public, max-age=999\n"}
	base := "/subdomains/recovery/projects/site"
	for _, environment := range []string{"production", "preview"} {
		req := buildDeployRequest(t, base+"/deploy?environment="+environment, createTestTarGz(t, files), f.token)
		response := httptest.NewRecorder()
		f.srv.ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	history := f.history(t)
	project, err := f.st.GetProject(1, "site")
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []bool{false, true, false} {
		method, body := "DELETE", ""
		if private {
			method, body = "PUT", `{"allowed_ips":["192.0.2.1"]}`
		}
		if result := projectCredentialRequest(f, method, base+"/access", f.token, body); result.Code != 200 && result.Code != 204 {
			t.Fatal(result.Code, result.Body.String())
		}
		for _, host := range []struct {
			name, prefix string
			preview      bool
		}{
			{"recovery.droplydoc.com", "/site", false},
			{project.HostLabel + ".droplydoc.com", "", false},
			{history[0].PreviewLabel + ".droplydoc.com", "", true},
		} {
			for _, method := range []string{"GET", "HEAD", "Range"} {
				req := httptest.NewRequest("GET", host.prefix+"/asset.txt", nil)
				req.Host = host.name
				status, body := 200, "0123456789"
				switch method {
				case "HEAD":
					req.Method = "HEAD"
					body = ""
				case "Range":
					req.Header.Set("Range", "bytes=2-5")
					status, body = 206, "2345"
				}
				response := httptest.NewRecorder()
				f.srv.ServeHTTP(response, req)
				if response.Code != status || response.Body.String() != body {
					t.Fatalf("private=%t %s %s: %d %q", private, host.name, method, response.Code, response.Body.String())
				}
				cache := response.Header().Get("Cache-Control")
				if private {
					if cache != "private, no-store" || !strings.Contains(strings.Join(response.Header().Values("Vary"), ","), "Cookie") {
						t.Fatalf("private cache: %v", response.Header())
					}
				} else if cache != "public, max-age=999" {
					t.Fatalf("rule deletion left private state: %s", cache)
				}
				if strings.Contains(response.Header().Get("X-Robots-Tag"), "noindex") != host.preview {
					t.Fatalf("preview state: %v", response.Header())
				}
			}
		}
	}
}
