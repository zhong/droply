package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zhong/droply/internal/model"
	"github.com/zhong/droply/internal/store"
)

const projectTokenContextKey contextKey = "project-token"

func (s *Server) authenticateBearer(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) || len(auth) <= len(prefix) {
		jsonError(w, "unauthorized", 401)
		return nil, false
	}
	raw := strings.TrimPrefix(auth, prefix)
	if !strings.HasPrefix(raw, store.ProjectTokenPrefix) {
		user, err := s.store.GetUserByToken(raw)
		if err != nil {
			jsonError(w, "unauthorized", 401)
			return nil, false
		}
		return context.WithValue(r.Context(), userContextKey, user), true
	}
	token, err := s.store.AuthenticateProjectToken(r.Context(), raw)
	if err != nil {
		jsonError(w, "unauthorized", 401)
		return nil, false
	}
	if !s.projectTokenAllows(r, token) {
		jsonError(w, "project token does not allow this operation", 403)
		return nil, false
	}
	ctx := context.WithValue(r.Context(), userContextKey, &model.User{ID: token.IssuerID})
	return context.WithValue(ctx, projectTokenContextKey, token), true
}

func (s *Server) projectTokenAllows(r *http.Request, token *model.ProjectToken) bool {
	sub, err := s.store.GetSubdomainByName(chi.URLParam(r, "sub"))
	if err != nil {
		return false
	}
	project, err := s.store.GetProject(sub.ID, chi.URLParam(r, "project"))
	if err != nil || project.ID != token.ProjectID {
		return false
	}
	return s.projectAllows(r, project, token.IssuerID, token)
}

func (s *Server) handleCreateProjectToken(w http.ResponseWriter, r *http.Request) {
	if r.Context().Value(projectTokenContextKey) != nil {
		jsonError(w, "user token required", 403)
		return
	}
	project := s.requireProject(w, r)
	if project == nil {
		return
	}
	var input struct {
		Name      string    `json:"name"`
		Scopes    []string  `json:"scopes"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		jsonError(w, "invalid project token request", 400)
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		jsonError(w, "invalid project token request", 400)
		return
	}
	token, raw, err := s.store.CreateProjectToken(r.Context(), project.ID, userFromContext(r.Context()).ID, input.Name, input.Scopes, input.ExpiresAt)
	if errors.Is(err, store.ErrProjectTokenInput) {
		jsonError(w, "use preview/production scopes and an expiration within one year", 400)
		return
	}
	if err != nil {
		jsonError(w, "cannot create project token", 500)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonResponse(w, struct {
		*model.ProjectToken
		Token string `json:"token"`
	}{token, raw}, 201)
}
func (s *Server) handleListProjectTokens(w http.ResponseWriter, r *http.Request) {
	if r.Context().Value(projectTokenContextKey) != nil {
		jsonError(w, "user token required", 403)
		return
	}
	project := s.requireProject(w, r)
	if project == nil {
		return
	}
	tokens, err := s.store.ListProjectTokens(r.Context(), project.ID, userFromContext(r.Context()).ID)
	if err != nil {
		jsonError(w, "cannot list project tokens", 500)
		return
	}
	jsonResponse(w, tokens, 200)
}
func (s *Server) handleRevokeProjectToken(w http.ResponseWriter, r *http.Request) {
	if r.Context().Value(projectTokenContextKey) != nil {
		jsonError(w, "user token required", 403)
		return
	}
	project := s.requireProject(w, r)
	if project == nil {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id < 1 {
		jsonError(w, "invalid token ID", 400)
		return
	}
	s.deploymentMu.Lock()
	defer s.deploymentMu.Unlock()
	err = s.store.RevokeProjectToken(r.Context(), project.ID, userFromContext(r.Context()).ID, id)
	if errors.Is(err, sql.ErrNoRows) {
		jsonError(w, "project token not found", 404)
		return
	}
	if err != nil {
		jsonError(w, "cannot revoke project token", 500)
		return
	}
	w.WriteHeader(204)
}
