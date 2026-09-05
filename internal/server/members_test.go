package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
	"io"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestProjectCollaborationPermissionMatrixAndRevocation(t *testing.T) {
	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := server.New(st, t.TempDir(), "droplydoc.com", []byte("members-test-key"))
	ids := map[string]int64{}
	for _, name := range []string{"owner", "deployer", "viewer", "stranger"} {
		u, err := st.CreateUser(name+"@test", "hash", name)
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = u.ID
	}
	sub, err := st.CreateSubdomain(ids["owner"], "team")
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateProject(sub.ID, "site")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProject(sub.ID, "secret"); err != nil {
		t.Fatal(err)
	}
	base := "/subdomains/team/projects/site"
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Host = "api.droplydoc.com"
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
	for _, role := range []string{"deployer", "viewer"} {
		w := request("PUT", base+"/members", "owner", fmt.Sprintf(`{"email":%q,"role":%q}`, role+"@test", role))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	upload := func(token, query, content string) *httptest.ResponseRecorder {
		t.Helper()
		r := buildDeployRequest(t, base+"/deploy"+query, createTestTarGz(t, map[string]string{"index.html": content}), token)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
	if w := upload("owner", "", "A"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := upload("deployer", "?environment=preview", "B"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("PUT", base+"/access", "owner", `{"allowed_ips":["203.0.113.0/24"]}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	visitor := httptest.NewRequest("GET", "/", nil)
	visitor.Host = project.HostLabel + ".droplydoc.com"
	visitor.Header.Set("Authorization", "Bearer owner")
	visitorResult := httptest.NewRecorder()
	srv.NewSiteHandler().ServeHTTP(visitorResult, visitor)
	if visitorResult.Code != 403 {
		t.Fatal("publisher membership bypassed visitor access rules", visitorResult.Code)
	}
	reads := []string{base + "/deployments", base + "/events", base + "/domains", base + "/access", base + "/members", base + "/stats", base + "/logs", base + "/cleanup"}
	for _, role := range []string{"owner", "deployer", "viewer", "stranger"} {
		for _, path := range reads {
			w := request("GET", path, role, "")
			want := 200
			if role == "stranger" {
				want = 403
			}
			if w.Code != want {
				t.Errorf("%s GET %s: %d %s", role, path, w.Code, w.Body.String())
			}
		}
	}
	for _, role := range []string{"viewer", "stranger"} {
		if w := upload(role, "", "BAD"); w.Code != 403 {
			t.Errorf("%s deployed: %d", role, w.Code)
		}
	}
	for _, role := range []string{"deployer", "viewer", "stranger"} {
		for _, tc := range []struct{ method, path, body string }{{"PUT", base + "/members", `{"email":"stranger@test","role":"deployer"}`}, {"PUT", base + "/access", `{"allowed_ips":[]}`}, {"POST", base + "/domains", `{"domain":"new.example.test"}`}, {"DELETE", base, ""}, {"DELETE", "/subdomains/team", ""}, {"POST", base + "/cleanup", ""}} {
			if w := request(tc.method, tc.path, role, tc.body); w.Code != 403 {
				t.Errorf("%s %s %s: %d", role, tc.method, tc.path, w.Code)
			}
		}
	}
	for _, role := range []string{"viewer", "stranger"} {
		for _, action := range []string{"promote/2", "rollback/1"} {
			if w := request("POST", base+"/"+action, role, ""); w.Code != 403 {
				t.Errorf("%s %s: %d", role, action, w.Code)
			}
		}
	}
	if w := request("POST", base+"/promote/2", "deployer", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("POST", base+"/rollback/1", "deployer", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Shared users see only granted projects in both list surfaces.
	for _, path := range []string{"/projects", "/subdomains/team/projects"} {
		w := request("GET", path, "viewer", "")
		if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte("secret")) {
			t.Fatal("project list leaked", w.Code, w.Body.String())
		}
	}
	if w := request("DELETE", fmt.Sprintf("%s/members/%d", base, ids["owner"]), "owner", ""); w.Code != 409 {
		t.Fatal("owner removable", w.Code)
	}
	if w := request("POST", base+"/tokens", "viewer", `{"name":"ci"}`); w.Code != 403 {
		t.Fatal("viewer created token", w.Code)
	}
	tokenResult := request("POST", base+"/tokens", "deployer", `{"name":"ci","scopes":["preview","production"]}`)
	if tokenResult.Code != 201 {
		t.Fatal(tokenResult.Code, tokenResult.Body.String())
	}
	var credential struct {
		Token string `json:"token"`
	}
	json.Unmarshal(tokenResult.Body.Bytes(), &credential)
	if w := upload(credential.Token, "?environment=preview", "CI"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("GET", base+"/tokens", "owner", ""); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("issuer_id")) {
		t.Fatal("owner cannot inspect issuer")
	}
	if w := request("DELETE", fmt.Sprintf("%s/members/%d", base, ids["deployer"]), "owner", ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("GET", base+"/deployments", "deployer", ""); w.Code != 403 {
		t.Fatal("removed member retained read", w.Code)
	}
	if w := upload(credential.Token, "?environment=preview", "REVOKED"); w.Code != 401 {
		t.Fatal("removed issuer retained token", w.Code)
	}
	role, err := st.ProjectRole(t.Context(), project.ID, ids["owner"])
	if err != nil || role != "owner" {
		t.Fatal("legacy owner lost")
	}
}

// An upload may be streaming while its publisher loses membership. Publication
// rechecks the current role after staging, while the old production stays live.
func TestMembershipRevokedDuringUploadPreventsPublication(t *testing.T) {
	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	owner, err := st.CreateUser("owner@test", "hash", "owner")
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateUser("member@test", "hash", "member")
	if err != nil {
		t.Fatal(err)
	}
	sub, err := st.CreateSubdomain(owner.ID, "stream")
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateProject(sub.ID, "site")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutProjectMember(t.Context(), project.ID, member.Email, "deployer"); err != nil {
		t.Fatal(err)
	}
	srv := server.New(st, t.TempDir(), "droplydoc.com", []byte("stream-key"))
	base := "/subdomains/stream/projects/site"
	request := buildDeployRequest(t, base+"/deploy", createTestTarGz(t, map[string]string{"index.html": "should-not-publish"}), "member")
	blocked := &pausedUploadBody{ReadCloser: request.Body, entered: make(chan struct{}), resume: make(chan struct{})}
	request.Body = blocked
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { w := httptest.NewRecorder(); srv.ServeHTTP(w, request); result <- w }()
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not start")
	}
	revoke := httptest.NewRequest("DELETE", fmt.Sprintf("%s/members/%d", base, member.ID), nil)
	revoke.Host = "api.droplydoc.com"
	revoke.Header.Set("Authorization", "Bearer owner")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, revoke)
	close(blocked.resume)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	select {
	case w := <-result:
		if w.Code != 403 {
			t.Fatal("revoked upload published", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not finish")
	}
	if _, err := st.GetActiveDeployment(t.Context(), project.ID); err == nil {
		t.Fatal("revoked upload became production")
	}
}

type pausedUploadBody struct {
	io.ReadCloser
	once            sync.Once
	entered, resume chan struct{}
}

func (b *pausedUploadBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered); <-b.resume })
	return b.ReadCloser.Read(p)
}
