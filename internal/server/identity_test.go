package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
	"net/http/httptest"
	"testing"
)

func TestPrivateIdentityRegistrationAndAdministration(t *testing.T) {
	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := server.New(st, t.TempDir(), "example.test", []byte("identity-test-key"))
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Host = "api.example.test"
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
	if w := request("POST", "/auth/register", "", `{"email":"attacker@test","password":"password"}`); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := st.GetUserByEmail("attacker@test"); err == nil {
		t.Fatal("remote bootstrap created user")
	}
	admin, err := st.BootstrapAdmin(t.Context(), "admin@test", "hash", "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	if w := request("GET", "/auth/me", admin.APIToken, ""); w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte(admin.APIToken)) {
		t.Fatal("identity response leaks token")
	}
	w := request("POST", "/admin/invitations", admin.APIToken, `{"email":"member@test"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var invitation struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &invitation); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"email": "member@test", "password": "password123", "invite": invitation.Token})
	w = request("POST", "/auth/register", "", string(body))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var auth struct {
		Token string `json:"api_token"`
	}
	json.Unmarshal(w.Body.Bytes(), &auth)
	if w := request("POST", "/auth/register", "", string(body)); w.Code != 403 {
		t.Fatal("invitation reusable", w.Code)
	}
	for _, path := range []string{"/admin/invitations"} {
		if w := request("GET", path, auth.Token, ""); w.Code != 403 {
			t.Fatal("member admin bypass", w.Code)
		}
	}
	if w := request("POST", "/auth/login", "", `{"email":"member@test","password":"password123"}`); w.Code != 200 {
		t.Fatal("closed registration broke existing login", w.Code)
	}
	revoked := request("POST", "/admin/invitations", admin.APIToken, `{"email":"revoked@test"}`)
	var revokeInfo struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if revoked.Code != 201 || json.Unmarshal(revoked.Body.Bytes(), &revokeInfo) != nil {
		t.Fatal(revoked.Code, revoked.Body.String())
	}
	if w := request("DELETE", fmt.Sprintf("/admin/invitations/%d", revokeInfo.ID), auth.Token, ""); w.Code != 403 {
		t.Fatal("member revoked invitation", w.Code)
	}
	if w := request("DELETE", fmt.Sprintf("/admin/invitations/%d", revokeInfo.ID), admin.APIToken, ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	revokedBody, _ := json.Marshal(map[string]string{"email": "revoked@test", "password": "password123", "invite": revokeInfo.Token})
	if w := request("POST", "/auth/register", "", string(revokedBody)); w.Code != 403 {
		t.Fatal("revoked invitation accepted", w.Code)
	}
	if w := request("GET", "/admin/invitations", admin.APIToken, ""); w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte(revokeInfo.Token)) {
		t.Fatal("invitation list exposed credential")
	}
	srv.SetOpenRegistration(true)
	if w := request("POST", "/auth/register", "", `{"email":"open@test","password":"password123"}`); w.Code != 201 {
		t.Fatal("explicit open registration", w.Code)
	}
}
func TestAuthenticationThrottleIgnoresForgedForwardingHeaders(t *testing.T) {
	// Malformed authentication attempts exercise the same limiter without bcrypt
	// allowing refill time to elapse on heavily instrumented race builds.
	srv := newTestServer(t)
	for i := range 12 {
		r := httptest.NewRequest("POST", "/auth/login", bytes.NewBufferString(`{"email":"absent@test","password":"secret"`))
		r.Host = "api.droplydoc.com"
		r.Header.Set("X-Forwarded-For", string(rune(65+i)))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		want := 400
		if i >= 10 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
		if bytes.Contains(w.Body.Bytes(), []byte("secret")) {
			t.Fatal("credential leaked")
		}
	}
}
