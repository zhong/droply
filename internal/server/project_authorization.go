package server

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/zhong/droply/internal/model"
)

var errProjectForbidden = errors.New("project operation forbidden")

// projectAllows checks the exact project returned to the handler. A project
// credential also requires its issuer to remain a publisher, even for reads.
func (s *Server) projectAllows(r *http.Request, project *model.Project, userID int64, token *model.ProjectToken) bool {
	required := operationFromRequest(r).policy().role
	if token != nil {
		if token.ProjectID != project.ID || !operationFromRequest(r).tokenAllows(r, token) {
			return false
		}
		required = "deployer"
	}
	role, err := s.store.ProjectRole(r.Context(), project.ID, userID)
	return err == nil && roleAllows(role, required)
}

// projectForOperation resolves and authorizes one identity. A nil project is
// allowed only for the subdomain owner: legacy stats/delete and first deployment
// retain their missing-project behavior. requireProject rejects that case.
func (s *Server) projectForOperation(r *http.Request, sub *model.Subdomain) (*model.Project, error) {
	userID := userFromContext(r.Context()).ID
	name := chi.URLParam(r, "project")
	if name == "" {
		if sub.UserID == userID {
			return nil, nil
		}
		return nil, errProjectForbidden
	}
	project, err := s.store.GetProject(sub.ID, name)
	if errors.Is(err, sql.ErrNoRows) && sub.UserID == userID {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	token, _ := r.Context().Value(projectTokenContextKey).(*model.ProjectToken)
	if !s.projectAllows(r, project, userID, token) {
		return nil, errProjectForbidden
	}
	return project, nil
}

func (s *Server) canAccessSubdomainProject(r *http.Request, sub *model.Subdomain) bool {
	_, err := s.projectForOperation(r, sub)
	return err == nil
}

func (s *Server) requireProject(w http.ResponseWriter, r *http.Request) *model.Project {
	sub, err := s.store.GetSubdomainByName(chi.URLParam(r, "sub"))
	if err != nil {
		jsonError(w, "subdomain not found", 404)
		return nil
	}
	project, err := s.projectForOperation(r, sub)
	if err != nil {
		jsonError(w, "forbidden", 403)
		return nil
	}
	if project == nil {
		jsonError(w, "project not found", 404)
		return nil
	}
	return project
}

func roleAllows(role, required string) bool {
	if required == "" {
		return false
	}
	if role == "owner" {
		return true
	}
	if required == "viewer" {
		return role == "viewer" || role == "deployer"
	}
	return role == "deployer" && required == "deployer"
}

// recheckPublication must run under deploymentMu after any upload or lock wait.
// Stored project identity, current issuer role, token revocation and scope all
// remain authoritative; the authenticated request is not a permission cache.
func (s *Server) recheckPublication(r *http.Request, project *model.Project) error {
	userID := userFromContext(r.Context()).ID
	if !s.projectAllows(r, project, userID, nil) {
		return errors.New("project permission revoked")
	}
	if token, ok := r.Context().Value(projectTokenContextKey).(*model.ProjectToken); ok {
		current, err := s.store.AuthenticateProjectToken(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if err != nil || current.ID != token.ID || current.IssuerID != userID || !s.projectAllows(r, project, current.IssuerID, current) {
			return errors.New("project token permission revoked")
		}
	}
	return nil
}
