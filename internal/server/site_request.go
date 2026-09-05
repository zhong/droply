package server

import (
	"context"
	"github.com/zhong/droply/internal/model"
	"net"
	"strings"
)

// These values live only for one request. Bindings, access rules and production
// pointers are resolved again on the next request; no authorization is cached.
type siteHost struct {
	subdomain, project string
	target             *model.SiteTarget
}

type siteRequest struct {
	subdomain        *model.Subdomain
	project          *model.Project
	target           *model.SiteTarget
	deployment       *model.Deployment
	path, prefix     string
	private, preview bool
}

func (s *Server) resolveHost(ctx context.Context, host string) (siteHost, bool) {
	// Strip port if present.
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	host = strings.ToLower(strings.TrimSuffix(host, "."))

	// Check if it's a subdomain of baseDomain.
	suffix := "." + s.baseDomain
	if sub, ok := strings.CutSuffix(host, suffix); ok {
		if sub != "" && !strings.Contains(sub, ".") {
			if target, err := s.store.GetSiteTarget(ctx, sub); err == nil {
				return siteHost{subdomain: target.SubdomainName, project: target.ProjectName, target: target}, true
			}
			return siteHost{subdomain: sub}, true
		}
	}

	// Check custom domains.
	domains, err := s.store.ListAllVerifiedDomainsWithPaths()
	if err != nil {
		return siteHost{}, false
	}
	for _, d := range domains {
		if strings.EqualFold(d.Domain, host) {
			return siteHost{subdomain: d.SubdomainName, project: d.ProjectName}, true
		}
	}

	return siteHost{}, false
}
