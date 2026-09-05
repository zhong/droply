package server

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/zhong/droply/internal/model"
)

const maxUploadSize = 50 << 20 // 50MB

type deployResponse struct {
	DeploymentID int64  `json:"deployment_id"`
	Version      int    `json:"version"`
	FileCount    int    `json:"file_count"`
	TotalSize    int64  `json:"total_size"`
	URL          string `json:"url"`
}

// handleDeploy validates the project name, verifies subdomain ownership,
// extracts the uploaded tar.gz, creates (or reuses) the project record,
// creates a deployment record, activates it, and returns deployment details.
func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "project")

	if !validName(projName) {
		jsonError(w, "invalid project name", http.StatusBadRequest)
		return
	}

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if sub.UserID != user.ID {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	file, _, err := r.FormFile("file")
	if err != nil {
		jsonError(w, "file upload required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Auto-create project if it doesn't exist.
	proj, err := s.store.GetProject(sub.ID, projName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), "no rows") {
			proj, err = s.store.CreateProject(sub.ID, projName)
			if err != nil {
				jsonError(w, "internal server error", http.StatusInternalServerError)
				return
			}
		} else {
			jsonError(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	destDir := filepath.Join(s.sitesDir, subName, projName)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	fileCount, totalSize, err := extractTarGz(file, destDir)
	if err != nil {
		jsonError(w, fmt.Sprintf("extract error: %v", err), http.StatusBadRequest)
		return
	}

	deployment, err := s.store.CreateDeployment(proj.ID, fileCount, totalSize)
	if err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := s.store.ActivateDeployment(deployment.ID); err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	url := fmt.Sprintf("https://%s.%s/%s", subName, s.baseDomain, projName)

	jsonResponse(w, deployResponse{
		DeploymentID: deployment.ID,
		Version:      deployment.Version,
		FileCount:    fileCount,
		TotalSize:    totalSize,
		URL:          url,
	}, http.StatusOK)
}

// extractTarGz decompresses and extracts a .tar.gz archive from r into destDir.
// It skips any entries with paths starting with ".." or "/" for security.
// Returns the number of regular files extracted and the total bytes written.
func extractTarGz(r io.Reader, destDir string) (fileCount int, totalSize int64, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, 0, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fileCount, totalSize, fmt.Errorf("tar: %w", err)
		}

		// Security: skip paths that could escape the destination directory.
		if strings.HasPrefix(hdr.Name, "..") || strings.HasPrefix(hdr.Name, "/") {
			continue
		}
		// Also check each component.
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") {
			continue
		}

		target := filepath.Join(destDir, clean)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return fileCount, totalSize, fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			// Ensure parent directory exists.
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fileCount, totalSize, fmt.Errorf("mkdir parent %s: %w", target, err)
			}
			f, err := os.Create(target)
			if err != nil {
				return fileCount, totalSize, fmt.Errorf("create %s: %w", target, err)
			}
			n, copyErr := io.Copy(f, tr)
			f.Close()
			if copyErr != nil {
				return fileCount, totalSize, fmt.Errorf("write %s: %w", target, copyErr)
			}
			fileCount++
			totalSize += n
		}
	}
	return fileCount, totalSize, nil
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
