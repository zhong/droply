package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zhong/droply/internal/store"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/zhong/droply/internal/model"
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
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "project")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if !s.canAccessSubdomainProject(r, sub) {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	proj, err := s.authorizedProject(r, sub.ID, projName)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}

	var req createDomainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Domain == "" {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	domain, err := normalizeDomain(req.Domain)
	if err != nil || domain == strings.ToLower(s.baseDomain) || strings.HasSuffix(domain, "."+strings.ToLower(s.baseDomain)) {
		jsonError(w, "invalid or reserved domain", http.StatusBadRequest)
		return
	}
	req.Domain = domain
	cd, err := s.store.CreateCustomDomain(proj.ID, req.Domain)
	if err != nil {
		if errors.Is(err, store.ErrDomainTaken) {
			jsonError(w, "domain already bound", http.StatusConflict)
			return
		}
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
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
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "project")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if !s.canAccessSubdomainProject(r, sub) {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	proj, err := s.authorizedProject(r, sub.ID, projName)
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
// and returns 204.
func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "project")
	domainName, err := normalizeDomain(chi.URLParam(r, "domain"))
	if err != nil {
		jsonError(w, "invalid domain", http.StatusBadRequest)
		return
	}

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if !s.canAccessSubdomainProject(r, sub) {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	proj, err := s.authorizedProject(r, sub.ID, projName)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}

	if err := s.store.DeleteCustomDomain(proj.ID, domainName); err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleVerifyDomain checks DNS records for the custom domain and marks it as verified
// if its dedicated TXT challenge proves ownership.
func (s *Server) handleVerifyDomain(w http.ResponseWriter, r *http.Request) {
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "project")
	domainName, err := normalizeDomain(chi.URLParam(r, "domain"))
	if err != nil {
		jsonError(w, "invalid domain", http.StatusBadRequest)
		return
	}

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if !s.canAccessSubdomainProject(r, sub) {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	proj, err := s.authorizedProject(r, sub.ID, projName)
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

	resolver := s.dnsResolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	records, err := resolver.LookupTXT(r.Context(), cd.VerificationRecord)
	if err != nil {
		jsonError(w, "DNS verification lookup failed; retry after publishing the TXT record", http.StatusBadGateway)
		return
	}
	for _, record := range records {
		if record != cd.VerificationToken || cd.VerificationToken == "" {
			continue
		}
		if err := s.store.VerifyCustomDomainChallenge(domainName, cd.VerificationToken); err != nil {
			jsonError(w, "failed to persist verification", http.StatusInternalServerError)
			return
		}
		jsonResponse(w, map[string]any{"verified": true, "status": "verified"}, http.StatusOK)
		return
	}
	markAuditFailure(r)
	jsonResponse(w, map[string]any{"verified": false, "status": "pending", "message": "publish the exact verification TXT record", "verification_record": cd.VerificationRecord, "verification_token": cd.VerificationToken}, http.StatusOK)
}

// DNSResolver is the external DNS boundary for ownership verification.
type DNSResolver interface {
	LookupTXT(context.Context, string) ([]string, error)
}

// SetDNSResolver configures DNS lookup before serving requests. Nil uses the system resolver.
func (s *Server) SetDNSResolver(resolver DNSResolver) { s.dnsResolver = resolver }

func normalizeDomain(domain string) (string, error) {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if len(domain) > 253 || !strings.Contains(domain, ".") || net.ParseIP(domain) != nil {
		return "", fmt.Errorf("invalid hostname")
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid hostname")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", fmt.Errorf("invalid hostname")
			}
		}
	}
	return domain, nil
}
