package server

import (
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/zhong/droply/internal/model"
)

// skippedExtensions are file extensions excluded from visit tracking.
var skippedExtensions = map[string]bool{
	".css": true, ".js": true, ".map": true,
	".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".svg": true, ".ico": true, ".webp": true,
	".mp4": true, ".webm": true, ".mp3": true,
}

// visitRecord is a single visit to record asynchronously.
type visitRecord struct {
	SubdomainID int64
	Project     string
	Path        string
	IP          string
	Referer     string
	UserAgent   string
}

// normalizePath canonicalizes a request path for consistent analytics.
func normalizePath(p string) string {
	p = strings.ToLower(p)
	// Strip trailing slash (but keep "/" as-is)
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	// Resolve index.html at root
	if p == "/index.html" {
		p = "/"
	}
	return p
}

// shouldTrack returns true if the path should be tracked for analytics.
func shouldTrack(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return !skippedExtensions[ext]
}

// StartAnalytics initializes the async visit processing goroutine.
func (s *Server) StartAnalytics() {
	s.analyticsStart.Do(func() { go s.processVisits() })
}

// ShutdownAnalytics drains the visit channel and waits for processing to complete.
func (s *Server) ShutdownAnalytics() {
	s.StartAnalytics()
	s.analyticsStop.Do(func() {
		s.visitsMu.Lock()
		s.visitsClosed = true
		close(s.visitCh)
		s.visitsMu.Unlock()
	})
	<-s.done
}

// processVisits consumes visit records from the channel and writes them to the store.
func (s *Server) processVisits() {
	for rec := range s.visitCh {
		if err := s.store.RecordVisit(rec.SubdomainID, rec.Project, rec.Path, rec.IP, rec.Referer, rec.UserAgent); err != nil {
			log.Printf("analytics: failed to record visit: %v", err)
		}
	}
	close(s.done)
}

// recordVisit enqueues a visit record for async processing.
// Uses non-blocking send — drops the record if the channel is full.
func (s *Server) recordVisit(subdomainID int64, project, path, ip, referer, userAgent string) {
	s.visitsMu.RLock()
	defer s.visitsMu.RUnlock()
	if s.visitsClosed {
		return
	}
	select {
	case s.visitCh <- visitRecord{
		SubdomainID: subdomainID,
		Project:     project,
		Path:        path,
		IP:          ip,
		Referer:     referer,
		UserAgent:   userAgent,
	}:
	default:
		// Channel full, drop the visit.
	}
}

type statsResponse struct {
	TotalPV int                   `json:"total_pv"`
	TotalUV int                   `json:"total_uv"`
	Pages   []model.PageDailyStat `json:"pages"`
}

type logsResponse struct {
	Logs  []model.VisitLog `json:"logs"`
	Total int              `json:"total"`
}

// handleGetStats returns aggregated page view statistics for a project.
func (s *Server) handleGetStats(w http.ResponseWriter, r *http.Request) {
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "project")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if !s.canAccessSubdomainProject(r, sub) {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	period := r.URL.Query().Get("period")
	if period == "" {
		period = "30d"
	}
	if period != "7d" && period != "30d" && period != "all" {
		jsonError(w, "invalid period, use 7d, 30d, or all", http.StatusBadRequest)
		return
	}

	pages, err := s.store.GetPageStats(sub.ID, projName, period)
	if err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if pages == nil {
		pages = []model.PageDailyStat{}
	}

	var totalPV, totalUV int
	for _, p := range pages {
		totalPV += p.PV
		totalUV += p.UV
	}

	jsonResponse(w, statsResponse{
		TotalPV: totalPV,
		TotalUV: totalUV,
		Pages:   pages,
	}, http.StatusOK)
}

// handleGetLogs returns detailed visit logs for a project.
func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "project")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if !s.canAccessSubdomainProject(r, sub) {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	pathFilter := r.URL.Query().Get("path")

	logs, total, err := s.store.GetVisitLogs(sub.ID, projName, limit, offset, pathFilter)
	if err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if logs == nil {
		logs = []model.VisitLog{}
	}

	jsonResponse(w, logsResponse{
		Logs:  logs,
		Total: total,
	}, http.StatusOK)
}
