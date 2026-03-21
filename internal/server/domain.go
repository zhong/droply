package server

import (
	"encoding/json"
	"fmt"
	"net/http"

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
