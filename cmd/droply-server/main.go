package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/zhong/droply/internal/caddy"
	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dataDir := flag.String("data-dir", "/data/droply", "directory for SQLite database and site files")
	domain := flag.String("domain", "droplydoc.com", "base domain for subdomains")
	caddyAddr := flag.String("caddy-admin", "http://localhost:2019", "Caddy admin API address")
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

	sitesDir := fmt.Sprintf("%s/sites", *dataDir)
	caddyClient := caddy.NewClient(*caddyAddr, *domain, sitesDir)
	srv := server.New(st, sitesDir, *domain, caddyClient)

	if err := srv.RecoverCaddyRoutes(); err != nil {
		log.Printf("Warning: route recovery failed: %v", err)
	}

	log.Printf("droply-server listening on %s (domain=%s, data=%s)", *addr, *domain, *dataDir)
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
