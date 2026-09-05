package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/zhong/droply/internal/certificates"
)

// SetCertificates attaches certificate status reporting before serving requests.
func (s *Server) SetCertificates(manager *certificates.Manager) { s.certificates = manager }

func (s *Server) handleCertificateStatus(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(strings.TrimSuffix(chi.URLParam(r, "domain"), "."))
	// Platform certificate metadata is shared with authenticated accounts; tenant
	// certificates remain owner-only until a separate administrator role exists.
	if host != s.baseDomain && host != "api."+s.baseDomain {
		sub, projectName, ok := s.resolveHost(host)
		if !ok {
			jsonError(w, "domain not found", http.StatusNotFound)
			return
		}
		owner, err := s.store.GetSubdomainByName(sub)
		if err != nil {
			jsonError(w, "domain not found", http.StatusNotFound)
			return
		}
		allowed := owner.UserID == userFromContext(r.Context()).ID
		if !allowed && projectName != "" {
			if project, err := s.store.GetProject(owner.ID, projectName); err == nil {
				role, err := s.store.ProjectRole(r.Context(), project.ID, userFromContext(r.Context()).ID)
				allowed = err == nil && roleAllows(role, "viewer")
			}
		}
		if !allowed {
			jsonError(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	if s.certificates == nil {
		jsonResponse(w, map[string]string{"domain": host, "state": "externally-managed"}, http.StatusOK)
		return
	}
	status, err := s.certificates.Status(host)
	if err != nil {
		jsonError(w, "certificate status unavailable", http.StatusNotFound)
		return
	}
	jsonResponse(w, status, http.StatusOK)
}
