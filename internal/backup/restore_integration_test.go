//go:build integration

package backup

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhong/droply/internal/hosting"
	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func TestRestoreRealHTTPAndTLSAccessDomainsAndRollback(t *testing.T) {
	cfg := fixture(t)
	st, err := store.NewSQLiteStore(filepath.Join(cfg.DataDir, "droply.db"))
	must(t, err)
	_, err = st.CreateCustomDomain(1, "custom.other.test")
	must(t, err)
	must(t, st.VerifyCustomDomain("custom.other.test"))
	hash, err := bcrypt.GenerateFromPassword([]byte("secret-password"), bcrypt.MinCost)
	must(t, err)
	pid := int64(1)
	_, err = st.PutAccessRule(t.Context(), store.AccessRuleInput{SubdomainID: 1, ProjectID: &pid, AllowedIPs: nil, PasswordHash: string(hash), SessionTTL: 3600, WeWorkEnabled: false, AllowedWeWorkUsers: nil})
	must(t, err)
	key, err := os.ReadFile(filepath.Join(cfg.DataDir, "hmac.key"))
	must(t, err)
	source := server.New(st, filepath.Join(cfg.DataDir, "sites"), "example.test", key)
	must(t, source.PrepareDeployments(t.Context()))
	form := url.Values{"password": {"secret-password"}, "host": {"custom.other.test"}, "redirect": {"/"}}
	req := httptest.NewRequest(http.MethodPost, "http://custom.other.test/_droply/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	source.ServeHTTP(w, req)
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("login %d %s", w.Code, w.Body.String())
	}
	source.ShutdownAnalytics()
	must(t, st.Close())
	certDir := filepath.Join(filepath.Dir(cfg.DataDir), "tls")
	must(t, os.Mkdir(certDir, 0700))
	certPEM, keyPEM := testCertificate(t)
	must(t, os.WriteFile(filepath.Join(certDir, "cert.pem"), certPEM, 0600))
	must(t, os.WriteFile(filepath.Join(certDir, "key.pem"), keyPEM, 0600))
	must(t, os.WriteFile(filepath.Join(certDir, "account.json"), []byte(`{"private_account":"preserved"}`), 0600))
	cfg.Include = []string{certDir}
	must(t, Create(t.Context(), cfg))
	target := cfg.DataDir + "-restored"
	must(t, Restore(t.Context(), RestoreConfig{Input: cfg.Output, DataDir: target}))
	manifests, err := filepath.Glob(filepath.Join(target, ".restore", "*", "manifest.json"))
	must(t, err)
	if len(manifests) != 1 {
		t.Fatal(manifests)
	}
	data, err := os.ReadFile(manifests[0])
	must(t, err)
	var m Manifest
	must(t, json.Unmarshal(data, &m))
	mapped := filepath.Join(target, filepath.FromSlash(m.External[1].Path))
	certificate, err := tls.LoadX509KeyPair(filepath.Join(mapped, "cert.pem"), filepath.Join(mapped, "key.pem"))
	must(t, err)
	account, err := os.ReadFile(filepath.Join(mapped, "account.json"))
	must(t, err)
	if !strings.Contains(string(account), "preserved") {
		t.Fatal("lost ACME account")
	}
	st, err = store.NewSQLiteStore(filepath.Join(target, "droply.db"))
	must(t, err)
	defer st.Close()
	restoredKey, err := os.ReadFile(filepath.Join(target, "hmac.key"))
	must(t, err)
	srv := server.New(st, filepath.Join(target, "sites"), "example.test", restoredKey)
	defer srv.ShutdownAnalytics()
	must(t, srv.PrepareDeployments(t.Context()))
	running, err := hosting.Start(hosting.Config{HTTPAddr: "127.0.0.1:0", HTTPSAddr: "127.0.0.1:0", Handler: srv, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}})
	must(t, err)
	defer running.Shutdown(t.Context())
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "custom.other.test"}}}
	defer client.CloseIdleConnections()
	check := func(scheme, address, expected string, authenticated bool) {
		t.Helper()
		r, err := http.NewRequest(http.MethodGet, scheme+"://"+address+"/", nil)
		must(t, err)
		r.Host = "custom.other.test"
		if authenticated {
			for _, cookie := range cookies {
				r.AddCookie(cookie)
			}
		}
		resp, err := client.Do(r)
		must(t, err)
		body, err := io.ReadAll(resp.Body)
		must(t, err)
		resp.Body.Close()
		if authenticated {
			if resp.StatusCode != 200 || string(body) != expected {
				t.Fatalf("%s %d %s", scheme, resp.StatusCode, body)
			}
		} else if string(body) == "new version" {
			t.Fatal("private production leaked")
		}
	}
	check("http", running.HTTPAddress(), "", false)
	check("http", running.HTTPAddress(), "new version", true)
	check("https", running.HTTPSAddress(), "new version", true)
	api := httptest.NewServer(srv)
	defer api.Close()
	r, err := http.NewRequest(http.MethodPost, api.URL+"/subdomains/backup/projects/site/rollback/1", nil)
	must(t, err)
	r.Host = "api.example.test"
	r.Header.Set("Authorization", "Bearer dp_backup")
	resp, err := api.Client().Do(r)
	must(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("rollback %d %s", resp.StatusCode, body)
	}
	check("https", running.HTTPSAddress(), "old version", true)
}
func testCertificate(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, err)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "custom.other.test"}, DNSNames: []string{"custom.other.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	must(t, err)
	private, err := x509.MarshalPKCS8PrivateKey(key)
	must(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
}
