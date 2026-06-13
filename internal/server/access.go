package server

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

const alphanumChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// generatePassword returns a 16-character random alphanumeric string.
func generatePassword() string {
	b := make([]byte, 16)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphanumChars))))
		b[i] = alphanumChars[n.Int64()]
	}
	return string(b)
}

type setAccessRequest struct {
	AllowedIPs         []string `json:"allowed_ips,omitempty"`
	Password           string   `json:"password,omitempty"`
	AutoPassword       bool     `json:"auto_password,omitempty"`
	SessionTTL         int      `json:"session_ttl,omitempty"`
	WeWorkEnabled      bool     `json:"wework_enabled,omitempty"`
	AllowedWeWorkUsers []string `json:"allowed_wework_users,omitempty"`
}

type setAccessResponse struct {
	ID                 int64    `json:"id"`
	AllowedIPs         []string `json:"allowed_ips,omitempty"`
	HasPassword        bool     `json:"has_password"`
	WeWorkEnabled      bool     `json:"wework_enabled"`
	AllowedWeWorkUsers []string `json:"allowed_wework_users,omitempty"`
	SessionTTL         int      `json:"session_ttl"`
	GeneratedPassword  string   `json:"generated_password,omitempty"`
}

type getAccessResponse struct {
	ID                 int64    `json:"id"`
	AllowedIPs         []string `json:"allowed_ips,omitempty"`
	HasPassword        bool     `json:"has_password"`
	WeWorkEnabled      bool     `json:"wework_enabled"`
	AllowedWeWorkUsers []string `json:"allowed_wework_users,omitempty"`
	SessionTTL         int      `json:"session_ttl"`
}

// handleSetAccess sets access rules at the subdomain level.
func (s *Server) handleSetAccess(w http.ResponseWriter, r *http.Request) {
	s.setAccess(w, r, false)
}

// handleSetProjectAccess sets access rules at the project level.
func (s *Server) handleSetProjectAccess(w http.ResponseWriter, r *http.Request) {
	s.setAccess(w, r, true)
}

// handleGetAccess retrieves access rules at the subdomain level.
func (s *Server) handleGetAccess(w http.ResponseWriter, r *http.Request) {
	s.getAccess(w, r, false)
}

// handleGetProjectAccess retrieves access rules at the project level.
func (s *Server) handleGetProjectAccess(w http.ResponseWriter, r *http.Request) {
	s.getAccess(w, r, true)
}

// handleDeleteAccess removes access rules at the subdomain level.
func (s *Server) handleDeleteAccess(w http.ResponseWriter, r *http.Request) {
	s.deleteAccess(w, r, false)
}

// handleDeleteProjectAccess removes access rules at the project level.
func (s *Server) handleDeleteProjectAccess(w http.ResponseWriter, r *http.Request) {
	s.deleteAccess(w, r, true)
}

func (s *Server) setAccess(w http.ResponseWriter, r *http.Request, isProject bool) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if sub.UserID != user.ID {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	var projectID *int64
	if isProject {
		projName := chi.URLParam(r, "project")
		proj, err := s.store.GetProject(sub.ID, projName)
		if err != nil {
			jsonError(w, "project not found", http.StatusNotFound)
			return
		}
		projectID = &proj.ID
	}

	var req setAccessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validate: password and auto_password are mutually exclusive.
	if req.Password != "" && req.AutoPassword {
		jsonError(w, "password and auto_password are mutually exclusive", http.StatusBadRequest)
		return
	}

	// At least one of allowed_ips or password/auto_password or wework must be provided.
	if len(req.AllowedIPs) == 0 && req.Password == "" && !req.AutoPassword && !req.WeWorkEnabled {
		jsonError(w, "at least one of allowed_ips, password/auto_password, or wework_enabled is required", http.StatusBadRequest)
		return
	}

	// Validate password length.
	if req.Password != "" && len(req.Password) < 8 {
		jsonError(w, "password must be at least 8 characters", http.StatusBadRequest)
		return
	}

	// Validate CIDR/IP format.
	for _, ip := range req.AllowedIPs {
		ip = strings.TrimSpace(ip)
		if strings.Contains(ip, "/") {
			if _, _, err := net.ParseCIDR(ip); err != nil {
				jsonError(w, "invalid CIDR: "+ip, http.StatusBadRequest)
				return
			}
		} else {
			if net.ParseIP(ip) == nil {
				jsonError(w, "invalid IP: "+ip, http.StatusBadRequest)
				return
			}
		}
	}

	// TTL: default 86400, range 300-315360000.
	ttl := req.SessionTTL
	if ttl == 0 {
		ttl = 86400
	}
	if ttl < 300 || ttl > 315360000 {
		jsonError(w, "session_ttl must be between 300 and 315360000", http.StatusBadRequest)
		return
	}

	// Handle password: generate or hash.
	var passwordHash string
	var generatedPassword string
	if req.AutoPassword {
		generatedPassword = generatePassword()
		hash, err := bcrypt.GenerateFromPassword([]byte(generatedPassword), bcrypt.DefaultCost)
		if err != nil {
			jsonError(w, "internal server error", http.StatusInternalServerError)
			return
		}
		passwordHash = string(hash)
	} else if req.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			jsonError(w, "internal server error", http.StatusInternalServerError)
			return
		}
		passwordHash = string(hash)
	}

	// Validate: WeWork allow-list requires WeWork enabled.
	if len(req.AllowedWeWorkUsers) > 0 && !req.WeWorkEnabled {
		jsonError(w, "allowed_wework_users requires wework_enabled=true", http.StatusBadRequest)
		return
	}

	rule, err := s.store.CreateOrUpdateAccessRule(sub.ID, projectID, req.AllowedIPs, passwordHash, ttl, req.WeWorkEnabled, req.AllowedWeWorkUsers)
	if err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Update custom domain Caddy routes to protected.
	if s.caddy != nil {
		s.updateCustomDomainRoutes(sub.ID, subName, true)
	}

	resp := setAccessResponse{
		ID:                 rule.ID,
		AllowedIPs:         rule.AllowedIPs,
		HasPassword:        rule.HasPassword,
		WeWorkEnabled:      rule.WeWorkEnabled,
		AllowedWeWorkUsers: rule.AllowedWeWorkUsers,
		SessionTTL:         rule.SessionTTL,
		GeneratedPassword:  generatedPassword,
	}
	jsonResponse(w, resp, http.StatusOK)
}

func (s *Server) getAccess(w http.ResponseWriter, r *http.Request, isProject bool) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if sub.UserID != user.ID {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	var projectID *int64
	if isProject {
		projName := chi.URLParam(r, "project")
		proj, err := s.store.GetProject(sub.ID, projName)
		if err != nil {
			jsonError(w, "project not found", http.StatusNotFound)
			return
		}
		projectID = &proj.ID
	}

	rule, err := s.store.GetAccessRule(sub.ID, projectID)
	if err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if rule == nil {
		jsonError(w, "access rule not found", http.StatusNotFound)
		return
	}

	resp := getAccessResponse{
		ID:                 rule.ID,
		AllowedIPs:         rule.AllowedIPs,
		HasPassword:        rule.HasPassword,
		WeWorkEnabled:      rule.WeWorkEnabled,
		AllowedWeWorkUsers: rule.AllowedWeWorkUsers,
		SessionTTL:         rule.SessionTTL,
	}
	jsonResponse(w, resp, http.StatusOK)
}

func (s *Server) deleteAccess(w http.ResponseWriter, r *http.Request, isProject bool) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if sub.UserID != user.ID {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	var projectID *int64
	if isProject {
		projName := chi.URLParam(r, "project")
		proj, err := s.store.GetProject(sub.ID, projName)
		if err != nil {
			jsonError(w, "project not found", http.StatusNotFound)
			return
		}
		projectID = &proj.ID
	}

	if err := s.store.DeleteAccessRule(sub.ID, projectID); err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Check if subdomain still has any access rules; if not, switch custom domains back to unprotected.
	if s.caddy != nil {
		hasRules, err := s.store.HasAccessRules(sub.ID)
		if err == nil && !hasRules {
			s.updateCustomDomainRoutes(sub.ID, subName, false)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// updateCustomDomainRoutes iterates all verified custom domains under the subdomain
// and switches their Caddy routes to protected or unprotected.
func (s *Server) updateCustomDomainRoutes(subdomainID int64, subdomainName string, protect bool) {
	projects, err := s.store.ListProjects(subdomainID)
	if err != nil {
		return
	}
	for _, proj := range projects {
		domains, err := s.store.ListCustomDomains(proj.ID)
		if err != nil {
			continue
		}
		for _, d := range domains {
			if !d.Verified {
				continue
			}
			if protect {
				_ = s.caddy.SetCustomDomainProtected(d.Domain, s.siteAddr)
			} else {
				_ = s.caddy.SetCustomDomainUnprotected(d.Domain, subdomainName, proj.Name)
			}
		}
	}
}
