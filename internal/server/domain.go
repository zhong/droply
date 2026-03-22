package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/zhong/droply/internal/model"
	"github.com/go-chi/chi/v5"
)

type createDomainRequest struct {
	Domain string `json:"domain"`
}

type createDomainResponse struct {
	*model.CustomDomain
	CnameTarget string `json:"cname_target"`
}

// handleCreateDomain validates subdomain ownership, creates a custom domain record,
// and returns 201 with the domain, verified=false, and the CNAME target.
func (s *Server) handleCreateDomain(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "project")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if sub.UserID != user.ID {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	proj, err := s.store.GetProject(sub.ID, projName)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}

	var req createDomainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Domain == "" {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	cd, err := s.store.CreateCustomDomain(proj.ID, req.Domain)
	if err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if s.caddy != nil {
		rule, _ := s.store.FindAccessRuleForSite(subName, projName)
		if rule != nil {
			_ = s.caddy.SetCustomDomainProtected(req.Domain, s.siteAddr)
		} else {
			_ = s.caddy.AddCustomDomainRoute(req.Domain, subName, projName)
		}
	}

	cnameTarget := fmt.Sprintf("%s.%s", subName, s.baseDomain)
	jsonResponse(w, createDomainResponse{
		CustomDomain: cd,
		CnameTarget:  cnameTarget,
	}, http.StatusCreated)
}

// handleListDomains verifies subdomain ownership and returns all custom domains for the project.
// Always returns an array (never null).
func (s *Server) handleListDomains(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "project")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if sub.UserID != user.ID {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	proj, err := s.store.GetProject(sub.ID, projName)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}

	domains, err := s.store.ListCustomDomains(proj.ID)
	if err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if domains == nil {
		domains = []model.CustomDomain{}
	}
	jsonResponse(w, domains, http.StatusOK)
}

// handleDeleteDomain verifies subdomain ownership, deletes the custom domain from the store,
// removes it from Caddy if configured, and returns 204.
func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "project")
	domainName := chi.URLParam(r, "domain")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if sub.UserID != user.ID {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	proj, err := s.store.GetProject(sub.ID, projName)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}

	if err := s.store.DeleteCustomDomain(proj.ID, domainName); err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if s.caddy != nil {
		_ = s.caddy.RemoveCustomDomainRoute(domainName)
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleVerifyDomain checks DNS records for the custom domain and marks it as verified
// if a CNAME or A record resolves to the expected target.
func (s *Server) handleVerifyDomain(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "project")
	domainName := chi.URLParam(r, "domain")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if sub.UserID != user.ID {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	proj, err := s.store.GetProject(sub.ID, projName)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}

	cd, err := s.store.GetCustomDomain(domainName)
	if err != nil || cd.ProjectID != proj.ID {
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}

	if cd.Verified {
		jsonResponse(w, map[string]any{"verified": true, "message": "already verified"}, http.StatusOK)
		return
	}

	expectedTarget := fmt.Sprintf("%s.%s", subName, s.baseDomain)

	// Check CNAME records.
	if cname, err := net.LookupCNAME(domainName); err == nil {
		cname = strings.TrimSuffix(cname, ".")
		if strings.EqualFold(cname, expectedTarget) {
			s.store.VerifyCustomDomain(domainName)
			jsonResponse(w, map[string]any{"verified": true}, http.StatusOK)
			return
		}
	}

	// Check A records — if the domain resolves to the same IPs as the expected target.
	domainIPs, err := net.LookupHost(domainName)
	if err == nil && len(domainIPs) > 0 {
		targetIPs, err := net.LookupHost(expectedTarget)
		if err == nil {
			ipSet := map[string]bool{}
			for _, ip := range targetIPs {
				ipSet[ip] = true
			}
			for _, ip := range domainIPs {
				if ipSet[ip] {
					s.store.VerifyCustomDomain(domainName)
					jsonResponse(w, map[string]any{"verified": true}, http.StatusOK)
					return
				}
			}
		}
	}

	jsonResponse(w, map[string]any{
		"verified": false,
		"message":  fmt.Sprintf("DNS not pointing to %s (CNAME or A record required)", expectedTarget),
	}, http.StatusOK)
}
