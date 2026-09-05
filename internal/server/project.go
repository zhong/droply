package server

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"github.com/zhong/droply/internal/model"
)

// handleListProjects verifies subdomain ownership and returns all projects under it.
// Always returns an array (never null).
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
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

	projects, err := s.store.ListProjects(sub.ID)
	if err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if projects == nil {
		projects = []model.Project{}
	}
	jsonResponse(w, projects, http.StatusOK)
}

// handleDeleteProject verifies subdomain ownership, deletes the project from the store,
// removes the project directory from disk, and returns 204.
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
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

	if err := s.store.DeleteProject(sub.ID, projName); err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	_ = os.RemoveAll(filepath.Join(s.sitesDir, subName, projName))

	w.WriteHeader(http.StatusNoContent)
}
