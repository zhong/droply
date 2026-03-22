package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zhong/droply/internal/model"
	"github.com/go-chi/chi/v5"
)

// nameRegex validates subdomain names: lowercase alphanumeric, hyphens allowed in middle,
// length 3-32 characters total.
var nameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$`)

// validName returns true if name matches the allowed subdomain name pattern.
func validName(name string) bool {
	return nameRegex.MatchString(name)
}

type createSubdomainRequest struct {
	Name string `json:"name"`
}

// handleCreateSubdomain validates the name, creates the subdomain record in the store,
// optionally registers it with Caddy, and returns 201 with the new subdomain.
func (s *Server) handleCreateSubdomain(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())

	var req createSubdomainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if !validName(req.Name) {
		jsonError(w, "invalid subdomain name", http.StatusBadRequest)
		return
	}

	sub, err := s.store.CreateSubdomain(user.ID, req.Name)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			jsonError(w, "subdomain already taken", http.StatusConflict)
			return
		}
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, sub, http.StatusCreated)
}

// handleListSubdomains returns all subdomains belonging to the authenticated user.
// Always returns an array (never null).
func (s *Server) handleListSubdomains(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())

	subs, err := s.store.ListSubdomains(user.ID)
	if err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if subs == nil {
		subs = []model.Subdomain{}
	}
	jsonResponse(w, subs, http.StatusOK)
}

// handleDeleteSubdomain verifies the user owns the subdomain, deletes it from the store,
// removes it from Caddy, and returns 204.
func (s *Server) handleDeleteSubdomain(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if sub.UserID != user.ID {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := s.store.DeleteSubdomain(user.ID, subName); err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	_ = os.RemoveAll(filepath.Join(s.sitesDir, subName))

	w.WriteHeader(http.StatusNoContent)
}
