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

	// Add routes for all subdomains from DB
	subs, err := s.store.ListAllSubdomains()
	if err != nil {
		return fmt.Errorf("list subdomains: %w", err)
	}
	for _, sub := range subs {
		if err := s.caddy.AddSubdomainRoute(sub.Name); err != nil {
			log.Printf("Warning: failed to add route for subdomain %s: %v", sub.Name, err)
		}
	}

	// Add routes for all verified custom domains from DB
	domains, err := s.store.ListAllVerifiedDomainsWithPaths()
	if err != nil {
		return fmt.Errorf("list domains: %w", err)
	}
	for _, d := range domains {
		if err := s.caddy.AddCustomDomainRoute(d.Domain, d.SubdomainName, d.ProjectName); err != nil {
			log.Printf("Warning: failed to add route for domain %s: %v", d.Domain, err)
		}
	}

	log.Printf("Recovered %d subdomain routes and %d custom domain routes", len(subs), len(domains))
	return nil
}
