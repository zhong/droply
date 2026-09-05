package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zhong/droply/internal/model"
)

const auditOutcomeContextKey contextKey = "audit-outcome"

type auditOutcome struct{ failed bool }

func markAuditFailure(r *http.Request) {
	if outcome, ok := r.Context().Value(auditOutcomeContextKey).(*auditOutcome); ok {
		outcome.failed = true
	}
}

// Reserve a durable pending event before mutation. A crash or failed final write
// leaves an honest unknown outcome, rather than an invented successful event.
func (s *Server) auditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := operationFromRequest(r).policy().audit
		if action == "" {
			next.ServeHTTP(w, r)
			return
		}
		user := userFromContext(r.Context())
		event := model.AuditEvent{ActorKind: "user", ActorID: user.ID, UserID: user.ID, Action: action, Target: "request"}
		if token, ok := r.Context().Value(projectTokenContextKey).(*model.ProjectToken); ok {
			event.ActorKind = "project_token"
			event.ActorID = token.ID
		}
		if sub, err := s.store.GetSubdomainByName(chi.URLParam(r, "sub")); err == nil {
			event.SubdomainID = sub.ID
			event.Target = fmt.Sprintf("subdomain:%d", sub.ID)
			if project, err := s.store.GetProject(sub.ID, chi.URLParam(r, "project")); err == nil {
				event.ProjectID = project.ID
				event.Target = fmt.Sprintf("project:%d", project.ID)
			}
		}
		if hostname, err := normalizeDomain(chi.URLParam(r, "domain")); err == nil {
			if domain, err := s.store.GetCustomDomain(hostname); err == nil && domain.ProjectID == event.ProjectID {
				event.Target = fmt.Sprintf("domain:%d", domain.ID)
			}
		}
		for _, name := range []string{"version", "id"} {
			if n, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64); err == nil && n > 0 {
				event.Target = fmt.Sprintf("%s:%d", name, n)
			}
		}
		id, err := s.store.BeginAuditEvent(r.Context(), event)
		if err != nil {
			jsonError(w, "audit storage unavailable; operation not started", 503)
			return
		}
		outcome := &auditOutcome{}
		r = r.WithContext(context.WithValue(r.Context(), auditOutcomeContextKey, outcome))
		capture := &auditResponse{ResponseWriter: w}
		completed := false
		defer func() {
			status := capture.status
			if !completed {
				status = 500
			} else if status == 0 {
				status = 200
			}
			// Decode only numeric resource identifiers; neither body nor credentials are persisted.
			var result struct {
				ID         int64 `json:"id"`
				UserID     int64 `json:"user_id"`
				Version    int   `json:"version"`
				Deployment struct {
					Version int `json:"version"`
				} `json:"deployment"`
			}
			if completed && json.Unmarshal(capture.body, &result) == nil {
				switch {
				case result.Version > 0:
					event.Target = fmt.Sprintf("version:%d", result.Version)
				case result.Deployment.Version > 0:
					event.Target = fmt.Sprintf("version:%d", result.Deployment.Version)
				case result.UserID > 0:
					event.Target = fmt.Sprintf("user:%d", result.UserID)
				case result.ID > 0:
					event.Target = fmt.Sprintf("id:%d", result.ID)
				}
			}
			if event.ProjectID == 0 && event.SubdomainID > 0 && chi.URLParam(r, "project") != "" {
				if project, err := s.store.GetProject(event.SubdomainID, chi.URLParam(r, "project")); err == nil {
					event.ProjectID = project.ID
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			resultName := "failure"
			if completed && status >= 200 && status < 300 && !outcome.failed {
				resultName = "success"
			}
			if err := s.store.FinishAuditEvent(ctx, id, event.ProjectID, event.Target, status, resultName); err != nil {
				log.Printf("audit event %d remains pending: finalization failed", id)
			}
		}()
		next.ServeHTTP(capture, r)
		completed = true
	})
}

type auditResponse struct {
	http.ResponseWriter
	status int
	body   []byte
}

func (w *auditResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *auditResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *auditResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if len(w.body) < 4096 {
		w.body = append(w.body, data[:min(len(data), 4096-len(w.body))]...)
	}
	return w.ResponseWriter.Write(data)
}

func (s *Server) registerAuditRoutes(r chi.Router) {
	r.With(s.forOperation(opProjectAudit)).Get("/subdomains/{sub}/projects/{project}/audit", s.handleProjectAudit)
	r.With(s.forOperation(opAdminAudit)).Get("/admin/audit", s.handleAdminAudit)
}
func (s *Server) handleProjectAudit(w http.ResponseWriter, r *http.Request) {
	project := s.requireProject(w, r)
	if project == nil {
		return
	}
	s.writeAuditPage(w, r, project.ID, project.SubdomainID)
}
func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	s.writeAuditPage(w, r, 0, 0)
}
func (s *Server) writeAuditPage(w http.ResponseWriter, r *http.Request, projectID, subdomainID int64) {
	limit := 50
	before := int64(0)
	if value := r.URL.Query().Get("limit"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 100 {
			jsonError(w, "limit must be between 1 and 100", 400)
			return
		}
		limit = n
	}
	if value := r.URL.Query().Get("before"); value != "" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 {
			jsonError(w, "before must be a non-negative cursor", 400)
			return
		}
		before = n
	}
	events, err := s.store.ListAuditEvents(r.Context(), projectID, subdomainID, before, limit)
	if err != nil {
		jsonError(w, "cannot query audit events", 500)
		return
	}
	next := int64(0)
	if len(events) == limit {
		next = events[len(events)-1].ID
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonResponse(w, struct {
		Events     []model.AuditEvent `json:"events"`
		NextCursor int64              `json:"next_cursor"`
	}{events, next}, 200)
}
