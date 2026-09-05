package certificates

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUnauthorizedNeverContactsCA(t *testing.T) {
	m, err := New(Config{Directory: t.TempDir(), Allowed: func(string) bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.GetCertificate(&tls.ClientHelloInfo{ServerName: "unknown.example.com"}); err == nil {
		t.Fatal("unauthorized host accepted")
	}
	if _, err = m.Status("unknown.example.com"); err == nil {
		t.Fatal("unauthorized status accepted")
	}
}

func TestFailureBackoffAndSanitizedStatus(t *testing.T) {
	var calls atomic.Int32
	ca := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "secret-token-123", 500) }))
	defer ca.Close()
	now := time.Now()
	m, err := New(Config{Directory: t.TempDir(), CAURL: ca.URL, HTTPClient: ca.Client(), Allowed: func(string) bool { return true }, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _, _ = m.GetCertificate(&tls.ClientHelloInfo{ServerName: "test.example.com"}) })
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("CA calls = %d, want one", calls.Load())
	}
	s, err := m.Status("test.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if s.LastError != "acme_issuance_failed" || !s.RetryAt.After(now) {
		t.Fatalf("bad status: %+v", s)
	}
	now = now.Add(2 * time.Minute)
	_, _ = m.GetCertificate(&tls.ClientHelloInfo{ServerName: "test.example.com"})
	if calls.Load() != 2 {
		t.Fatalf("retry did not contact CA: %d", calls.Load())
	}
}

func TestChallengeHandler(t *testing.T) {
	allowed := true
	m, err := New(Config{Directory: t.TempDir(), Allowed: func(string) bool { return allowed }})
	if err != nil {
		t.Fatal(err)
	}
	h := m.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com", http.StatusPermanentRedirect)
	}))
	_ = m.Present("example.com", "token", "proof")
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://example.com"+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("/.well-known/acme-challenge/token"); w.Code != 200 || w.Body.String() != "proof" {
		t.Fatal(w)
	}
	if w := request("/"); w.Code != 308 {
		t.Fatal(w)
	}
	allowed = false
	if w := request("/.well-known/acme-challenge/token"); w.Code != 404 {
		t.Fatal(w)
	}
	allowed = true
	_ = m.CleanUp("example.com", "token", "proof")
	if w := request("/.well-known/acme-challenge/token"); w.Code != 404 {
		t.Fatal(w)
	}
}

func TestCancellationWhileQueued(t *testing.T) {
	m, err := New(Config{Directory: t.TempDir(), Allowed: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	m.issueMu <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan error, 1)
	go func() { _, err := m.obtain(ctx, "example.com", false); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel ignored")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter blocked")
	}
	<-m.issueMu
}

func TestAccountStoragePermissionsAndCorruption(t *testing.T) {
	cfg := Config{Directory: t.TempDir(), Allowed: func(string) bool { return true }}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.directory, "account.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = New(cfg); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("account replaced on restart")
	}
	if err = os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = New(cfg); err == nil {
		t.Fatal("corrupt account silently replaced")
	}
}

func TestExpiredCertificateIsNotServedAndRevokedIsNotRenewed(t *testing.T) {
	ca := httptest.NewTLSServer(http.NotFoundHandler())
	defer ca.Close()
	now := time.Now()
	allowed := true
	m, err := New(Config{Directory: t.TempDir(), Allowed: func(string) bool { return allowed }, Now: func() time.Time { return now }, CAURL: ca.URL, HTTPClient: ca.Client()})
	if err != nil {
		t.Fatal(err)
	}
	cert := ca.TLS.Certificates[0]
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	m.entries["example.com"] = &entry{storedCertificate: storedCertificate{Hosts: []string{"example.com"}}, cert: &cert}
	now = cert.Leaf.NotAfter.Add(time.Second)
	if _, err = m.GetCertificate(&tls.ClientHelloInfo{ServerName: "example.com"}); err == nil {
		t.Fatal("expired certificate served")
	}
	status, _ := m.Status("example.com")
	if status.State != "error" {
		t.Fatal(status)
	}
	allowed = false
	before := m.entries["example.com"].retryAt
	now = now.Add(24 * time.Hour)
	m.renew(t.Context())
	if m.entries["example.com"].retryAt != before {
		t.Fatal("revoked certificate renewed")
	}
}

func TestCancellationReachesCARequest(t *testing.T) {
	started := make(chan struct{})
	ca := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer ca.Close()
	m, err := New(Config{Directory: t.TempDir(), Allowed: func(string) bool { return true }, CAURL: ca.URL, HTTPClient: ca.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { _, _ = m.obtain(ctx, "example.com", false); close(done) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("CA request never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("CA request ignored cancellation")
	}
}
