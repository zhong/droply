// Package certificates manages durable, authorized ACME certificates.
package certificates

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/lego"
	legolog "github.com/go-acme/lego/v4/log"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/registration"
)

// lego may log CA/DNS error bodies. Expose only controlled status codes instead.
func init() { legolog.Logger = log.New(io.Discard, "", 0) }

// DefaultIssuanceTimeout bounds one certificate issuance operation.
const DefaultIssuanceTimeout = 5 * time.Minute

// Config holds deployment configuration. Never serialize Config: it contains credentials.
type Config struct {
	Directory, Email, CAURL, BaseDomain, CloudflareAPIToken string
	Allowed                                                 func(string) bool
	HTTPClient                                              *http.Client
	DNSProvider                                             challenge.Provider
	DNSOptions                                              []dns01.ChallengeOption
	Now                                                     func() time.Time
	RenewBefore, RetryBackoff, CheckInterval                time.Duration
	IssuanceTimeout                                         time.Duration
}

type Status struct {
	Domain    string    `json:"domain"`
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	LastError string    `json:"last_error,omitempty"`
	RetryAt   time.Time `json:"retry_at,omitzero"`
}

type storedCertificate struct {
	Hosts       []string              `json:"hosts"`
	Resource    *certificate.Resource `json:"resource"`
	Certificate []byte                `json:"certificate"`
	PrivateKey  []byte                `json:"private_key"`
}
type entry struct {
	storedCertificate
	cert      *tls.Certificate
	lastError string
	retryAt   time.Time
	failures  int
}
type account struct {
	Email        string                 `json:"email"`
	Key          []byte                 `json:"key"`
	Registration *registration.Resource `json:"registration,omitempty"`
	private      crypto.PrivateKey
}

func (a *account) GetEmail() string                        { return a.Email }
func (a *account) GetRegistration() *registration.Resource { return a.Registration }
func (a *account) GetPrivateKey() crypto.PrivateKey        { return a.private }

type Manager struct {
	cfg       Config
	directory string
	mu        sync.Mutex
	issueMu   chan struct{}
	entries   map[string]*entry
	tokens    map[string]string
	account   account
}

func New(cfg Config) (*Manager, error) {
	if cfg.Directory == "" || cfg.Allowed == nil {
		return nil, errors.New("certificate directory and domain authorization are required")
	}
	if cfg.CAURL == "" {
		cfg.CAURL = lego.LEDirectoryProduction
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.RenewBefore <= 0 {
		cfg.RenewBefore = 30 * 24 * time.Hour
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = time.Minute
	}
	if cfg.CheckInterval <= 0 {
		cfg.CheckInterval = time.Hour
	}
	if cfg.IssuanceTimeout <= 0 {
		cfg.IssuanceTimeout = DefaultIssuanceTimeout
	}
	cfg.BaseDomain = normalize(cfg.BaseDomain)
	if cfg.CloudflareAPIToken != "" && cfg.DNSProvider == nil {
		c := cloudflare.NewDefaultConfig()
		c.AuthToken = cfg.CloudflareAPIToken
		c.ZoneToken = cfg.CloudflareAPIToken
		var err error
		cfg.DNSProvider, err = cloudflare.NewDNSProviderConfig(c)
		if err != nil {
			return nil, errors.New("invalid Cloudflare DNS configuration")
		}
	}
	if cfg.DNSProvider != nil && cfg.BaseDomain == "" {
		return nil, errors.New("DNS certificates require a base domain")
	}
	m := &Manager{cfg: cfg, directory: filepath.Join(cfg.Directory, digest(cfg.CAURL)), issueMu: make(chan struct{}, 1), entries: map[string]*entry{}, tokens: map[string]string{}}
	if err := os.MkdirAll(m.directory, 0700); err != nil {
		return nil, errors.New("certificate storage unavailable")
	}
	if err := os.Chmod(m.directory, 0700); err != nil {
		return nil, errors.New("certificate storage permissions unavailable")
	}
	data, err := os.ReadFile(filepath.Join(m.directory, "account.json"))
	switch {
	case err == nil:
		if json.Unmarshal(data, &m.account) != nil {
			return nil, errors.New("invalid stored ACME account")
		}
		block, _ := pem.Decode(m.account.Key)
		if block == nil {
			return nil, errors.New("invalid stored ACME account key")
		}
		m.account.private, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("invalid stored ACME account key")
		}
	case errors.Is(err, os.ErrNotExist):
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return nil, e
		}
		der, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			return nil, e
		}
		m.account = account{Email: cfg.Email, Key: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), private: key}
		if m.saveAccount() != nil {
			return nil, errors.New("cannot persist ACME account")
		}
	default:
		return nil, errors.New("cannot load ACME account")
	}
	files, err := filepath.Glob(filepath.Join(m.directory, "cert-*.json"))
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		data, e := os.ReadFile(file)
		if e != nil {
			return nil, errors.New("cannot load certificate cache")
		}
		var stored storedCertificate
		if json.Unmarshal(data, &stored) != nil || stored.Resource == nil || len(stored.Hosts) == 0 {
			return nil, errors.New("invalid certificate cache")
		}
		stored.Resource.Certificate = stored.Certificate
		stored.Resource.PrivateKey = stored.PrivateKey
		cert, e := parse(stored.Resource)
		if e != nil {
			return nil, errors.New("invalid certificate cache")
		}
		m.entries[stored.Resource.Domain] = &entry{storedCertificate: stored, cert: cert}
	}
	return m, nil
}
func normalize(host string) string { return strings.ToLower(strings.TrimSuffix(host, ".")) }
func digest(s string) string       { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func (m *Manager) key(host string) string {
	if m.cfg.DNSProvider != nil && strings.HasSuffix(host, "."+m.cfg.BaseDomain) && !strings.Contains(strings.TrimSuffix(host, "."+m.cfg.BaseDomain), ".") {
		return "*." + m.cfg.BaseDomain
	}
	return host
}
func (m *Manager) authorized(host string) bool {
	return host != "" && !strings.ContainsAny(host, "/:*\\") && m.cfg.Allowed(host)
}
func (m *Manager) valid(e *entry) bool {
	return e != nil && e.cert != nil && !m.cfg.Now().Before(e.cert.Leaf.NotBefore) && m.cfg.Now().Before(e.cert.Leaf.NotAfter)
}

func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	host := normalize(hello.ServerName)
	if !m.authorized(host) {
		return nil, errors.New("domain is not authorized")
	}
	m.mu.Lock()
	e := m.entries[m.key(host)]
	if m.valid(e) {
		cert := e.cert
		m.remember(e, host)
		m.mu.Unlock()
		return cert, nil
	}
	m.mu.Unlock()
	ctx := hello.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	return m.obtain(ctx, host, false)
}
func (m *Manager) remember(e *entry, host string) {
	if slices.Contains(e.Hosts, host) {
		return
	}
	e.Hosts = append(e.Hosts, host)
}

func (m *Manager) obtain(ctx context.Context, host string, renew bool) (*tls.Certificate, error) {
	select {
	case m.issueMu <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-m.issueMu }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !m.authorized(host) {
		return nil, errors.New("domain is not authorized")
	}
	key := m.key(host)
	m.mu.Lock()
	e := m.entries[key]
	if e == nil {
		e = &entry{Hosts: []string{host}}
		m.entries[key] = e
	}
	m.remember(e, host)
	if m.valid(e) && (!renew || e.cert.Leaf.NotAfter.Sub(m.cfg.Now()) > m.cfg.RenewBefore) {
		c := e.cert
		m.mu.Unlock()
		return c, nil
	}
	if m.cfg.Now().Before(e.retryAt) {
		m.mu.Unlock()
		return nil, errors.New("certificate issuance is backing off")
	}
	m.mu.Unlock()
	resource, err := m.issue(ctx, key)
	code := "acme_issuance_failed"
	if errors.Is(err, errDNSPresent) {
		code = "dns_present_failed"
	}
	if errors.Is(err, errDNSCleanup) {
		code = "dns_cleanup_failed"
	}
	if errors.Is(err, errAccountStorage) {
		code = "account_storage_failed"
	}
	var cert *tls.Certificate
	if err == nil {
		cert, err = parse(resource)
	}
	if err == nil && (cert.Leaf.VerifyHostname(host) != nil || !m.cfg.Now().Before(cert.Leaf.NotAfter) || m.cfg.Now().Before(cert.Leaf.NotBefore)) {
		err = errors.New("invalid issued certificate")
	}
	if err == nil && !m.authorized(host) {
		err = errors.New("authorization revoked")
		code = "domain_not_authorized"
	}
	if err == nil {
		m.mu.Lock()
		hosts := append([]string(nil), e.Hosts...)
		m.mu.Unlock()
		err = m.save("cert-"+digest(key)+".json", storedCertificate{Hosts: hosts, Resource: resource, Certificate: resource.Certificate, PrivateKey: resource.PrivateKey})
		if err != nil {
			code = "certificate_storage_failed"
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		e.failures++
		e.lastError = code
		e.retryAt = m.cfg.Now().Add(min(m.cfg.RetryBackoff*time.Duration(1<<min(e.failures-1, 10)), 24*time.Hour))
		return nil, errors.New(code)
	}
	e.Resource = resource
	e.cert = cert
	e.lastError = ""
	e.retryAt = time.Time{}
	e.failures = 0
	return cert, nil
}

func parse(r *certificate.Resource) (*tls.Certificate, error) {
	cert, err := tls.X509KeyPair(r.Certificate, r.PrivateKey)
	if err != nil {
		return nil, err
	}
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	return &cert, nil
}
func (m *Manager) saveAccount() error { return m.save("account.json", &m.account) }
func (m *Manager) save(name string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(m.directory, ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), filepath.Join(m.directory, name)); err != nil {
		return err
	}
	dir, err := os.Open(m.directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

type contextTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t contextTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.base.RoundTrip(req.Clone(t.ctx))
}
func (m *Manager) issue(ctx context.Context, key string) (*certificate.Resource, error) {
	ctx, cancel := context.WithTimeout(ctx, m.cfg.IssuanceTimeout)
	defer cancel()
	cfg := lego.NewConfig(&m.account)
	cfg.CADirURL = m.cfg.CAURL
	cfg.Certificate.KeyType = certcrypto.EC256
	if m.cfg.HTTPClient != nil {
		copy := *m.cfg.HTTPClient
		cfg.HTTPClient = &copy
	}
	transport := cfg.HTTPClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	cfg.HTTPClient.Transport = contextTransport{ctx: ctx, base: transport}
	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	if err = client.Challenge.SetHTTP01Provider(m); err != nil {
		return nil, err
	}
	var dns *observedDNS
	if strings.HasPrefix(key, "*.") {
		dns = &observedDNS{provider: m.cfg.DNSProvider}
		if err = client.Challenge.SetDNS01Provider(dns, m.cfg.DNSOptions...); err != nil {
			return nil, err
		}
	}
	if m.account.Registration == nil {
		registration, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return nil, err
		}
		m.account.Registration = registration
		if err = m.saveAccount(); err != nil {
			m.account.Registration = nil
			return nil, errAccountStorage
		}
	}
	resource, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: []string{key}, Bundle: true})
	if dns != nil && dns.failure != nil {
		return nil, dns.failure
	}
	return resource, err
}

// Present and CleanUp implement lego's HTTP-01 challenge provider.
func (m *Manager) Present(domain, token, keyAuth string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[normalize(domain)+"/"+token] = keyAuth
	return nil
}
func (m *Manager) CleanUp(domain, token, keyAuth string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tokens, normalize(domain)+"/"+token)
	return nil
}
func (m *Manager) HTTPHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token, ok := strings.CutPrefix(r.URL.Path, "/.well-known/acme-challenge/"); ok {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			host = normalize(host)
			m.mu.Lock()
			value, found := m.tokens[host+"/"+token]
			m.mu.Unlock()
			if !found || !m.authorized(host) {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, value)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (m *Manager) Status(host string) (Status, error) {
	host = normalize(host)
	if !m.authorized(host) {
		return Status{}, errors.New("domain is not authorized")
	}
	s := Status{Domain: host, State: "pending"}
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[m.key(host)]
	if e == nil {
		return s, nil
	}
	if e.cert != nil {
		s.ExpiresAt = e.cert.Leaf.NotAfter
		s.State = "expired"
		if m.valid(e) {
			s.State = "ready"
		}
	}
	s.LastError = e.lastError
	s.RetryAt = e.retryAt
	if e.lastError != "" {
		s.State = "error"
	}
	return s, nil
}

// Run renews eligible certificates until ctx is canceled. Existing valid certificates
// remain available during renewal, including when a CA or persistent storage fails.
func (m *Manager) Run(ctx context.Context) {
	m.renew(ctx)
	ticker := time.NewTicker(m.cfg.CheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.renew(ctx)
		}
	}
}
func (m *Manager) renew(ctx context.Context) {
	m.mu.Lock()
	var hosts []string
	for key, e := range m.entries {
		if e.cert == nil {
			continue
		}
		if m.cfg.DNSProvider != nil && key == "*."+m.cfg.BaseDomain {
			// The shared platform wildcard also serves the API. Its renewal must
			// survive deletion of the first tenant that requested it and restart.
			// Authorization is checked below against the current platform state.
			hosts = append(hosts, "api."+m.cfg.BaseDomain)
			continue
		}
		hosts = append(hosts, e.Hosts...)
	}
	m.mu.Unlock()
	for _, host := range hosts {
		if ctx.Err() != nil {
			return
		}
		if m.authorized(host) {
			_, _ = m.obtain(ctx, host, true)
		}
	}
}

var (
	errDNSPresent     = errors.New("DNS presentation failed")
	errDNSCleanup     = errors.New("DNS cleanup failed")
	errAccountStorage = errors.New("ACME account storage failed")
)

// lego logs cleanup failures without returning them; retain a controlled error so
// operators can detect abandoned TXT records even if the CA issued a certificate.
type observedDNS struct {
	provider challenge.Provider
	failure  error
}

func (p *observedDNS) Present(domain, token, keyAuth string) error {
	if err := p.provider.Present(domain, token, keyAuth); err != nil {
		p.failure = errDNSPresent
		return errDNSPresent
	}
	return nil
}
func (p *observedDNS) CleanUp(domain, token, keyAuth string) error {
	if err := p.provider.CleanUp(domain, token, keyAuth); err != nil {
		p.failure = errDNSCleanup
		return errDNSCleanup
	}
	return nil
}
func (p *observedDNS) Timeout() (time.Duration, time.Duration) {
	if provider, ok := p.provider.(challenge.ProviderTimeout); ok {
		return provider.Timeout()
	}
	return 2 * time.Minute, 2 * time.Second
}
