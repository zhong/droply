//go:build integration

package server_test

import (
	"fmt"
	"net/http/httptest"
	"testing"
)

func TestCachedRulesFollowDeploymentAndLiveAccess(t *testing.T) {
	f := newRecoveryFixture(t)
	base := "/subdomains/recovery/projects/site"
	for _, name := range []string{"production", "preview"} {
		w := httptest.NewRecorder()
		files := map[string]string{"asset.txt": name, "_headers": "/*\n  X-Artifact: " + name + "\n"}
		f.srv.ServeHTTP(w, buildDeployRequest(t, base+"/deploy?environment="+name, createTestTarGz(t, files), f.token))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	history := f.history(t)
	project, err := f.st.GetProject(1, "site")
	if err != nil {
		t.Fatal(err)
	}
	production := project.HostLabel + ".droplydoc.com"
	preview := history[0].PreviewLabel + ".droplydoc.com"
	check := func(host, want string, isPreview bool) {
		t.Helper()
		for range 2 {
			r := httptest.NewRequest("GET", "/asset.txt", nil)
			r.Host = host
			w := httptest.NewRecorder()
			f.srv.ServeHTTP(w, r)
			if w.Code != 200 || w.Body.String() != want || w.Header().Get("X-Artifact") != want {
				t.Fatalf("%s: %d %s %v", host, w.Code, w.Body.String(), w.Header())
			}
			if (w.Header().Get("X-Robots-Tag") == "noindex, nofollow") != isPreview {
				t.Fatal("preview state was cached")
			}
		}
	}
	check(production, "production", false)
	check(preview, "preview", true)
	w := projectCredentialRequest(f, "POST", fmt.Sprintf("%s/promote/%d", base, history[0].Version), f.token, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	check(production, "preview", false)
	check(preview, "preview", true)
	w = projectCredentialRequest(f, "PUT", base+"/access", f.token, `{"allowed_ips":["203.0.113.0/24"]}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, host := range []string{production, preview} {
		r := httptest.NewRequest("GET", "/asset.txt", nil)
		r.Host = host
		w := httptest.NewRecorder()
		f.srv.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("warm cache bypassed live access:", w.Code)
		}
	}
	w = projectCredentialRequest(f, "DELETE", base+"/access", f.token, "")
	if w.Code != 200 && w.Code != 204 {
		t.Fatal(w.Code)
	}
	check(production, "preview", false)
	check(preview, "preview", true)
}
