package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"path/filepath"
	"strconv"

	"os"
	"strings"
	"time"

	"github.com/zhong/droply/internal/server"
)

// serverConfig contains the command-line and environment configuration.
type serverConfig struct {
	cloudflareToken  string
	addr             string
	httpsAddr        string
	legacySiteAddr   string
	legacyCaddy      string
	mode             string
	certFile         string
	keyFile          string
	dataDir          string
	domain           string
	email            string
	ca               string
	certDir          string
	tokenFile        string
	proxies          string
	hmacSecret       string
	openRegistration bool
	auditRetention   int
	retention        int
	deploymentCount  int
	deploymentDays   int
	artifactQuota    int64
	expandedLimit    int64
	fileLimit        int
	orphanGrace      time.Duration
	corp             string
	agent            string
	secret           string
	callback         string
}

func parseServerConfig(args []string) (serverConfig, error) {
	cfg := serverConfig{cloudflareToken: os.Getenv("DROPLY_CLOUDFLARE_API_TOKEN")}
	flags := flag.NewFlagSet("droply-server", flag.ContinueOnError)
	flags.StringVar(&cfg.addr, "addr", ":8080", "HTTP listen address (API and sites); use :80 for automatic HTTPS")
	flags.StringVar(&cfg.httpsAddr, "https-addr", ":443", "HTTPS listen address")
	flags.StringVar(&cfg.legacySiteAddr, "site-addr", "", "Deprecated: optional additional unified HTTP listener")
	flags.StringVar(&cfg.legacyCaddy, "caddy-admin", "", "Deprecated and ignored; Droply no longer uses Caddy")
	flags.StringVar(&cfg.mode, "tls-mode", "http", "http | manual | auto | cloudflare")
	flags.StringVar(&cfg.certFile, "tls-cert", "", "PEM certificate chain for manual TLS")
	flags.StringVar(&cfg.keyFile, "tls-key", "", "PEM private key for manual TLS")
	flags.StringVar(&cfg.dataDir, "data-dir", "/data/droply", "SQLite database and static content directory")
	flags.StringVar(&cfg.domain, "domain", "droplydoc.com", "Base domain")
	flags.StringVar(&cfg.email, "acme-email", os.Getenv("DROPLY_ACME_EMAIL"), "ACME account email")
	flags.StringVar(&cfg.ca, "acme-ca", "", "ACME directory URL (empty: Let's Encrypt production)")
	flags.StringVar(&cfg.certDir, "cert-dir", "", "Certificate storage (default: data-dir/certificates)")
	flags.StringVar(&cfg.tokenFile, "cloudflare-token-file", "", "Cloudflare DNS API token file (or DROPLY_CLOUDFLARE_API_TOKEN)")
	flags.StringVar(&cfg.proxies, "trusted-proxies", "", "Comma-separated trusted proxy CIDRs (default: none)")
	flags.StringVar(&cfg.hmacSecret, "hmac-secret", "", "Existing cookie signing key; otherwise persist data-dir/hmac.key")
	flags.BoolVar(&cfg.openRegistration, "open-registration", os.Getenv("DROPLY_OPEN_REGISTRATION") == "true", "Explicitly allow public account registration (default: invitations only)")
	flags.IntVar(&cfg.auditRetention, "audit-retention-days", 90, "Audit event retention in days")
	flags.IntVar(&cfg.retention, "log-retention-days", 30, "Detailed visit log retention")
	flags.IntVar(&cfg.deploymentCount, "deployment-retain-count", 10, "Keep this many successful deployments per project (0: disable count protection)")
	flags.IntVar(&cfg.deploymentDays, "deployment-retain-days", 30, "Keep successful deployments this many days (0: disable age protection)")
	flags.Int64Var(&cfg.artifactQuota, "artifact-max-bytes", 0, "Managed artifact and staging byte quota (0: disk capacity only)")
	flags.Int64Var(&cfg.expandedLimit, "deploy-max-expanded-bytes", 256<<20, "Maximum extracted bytes per deployment")
	flags.IntVar(&cfg.fileLimit, "deploy-max-files", 10000, "Maximum archive entries per deployment")
	flags.DurationVar(&cfg.orphanGrace, "artifact-orphan-grace", time.Hour, "Minimum age before reclaiming abandoned artifact/staging directories")
	flags.StringVar(&cfg.corp, "wework-corp-id", os.Getenv("DROPLY_WEWORK_CORP_ID"), "WeCom Corp ID")
	flags.StringVar(&cfg.agent, "wework-agent-id", os.Getenv("DROPLY_WEWORK_AGENT_ID"), "WeCom Agent ID")
	flags.StringVar(&cfg.secret, "wework-secret", os.Getenv("DROPLY_WEWORK_SECRET"), "WeCom Agent Secret")
	flags.StringVar(&cfg.callback, "wework-redirect-uri", os.Getenv("DROPLY_WEWORK_REDIRECT_URI"), "WeCom OAuth callback URL")
	if err := flags.Parse(args); err != nil {
		return serverConfig{}, err
	}
	if flags.NArg() != 0 {
		return serverConfig{}, errors.New("unexpected positional arguments")
	}
	return cfg, nil
}

// validate checks configuration and reads credential material before data access.
func (cfg *serverConfig) validate() (*tls.Config, error) {
	cfg.domain = strings.ToLower(strings.TrimSuffix(cfg.domain, "."))
	if !validBaseDomain(cfg.domain) {
		return nil, errors.New("invalid base domain: use a DNS hostname without scheme or port")
	}
	if cfg.auditRetention < 1 {
		return nil, errors.New("audit retention must be positive")
	}
	if cfg.retention < 1 {
		return nil, errors.New("log retention must be positive")
	}
	if cfg.mode == "on-demand" {
		cfg.mode = "auto"
	}
	switch cfg.mode {
	case "http", "manual", "auto", "cloudflare":
	default:
		return nil, errors.New("invalid tls-mode")
	}
	if cfg.mode != "http" && cfg.httpsAddr == "" {
		return nil, errors.New("https-addr is required for TLS")
	}
	if cfg.mode == "auto" && cfg.addr == "" {
		return nil, errors.New("automatic HTTP challenge requires an HTTP listener")
	}
	if cfg.legacyCaddy != "" {
		log.Print("--caddy-admin is ignored; configure tls-mode or use an existing proxy")
	}
	var tlsConfig *tls.Config
	if cfg.mode == "manual" {
		pair, err := tls.LoadX509KeyPair(cfg.certFile, cfg.keyFile)
		if err != nil {
			return nil, fmt.Errorf("load manual TLS certificate/key: %w", err)
		}
		if pair.Leaf == nil {
			return nil, errors.New("missing certificate leaf")
		}
		if err := pair.Leaf.VerifyHostname("api." + cfg.domain); err != nil {
			return nil, fmt.Errorf("manual certificate must cover API hostname: %w", err)
		}
		if time.Now().Before(pair.Leaf.NotBefore) || !time.Now().Before(pair.Leaf.NotAfter) {
			return nil, errors.New("manual TLS certificate is not currently valid")
		}
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	}
	if err := cfg.deploymentOptions().Validate(); err != nil {
		return nil, err
	}
	if cfg.proxies != "" {
		if _, err := server.ParseTrustedProxies(strings.Split(cfg.proxies, ",")); err != nil {
			return nil, err
		}
	}
	if cfg.corp != "" || cfg.agent != "" || cfg.secret != "" || cfg.callback != "" {
		if cfg.corp == "" || cfg.agent == "" || cfg.secret == "" || cfg.callback == "" {
			return nil, errors.New("all four WeCom options must be configured")
		}
		if !validHTTPURL(cfg.callback) {
			return nil, errors.New("invalid WeCom callback URL")
		}
	}
	if (cfg.mode == "auto" || cfg.mode == "cloudflare") && cfg.ca != "" && !validHTTPURL(cfg.ca) {
		return nil, errors.New("invalid ACME directory URL")
	}
	if cfg.mode == "auto" || cfg.mode == "cloudflare" {
		certDir := cfg.certDir
		if certDir == "" {
			certDir = filepath.Join(cfg.dataDir, "certificates")
		}
		if err := validateDirectoryPath(certDir); err != nil {
			return nil, fmt.Errorf("certificate directory: %w", err)
		}
	}
	if cfg.mode == "cloudflare" {
		if cfg.tokenFile != "" {
			data, err := os.ReadFile(cfg.tokenFile)
			if err != nil {
				return nil, errors.New("cannot read Cloudflare token file")
			}
			cfg.cloudflareToken = strings.TrimSpace(string(data))
		}
		if cfg.cloudflareToken == "" {
			return nil, errors.New("Cloudflare DNS mode requires a token file or DROPLY_CLOUDFLARE_API_TOKEN")
		}
	}
	if cfg.addr == "" && cfg.mode == "http" {
		return nil, errors.New("at least one listener is required")
	}
	addresses := []string{cfg.addr, cfg.legacySiteAddr}
	if cfg.mode != "http" {
		addresses = append(addresses, cfg.httpsAddr)
	}
	for _, address := range addresses {
		if address == "" {
			continue
		}
		if _, err := net.ResolveTCPAddr("tcp", address); err != nil {
			return nil, fmt.Errorf("invalid listen address %q: %w", address, err)
		}
	}
	return tlsConfig, nil
}

func (cfg serverConfig) deploymentOptions() server.DeploymentOptions {
	return server.DeploymentOptions{MaxExpandedBytes: cfg.expandedLimit, MaxFiles: cfg.fileLimit, MaxStorageBytes: cfg.artifactQuota, RetainCount: cfg.deploymentCount, RetainDays: cfg.deploymentDays, OrphanGrace: cfg.orphanGrace}
}

func validHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 0 || number > 65535 {
			return false
		}
	}
	return true
}

func validBaseDomain(domain string) bool {
	if len(domain) > 253 || !strings.Contains(domain, ".") {
		return false
	}
	for label := range strings.SplitSeq(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// Reject known non-directory components; access and races are checked again by
// the filesystem when resources are opened under the data-directory lock.
func validateDirectoryPath(path string) error {
	path = filepath.Clean(path)
	for {
		info, err := os.Stat(path)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("%q is not a directory", path)
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return err
		}
		path = parent
	}
}
