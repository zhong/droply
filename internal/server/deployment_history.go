package server

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/zhong/droply/internal/model"
	"github.com/zhong/droply/internal/store"
)

func (s *Server) ownedProject(w http.ResponseWriter, r *http.Request) *model.Project {
	sub, err := s.store.GetSubdomainByName(chi.URLParam(r, "sub"))
	if err != nil {
		jsonError(w, "subdomain not found", 404)
		return nil
	}
	if sub.UserID != userFromContext(r.Context()).ID {
		jsonError(w, "forbidden", 403)
		return nil
	}
	project, err := s.store.GetProject(sub.ID, chi.URLParam(r, "project"))
	if err != nil {
		jsonError(w, "project not found", 404)
		return nil
	}
	if token, ok := r.Context().Value(projectTokenContextKey).(*model.ProjectToken); ok && token.ProjectID != project.ID {
		jsonError(w, "project token cannot target this project", 403)
		return nil
	}
	return project
}

func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	s.switchPublication(w, r, false)
}

func (s *Server) handlePromote(w http.ResponseWriter, r *http.Request) {
	s.switchPublication(w, r, true)
}

func (s *Server) switchPublication(w http.ResponseWriter, r *http.Request, promote bool) {
	project := s.ownedProject(w, r)
	if project == nil {
		return
	}
	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || version < 1 {
		jsonError(w, "version must be a positive integer", 400)
		return
	}
	if err := s.PrepareDeployments(r.Context()); err != nil {
		jsonError(w, "deployment storage unavailable", 503)
		return
	}
	s.deploymentMu.Lock()
	defer s.deploymentMu.Unlock()
	d, err := s.store.GetDeployment(r.Context(), project.ID, version)
	if errors.Is(err, sql.ErrNoRows) {
		jsonError(w, "version not found in this project", 404)
		return
	}
	if err != nil {
		jsonError(w, "cannot read deployment", 500)
		return
	}
	eligible := d.Status == "active" || d.Status == "archived"
	if promote {
		eligible = d.Environment == "preview" && (eligible || d.Status == "preview")
	}
	if !d.Available || !eligible {
		jsonError(w, "version has no retained production artifact", 409)
		return
	}
	info, err := s.artifacts.Verify(r.Context(), d.ArtifactID)
	if err != nil || info.Checksum != d.Checksum {
		if r.Context().Err() == nil {
			if err := s.store.SetArtifactState(r.Context(), d.ID, "missing"); err != nil {
				jsonError(w, "cannot record unavailable artifact", 500)
				return
			}
		}
		jsonError(w, "artifact unavailable or damaged; production was not switched", 409)
		return
	}
	var changed bool
	if promote {
		d, changed, err = s.store.PromoteDeployment(r.Context(), project.ID, version, userFromContext(r.Context()).ID)
	} else {
		d, changed, err = s.store.SwitchDeployment(r.Context(), project.ID, version)
	}
	if errors.Is(err, store.ErrDeploymentState) {
		jsonError(w, "version cannot be activated", 409)
		return
	}
	if err != nil {
		jsonError(w, "rollback transaction failed; query current production", 500)
		return
	}
	jsonResponse(w, struct {
		Deployment *model.Deployment `json:"deployment"`
		Changed    bool              `json:"changed"`
	}{d, changed}, 200)
}

func (s *Server) handlePublicationEvents(w http.ResponseWriter, r *http.Request) {
	project := s.ownedProject(w, r)
	if project == nil {
		return
	}
	events, err := s.store.ListPublicationEvents(r.Context(), project.ID)
	if err != nil {
		jsonError(w, "cannot read publication events", 500)
		return
	}
	if events == nil {
		events = []model.PublicationEvent{}
	}
	jsonResponse(w, events, 200)
}
