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
	subName := chi.URLParam(r, "sub")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if sub.UserID != userFromContext(r.Context()).ID {
		allowed, err := s.store.ListAccessibleProjects(r.Context(), userFromContext(r.Context()).ID)
		if err != nil {
			jsonError(w, "cannot list projects", 500)
			return
		}
		projects := []model.Project{}
		for _, p := range allowed {
			if p.SubdomainName == sub.Name {
				project, err := s.store.GetProject(sub.ID, p.Project)
				if err != nil {
					jsonError(w, "cannot list projects", 500)
					return
				}
				if project.ID == p.ProjectID {
					projects = append(projects, *project)
				}
			}
		}
		if len(projects) == 0 {
			jsonError(w, "forbidden", 403)
			return
		}
		jsonResponse(w, projects, 200)
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

	if err := s.PrepareDeployments(r.Context()); err != nil {
		jsonError(w, "deployment storage unavailable", 503)
		return
	}
	s.deploymentMu.Lock()
	defer s.deploymentMu.Unlock()
	if err := s.store.DeleteProject(sub.ID, projName); err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	_ = os.RemoveAll(filepath.Join(s.sitesDir, subName, projName))

	w.WriteHeader(http.StatusNoContent)
}
