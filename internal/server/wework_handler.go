package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/zhong/droply/internal/wework"
)

// weworkAuthHandler initiates the WeWork OAuth flow.
// It validates the host/redirect, generates a state token, and redirects to WeWork.
func (s *Server) weworkAuthHandler(w http.ResponseWriter, r *http.Request) {
	if s.wework == nil || s.weworkState == nil {
		http.Error(w, "WeWork login is not configured", http.StatusServiceUnavailable)
		return
	}

	redirect := r.URL.Query().Get("redirect")
	host := r.URL.Query().Get("host")
	if redirect == "" {
		redirect = "/"
	}
	if host == "" {
		host = r.Host
	}

	// Resolve host to find subdomain/project.
	subdomainName, customProject, ok := s.resolveHost(host)
	if !ok {
		http.NotFound(w, r)
		return
	}

	var projectName string
	isCustomDomain := customProject != ""
	if isCustomDomain {
		projectName = customProject
	} else {
		// Extract project from redirect path.
		path := strings.TrimPrefix(redirect, "/")
		parts := strings.SplitN(path, "/", 2)
		projectName = parts[0]
	}

	// Verify a rule with WeWork enabled exists.
	rule, err := s.store.FindAccessRuleForSite(subdomainName, projectName)
	if err != nil || rule == nil || !rule.WeWorkEnabled {
		http.NotFound(w, r)
		return
	}

	state, err := s.weworkState.Generate(wework.StateData{
		Subdomain: subdomainName,
		Project:   projectName,
		Host:      host,
		Redirect:  redirect,
		IsCustom:  isCustomDomain,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	authURL := s.wework.GetAuthorizeURL(state)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// weworkCallbackHandler handles the WeWork OAuth callback.
// It validates state, exchanges code for user_id, checks the allow-list, and sets a session cookie.
func (s *Server) weworkCallbackHandler(w http.ResponseWriter, r *http.Request) {
	if s.wework == nil || s.weworkState == nil {
		http.Error(w, "WeWork login is not configured", http.StatusServiceUnavailable)
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		http.Error(w, "missing code or state", http.StatusBadRequest)
		return
	}

	stateData, ok := s.weworkState.Consume(state)
	if !ok {
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	// Exchange code for user_id.
	userID, err := s.wework.GetUserIDByCode(code)
	if err != nil {
		http.Error(w, "WeWork login failed", http.StatusUnauthorized)
		return
	}

	// Re-resolve rule (state could be stale if rule was deleted).
	rule, err := s.store.FindAccessRuleForSite(stateData.Subdomain, stateData.Project)
	if err != nil || rule == nil || !rule.WeWorkEnabled {
		http.NotFound(w, r)
		return
	}

	// Check allow-list.
	if !isWeWorkUserAllowed(userID, rule.AllowedWeWorkUsers) {
		http.Error(w, "Access denied: user not in allow-list", http.StatusForbidden)
		return
	}

	// Set session cookie.
	ttl := rule.SessionTTL
	if ttl == 0 {
		ttl = 86400
	}
	expiry := time.Now().Add(time.Duration(ttl) * time.Second)

	cookieSubdomain := stateData.Subdomain
	cookieProject := stateData.Project
	if !stateData.IsCustom && rule.ProjectID == nil {
		// Subdomain-level rule on a subdomain host: scope cookie to subdomain.
		cookieProject = ""
	}

	hashMaterial := weWorkHashMaterial(rule.AllowedWeWorkUsers, userID)
	cookieValue := s.signCookie(cookieSubdomain, cookieProject, cookieAuthWeWork, userID, expiry, hashMaterial)
	http.SetCookie(w, &http.Cookie{
		Name:     "_droply_access",
		Value:    cookieValue,
		Path:     "/",
		Expires:  expiry,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, stateData.Redirect, http.StatusFound)
}
