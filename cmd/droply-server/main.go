package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/zhong/droply/internal/caddy"
	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
	"github.com/zhong/droply/internal/wework"
)

var version string

func main() {
	if version == "" {
		version = "dev"
	}
	log.Printf("droply-server %s starting", version)
	addr := flag.String("addr", ":8080", "API listen address")
	siteAddr := flag.String("site-addr", ":8081", "site serving listen address")
	dataDir := flag.String("data-dir", "/data/droply", "directory for SQLite database and site files")
	domain := flag.String("domain", "droplydoc.com", "base domain for subdomains")
	caddyAddr := flag.String("caddy-admin", "http://localhost:2019", "Caddy admin API address")
	hmacSecret := flag.String("hmac-secret", "", "HMAC secret for cookie signing (auto-generated if empty)")
	logRetention := flag.Int("log-retention-days", 30, "days to retain detailed visit logs")
	weWorkCorpID := flag.String("wework-corp-id", os.Getenv("DROPLY_WEWORK_CORP_ID"), "WeWork corp ID for QR code login (also DROPLY_WEWORK_CORP_ID)")
	weWorkAgentID := flag.String("wework-agent-id", os.Getenv("DROPLY_WEWORK_AGENT_ID"), "WeWork agent ID (also DROPLY_WEWORK_AGENT_ID)")
	weWorkSecret := flag.String("wework-secret", os.Getenv("DROPLY_WEWORK_SECRET"), "WeWork agent secret (also DROPLY_WEWORK_SECRET)")
	weWorkRedirectURI := flag.String("wework-redirect-uri", os.Getenv("DROPLY_WEWORK_REDIRECT_URI"), "WeWork OAuth callback URL, e.g. https://api.droplydoc.com/_droply/wework/callback (also DROPLY_WEWORK_REDIRECT_URI)")
	flag.Parse()

	dsn := fmt.Sprintf("%s/droply.db", *dataDir)
	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	st, err := store.NewSQLiteStore(dsn)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	hmacKey, err := loadOrGenerateHMACKey(*hmacSecret, *dataDir)
	if err != nil {
		log.Fatalf("HMAC key: %v", err)
	}

	sitesDir := fmt.Sprintf("%s/sites", *dataDir)
	caddyClient := caddy.NewClient(*caddyAddr, *domain, sitesDir)

	siteProxyAddr := "localhost" + *siteAddr
	srv := server.New(st, sitesDir, *domain, caddyClient, hmacKey, siteProxyAddr)

	// Configure WeWork OAuth if all required fields are provided.
	if *weWorkCorpID != "" && *weWorkAgentID != "" && *weWorkSecret != "" && *weWorkRedirectURI != "" {
		weClient := wework.NewClient(wework.Config{
			CorpID:      *weWorkCorpID,
			AgentID:     *weWorkAgentID,
			Secret:      *weWorkSecret,
			RedirectURI: *weWorkRedirectURI,
		})
		srv.SetWeWork(weClient)
		log.Printf("WeWork OAuth enabled (corp=%s, agent=%s)", *weWorkCorpID, *weWorkAgentID)
	} else if *weWorkCorpID != "" || *weWorkAgentID != "" || *weWorkSecret != "" || *weWorkRedirectURI != "" {
		log.Printf("WeWork OAuth NOT enabled: all of corp-id, agent-id, secret, redirect-uri are required")
	}

	if err := srv.RecoverCaddyRoutes(); err != nil {
		log.Printf("Warning: route recovery failed: %v", err)
	}

	srv.StartAnalytics()

	// Start cleanup goroutine
	go func() {
		if n, err := st.CleanupVisitLogs(*logRetention); err == nil && n > 0 {
			log.Printf("Cleaned up %d expired visit logs", n)
		}
		for {
			time.Sleep(24 * time.Hour)
			if n, err := st.CleanupVisitLogs(*logRetention); err == nil && n > 0 {
				log.Printf("Cleaned up %d expired visit logs", n)
			}
		}
	}()

	// Graceful shutdown: drain analytics channel on SIGINT/SIGTERM
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt)
		<-sigCh
		log.Println("Shutting down analytics...")
		srv.ShutdownAnalytics()
		st.Close()
		os.Exit(0)
	}()

	siteHandler := srv.NewSiteHandler()
	go func() {
		log.Printf("site server listening on %s", *siteAddr)
		if err := http.ListenAndServe(*siteAddr, siteHandler); err != nil {
			log.Fatalf("site server error: %v", err)
		}
	}()

	log.Printf("droply-server listening on %s (domain=%s, data=%s)", *addr, *domain, *dataDir)
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func loadOrGenerateHMACKey(secret, dataDir string) ([]byte, error) {
	if secret != "" {
		return []byte(secret), nil
	}

	keyPath := filepath.Join(dataDir, "hmac.key")

	if data, err := os.ReadFile(keyPath); err == nil && len(data) == 32 {
		return data, nil
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate HMAC key: %w", err)
	}

	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		return nil, fmt.Errorf("write HMAC key: %w", err)
	}

	log.Printf("Generated new HMAC key at %s", keyPath)
	return key, nil
}
