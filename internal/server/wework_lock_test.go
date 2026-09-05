//go:build integration

package server_test

import (
	"github.com/zhong/droply/internal/store"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/zhong/droply/internal/wework"
)

func TestWeWorkWaitAllowsPublicationAndRechecksDestination(t *testing.T) {
	for _, change := range []string{"none", "delete rule", "tighten rule", "delete project", "replace project", "unbind domain"} {
		t.Run(change, func(t *testing.T) {
			f := newRecoveryFixture(t)
			uploadCleanupVersions(t, f, 1)
			projectID := f.history(t)[0].ProjectID
			sub, err := f.st.GetSubdomainByName("recovery")
			if err != nil {
				t.Fatal(err)
			}
			setRule := func(users []string) {
				t.Helper()
				p, err := f.st.GetProject(sub.ID, "site")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.st.PutAccessRule(t.Context(), store.AccessRuleInput{SubdomainID: sub.ID, ProjectID: &p.ID, AllowedIPs: nil, PasswordHash: "", SessionTTL: 86400, WeWorkEnabled: true, AllowedWeWorkUsers: users}); err != nil {
					t.Fatal(err)
				}
			}
			setRule([]string{"alice"})
			host, redirect := "recovery.droplydoc.com", "/site/"
			if change == "unbind domain" {
				domain, err := f.st.CreateCustomDomain(projectID, "oauth.example.test")
				if err != nil {
					t.Fatal(err)
				}
				if err := f.st.VerifyCustomDomainChallenge(domain.Domain, domain.VerificationToken); err != nil {
					t.Fatal(err)
				}
				host, redirect = domain.Domain, "/"
			}
			entered := make(chan struct{})
			resume := make(chan struct{})
			var release sync.Once
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/cgi-bin/gettoken" {
					w.Write([]byte(`{"access_token":"test-token"}`))
					return
				}
				close(entered)
				select {
				case <-resume:
					w.Write([]byte(`{"userid":"alice"}`))
				case <-r.Context().Done():
				}
			}))
			defer upstream.Close()
			defer release.Do(func() { close(resume) })
			f.srv.SetWeWork(wework.NewClient(wework.Config{APIBaseURL: upstream.URL}))
			handler := f.srv.NewSiteHandler()
			req := httptest.NewRequest(http.MethodGet, "/_droply/wework/auth?redirect="+redirect+"&host="+host, nil)
			req.Host = host
			auth := httptest.NewRecorder()
			handler.ServeHTTP(auth, req)
			state := extractQueryParam(auth.Header().Get("Location"), "state")
			if state == "" {
				t.Fatalf("auth = %d %s", auth.Code, auth.Body.String())
			}
			req = httptest.NewRequest(http.MethodGet, "/_droply/wework/callback?code=code&state="+state, nil)
			req.Host = host
			callback := httptest.NewRecorder()
			callbackDone := make(chan struct{})
			go func() { handler.ServeHTTP(callback, req); close(callbackDone) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("callback did not reach upstream")
			}

			// Both operations acquire the deployment write lock. They must finish while
			// the upstream exchange remains paused, and cleanup must actually reclaim v1.
			upload := buildDeployRequest(t, "/subdomains/recovery/projects/site/deploy", createTestTarGz(t, map[string]string{"index.html": "new"}), f.token)
			publishDone := make(chan *httptest.ResponseRecorder, 1)
			go func() { w := httptest.NewRecorder(); f.srv.ServeHTTP(w, upload); publishDone <- w }()
			select {
			case w := <-publishDone:
				if w.Code != 200 {
					t.Fatalf("publish = %d %s", w.Code, w.Body.String())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("OAuth blocked publication")
			}
			cleanupDone := make(chan *httptest.ResponseRecorder, 1)
			go func() { cleanupDone <- cleanupRequest(f, http.MethodPost, "?keep=0&days=0") }()
			select {
			case w := <-cleanupDone:
				result := cleanupResult(t, w)
				if len(result.DeletedVersions) != 1 || result.DeletedVersions[0] != 1 {
					t.Fatalf("cleanup did not reclaim old version: %+v", result)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("OAuth blocked cleanup")
			}

			switch change {
			case "delete rule":
				if err := f.st.DeleteAccessRule(t.Context(), sub.ID, &projectID); err != nil {
					t.Fatal(err)
				}
			case "tighten rule":
				setRule([]string{"bob"})
			case "unbind domain":
				if err := f.st.DeleteCustomDomain(projectID, host); err != nil {
					t.Fatal(err)
				}
			case "delete project", "replace project":
				r := httptest.NewRequest(http.MethodDelete, "/subdomains/recovery/projects/site", nil)
				r.Header.Set("Authorization", "Bearer "+f.token)
				w := httptest.NewRecorder()
				f.srv.ServeHTTP(w, r)
				if w.Code != 204 {
					t.Fatalf("delete project = %d %s", w.Code, w.Body.String())
				}
				if change == "replace project" {
					if w := f.upload(t, "replacement"); w.Code != 200 {
						t.Fatal(w.Body.String())
					}
					setRule([]string{"alice"})
				}
			}
			release.Do(func() { close(resume) })
			select {
			case <-callbackDone:
			case <-time.After(3 * time.Second):
				t.Fatal("callback did not finish")
			}
			want := http.StatusNotFound
			if change == "none" {
				want = http.StatusFound
			}
			if change == "tighten rule" {
				want = http.StatusForbidden
			}
			if callback.Code != want {
				t.Fatalf("callback = %d %s, want %d", callback.Code, callback.Body.String(), want)
			}
			access := false
			for _, cookie := range callback.Result().Cookies() {
				if cookie.Name == "_droply_access" {
					access = true
				}
			}
			if access != (change == "none") {
				t.Fatalf("unexpected access cookie = %t", access)
			}
		})
	}
}

type pausedSiteWriter struct {
	*httptest.ResponseRecorder
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (w *pausedSiteWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.resume })
	return w.ResponseRecorder.Write(data)
}

func TestSiteResponseKeepsCleanupLockUntilComplete(t *testing.T) {
	f := newRecoveryFixture(t)
	uploadCleanupVersions(t, f, 2)
	sub, err := f.st.GetSubdomainByName("recovery")
	if err != nil {
		t.Fatal(err)
	}
	projectID := f.history(t)[0].ProjectID
	if _, err := f.st.PutAccessRule(t.Context(), store.AccessRuleInput{SubdomainID: sub.ID, ProjectID: &projectID, AllowedIPs: []string{"192.0.2.1"}, PasswordHash: "", SessionTTL: 86400, WeWorkEnabled: false, AllowedWeWorkUsers: nil}); err != nil {
		t.Fatal(err)
	}

	writer := &pausedSiteWriter{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), resume: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(writer.resume) })
	req := httptest.NewRequest(http.MethodGet, "/site/", nil)
	req.Host = "recovery.droplydoc.com"
	served := make(chan struct{})
	go func() { f.srv.NewSiteHandler().ServeHTTP(writer, req); close(served) }()
	select {
	case <-writer.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("site response did not start")
	}
	cleaned := make(chan *httptest.ResponseRecorder, 1)
	go func() { cleaned <- cleanupRequest(f, http.MethodPost, "?keep=0&days=0") }()
	select {
	case <-cleaned:
		t.Fatal("cleanup ran before site response completed")
	case <-time.After(100 * time.Millisecond):
	}
	release.Do(func() { close(writer.resume) })
	select {
	case <-served:
	case <-time.After(3 * time.Second):
		t.Fatal("site response did not finish")
	}
	if writer.Code != 200 || writer.Body.String() != "version-2" || writer.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("incomplete response = %d %q", writer.Code, writer.Body.String())
	}
	select {
	case w := <-cleaned:
		result := cleanupResult(t, w)
		if len(result.DeletedVersions) != 1 {
			t.Fatalf("cleanup = %+v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup did not finish after response")
	}
}
