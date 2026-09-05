package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/zhong/droply/internal/store"
)

func (s *Server) handleAccessibleProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListAccessibleProjects(r.Context(), userFromContext(r.Context()).ID)
	if err != nil {
		jsonError(w, "cannot list projects", 500)
		return
	}
	jsonResponse(w, projects, 200)
}
func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	project := s.requireProject(w, r)
	if project == nil {
		return
	}
	members, err := s.store.ListProjectMembers(r.Context(), project.ID)
	if err != nil {
		jsonError(w, "cannot list members", 500)
		return
	}
	jsonResponse(w, members, 200)
}
func (s *Server) handlePutMember(w http.ResponseWriter, r *http.Request) {
	project := s.requireProject(w, r)
	if project == nil {
		return
	}
	var input struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input); err != nil {
		jsonError(w, "invalid membership", 400)
		return
	}
	s.deploymentMu.Lock()
	defer s.deploymentMu.Unlock()
	member, err := s.store.PutProjectMember(r.Context(), project.ID, input.Email, input.Role)
	if errors.Is(err, store.ErrMembership) {
		jsonError(w, "use deployer or viewer; owner cannot be changed", 400)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		jsonError(w, "existing user required", 404)
		return
	}
	if err != nil {
		jsonError(w, "cannot update membership", 500)
		return
	}
	jsonResponse(w, member, 200)
}
func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	project := s.requireProject(w, r)
	if project == nil {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id < 1 {
		jsonError(w, "invalid member ID", 400)
		return
	}
	s.deploymentMu.Lock()
	defer s.deploymentMu.Unlock()
	err = s.store.RemoveProjectMember(r.Context(), project.ID, id)
	if errors.Is(err, store.ErrMembership) {
		jsonError(w, "owner cannot be removed", 409)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		jsonError(w, "member not found", 404)
		return
	}
	if err != nil {
		jsonError(w, "cannot remove member", 500)
		return
	}
	w.WriteHeader(204)
}
