//go:build integration

package server_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhong/droply/internal/model"
)

type issuedProjectToken struct {
	*model.ProjectToken
	Token string `json:"token"`
}

func projectCredentialRequest(f *recoveryFixture, method, path, credential string, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+credential)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.srv.ServeHTTP(w, r)
	return w
}
func issueProjectCredential(t *testing.T, f *recoveryFixture, scopes []string) issuedProjectToken {
	t.Helper()
	body, err := json.Marshal(map[string]any{"name": "pipeline", "scopes": scopes})
	if err != nil {
		t.Fatal(err)
	}
	w := projectCredentialRequest(f, http.MethodPost, "/subdomains/recovery/projects/site/tokens", f.token, string(body))
	if w.Code != 201 {
		t.Fatalf("create token: %d %s", w.Code, w.Body.String())
	}
	var issued issuedProjectToken
	if err = json.Unmarshal(w.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	if issued.Token == "" || issued.ProjectToken == nil {
		t.Fatal("token not returned once")
	}
	return issued
}

func TestProjectTokenHTTPAuthorizationMatrix(t *testing.T) {
	f := newRecoveryFixture(t)
	uploadCleanupVersions(t, f, 1)
	preview := issueProjectCredential(t, f, nil)
	production := issueProjectCredential(t, f, []string{"production"})
	const base = "/subdomains/recovery/projects/site"
	// Existing long-lived user authentication remains valid.
	if w := projectCredentialRequest(f, "GET", base+"/deployments", f.token, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, credential := range []issuedProjectToken{preview, production} {
		if w := projectCredentialRequest(f, "GET", base+"/events", credential.Token, ""); w.Code != 200 {
			t.Fatalf("scoped events: %d %s", w.Code, w.Body.String())
		}
		if w := projectCredentialRequest(f, "GET", base+"/deployments", credential.Token, ""); w.Code != 200 {
			t.Fatalf("scoped history: %d %s", w.Code, w.Body.String())
		}
		for _, test := range []struct{ method, path string }{{"POST", base + "/tokens"}, {"GET", base + "/tokens"}, {"DELETE", base + "/tokens/1"}, {"GET", base + "/domains"}, {"GET", base + "/members"}, {"GET", base + "/access"}, {"GET", base + "/stats"}, {"GET", base + "/logs"}, {"GET", base + "/cleanup"}, {"GET", base + "/audit"}, {"PUT", base + "/members"}, {"DELETE", base + "/members/1"}, {"DELETE", base + "/access"}, {"POST", base + "/domains"}, {"DELETE", base + "/domains/example.test"}, {"POST", base + "/domains/example.test/verify"}, {"PUT", base + "/access"}, {"POST", base + "/cleanup"}, {"DELETE", base}, {"GET", "/subdomains"}, {"POST", "/subdomains"}} {
			if w := projectCredentialRequest(f, test.method, test.path, credential.Token, "{}"); w.Code != 403 {
				t.Fatalf("%s %s accepted project token: %d %s", test.method, test.path, w.Code, w.Body.String())
			}
		}
	}
	if w := projectCredentialRequest(f, "POST", base+"/promote/1", preview.Token, ""); w.Code != 403 {
		t.Fatalf("preview promotion: %d", w.Code)
	}
	if w := projectCredentialRequest(f, "POST", base+"/rollback/1", preview.Token, ""); w.Code != 403 {
		t.Fatalf("preview rollback: %d", w.Code)
	}
	if w := projectCredentialRequest(f, "POST", base+"/rollback/1", production.Token, ""); w.Code != 200 {
		t.Fatalf("production rollback: %d %s", w.Code, w.Body.String())
	}
	for _, test := range []struct {
		credential, environment string
		want                    int
	}{{preview.Token, "", 403}, {preview.Token, "?environment=production", 403}, {preview.Token, "?environment=preview&environment=production", 403}, {production.Token, "?environment=preview", 403}, {preview.Token, "?environment=preview", 200}, {production.Token, "?environment=production", 200}} {
		r := buildDeployRequest(t, base+"/deploy"+test.environment, createTestTarGz(t, map[string]string{"index.html": "scoped"}), test.credential)
		w := httptest.NewRecorder()
		f.srv.ServeHTTP(w, r)
		if w.Code != test.want {
			t.Fatalf("upload environment %q: got %d want %d: %s", test.environment, w.Code, test.want, w.Body.String())
		}
	}
	// The successful preview upload is version 2; promotion requires production scope.
	if w := projectCredentialRequest(f, "POST", base+"/promote/2", production.Token, ""); w.Code != 200 {
		t.Fatalf("production-scope promotion: %d %s", w.Code, w.Body.String())
	}
	// Same owner, different project must not widen credential scope.
	w := httptest.NewRecorder()
	f.srv.ServeHTTP(w, buildDeployRequest(t, "/subdomains/recovery/projects/other/deploy", createTestTarGz(t, map[string]string{"index.html": "other"}), f.token))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := projectCredentialRequest(f, "GET", "/subdomains/recovery/projects/other/deployments", preview.Token, ""); w.Code != 403 {
		t.Fatalf("cross-project history: %d", w.Code)
	}
	listed := projectCredentialRequest(f, "GET", base+"/tokens", f.token, "")
	if listed.Code != 200 || bytes.Contains(listed.Body.Bytes(), []byte(preview.Token)) || bytes.Contains(listed.Body.Bytes(), []byte("digest")) {
		t.Fatalf("list secret exposure: %d", listed.Code)
	}
	if w := projectCredentialRequest(f, "DELETE", fmt.Sprintf("%s/tokens/%d", base, preview.ID), f.token, ""); w.Code != 204 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}
	if w := projectCredentialRequest(f, "GET", base+"/deployments", preview.Token, ""); w.Code != 401 {
		t.Fatalf("revoked token: %d", w.Code)
	}
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`UPDATE project_tokens SET expires_at=? WHERE id=?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), production.ID); err != nil {
		t.Fatal(err)
	}
	if w := projectCredentialRequest(f, "GET", base+"/deployments", production.Token, ""); w.Code != 401 {
		t.Fatalf("expired token: %d", w.Code)
	}
}

func TestProjectTokenRevokedDuringUploadPreventsPublication(t *testing.T) {
	for _, viaHTTP := range []bool{true, false} {
		t.Run(fmt.Sprintf("http=%t", viaHTTP), func(t *testing.T) {
			f := newRecoveryFixture(t)
			if w := f.upload(t, "original"); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			credential := issueProjectCredential(t, f, []string{"production"})
			request := buildDeployRequest(t, "/subdomains/recovery/projects/site/deploy", createTestTarGz(t, map[string]string{"index.html": "revoked"}), credential.Token)
			request.Host = "api.droplydoc.com"
			blocked := &pausedUploadBody{ReadCloser: request.Body, entered: make(chan struct{}), resume: make(chan struct{})}
			request.Body = blocked
			resume := sync.OnceFunc(func() { close(blocked.resume) })
			defer resume()
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() { w := httptest.NewRecorder(); f.srv.ServeHTTP(w, request); result <- w }()
			select {
			case <-blocked.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("upload did not start")
			}
			if viaHTTP {
				revoke := httptest.NewRequest("DELETE", fmt.Sprintf("/subdomains/recovery/projects/site/tokens/%d", credential.ID), nil)
				revoke.Host = "api.droplydoc.com"
				revoke.Header.Set("Authorization", "Bearer "+f.token)
				response := httptest.NewRecorder()
				f.srv.ServeHTTP(response, revoke)
				if response.Code != 204 {
					t.Fatal(response.Code, response.Body.String())
				}
			} else if err := f.st.RevokeProjectToken(t.Context(), credential.ProjectID, credential.IssuerID, credential.ID); err != nil {
				t.Fatal(err)
			}
			// Revocation has returned before upload can finish streaming.
			resume()
			select {
			case response := <-result:
				if response.Code != 403 {
					t.Fatalf("revoked upload: %d %s", response.Code, response.Body.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("upload did not finish")
			}
			f.assertContent(t, "original")
			active, err := f.st.GetActiveDeployment(t.Context(), credential.ProjectID)
			if err != nil || active.Version != 1 {
				t.Fatalf("revoked credential published: %+v %v", active, err)
			}
		})
	}
}
