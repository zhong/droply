//go:build integration

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhong/droply/internal/store"
	"github.com/zhong/droply/internal/wework"
)

func TestWeWorkLegacyStateCannotAuthorizeProject(t *testing.T) {
	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	owner, err := st.CreateUser("owner@test", "hash", "owner")
	if err != nil {
		t.Fatal(err)
	}
	sub, err := st.CreateSubdomain(owner.ID, "team")
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateProject(sub.ID, "site")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutAccessRule(t.Context(), store.AccessRuleInput{SubdomainID: sub.ID, ProjectID: &project.ID, AllowedIPs: nil, PasswordHash: "", SessionTTL: 86400, WeWorkEnabled: true, AllowedWeWorkUsers: []string{"alice"}}); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/gettoken" {
			w.Write([]byte(`{"access_token":"test-token"}`))
			return
		}
		w.Write([]byte(`{"userid":"alice"}`))
	}))
	defer upstream.Close()
	srv := New(st, t.TempDir(), "example.test", []byte("legacy-state-test-key"))
	srv.SetWeWork(wework.NewClient(wework.Config{APIBaseURL: upstream.URL}))
	// State is only kept in process memory and disappears on binary upgrade.
	// Even if a caller supplies an old zero-ID shape, it must fail closed.
	state, err := srv.weworkState.Generate(wework.StateData{Subdomain: "team", Project: "site", Host: "team.example.test", Redirect: "/site/"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/_droply/wework/callback?code=code&state="+state, nil)
	req.Host = "team.example.test"
	response := httptest.NewRecorder()
	srv.NewSiteHandler().ServeHTTP(response, req)
	if response.Code != 404 {
		t.Fatalf("legacy state status = %d, want 404", response.Code)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "_droply_access" {
			t.Fatal("legacy state issued access cookie")
		}
	}
	if _, ok := srv.weworkState.Consume(state); ok {
		t.Fatal("legacy state was not consumed")
	}
}
