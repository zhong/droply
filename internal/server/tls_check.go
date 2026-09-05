package server

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// handleTLSCheck exposes hostname authorization for optional external gateways.
func (s *Server) handleTLSCheck(w http.ResponseWriter, r *http.Request) {
	domain := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("domain")))
	if domain == "" {
		http.Error(w, "missing domain", http.StatusBadRequest)
		return
	}

	// Strip an optional port.
	if host, _, err := net.SplitHostPort(domain); err == nil {
		domain = host
	}

	if s.isAllowedTLSHost(domain) {
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Error(w, "not authorized", http.StatusForbidden)
}

// AllowedTLSHost authorizes certificates only for active platform or verified custom hosts.
func (s *Server) AllowedTLSHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	base := strings.ToLower(s.baseDomain)

	// Exact base domain or api.{base}
	if host == base || host == "api."+base {
		return true
	}

	// Direct subdomain of base: must be a single label, must exist in store.
	if sub, ok := strings.CutSuffix(host, "."+base); ok {
		if sub == "" || strings.Contains(sub, ".") {
			return false
		}
		if _, err := s.store.GetSiteTarget(context.Background(), sub); err == nil {
			return true
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

func (s *Server) isAllowedTLSHost(host string) bool { return s.AllowedTLSHost(host) }
