package server

import (
	"net"
	"net/http"
	"strings"
)

// handleTLSCheck is called by Caddy's on-demand TLS to decide whether to obtain a certificate
// for a given hostname. This prevents Let's Encrypt rate-limit exhaustion from random scanning.
//
// Allowed hostnames:
//   - The base domain itself (e.g. droplydoc.com)
//   - The api.{baseDomain} hostname (used by the CLI)
//   - Any direct subdomain registered in the store (alice.droplydoc.com)
//   - Any verified custom domain (blog.example.com)
//
// Caddy contract:
//   - HTTP 200 with any body → obtain certificate
//   - Any other status       → refuse
//
// Reference: https://caddyserver.com/docs/json/apps/tls/automation/on_demand/ask/
func (s *Server) handleTLSCheck(w http.ResponseWriter, r *http.Request) {
	domain := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("domain")))
	if domain == "" {
		http.Error(w, "missing domain", http.StatusBadRequest)
		return
	}

	// Strip port if Caddy includes one.
	if host, _, err := net.SplitHostPort(domain); err == nil {
		domain = host
	}

	if s.isAllowedTLSHost(domain) {
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Error(w, "not authorized", http.StatusForbidden)
}

// isAllowedTLSHost returns true if Caddy should be permitted to obtain a TLS certificate
// for the given hostname.
func (s *Server) isAllowedTLSHost(host string) bool {
	base := strings.ToLower(s.baseDomain)

	// Exact base domain or api.{base}
	if host == base || host == "api."+base {
		return true
	}

	// Direct subdomain of base: must be a single label, must exist in store.
	if suffix := "." + base; strings.HasSuffix(host, suffix) {
		sub := strings.TrimSuffix(host, suffix)
		if sub == "" || strings.Contains(sub, ".") {
			return false
		}
		if _, err := s.store.GetSubdomainByName(sub); err == nil {
			return true
		}
		return false
	}

	// Verified custom domain.
	domains, err := s.store.ListAllVerifiedDomainsWithPaths()
	if err != nil {
		return false
	}
	for _, d := range domains {
		if strings.EqualFold(d.Domain, host) {
			return true
		}
	}
	return false
}
