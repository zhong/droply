package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/zhong/droply/internal/store"
)

type cleanupCandidate struct {
	Version int   `json:"version"`
	Bytes   int64 `json:"bytes"`
}
type cleanupReport struct {
	DryRun           bool               `json:"dry_run"`
	UsedBytes        int64              `json:"used_bytes"`
	ReclaimableBytes int64              `json:"reclaimable_bytes"`
	Candidates       []cleanupCandidate `json:"candidates"`
	DeletedVersions  []int              `json:"deleted_versions"`
	Errors           []string           `json:"errors"`
}

func (s *Server) handleDeploymentCleanup(w http.ResponseWriter, r *http.Request) {
	project := s.requireProject(w, r)
	if project == nil {
		return
	}
	keep, days := s.deploymentOptions.RetainCount, s.deploymentOptions.RetainDays
	for key, dst := range map[string]*int{"keep": &keep, "days": &days} {
		if values, ok := r.URL.Query()[key]; ok {
			if len(values) != 1 {
				jsonError(w, "retention parameter must occur once", 400)
				return
			}
			value, err := strconv.Atoi(values[0])
			if err != nil || value < 0 || value > 100000 {
				jsonError(w, "retention must be between 0 and 100000", 400)
				return
			}
			*dst = value
		}
	}
	if err := s.PrepareDeployments(r.Context()); err != nil {
		recordAudit(r, auditFailure)
		jsonError(w, "deployment storage unavailable", 503)
		return
	}
	s.deploymentMu.Lock()
	defer s.deploymentMu.Unlock()
	report, err := s.cleanupProject(r.Context(), project.ID, keep, days, r.Method == http.MethodPost)
	if err != nil {
		jsonError(w, "cannot inspect artifact retention", 500)
		return
	}
	recordAudit(r, auditSuccess)
	if len(report.Errors) > 0 {
		recordAudit(r, auditFailure)
	}
	jsonResponse(w, report, 200)
}

// The caller holds deploymentMu, preventing publication/rollback and protecting
// complete in-flight site responses. SQLite rechecks alias references atomically.
func (s *Server) cleanupProject(ctx context.Context, projectID int64, keep, days int, apply bool) (cleanupReport, error) {
	report := cleanupReport{DryRun: !apply, Candidates: []cleanupCandidate{}, DeletedVersions: []int{}, Errors: []string{}}
	deployments, err := s.store.ListDeployments(projectID)
	if err != nil {
		return report, err
	}
	entries, err := s.artifacts.Entries()
	if err != nil {
		return report, err
	}
	sizes := map[string]int64{}
	for _, entry := range entries {
		sizes[entry.ID] += entry.Size
	}
	now := time.Now()
	rank := 0
	for _, d := range deployments {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		report.UsedBytes += sizes[d.ArtifactID]
		if d.ArtifactID == "" || d.ArtifactState == "deleted" {
			continue
		}
		pinned, err := s.store.DeploymentReferenced(ctx, d.ID)
		if err != nil {
			return report, err
		}
		if d.Available && (d.Status == "active" || d.Status == "archived" || d.Status == "preview") {
			rank++
		}
		if pinned {
			continue
		}
		if d.ArtifactState != "deleting" {
			if d.Available && (d.Status == "active" || d.Status == "archived" || d.Status == "preview") {
				// Count and age are independent protections; a version survives if
				// either applies. Production and named references always survive.
				if (keep > 0 && rank <= keep) || (days > 0 && now.Sub(d.CreatedAt) < time.Duration(days)*24*time.Hour) {
					continue
				}
			} else if now.Sub(d.CreatedAt) < s.deploymentOptions.OrphanGrace {
				continue
			}
		}
		report.Candidates = append(report.Candidates, cleanupCandidate{Version: d.Version, Bytes: sizes[d.ArtifactID]})
		report.ReclaimableBytes += sizes[d.ArtifactID]
		if !apply {
			continue
		}
		if err := s.store.MarkArtifactDeleting(ctx, d.ID); err != nil {
			if errors.Is(err, store.ErrDeploymentState) {
				continue
			}
			return report, err
		}
		// Tombstone first: a crash or removal error leaves a retryable record,
		// which cannot be rolled back or acquire new aliases while deleting.
		s.staticSites.forget(d.ArtifactID)
		err = errors.Join(s.artifacts.RemoveStage(d.ArtifactID), s.artifacts.Remove(d.ArtifactID))
		if err == nil {
			err = s.store.SetArtifactState(ctx, d.ID, "deleted")
		}
		if err != nil {
			log.Printf("cleanup deployment %d: %v", d.ID, err)
			report.Errors = append(report.Errors, fmt.Sprintf("version %d cleanup incomplete; retry required", d.Version))
			continue
		}
		report.DeletedVersions = append(report.DeletedVersions, d.Version)
	}
	return report, nil
}

// CleanupDeployments applies configured retention and reclaims aged orphan
// directories. It is called by the server's single-instance maintenance worker.
func (s *Server) CleanupDeployments(ctx context.Context) error {
	if err := s.PrepareDeployments(ctx); err != nil {
		return err
	}
	s.deploymentMu.Lock()
	defer s.deploymentMu.Unlock()
	deployments, err := s.store.ListAllDeployments(ctx)
	if err != nil {
		return err
	}
	projects := map[int64]bool{}
	var result error
	for _, d := range deployments {
		if projects[d.ProjectID] {
			continue
		}
		projects[d.ProjectID] = true
		report, err := s.cleanupProject(ctx, d.ProjectID, s.deploymentOptions.RetainCount, s.deploymentOptions.RetainDays, true)
		result = errors.Join(result, err)
		if len(report.Errors) > 0 {
			result = errors.Join(result, errors.New("some artifact removals require retry"))
		}
	}
	if ctx.Err() != nil {
		return errors.Join(result, ctx.Err())
	}
	deployments, err = s.store.ListAllDeployments(ctx)
	if err != nil {
		return errors.Join(result, err)
	}
	protected := map[string]bool{}
	for _, d := range deployments {
		// Failed/deleting records are handled through their tombstone above;
		// never bypass database transitions in the orphan sweep.
		if d.ArtifactID != "" && d.ArtifactState != "deleted" {
			protected[d.ArtifactID] = true
		}
	}
	entries, err := s.artifacts.Entries()
	if err != nil {
		return errors.Join(result, err)
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return errors.Join(result, ctx.Err())
		}
		if protected[entry.ID] || time.Since(entry.ModifiedAt) < s.deploymentOptions.OrphanGrace {
			continue
		}
		if entry.Staging {
			err = s.artifacts.RemoveStage(entry.ID)
		} else {
			s.staticSites.forget(entry.ID)
			err = s.artifacts.Remove(entry.ID)
		}
		result = errors.Join(result, err)
	}
	return result
}

func (s *Server) RunDeploymentCleanup(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.CleanupDeployments(ctx); err != nil {
				log.Printf("deployment maintenance: %v", err)
			}
		}
	}
}
