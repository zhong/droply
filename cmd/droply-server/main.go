package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zhong/droply/internal/certificates"
	"github.com/zhong/droply/internal/hosting"
	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
	"github.com/zhong/droply/internal/wework"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("droply-server", flag.ContinueOnError)
	addr := flags.String("addr", ":8080", "HTTP listen address (API and sites); use :80 for automatic HTTPS")
	httpsAddr := flags.String("https-addr", ":443", "HTTPS listen address")
	legacySiteAddr := flags.String("site-addr", "", "Deprecated: optional additional unified HTTP listener")
	legacyCaddy := flags.String("caddy-admin", "", "Deprecated and ignored; Droply no longer uses Caddy")
	mode := flags.String("tls-mode", "http", "http | manual | auto | cloudflare")
	certFile := flags.String("tls-cert", "", "PEM certificate chain for manual TLS")
	keyFile := flags.String("tls-key", "", "PEM private key for manual TLS")
	dataDir := flags.String("data-dir", "/data/droply", "SQLite database and static content directory")
	domain := flags.String("domain", "droplydoc.com", "Base domain")
	email := flags.String("acme-email", os.Getenv("DROPLY_ACME_EMAIL"), "ACME account email")
	ca := flags.String("acme-ca", "", "ACME directory URL (empty: Let's Encrypt production)")
	certDir := flags.String("cert-dir", "", "Certificate storage (default: data-dir/certificates)")
	tokenFile := flags.String("cloudflare-token-file", "", "Cloudflare DNS API token file (or DROPLY_CLOUDFLARE_API_TOKEN)")
	proxies := flags.String("trusted-proxies", "", "Comma-separated trusted proxy CIDRs (default: none)")
	hmacSecret := flags.String("hmac-secret", "", "Existing cookie signing key; otherwise persist data-dir/hmac.key")
	retention := flags.Int("log-retention-days", 30, "Detailed visit log retention")
	deploymentCount := flags.Int("deployment-retain-count", 10, "Keep this many successful deployments per project (0: disable count protection)")
	deploymentDays := flags.Int("deployment-retain-days", 30, "Keep successful deployments this many days (0: disable age protection)")
	artifactQuota := flags.Int64("artifact-max-bytes", 0, "Managed artifact and staging byte quota (0: disk capacity only)")
	expandedLimit := flags.Int64("deploy-max-expanded-bytes", 256<<20, "Maximum extracted bytes per deployment")
	fileLimit := flags.Int("deploy-max-files", 10000, "Maximum archive entries per deployment")
	orphanGrace := flags.Duration("artifact-orphan-grace", time.Hour, "Minimum age before reclaiming abandoned artifact/staging directories")
	corp := flags.String("wework-corp-id", os.Getenv("DROPLY_WEWORK_CORP_ID"), "WeCom Corp ID")
	agent := flags.String("wework-agent-id", os.Getenv("DROPLY_WEWORK_AGENT_ID"), "WeCom Agent ID")
	secret := flags.String("wework-secret", os.Getenv("DROPLY_WEWORK_SECRET"), "WeCom Agent Secret")
	callback := flags.String("wework-redirect-uri", os.Getenv("DROPLY_WEWORK_REDIRECT_URI"), "WeCom OAuth callback URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	*domain = strings.ToLower(strings.TrimSuffix(*domain, "."))
	if !validBaseDomain(*domain) {
		return errors.New("invalid base domain: use a DNS hostname without scheme or port")
	}
	if *retention < 1 {
		return errors.New("log retention must be positive")
	}
	if *mode == "on-demand" {
		*mode = "auto"
	}
	switch *mode {
	case "http", "manual", "auto", "cloudflare":
	default:
		return errors.New("invalid tls-mode")
	}
	if *mode != "http" && *httpsAddr == "" {
		return errors.New("https-addr is required for TLS")
	}
	if *mode == "auto" && *addr == "" {
		return errors.New("automatic HTTP challenge requires an HTTP listener")
	}
	if *legacyCaddy != "" {
		log.Print("--caddy-admin is ignored; configure tls-mode or use an existing proxy")
	}
	var tlsConfig *tls.Config
	if *mode == "manual" {
		pair, err := tls.LoadX509KeyPair(*certFile, *keyFile)
		if err != nil {
			return fmt.Errorf("load manual TLS certificate/key: %w", err)
		}
		if pair.Leaf == nil {
			return errors.New("missing certificate leaf")
		}
		if err := pair.Leaf.VerifyHostname("api." + *domain); err != nil {
			return fmt.Errorf("manual certificate must cover API hostname: %w", err)
		}
		if time.Now().Before(pair.Leaf.NotBefore) || !time.Now().Before(pair.Leaf.NotAfter) {
			return errors.New("manual TLS certificate is not currently valid")
		}
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	}
	if err := os.MkdirAll(*dataDir, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	dataLock, err := hosting.LockDataDirectory(*dataDir)
	if err != nil {
		return err
	}
	defer dataLock.Close()
	st, err := store.NewSQLiteStore(filepath.Join(*dataDir, "droply.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	key, err := loadOrGenerateHMACKey(*hmacSecret, *dataDir)
	if err != nil {
		return err
	}
	srv := server.New(st, filepath.Join(*dataDir, "sites"), *domain, key)
	if err := srv.SetDeploymentOptions(server.DeploymentOptions{MaxExpandedBytes: *expandedLimit, MaxFiles: *fileLimit, MaxStorageBytes: *artifactQuota, RetainCount: *deploymentCount, RetainDays: *deploymentDays, OrphanGrace: *orphanGrace}); err != nil {
		return err
	}
	if err := srv.PrepareDeployments(ctx); err != nil {
		return fmt.Errorf("prepare deployments: %w", err)
	}
	if *proxies != "" {
		if err := srv.SetTrustedProxies(strings.Split(*proxies, ",")); err != nil {
			return err
		}
	}
	if *corp != "" || *agent != "" || *secret != "" || *callback != "" {
		if *corp == "" || *agent == "" || *secret == "" || *callback == "" {
			return errors.New("all four WeCom options must be configured")
		}
		srv.SetWeWork(wework.NewClient(wework.Config{CorpID: *corp, AgentID: *agent, Secret: *secret, RedirectURI: *callback}))
	}
	handler := srv.Handler()
	httpHandler := handler
	var manager *certificates.Manager
	if *mode == "auto" || *mode == "cloudflare" {
		token := ""
		if *mode == "cloudflare" {
			token = os.Getenv("DROPLY_CLOUDFLARE_API_TOKEN")
			if *tokenFile != "" {
				data, err := os.ReadFile(*tokenFile)
				if err != nil {
					return errors.New("cannot read Cloudflare token file")
				}
				token = strings.TrimSpace(string(data))
			}
			if token == "" {
				return errors.New("Cloudflare DNS mode requires a token file or DROPLY_CLOUDFLARE_API_TOKEN")
			}
		}
		if *certDir == "" {
			*certDir = filepath.Join(*dataDir, "certificates")
		}
		manager, err = certificates.New(certificates.Config{Directory: *certDir, Email: *email, CAURL: *ca, BaseDomain: *domain, CloudflareAPIToken: token, Allowed: srv.AllowedTLSHost})
		if err != nil {
			return err
		}
		srv.SetCertificates(manager)
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: manager.GetCertificate}
	}
	if tlsConfig != nil {
		httpHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			if !srv.AllowedTLSHost(host) {
				http.NotFound(w, r)
				return
			}
			_, port, _ := net.SplitHostPort(*httpsAddr)
			if port != "" && port != "443" {
				host = net.JoinHostPort(host, port)
			}
			http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusPermanentRedirect)
		})
		if manager != nil {
			httpHandler = manager.HTTPHandler(httpHandler)
		}
	}
	cfg := hosting.Config{HTTPAddr: *addr, Handler: handler, HTTPHandler: httpHandler, TLSConfig: tlsConfig}
	if tlsConfig != nil {
		cfg.HTTPSAddr = *httpsAddr
	}
	service, err := hosting.Start(cfg)
	if err != nil {
		return err
	}
	var legacy *hosting.Service
	if *legacySiteAddr != "" && *legacySiteAddr != *addr {
		legacy, err = hosting.Start(hosting.Config{HTTPAddr: *legacySiteAddr, Handler: handler})
		if err != nil {
			service.Shutdown(context.Background())
			return err
		}
	}
	srv.StartAnalytics()
	bg, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	workers.Go(func() { srv.RunDeploymentCleanup(bg) })
	if manager != nil {
		workers.Go(func() { manager.Run(bg) })
	}
	workers.Go(func() {
		for {
			if _, err := st.CleanupVisitLogs(*retention); err != nil {
				log.Printf("visit cleanup failed: %v", err)
			}
			select {
			case <-bg.Done():
				return
			case <-time.After(24 * time.Hour):
			}
		}
	})
	log.Printf("Droply %s HTTP=%s HTTPS=%s domain=%s", version, service.HTTPAddress(), service.HTTPSAddress(), *domain)
	select {
	case <-ctx.Done():
	case err = <-service.Errors():
	}
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	err = errors.Join(err, service.Shutdown(shutdownCtx))
	if legacy != nil {
		err = errors.Join(err, legacy.Shutdown(shutdownCtx))
	}
	workers.Wait()
	srv.ShutdownAnalytics()
	return err
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

func loadOrGenerateHMACKey(secret, dataDir string) ([]byte, error) {
	if secret != "" {
		return []byte(secret), nil
	}
	path := filepath.Join(dataDir, "hmac.key")
	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) != 32 {
			return nil, errors.New("invalid persisted HMAC key; restore the existing key instead of rotating sessions")
		}
		return data, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read HMAC key: %w", err)
	}
	data = make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return nil, err
	}
	return data, nil
}
