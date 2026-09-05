package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zhong/droply/internal/certificates"
	"github.com/zhong/droply/internal/hosting"
	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
	"github.com/zhong/droply/internal/wework"
)

func runServer(ctx context.Context, cfg serverConfig, tlsConfig *tls.Config) error {
	if err := os.MkdirAll(cfg.dataDir, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	dataLock, err := hosting.LockDataDirectory(cfg.dataDir)
	if err != nil {
		return err
	}
	defer dataLock.Close()
	st, err := store.NewSQLiteStore(filepath.Join(cfg.dataDir, "droply.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	key, err := loadOrGenerateHMACKey(cfg.hmacSecret, cfg.dataDir)
	if err != nil {
		return err
	}
	setup, err := assembleServer(ctx, cfg, st, key, tlsConfig)
	if err != nil {
		return err
	}
	return setup.run(ctx, cfg, st)
}

// configuredServer holds the concrete services needed by the listener and worker loops.
type configuredServer struct {
	server         *server.Server
	manager        *certificates.Manager
	listenerConfig hosting.Config
}

func assembleServer(ctx context.Context, cfg serverConfig, st *store.SQLiteStore, key []byte, tlsConfig *tls.Config) (*configuredServer, error) {
	srv := server.New(st, filepath.Join(cfg.dataDir, "sites"), cfg.domain, key)
	srv.SetOpenRegistration(cfg.openRegistration)
	if err := srv.SetDeploymentOptions(server.DeploymentOptions{MaxExpandedBytes: cfg.expandedLimit, MaxFiles: cfg.fileLimit, MaxStorageBytes: cfg.artifactQuota, RetainCount: cfg.deploymentCount, RetainDays: cfg.deploymentDays, OrphanGrace: cfg.orphanGrace}); err != nil {
		return nil, err
	}
	if err := srv.PrepareDeployments(ctx); err != nil {
		return nil, fmt.Errorf("prepare deployments: %w", err)
	}
	if cfg.proxies != "" {
		if err := srv.SetTrustedProxies(strings.Split(cfg.proxies, ",")); err != nil {
			return nil, err
		}
	}
	if cfg.corp != "" || cfg.agent != "" || cfg.secret != "" || cfg.callback != "" {
		if cfg.corp == "" || cfg.agent == "" || cfg.secret == "" || cfg.callback == "" {
			return nil, errors.New("all four WeCom options must be configured")
		}
		srv.SetWeWork(wework.NewClient(wework.Config{CorpID: cfg.corp, AgentID: cfg.agent, Secret: cfg.secret, RedirectURI: cfg.callback}))
	}
	handler := srv.Handler()
	httpHandler := handler
	var manager *certificates.Manager
	issuanceTimeout := certificates.DefaultIssuanceTimeout
	if cfg.mode == "auto" || cfg.mode == "cloudflare" {
		token := ""
		if cfg.mode == "cloudflare" {
			token = cfg.cloudflareToken
			if cfg.tokenFile != "" {
				data, err := os.ReadFile(cfg.tokenFile)
				if err != nil {
					return nil, errors.New("cannot read Cloudflare token file")
				}
				token = strings.TrimSpace(string(data))
			}
			if token == "" {
				return nil, errors.New("Cloudflare DNS mode requires a token file or DROPLY_CLOUDFLARE_API_TOKEN")
			}
		}
		if cfg.certDir == "" {
			cfg.certDir = filepath.Join(cfg.dataDir, "certificates")
		}
		var err error
		manager, err = certificates.New(certificates.Config{Directory: cfg.certDir, Email: cfg.email, CAURL: cfg.ca, BaseDomain: cfg.domain, CloudflareAPIToken: token, IssuanceTimeout: issuanceTimeout, Allowed: srv.AllowedTLSHost})
		if err != nil {
			return nil, err
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
			_, port, _ := net.SplitHostPort(cfg.httpsAddr)
			if port != "" && port != "443" {
				host = net.JoinHostPort(host, port)
			}
			http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusPermanentRedirect)
		})
		if manager != nil {
			httpHandler = manager.HTTPHandler(httpHandler)
		}
	}
	listenerConfig := hosting.Config{HTTPAddr: cfg.addr, Handler: handler, HTTPHandler: httpHandler, TLSConfig: tlsConfig, CertificateTimeout: issuanceTimeout}
	if tlsConfig != nil {
		listenerConfig.HTTPSAddr = cfg.httpsAddr
	}
	return &configuredServer{server: srv, manager: manager, listenerConfig: listenerConfig}, nil
}

func (setup *configuredServer) run(ctx context.Context, cfg serverConfig, st *store.SQLiteStore) error {
	srv, manager := setup.server, setup.manager
	handler := setup.listenerConfig.Handler
	service, err := hosting.Start(setup.listenerConfig)
	if err != nil {
		return err
	}
	var legacy *hosting.Service
	if cfg.legacySiteAddr != "" && cfg.legacySiteAddr != cfg.addr {
		legacy, err = hosting.Start(hosting.Config{HTTPAddr: cfg.legacySiteAddr, Handler: handler})
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
			if _, err := st.CleanupAuditEvents(bg, cfg.auditRetention); err != nil {
				log.Print("audit cleanup failed")
			}
			if _, err := st.CleanupVisitLogs(cfg.retention); err != nil {
				log.Printf("visit cleanup failed: %v", err)
			}
			select {
			case <-bg.Done():
				return
			case <-time.After(24 * time.Hour):
			}
		}
	})
	log.Printf("Droply %s HTTP=%s HTTPS=%s domain=%s", version, service.HTTPAddress(), service.HTTPSAddress(), cfg.domain)
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
