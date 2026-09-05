package server

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/zhong/droply/internal/artifacts"
	"github.com/zhong/droply/internal/model"
)

const maxUploadSize = 50 << 20

type deployResponse struct {
	DeploymentID int64  `json:"deployment_id"`
	Version      int    `json:"version"`
	FileCount    int    `json:"file_count"`
	TotalSize    int64  `json:"total_size"`
	URL          string `json:"url"`
}

// Uploads are staged without holding the serving lock. Publication takes the
// write lock and commits SQLite only after the complete artifact is durable.
func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	subName, projName := chi.URLParam(r, "sub"), chi.URLParam(r, "project")
	if !validName(projName) {
		jsonError(w, "invalid project name", 400)
		return
	}
	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", 404)
		return
	}
	if sub.UserID != userFromContext(r.Context()).ID {
		jsonError(w, "forbidden", 403)
		return
	}
	if err := s.PrepareDeployments(r.Context()); err != nil {
		jsonError(w, "deployment storage unavailable", 503)
		return
	}
	// Bound aggregate staging space in this single-instance server. Readers and
	// rollbacks remain available while the archive streams to disk.
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	if r.Context().Err() != nil {
		jsonError(w, "upload canceled", 400)
		return
	}
	s.deploymentMu.Lock()
	proj, err := s.store.GetProject(sub.ID, projName)
	if errors.Is(err, sql.ErrNoRows) {
		proj, err = s.store.CreateProject(sub.ID, projName)
	}
	if err != nil {
		s.deploymentMu.Unlock()
		jsonError(w, "cannot create project", 500)
		return
	}
	id := rand.Text()
	deployment, err := s.store.BeginDeployment(r.Context(), proj.ID, id)
	s.deploymentMu.Unlock()
	if err != nil {
		jsonError(w, "cannot reserve deployment version", 500)
		return
	}
	success := false
	defer func() {
		if !success {
			if err := s.artifacts.RemoveStage(id); err != nil {
				log.Printf("remove staging for deployment %d: %v", deployment.ID, err)
			}
		}
	}()
	fail := func(reason string, code int) {
		s.failDeployment(deployment.ID, reason)
		jsonResponse(w, map[string]any{"error": reason, "deployment_id": deployment.ID, "version": deployment.Version}, code)
	}
	limits := s.artifactLimits()
	quotaReduced := false
	if s.deploymentOptions.MaxStorageBytes > 0 {
		used, err := s.artifacts.Usage()
		if err != nil {
			fail("cannot inspect storage capacity", 507)
			return
		}
		remaining := s.deploymentOptions.MaxStorageBytes - used
		if remaining <= 0 {
			fail("artifact storage quota exhausted", 507)
			return
		}
		if limits.MaxBytes <= 0 {
			limits.MaxBytes = 256 << 20
		}
		quotaReduced = remaining < limits.MaxBytes
		limits.MaxBytes = min(limits.MaxBytes, remaining)
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	reader, err := r.MultipartReader()
	if err != nil {
		fail("multipart file upload required", 400)
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		fail("multipart file upload required", 400)
		return
	}
	info, err := s.artifacts.Stage(r.Context(), id, part, limits)
	if err == nil {
		_, err = reader.NextPart()
		if errors.Is(err, io.EOF) {
			_, err = io.Copy(io.Discard, r.Body)
		} else if err == nil {
			err = errors.New("only one archive may be uploaded")
		}
	}
	if err != nil {
		code := http.StatusBadRequest
		reason := "invalid archive, interrupted upload, or extraction limit exceeded"
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			code = 413
			reason = "compressed upload exceeds 50 MiB"
		}
		if errors.Is(err, syscall.ENOSPC) {
			code = 507
			reason = "insufficient disk space"
		}
		if quotaReduced && errors.Is(err, artifacts.ErrByteLimit) {
			code = 507
			reason = "artifact storage quota exhausted"
		}
		log.Printf("deployment %d staging failed: %v", deployment.ID, err)
		fail(reason, code)
		return
	}
	s.deploymentMu.Lock()
	defer s.deploymentMu.Unlock()
	if r.Context().Err() != nil {
		fail("upload canceled before publication", 400)
		return
	}
	if s.deploymentOptions.MaxStorageBytes > 0 {
		used, err := s.artifacts.Usage()
		if err != nil || used > s.deploymentOptions.MaxStorageBytes {
			fail("artifact storage quota exhausted", 507)
			return
		}
	}
	if err := s.artifacts.Publish(id); err != nil {
		fail("cannot persist complete artifact", 507)
		return
	}
	if err := s.store.CommitDeployment(r.Context(), deployment.ID, info.FileCount, info.TotalSize, info.Checksum); err != nil {
		// Do not remove the published directory after an uncertain commit. SQLite
		// decides whether it is referenced; recovery/GC reclaim genuine orphans.
		fail("publication transaction failed; query deployment history", 500)
		return
	}
	success = true
	jsonResponse(w, deployResponse{DeploymentID: deployment.ID, Version: deployment.Version, FileCount: info.FileCount, TotalSize: info.TotalSize, URL: fmt.Sprintf("https://%s.%s/%s", subName, s.baseDomain, projName)}, 200)
}

// handleListDeployments verifies subdomain ownership and returns all deployments for the project.
// Always returns an array (never null).
func (s *Server) handleListDeployments(w http.ResponseWriter, r *http.Request) {
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

	proj, err := s.store.GetProject(sub.ID, projName)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}

	if err := s.PrepareDeployments(r.Context()); err != nil {
		jsonError(w, "deployment storage unavailable", 503)
		return
	}
	s.deploymentMu.RLock()
	defer s.deploymentMu.RUnlock()
	deployments, err := s.store.ListDeployments(proj.ID)
	if err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if deployments == nil {
		deployments = []model.Deployment{}
	}
	jsonResponse(w, deployments, http.StatusOK)
}
