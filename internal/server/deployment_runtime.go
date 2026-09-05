package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/zhong/droply/internal/artifacts"
)

// DeploymentOptions must be configured before PrepareDeployments or serving.
// Zero limits use safe defaults. MaxStorageBytes=0 disables the managed quota.
type DeploymentOptions struct {
	MaxExpandedBytes int64
	MaxFiles         int
	MaxStorageBytes  int64
	RetainCount      int
	RetainDays       int
	OrphanGrace      time.Duration
}

func (s *Server) SetDeploymentOptions(options DeploymentOptions) error {
	if options.MaxExpandedBytes < 0 || options.MaxFiles < 0 || options.MaxStorageBytes < 0 || options.RetainCount < 0 || options.RetainDays < 0 || options.OrphanGrace < 0 {
		return errors.New("deployment limits and retention must not be negative")
	}
	if options.RetainDays > 100000 {
		return errors.New("deployment retention days must not exceed 100000")
	}
	s.deploymentOptions = options
	return nil
}

func (s *Server) artifactLimits() artifacts.Limits {
	return artifacts.Limits{MaxBytes: s.deploymentOptions.MaxExpandedBytes, MaxFiles: s.deploymentOptions.MaxFiles}
}

// PrepareDeployments runs before listeners start. SQLite is the production
// authority: an artifact is durable before its transaction makes it visible.
// Incomplete uploads are recorded as failed; orphan files can be reclaimed later.
func (s *Server) PrepareDeployments(ctx context.Context) error {
	s.prepareOnce.Do(func() { s.prepareError = s.prepareDeployments(ctx) })
	return s.prepareError
}

func (s *Server) prepareDeployments(ctx context.Context) error {
	var err error
	s.artifacts, err = artifacts.New(filepath.Join(s.sitesDir, ".artifacts"))
	if err != nil {
		return err
	}
	deployments, err := s.store.ListAllDeployments(ctx)
	if err != nil {
		return err
	}
	for _, d := range deployments {
		if d.Available {
			info, verifyErr := s.artifacts.Verify(ctx, d.ArtifactID)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if verifyErr != nil || info.Checksum != d.Checksum {
				if err := s.store.SetArtifactState(ctx, d.ID, "missing"); err != nil {
					return err
				}
			}
		}
		if d.Status == "uploading" {
			if err := s.store.FailDeployment(ctx, d.ID, "interrupted before publication; previous production retained"); err != nil {
				return err
			}
		}
		if d.ArtifactState == "deleting" {
			if err := errors.Join(s.artifacts.RemoveStage(d.ArtifactID), s.artifacts.Remove(d.ArtifactID)); err != nil {
				log.Printf("artifact cleanup will retry for deployment %d: %v", d.ID, err)
				continue
			}
			if err := s.store.SetArtifactState(ctx, d.ID, "deleted"); err != nil {
				return err
			}
		}
	}
	subs, err := s.store.ListAllSubdomains()
	if err != nil {
		return err
	}
	for _, sub := range subs {
		projects, err := s.store.ListProjects(sub.ID)
		if err != nil {
			return err
		}
		for _, project := range projects {
			d, err := s.store.GetActiveDeployment(ctx, project.ID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil && d.ArtifactState != "legacy" {
				continue
			}
			legacy := filepath.Join(s.sitesDir, sub.Name, project.Name)
			info, statErr := os.Stat(legacy)
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			if statErr != nil {
				return statErr
			}
			if !info.IsDir() {
				return fmt.Errorf("legacy project %s/%s is not a directory", sub.Name, project.Name)
			}
			id := rand.Text()
			manifest, err := s.artifacts.Import(ctx, id, legacy, s.artifactLimits())
			if err != nil {
				return fmt.Errorf("migrate %s/%s: %w", sub.Name, project.Name, err)
			}
			if err := s.artifacts.Publish(id); err != nil {
				return err
			}
			if d == nil {
				d, err = s.store.BeginDeployment(ctx, project.ID, id)
				if err == nil {
					err = s.store.CommitDeployment(ctx, d.ID, manifest.FileCount, manifest.TotalSize, manifest.Checksum)
				}
			} else {
				err = s.store.AttachLegacyArtifact(ctx, d.ID, id, manifest.FileCount, manifest.TotalSize, manifest.Checksum)
			}
			if err != nil {
				return fmt.Errorf("record legacy artifact: %w", err)
			}
			// Leave the original directory as a migration backup. It is never served
			// again and is not counted in automatic artifact retention.
		}
	}
	return nil
}

func (s *Server) failDeployment(id int64, reason string) {
	// A canceled upload must still leave a durable failure record.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.FailDeployment(ctx, id, reason); err != nil {
		log.Printf("record deployment %d failure: %v", id, err)
	}
}
