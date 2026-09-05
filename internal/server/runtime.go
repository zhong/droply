package server

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Handler routes the public listener by host. ServeHTTP remains the API handler
// for in-process clients; it must not be exposed on a separate public listener.
func (s *Server) Handler() http.Handler {
	sites := s.NewSiteHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.TrimSuffix(host, ".")
		if host == "api."+strings.ToLower(s.baseDomain) {
			w.Header().Set("Cache-Control", "no-store")
			// Preserve the configured central OAuth callback used by existing installs.
			if r.URL.Path == "/_droply/wework/callback" {
				sites.ServeHTTP(w, r)
				return
			}
			s.ServeHTTP(w, r)
			return
		}
		if !s.AllowedTLSHost(host) {
			http.NotFound(w, r)
			return
		}
		sites.ServeHTTP(w, r)
	})
}

// SetTrustedProxies must be called before serving requests. An empty list means
// the peer address is authoritative, regardless of forwarded headers.
func (s *Server) SetTrustedProxies(cidrs []string) error {
	prefixes, err := ParseTrustedProxies(cidrs)
	if err != nil {
		return err
	}
	s.trustedProxies = prefixes
	return nil
}

// ParseTrustedProxies validates proxy configuration without changing a server.
func ParseTrustedProxies(cidrs []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", cidr, err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func (s *Server) trustedProxy(ip netip.Addr) bool {
	for _, prefix := range s.trustedProxies {
		if prefix.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}

func (s *Server) withTrustedProxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.Clone(r.Context())
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			peer = r.RemoteAddr
		}
		client, _ := netip.ParseAddr(peer)
		trusted := s.trustedProxy(client)
		if trusted {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				chain := strings.Split(xff, ",")
				for i := len(chain) - 1; i >= 0 && s.trustedProxy(client); i-- {
					ip, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
					if err != nil {
						client, _ = netip.ParseAddr(peer)
						break
					}
					client = ip.Unmap()
				}
			} else if ip, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
				client = ip.Unmap()
			}
		}
		r.Header.Del("X-Real-IP")
		r.Header.Del("X-Forwarded-For")
		if !trusted {
			r.Header.Del("X-Forwarded-Proto")
		}
		if client.IsValid() {
			r.Header.Set("X-Real-IP", client.String())
		}
		next.ServeHTTP(w, r)
	})
}

func requestSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func validRedirectPath(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") && !strings.ContainsAny(path, "\\\r\n")
}
