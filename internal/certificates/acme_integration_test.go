//go:build integration

package certificates

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-acme/lego/v4/challenge/dns01"
)

// TestLocalACME uses Pebble's actual CA, HTTP/DNS validation and real TLS handshakes.
// Enable with DROPLY_ACME_INTEGRATION=1 and PEBBLE_DIR pointing at the Pebble source.
// Install pebble and pebble-challtestsrv v2.10.1 on PATH; no public DNS or CA is used.
func TestLocalACME(t *testing.T) {
	if os.Getenv("DROPLY_ACME_INTEGRATION") != "1" {
		t.Skip("set DROPLY_ACME_INTEGRATION=1 to run local Pebble validation")
	}
	pebbleDir := os.Getenv("PEBBLE_DIR")
	if pebbleDir == "" {
		t.Fatal("PEBBLE_DIR required")
	}
	address := func() string {
		l, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		a := l.Addr().String()
		l.Close()
		return a
	}
	caAddress, mgmtAddress, dnsAddress, dnsMgmt := address(), address(), address(), address()
	run := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Env = append(os.Environ(), "PEBBLE_VA_NOSLEEP=1", "PEBBLE_WFE_NONCEREJECT=0", "PEBBLE_AUTHZREUSE=0")
		var logs bytes.Buffer
		cmd.Stdout = &logs
		cmd.Stderr = &logs
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			if t.Failed() {
				t.Log(logs.String())
			}
		})
	}
	run("pebble-challtestsrv", "-dnsserver", dnsAddress, "-management", dnsMgmt, "-http01", "", "-https01", "", "-tlsalpn01", "", "-doh", "", "-defaultIPv6", "")
	challengeListener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := challengeListener.Addr().(*net.TCPAddr).Port
	config := map[string]any{"pebble": map[string]any{"listenAddress": caAddress, "managementListenAddress": mgmtAddress, "certificate": filepath.Join(pebbleDir, "test/certs/localhost/cert.pem"), "privateKey": filepath.Join(pebbleDir, "test/certs/localhost/key.pem"), "httpPort": port, "tlsPort": 5001, "retryAfter": map[string]int{"authz": 1, "order": 1}}}
	data, _ := json.Marshal(config)
	configPath := filepath.Join(t.TempDir(), "pebble.json")
	if e = os.WriteFile(configPath, data, 0600); e != nil {
		t.Fatal(e)
	}
	run("pebble", "-config", configPath, "-dnsserver", dnsAddress)
	roots := x509.NewCertPool()
	pemBytes, e := os.ReadFile(filepath.Join(pebbleDir, "test/certs/pebble.minica.pem"))
	if e != nil {
		t.Fatal(e)
	}
	roots.AppendCertsFromPEM(pemBytes)
	caClient := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	caURL := "https://" + caAddress + "/dir"
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := caClient.Get(caURL)
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	resp, e := caClient.Get("https://" + mgmtAddress + "/roots/0")
	if e != nil {
		t.Fatal(e)
	}
	rootPEM, e := io.ReadAll(resp.Body)
	resp.Body.Close()
	if e != nil {
		t.Fatal(e)
	}
	issuedRoots := x509.NewCertPool()
	if !issuedRoots.AppendCertsFromPEM(rootPEM) {
		t.Fatalf("invalid issued root: %s", rootPEM)
	}
	var allowed sync.Map
	allowed.Store("site.example.test", true)
	allowed.Store("other.example.test", true)
	permitted := func(h string) bool { _, ok := allowed.Load(h); return ok }
	cfg := Config{Directory: t.TempDir(), Email: "admin@example.test", CAURL: caURL, HTTPClient: caClient, Allowed: permitted}
	m, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	var active atomic.Pointer[Manager]
	active.Store(m)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Load().HTTPHandler(http.NotFoundHandler()).ServeHTTP(w, r)
	})}
	go server.Serve(challengeListener)
	t.Cleanup(func() { server.Close() })
	verify := func(manager *Manager, host string) string {
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
		srv.TLS = &tls.Config{GetCertificate: manager.GetCertificate}
		srv.StartTLS()
		defer srv.Close()
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: issuedRoots, ServerName: host}}}
		res, err := client.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatal(res.Status)
		}
		return res.TLS.PeerCertificates[0].SerialNumber.String()
	}
	var concurrent sync.WaitGroup
	results := make(chan *tls.Certificate, 8)
	failures := make(chan error, 8)
	for range 8 {
		concurrent.Go(func() {
			cert, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "site.example.test"})
			if err != nil {
				failures <- err
				return
			}
			results <- cert
		})
	}
	concurrent.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var issued *tls.Certificate
	for cert := range results {
		if issued != nil && issued != cert {
			t.Fatal("concurrent handshakes issued multiple certificates")
		}
		issued = cert
	}
	first := verify(m, "site.example.test")
	restarted, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	if serial := verify(restarted, "site.example.test"); serial != first {
		t.Fatal("restart reissued certificate")
	}
	active.Store(restarted)
	// Force renewal against real ACME, then prove the new certificate is used.
	restarted.cfg.RenewBefore = 365 * 24 * time.Hour
	restarted.renew(t.Context())
	if serial := verify(restarted, "site.example.test"); serial == first {
		t.Fatal("renewal did not replace certificate")
	}
	// Failed renewal keeps the old certificate and reports only a controlled code.
	renewed := verify(restarted, "site.example.test")
	restarted.cfg.CAURL = "https://127.0.0.1:1/dir"
	restarted.renew(t.Context())
	if serial := verify(restarted, "site.example.test"); serial != renewed {
		t.Fatal("failed renewal changed certificate")
	}
	status, e := restarted.Status("site.example.test")
	if e != nil || status.LastError != "acme_issuance_failed" {
		t.Fatalf("%+v %v", status, e)
	}
	allowed.Delete("site.example.test")
	if _, e = restarted.GetCertificate(&tls.ClientHelloInfo{ServerName: "site.example.test"}); e == nil {
		t.Fatal("revoked host served")
	}
	allowed.Store("site.example.test", true)
	// Real DNS-01 wildcard issuance via controlled TXT records; lego still validates DNS propagation.
	provider := &testDNSProvider{base: "http://" + dnsMgmt}
	dnsCfg := cfg
	dnsCfg.Directory = t.TempDir()
	dnsCfg.BaseDomain = "example.test"
	dnsCfg.DNSProvider = provider
	dnsCfg.DNSOptions = []dns01.ChallengeOption{dns01.AddRecursiveNameservers([]string{dnsAddress}), dns01.DisableAuthoritativeNssPropagationRequirement(), dns01.RecursiveNSsPropagationRequirement()}
	dnsManager, e := New(dnsCfg)
	if e != nil {
		t.Fatal(e)
	}
	wildcard := verify(dnsManager, "site.example.test")
	if serial := verify(dnsManager, "other.example.test"); serial != wildcard {
		t.Fatal("wildcard was not reused")
	}
	if provider.present == 0 || provider.cleaned != provider.present {
		t.Fatalf("DNS not cleaned: %+v", provider)
	}
	if _, e = dnsManager.GetCertificate(&tls.ClientHelloInfo{ServerName: "unknown.example.test"}); e == nil {
		t.Fatal("wildcard bypassed authorization")
	}
	for _, failure := range []string{"present", "cleanup"} {
		failingCfg := dnsCfg
		failingCfg.Directory = t.TempDir()
		failingProvider := &testDNSProvider{base: provider.base, failPresent: failure == "present", failCleanup: failure == "cleanup"}
		failingCfg.DNSProvider = failingProvider
		failing, e := New(failingCfg)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = failing.GetCertificate(&tls.ClientHelloInfo{ServerName: "site.example.test"}); e == nil {
			t.Fatal("DNS failure reported success")
		}
		status, _ := failing.Status("site.example.test")
		if status.LastError != "dns_"+failure+"_failed" {
			t.Fatalf("DNS failure not controlled: %+v", status)
		}
	}
	// Storage failures must not activate an otherwise successfully issued certificate.
	dnsManager.cfg.RenewBefore = 365 * 24 * time.Hour
	oldDirectory := dnsManager.directory
	dnsManager.directory = filepath.Join(t.TempDir(), "missing")
	dnsManager.renew(t.Context())
	s, _ := dnsManager.Status("site.example.test")
	if s.LastError != "certificate_storage_failed" {
		t.Fatalf("storage failure status: %+v", s)
	}
	if serial := verify(dnsManager, "site.example.test"); serial != wildcard {
		t.Fatal("unsaved certificate activated")
	}
	dnsManager.directory = oldDirectory
}

type testDNSProvider struct {
	base                     string
	present, cleaned         int
	failPresent, failCleanup bool
}

func (p *testDNSProvider) update(path, host, value string) error {
	data, _ := json.Marshal(map[string]string{"host": host, "value": value})
	r, e := http.Post(p.base+path, "application/json", bytes.NewReader(data))
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("DNS test API: %s", r.Status)
	}
	return nil
}
func (p *testDNSProvider) Present(domain, token, keyAuth string) error {
	p.present++
	if p.failPresent {
		return errors.New("secret-provider-token")
	}
	info := dns01.GetChallengeInfo(domain, keyAuth)
	return p.update("/set-txt", info.EffectiveFQDN, info.Value)
}
func (p *testDNSProvider) CleanUp(domain, token, keyAuth string) error {
	p.cleaned++
	info := dns01.GetChallengeInfo(domain, keyAuth)
	err := p.update("/clear-txt", info.EffectiveFQDN, "")
	if p.failCleanup {
		return errors.New("secret-provider-token")
	}
	return err
}
func (*testDNSProvider) Timeout() (time.Duration, time.Duration) {
	return 10 * time.Second, time.Second
}
