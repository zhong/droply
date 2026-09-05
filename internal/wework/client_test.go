package wework

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

func TestGetMobileAuthorizeURL(t *testing.T) {
	c := NewClient(Config{
		CorpID:      "corp123",
		AgentID:     "1000002",
		Secret:      "secret",
		RedirectURI: "https://example.com/cb",
	})

	got := c.GetMobileAuthorizeURL("state-mob")

	// Must keep #wechat_redirect fragment for WeCom in-app browser to follow it.
	if !strings.HasSuffix(got, "#wechat_redirect") {
		t.Errorf("expected #wechat_redirect suffix, got %q", got)
	}
	urlPart := strings.TrimSuffix(got, "#wechat_redirect")
	u, err := url.Parse(urlPart)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	if u.Host != "open.weixin.qq.com" {
		t.Errorf("host: got %q want open.weixin.qq.com", u.Host)
	}
	if u.Path != "/connect/oauth2/authorize" {
		t.Errorf("path: got %q want /connect/oauth2/authorize", u.Path)
	}

	q := u.Query()
	if q.Get("appid") != "corp123" {
		t.Errorf("appid: got %q want corp123", q.Get("appid"))
	}
	if q.Get("redirect_uri") != "https://example.com/cb" {
		t.Errorf("redirect_uri mismatch: %q", q.Get("redirect_uri"))
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type: got %q want code", q.Get("response_type"))
	}
	if q.Get("scope") != "snsapi_base" {
		t.Errorf("scope: got %q want snsapi_base", q.Get("scope"))
	}
	if q.Get("agentid") != "1000002" {
		t.Errorf("agentid: got %q want 1000002", q.Get("agentid"))
	}
	if q.Get("state") != "state-mob" {
		t.Errorf("state: got %q want state-mob", q.Get("state"))
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
	tok, err := c.GetAccessToken(t.Context())
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
	if _, err := c.GetAccessToken(t.Context()); err == nil {
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
	uid, err := c.GetUserIDByCode(t.Context(), "auth-code-123")
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
	if _, err := c.GetUserIDByCode(t.Context(), "code"); err == nil {
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

func TestClientCancellationAndTimeout(t *testing.T) {
	for _, operation := range []string{"token", "code", "user"} {
		for _, failure := range []string{"cancel", "timeout", "body timeout"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				started := make(chan struct{})
				stopped := make(chan struct{})
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if operation != "token" && r.URL.Path == "/cgi-bin/gettoken" {
						w.Write([]byte(`{"access_token":"private-token"}`))
						return
					}
					if failure == "body timeout" {
						w.WriteHeader(http.StatusOK)
						w.(http.Flusher).Flush()
					}
					close(started)
					<-r.Context().Done()
					close(stopped)
				}))
				defer upstream.Close()
				injected := upstream.Client()
				if failure != "cancel" {
					injected.Timeout = 50 * time.Millisecond
				}
				client := NewClient(Config{APIBaseURL: upstream.URL, HTTPClient: injected})
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan error, 1)
				go func() {
					var err error
					switch operation {
					case "token":
						_, err = client.GetAccessToken(ctx)
					case "code":
						_, err = client.GetUserIDByCode(ctx, "private-code")
					case "user":
						_, err = client.GetUserInfo(ctx, "alice")
					}
					done <- err
				}()
				select {
				case <-started:
				case <-time.After(2 * time.Second):
					t.Fatal("upstream never started")
				}
				expected := context.DeadlineExceeded
				if failure == "cancel" {
					cancel()
					expected = context.Canceled
				}
				select {
				case err := <-done:
					if !errors.Is(err, expected) {
						t.Fatalf("error = %v, want %v", err, expected)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("operation did not terminate")
				}
				select {
				case <-stopped:
				case <-time.After(2 * time.Second):
					t.Fatal("upstream request not canceled")
				}
			})
		}
	}
}

func TestClientRejectsInvalidResponsesWithoutSecrets(t *testing.T) {
	for _, response := range []struct {
		name   string
		status int
		body   string
	}{
		{"http error", 503, `{"access_token":"private-token"}`},
		{"invalid JSON", 200, `private-secret private-code private-token`},
		{"api error", 200, `{"errcode":40001,"errmsg":"private-secret private-code private-token"}`},
		{"missing token", 200, `{}`},
	} {
		t.Run(response.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(response.status)
				w.Write([]byte(response.body))
			}))
			defer upstream.Close()
			client := NewClient(Config{APIBaseURL: upstream.URL, Secret: "private-secret"})
			_, err := client.GetUserIDByCode(t.Context(), "private-code")
			if err == nil {
				t.Fatal("expected upstream failure")
			}
			for _, secret := range []string{"private-secret", "private-code", "private-token"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error leaks %s: %v", secret, err)
				}
			}
		})
	}
}

func TestClientTransportErrorDoesNotExposeURL(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	upstream.Close()
	client := NewClient(Config{APIBaseURL: upstream.URL, Secret: "private-secret"})
	_, err := client.GetAccessToken(t.Context())
	if err == nil || strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), upstream.URL) {
		t.Fatalf("expected sanitized transport error, got %v", err)
	}
}

func TestClientDefaultTimeoutAndInjection(t *testing.T) {
	for _, timeout := range []time.Duration{0, -1, time.Hour, time.Second} {
		injected := &http.Client{Timeout: timeout}
		client := NewClient(Config{HTTPClient: injected})
		if injected.Timeout != timeout {
			t.Fatal("mutated injected client")
		}
		if client.httpClient.Timeout <= 0 || client.httpClient.Timeout > defaultRequestTimeout {
			t.Fatal("timeout not bounded")
		}
		if timeout == time.Second && client.httpClient.Timeout != timeout {
			t.Fatal("lost shorter injected timeout")
		}
	}
	if NewClient(Config{}).httpClient.Timeout != defaultRequestTimeout {
		t.Fatal("missing default timeout")
	}
}
