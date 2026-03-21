package server

import (
	"fmt"
	"log"
)

func (s *Server) RecoverCaddyRoutes() error {
	if s.caddy == nil {
		log.Println("No Caddy client configured, skipping route recovery")
		return nil
	}

	subs, err := s.store.ListAllSubdomains()
	if err != nil {
		return fmt.Errorf("list subdomains: %w", err)
	}

	protectedCount := 0
	for _, sub := range subs {
		hasRules, _ := s.store.HasAccessRules(sub.ID)
		if hasRules {
			if err := s.caddy.SetSubdomainProtected(sub.Name, s.siteAddr); err != nil {
				log.Printf("Warning: failed to set protected route for subdomain %s: %v", sub.Name, err)
			}
			protectedCount++
		} else {
			if err := s.caddy.AddSubdomainRoute(sub.Name); err != nil {
				log.Printf("Warning: failed to add route for subdomain %s: %v", sub.Name, err)
			}
		}
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

	log.Printf("Recovered %d subdomain routes (%d protected) and %d custom domain routes", len(subs), protectedCount, len(domains))
	return nil
}
