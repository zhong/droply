package wework

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestGetAuthorizeURL(t *testing.T) {
	c := NewClient(Config{
		CorpID:      "corp123",
		AgentID:     "1000002",
		Secret:      "secret",
		RedirectURI: "https://example.com/cb",
	})

	got := c.GetAuthorizeURL("state-xyz")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	// SSO login endpoint uses login.work.weixin.qq.com
	if u.Host != "login.work.weixin.qq.com" {
		t.Errorf("host: got %q want login.work.weixin.qq.com", u.Host)
	}
	if u.Path != "/wwlogin/sso/login" {
		t.Errorf("path: got %q want /wwlogin/sso/login", u.Path)
	}

	q := u.Query()
	if q.Get("login_type") != "CorpApp" {
		t.Errorf("login_type: got %q want CorpApp", q.Get("login_type"))
	}
	if q.Get("appid") != "corp123" {
		t.Errorf("appid: got %q want corp123", q.Get("appid"))
	}
	if q.Get("agentid") != "1000002" {
		t.Errorf("agentid: got %q want 1000002", q.Get("agentid"))
	}
	if q.Get("redirect_uri") != "https://example.com/cb" {
		t.Errorf("redirect_uri mismatch: %q", q.Get("redirect_uri"))
	}
	if q.Get("state") != "state-xyz" {
		t.Errorf("state: got %q", q.Get("state"))
	}
}

func TestGetAccessTokenSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cgi-bin/gettoken" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("corpid") != "corp123" {
			t.Errorf("corpid mismatch")
		}
		w.Write([]byte(`{"errcode":0,"errmsg":"ok","access_token":"tok-abc","expires_in":7200}`))
	}))
	defer srv.Close()

	c := NewClient(Config{
		CorpID:     "corp123",
		Secret:     "sec",
		APIBaseURL: srv.URL,
	})
	tok, err := c.GetAccessToken()
	if err != nil {
		t.Fatalf("GetAccessToken: %v", err)
	}
	if tok != "tok-abc" {
		t.Errorf("token: got %q", tok)
	}
}

func TestGetAccessTokenError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"errcode":40001,"errmsg":"invalid credential"}`))
	}))
	defer srv.Close()

	c := NewClient(Config{
		CorpID:     "corp123",
		Secret:     "bad",
		APIBaseURL: srv.URL,
	})
	if _, err := c.GetAccessToken(); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetUserIDByCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			w.Write([]byte(`{"errcode":0,"access_token":"tok-1"}`))
		case "/cgi-bin/auth/getuserinfo":
			if got := r.URL.Query().Get("code"); got != "auth-code-123" {
				t.Errorf("code mismatch: %q", got)
			}
			if got := r.URL.Query().Get("access_token"); got != "tok-1" {
				t.Errorf("access_token mismatch: %q", got)
			}
			w.Write([]byte(`{"errcode":0,"userid":"alice"}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewClient(Config{
		CorpID:     "corp",
		Secret:     "sec",
		APIBaseURL: srv.URL,
	})
	uid, err := c.GetUserIDByCode("auth-code-123")
	if err != nil {
		t.Fatalf("GetUserIDByCode: %v", err)
	}
	if uid != "alice" {
		t.Errorf("userid: got %q", uid)
	}
}

func TestGetUserIDByCodeNoUserID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			w.Write([]byte(`{"errcode":0,"access_token":"tok-1"}`))
		case "/cgi-bin/auth/getuserinfo":
			// External (non-corp) user — no userid in response.
			w.Write([]byte(`{"errcode":0,"OpenId":"ext-open-id"}`))
		}
	}))
	defer srv.Close()

	c := NewClient(Config{
		CorpID:     "corp",
		Secret:     "sec",
		APIBaseURL: srv.URL,
	})
	if _, err := c.GetUserIDByCode("code"); err == nil {
		t.Fatal("expected error for missing userid")
	}
}

func TestStateStoreGenerateAndConsume(t *testing.T) {
	s := NewStateStore(10 * time.Minute)

	data := StateData{
		Subdomain: "demo",
		Project:   "app",
		Host:      "demo.example.com",
		Redirect:  "/app/page",
	}
	token, err := s.Generate(data)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	got, ok := s.Consume(token)
	if !ok {
		t.Fatal("expected Consume ok")
	}
	if got.Subdomain != "demo" || got.Project != "app" || got.Redirect != "/app/page" {
		t.Errorf("StateData mismatch: %+v", got)
	}

	// Second consume should fail (one-time use).
	if _, ok := s.Consume(token); ok {
		t.Error("expected second Consume to fail")
	}
}

func TestStateStoreExpiration(t *testing.T) {
	s := NewStateStore(1 * time.Millisecond)

	token, err := s.Generate(StateData{Subdomain: "x"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, ok := s.Consume(token); ok {
		t.Error("expected expired state to fail Consume")
	}
}

func TestStateStoreUnknownToken(t *testing.T) {
	s := NewStateStore(time.Minute)
	if _, ok := s.Consume("nonexistent"); ok {
		t.Error("expected unknown token to fail")
	}
}
