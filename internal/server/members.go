package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/zhong/droply/internal/model"
	"github.com/zhong/droply/internal/store"
)

// projectOperationRole is the common publisher permission matrix. Public site
// passwords and WeWork cookies never enter this account-authenticated boundary.
func projectOperationRole(r *http.Request) string {
	pattern := chi.RouteContext(r.Context()).RoutePattern()
	const base = "/subdomains/{sub}/projects/{project}"
	if r.Method == http.MethodGet {
		if pattern == base+"/tokens" {
			return "deployer"
		}
		return "viewer"
	}
	switch pattern {
	case base + "/deploy", base + "/promote/{version}", base + "/rollback/{version}", base + "/tokens", base + "/tokens/{id}":
		return "deployer"
	default:
		return "owner"
	}
}
func roleAllows(role, required string) bool {
	if role == "owner" {
		return true
	}
	if required == "viewer" {
		return role == "viewer" || role == "deployer"
	}
	return role == "deployer" && required == "deployer"
}
func (s *Server) canAccessSubdomainProject(r *http.Request, sub *model.Subdomain) bool {
	userID := userFromContext(r.Context()).ID
	projectName := chi.URLParam(r, "project")
	if projectName == "" {
		return sub.UserID == userID
	}
	project, err := s.store.GetProject(sub.ID, projectName)
	if errors.Is(err, sql.ErrNoRows) {
		return sub.UserID == userID
	}
	if err != nil {
		return false
	}
	if token, ok := r.Context().Value(projectTokenContextKey).(*model.ProjectToken); ok && token.ProjectID != project.ID {
		return false
	}
	role, err := s.store.ProjectRole(r.Context(), project.ID, userID)
	return err == nil && roleAllows(role, projectOperationRole(r))
}
func (s *Server) handleAccessibleProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListAccessibleProjects(r.Context(), userFromContext(r.Context()).ID)
	if err != nil {
		jsonError(w, "cannot list projects", 500)
		return
	}
	jsonResponse(w, projects, 200)
}
func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	project := s.ownedProject(w, r)
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
	project := s.ownedProject(w, r)
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
	project := s.ownedProject(w, r)
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

// authorizedProject binds permission to the exact project ID returned to callers,
// preventing a delete/recreate of the same name from borrowing old membership.
func (s *Server) authorizedProject(r *http.Request, subdomainID int64, name string) (*model.Project, error) {
	project, err := s.store.GetProject(subdomainID, name)
	if err != nil {
		return nil, err
	}
	if token, ok := r.Context().Value(projectTokenContextKey).(*model.ProjectToken); ok && token.ProjectID != project.ID {
		return nil, sql.ErrNoRows
	}
	role, err := s.store.ProjectRole(r.Context(), project.ID, userFromContext(r.Context()).ID)
	if err != nil {
		return nil, err
	}
	if !roleAllows(role, projectOperationRole(r)) {
		return nil, sql.ErrNoRows
	}
	return project, nil
}
