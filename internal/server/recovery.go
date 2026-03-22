package server

import (
	"fmt"
	"log"
)

// RecoverCaddyRoutes restores custom domain routes in Caddy after a restart.
// Subdomain routes are not needed here because Caddy's wildcard block
// reverse-proxies all *.baseDomain traffic to the site server directly.
func (s *Server) RecoverCaddyRoutes() error {
	if s.caddy == nil {
		log.Println("No Caddy client configured, skipping route recovery")
		return nil
	}

	domains, err := s.store.ListAllVerifiedDomainsWithPaths()
	if err != nil {
		return fmt.Errorf("list domains: %w", err)
	}
	for _, d := range domains {
		rule, _ := s.store.FindAccessRuleForSite(d.SubdomainName, d.ProjectName)
		if rule != nil {
			if err := s.caddy.SetCustomDomainProtected(d.Domain, s.siteAddr); err != nil {
				log.Printf("Warning: failed to set protected route for domain %s: %v", d.Domain, err)
			}
		} else {
			if err := s.caddy.AddCustomDomainRoute(d.Domain, d.SubdomainName, d.ProjectName); err != nil {
				log.Printf("Warning: failed to add route for domain %s: %v", d.Domain, err)
			}
		}
	}

	log.Printf("Recovered %d custom domain routes", len(domains))
	return nil
}
