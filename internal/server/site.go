package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/zhong/droply/internal/model"
	"github.com/zhong/droply/internal/staticweb"
)

// loginPageTemplate is the HTML template for the access login page (password and/or WeWork QR).
var loginPageTemplate = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Login Required</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, sans-serif; display: flex; justify-content: center; align-items: center; min-height: 100vh; margin: 0; background: #f5f5f5; }
  .container { background: #fff; padding: 2rem; border-radius: 8px; box-shadow: 0 2px 8px rgba(0,0,0,0.1); width: 100%; max-width: 360px; }
  h1 { font-size: 1.25rem; margin: 0 0 1.5rem; text-align: center; }
  input[type="password"] { width: 100%; padding: 0.5rem; margin-bottom: 1rem; border: 1px solid #ccc; border-radius: 4px; box-sizing: border-box; }
  button { width: 100%; padding: 0.5rem; background: #333; color: #fff; border: none; border-radius: 4px; cursor: pointer; }
  button:hover { background: #555; }
  .error { color: #c00; font-size: 0.875rem; margin-bottom: 1rem; text-align: center; }
  .divider { display: flex; align-items: center; margin: 1rem 0; color: #999; font-size: 0.75rem; }
  .divider::before, .divider::after { content: ""; flex: 1; border-bottom: 1px solid #ddd; }
  .divider span { padding: 0 0.5rem; }
  .wework-btn { display: block; text-align: center; padding: 0.5rem; background: #07c160; color: #fff; text-decoration: none; border-radius: 4px; }
  .wework-btn:hover { background: #05a050; }
</style>
</head>
<body>
<div class="container">
  <h1>Login Required</h1>
  {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
  {{if .ShowPassword}}
  <form method="POST" action="/_droply/login">
    <input type="password" name="password" placeholder="Enter password" autofocus required>
    <input type="hidden" name="redirect" value="{{.Redirect}}">
    <input type="hidden" name="host" value="{{.Host}}">
    <button type="submit">Login</button>
  </form>
  {{end}}
  {{if and .ShowPassword .ShowWeWork}}<div class="divider"><span>OR</span></div>{{end}}
  {{if .ShowWeWork}}
  <a class="wework-btn" href="/_droply/wework/auth?redirect={{.Redirect | urlquery}}&host={{.Host | urlquery}}">Login with WeCom</a>
  {{end}}
</div>
</body>
</html>`))

// NewSiteHandler returns an http.Handler that serves site content with access control.
// Server selects this handler for site hosts on the unified listener.
func (s *Server) NewSiteHandler() http.Handler {
	rl := &ipLimiter{capacity: 4096, rejectWhenFull: true, idleTTL: 10 * time.Minute}
	return s.withTrustedProxy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.PrepareDeployments(r.Context()); err != nil {
			http.Error(w, "deployment storage unavailable", 503)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/_droply/") {
			if target := s.requestSiteTarget(r); target != nil && target.Kind != "production" {
				w.Header().Set("X-Robots-Tag", "noindex, nofollow")
			}
			w.Header().Set("Cache-Control", "private, no-store")
		}
		// OAuth callback performs remote I/O and never selects an artifact.
		// Its handler revalidates the destination after the network exchange.
		if r.Method == http.MethodGet && r.URL.Path == "/_droply/wework/callback" {
			s.weworkCallbackHandler(w, r)
			return
		}
		// Protect the selected artifact from publication cleanup/GC until the
		// response is complete. Moving only OAuth out preserves reader safety.
		s.deploymentMu.RLock()
		defer s.deploymentMu.RUnlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/_droply/login":
			s.siteLoginHandler(w, r, rl)
			return
		case r.Method == http.MethodGet && r.URL.Path == "/_droply/wework/auth":
			s.weworkAuthHandler(w, r)
			return
		}
		s.siteHandler(w, r)
	}))
}

// siteHandler serves site content, enforcing access rules.
func (s *Server) siteHandler(w http.ResponseWriter, r *http.Request) {
	host := r.Host

	identity, ok := s.resolveHost(r.Context(), host)
	subdomainName, customProject := identity.subdomain, identity.project
	if !ok {
		http.NotFound(w, r)
		return
	}

	preview := identity.target != nil && identity.target.Kind != "production"
	if preview {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	}

	// Verify the subdomain still exists in the database.
	sub, err := s.store.GetSubdomainByName(subdomainName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var projectName string
	var servePath string
	var isCustomDomain bool
	var redirectSlash bool

	if customProject != "" {
		// Custom domain: project is determined by the domain mapping.
		isCustomDomain = true
		projectName = customProject
		servePath = r.URL.Path
	} else {
		// Subdomain host: extract project from first path segment.
		path := strings.TrimPrefix(r.URL.Path, "/")
		parts := strings.SplitN(path, "/", 2)
		projectName = parts[0]
		if projectName == "" {
			http.NotFound(w, r)
			return
		}
		if len(parts) > 1 {
			servePath = "/" + parts[1]
		} else {
			// Request is /project without trailing slash — redirect to /project/
			// so relative URLs (e.g. href="style.css") resolve correctly.
			redirectSlash = true
		}
	}

	project, err := s.store.GetProject(sub.ID, projectName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Check access rule.
	rule, err := s.store.FindAccessRuleForSite(r.Context(), subdomainName, projectName)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resolved := siteRequest{
		subdomain: sub,
		project:   project,
		target:    identity.target,
		path:      servePath,
		private:   rule != nil || strings.HasPrefix(r.URL.Path, "/_droply/"),
		preview:   preview,
	}
	if !isCustomDomain {
		resolved.prefix = "/" + projectName
	}
	if rule != nil {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Add("Vary", "Cookie")
	}
	if redirectSlash {
		target := r.URL.Path + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}
	if rule != nil {
		// Check IP whitelist first.
		if len(rule.AllowedIPs) > 0 {
			clientIP := getClientIP(r)
			if isIPAllowed(clientIP, rule.AllowedIPs) {
				// IP is whitelisted, serve directly.
				s.serveFile(w, r, resolved)
				return
			}
			// If no other auth method, block.
			if !rule.HasPassword && !rule.WeWorkEnabled {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		}

		// Check cookie (works for both password and WeWork sessions).
		if rule.HasPassword || rule.WeWorkEnabled {
			if s.isValidAccessCookie(r, subdomainName, projectName, isCustomDomain, rule) {
				s.serveFile(w, r, resolved)
				return
			}
			// Auto-redirect to WeCom OAuth when the rule has only WeCom enabled
			// (no password choice to make), the server is configured for WeCom,
			// and the request is not bouncing back from a failed OAuth attempt.
			// This skips the "Login with WeCom" button click in the common case.
			if !rule.HasPassword && rule.WeWorkEnabled && s.wework != nil && !weworkRecentlyFailed(r) {
				authURL := "/_droply/wework/auth?redirect=" +
					url.QueryEscape(r.URL.RequestURI()) +
					"&host=" + url.QueryEscape(r.Host)
				http.Redirect(w, r, authURL, http.StatusFound)
				return
			}
			// Show login page (covers password-only, password+WeCom, or a recent
			// OAuth failure where we want the user to see what went wrong).
			s.renderLoginPage(w, r, rule, "")
			return
		}

		// Has IP whitelist only and IP was not allowed.
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// No rule — serve directly.
	s.serveFile(w, r, resolved)
}

// serveFile selects the current deployment after visitor authorization. The caller
// holds deploymentMu for the entire response, protecting its artifact from GC.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, resolved siteRequest) {
	var err error
	if resolved.target != nil && resolved.target.DeploymentID != 0 {
		resolved.deployment, err = s.store.GetDeploymentByID(r.Context(), resolved.target.DeploymentID)
	} else {
		resolved.deployment, err = s.store.GetActiveDeployment(r.Context(), resolved.project.ID)
	}
	if err != nil || !resolved.deployment.Available {
		http.NotFound(w, r)
		return
	}
	root := s.artifacts.Path(resolved.deployment.ArtifactID)
	if root == "" {
		http.Error(w, "artifact unavailable", 503)
		return
	}
	site, err := staticweb.Load(root)
	if err != nil {
		http.Error(w, "invalid static site configuration", 503)
		return
	}
	site.ServeHTTP(w, r, staticweb.Options{Path: resolved.path, Prefix: resolved.prefix,
		Private: resolved.private, Preview: resolved.preview, ETagSeed: resolved.deployment.Checksum})
	if shouldTrack(resolved.path) {
		s.recordVisit(resolved.subdomain.ID, resolved.project.Name, normalizePath(resolved.path), getClientIP(r), r.Referer(), r.UserAgent())
	}
}

// renderLoginPage renders the login page template, showing password and/or WeWork buttons based on rule.
func (s *Server) renderLoginPage(w http.ResponseWriter, r *http.Request, rule *model.AccessRule, errorMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := map[string]any{
		"Error":        errorMsg,
		"Redirect":     r.URL.RequestURI(),
		"Host":         r.Host,
		"ShowPassword": rule.HasPassword,
		"ShowWeWork":   rule.WeWorkEnabled && s.wework != nil,
	}
	loginPageTemplate.Execute(w, data)
}

// siteLoginHandler handles POST /_droply/login.
func (s *Server) siteLoginHandler(w http.ResponseWriter, r *http.Request, rl *ipLimiter) {
	clientIP := getClientIP(r)
	if !rl.allow(clientIP) {
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	password := r.FormValue("password")
	redirect := r.FormValue("redirect")
	host := r.FormValue("host")

	if redirect == "" {
		redirect = "/"
	}

	if !validRedirectPath(redirect) || !sameHost(host, r.Host) {
		jsonError(w, "invalid login destination", http.StatusBadRequest)
		return
	}

	// Resolve the host to find subdomain/project.
	identity, ok := s.resolveHost(r.Context(), host)
	subdomainName, customProject := identity.subdomain, identity.project
	if !ok {
		http.NotFound(w, r)
		return
	}

	var projectName string
	var isCustomDomain bool

	if customProject != "" {
		isCustomDomain = true
		projectName = customProject
	} else {
		// Extract project from the redirect path.
		path := strings.TrimPrefix(redirect, "/")
		parts := strings.SplitN(path, "/", 2)
		projectName = parts[0]
	}

	// Find access rule.
	rule, err := s.store.FindAccessRuleForSite(r.Context(), subdomainName, projectName)
	if err != nil || rule == nil || !rule.HasPassword {
		http.NotFound(w, r)
		return
	}

	// Compare password.
	if err := bcrypt.CompareHashAndPassword([]byte(rule.PasswordHash), []byte(password)); err != nil {
		// Wrong password — re-render login page.
		// Build a fake request with the redirect path for the login page rendering.
		r.URL.Path = redirect
		r.Host = host
		s.renderLoginPage(w, r, rule, "Incorrect password")
		return
	}

	// Password correct — set cookie and redirect.
	ttl := rule.SessionTTL
	if ttl == 0 {
		ttl = 86400
	}
	expiry := time.Now().Add(time.Duration(ttl) * time.Second)

	cookieSubdomain := subdomainName
	cookieProject := projectName
	if !isCustomDomain {
		// For subdomain-level rules (rule.ProjectID == nil), scope cookie to subdomain.
		if rule.ProjectID == nil {
			cookieProject = ""
		}
	}

	cookieValue := s.signCookie(cookieSubdomain, cookieProject, cookieAuthPwd, "", expiry, rule.PasswordHash)
	http.SetCookie(w, &http.Cookie{
		Name:     "_droply_access",
		Value:    cookieValue,
		Path:     "/",
		Expires:  expiry,
		Secure:   requestSecure(r),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, redirect, http.StatusFound)
}

// getClientIP extracts the client IP from the request.
// Priority: X-Real-IP > X-Forwarded-For > RemoteAddr.
func getClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// isIPAllowed checks if the clientIP is in the allowed list (supports CIDR and exact match).
func isIPAllowed(clientIP string, allowedIPs []string) bool {
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}
	for _, allowed := range allowedIPs {
		allowed = strings.TrimSpace(allowed)
		if strings.Contains(allowed, "/") {
			_, network, err := net.ParseCIDR(allowed)
			if err != nil {
				continue
			}
			if network.Contains(ip) {
				return true
			}
		} else {
			if net.ParseIP(allowed) != nil && net.ParseIP(allowed).Equal(ip) {
				return true
			}
		}
	}
	return false
}

// Cookie format:
//   New (v2): "v2:{sub}:{proj}:{authMethod}:{userid}:{expiry}:{sig}"
//   Legacy:   "{sub}:{proj}:{expiry}:{sig}"  (password-only, kept for backward compat)
//
// authMethod is "pwd" or "wework". userid is empty for "pwd".
// The HMAC payload includes a "hash material" derived from the rule:
//   pwd:    rule.PasswordHash
//   wework: sha256(JSON(allowedWeWorkUsers) || userid)
// so that changing the password or the WeWork allow-list invalidates outstanding sessions.

const (
	cookieAuthPwd    = "pwd"
	cookieAuthWeWork = "wework"
)

// signCookie creates a signed cookie value in the v2 format.
func (s *Server) signCookie(subdomain, project, authMethod, userID string, expiry time.Time, hashMaterial string) string {
	expiryStr := strconv.FormatInt(expiry.Unix(), 10)
	payload := fmt.Sprintf("v2:%s:%s:%s:%s:%s:%s", subdomain, project, authMethod, userID, expiryStr, hashMaterial)

	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))

	return fmt.Sprintf("v2:%s:%s:%s:%s:%s:%s", subdomain, project, authMethod, userID, expiryStr, sig)
}

// weWorkHashMaterial derives a stable hash material for WeWork cookie HMAC.
// Bound to (allowedWeWorkUsers, userID) so that allow-list changes invalidate cookies
// of users that are no longer authorized.
func weWorkHashMaterial(allowedUsers []string, userID string) string {
	// Use JSON for deterministic ordering; allowedUsers is stored as-given.
	listJSON, _ := json.Marshal(allowedUsers)
	h := sha256.New()
	h.Write(listJSON)
	h.Write([]byte("|"))
	h.Write([]byte(userID))
	return hex.EncodeToString(h.Sum(nil))
}

// isValidAccessCookie checks the _droply_access cookie for validity against the given rule.
// Supports both v2 (password + wework) and legacy (password-only) formats.
func (s *Server) isValidAccessCookie(r *http.Request, subdomain, project string, isCustomDomain bool, rule *model.AccessRule) bool {
	cookie, err := r.Cookie("_droply_access")
	if err != nil {
		return false
	}

	if strings.HasPrefix(cookie.Value, "v2:") {
		return s.validateV2Cookie(cookie.Value, subdomain, project, rule)
	}
	return s.validateLegacyCookie(cookie.Value, subdomain, project, rule)
}

func (s *Server) validateV2Cookie(value, subdomain, project string, rule *model.AccessRule) bool {
	// v2:{sub}:{proj}:{authMethod}:{userid}:{expiry}:{sig}
	parts := strings.SplitN(value, ":", 7)
	if len(parts) != 7 || parts[0] != "v2" {
		return false
	}
	cookieSub := parts[1]
	cookieProj := parts[2]
	authMethod := parts[3]
	cookieUserID := parts[4]
	expiryStr := parts[5]
	sig := parts[6]

	expiryUnix, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil || time.Now().Unix() > expiryUnix {
		return false
	}
	if cookieSub != subdomain {
		return false
	}

	if cookieProj == "" && rule.ProjectID != nil {
		return false
	}
	scopeRule := rule

	var hashMaterial string
	switch authMethod {
	case cookieAuthPwd:
		if !scopeRule.HasPassword {
			return false
		}
		hashMaterial = scopeRule.PasswordHash
	case cookieAuthWeWork:
		if !scopeRule.WeWorkEnabled {
			return false
		}
		// Re-check user is still allowed (list may have changed → hash material changes → sig fails anyway,
		// but explicit check shortcircuits and gives clearer behavior).
		if !isWeWorkUserAllowed(cookieUserID, scopeRule.AllowedWeWorkUsers) {
			return false
		}
		hashMaterial = weWorkHashMaterial(scopeRule.AllowedWeWorkUsers, cookieUserID)
	default:
		return false
	}

	payload := fmt.Sprintf("v2:%s:%s:%s:%s:%s:%s", cookieSub, cookieProj, authMethod, cookieUserID, expiryStr, hashMaterial)
	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return false
	}

	if cookieProj == "" {
		return true
	}
	return cookieProj == project
}

func (s *Server) validateLegacyCookie(value, subdomain, project string, rule *model.AccessRule) bool {
	parts := strings.SplitN(value, ":", 4)
	if len(parts) != 4 {
		return false
	}
	cookieSub := parts[0]
	cookieProj := parts[1]
	expiryStr := parts[2]
	sig := parts[3]

	expiryUnix, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil || time.Now().Unix() > expiryUnix {
		return false
	}
	if cookieSub != subdomain {
		return false
	}

	if !rule.HasPassword || (cookieProj == "" && rule.ProjectID != nil) {
		return false
	}
	hashForVerify := rule.PasswordHash

	payload := fmt.Sprintf("%s:%s:%s:%s", cookieSub, cookieProj, expiryStr, hashForVerify)
	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return false
	}

	if cookieProj == "" {
		return true
	}
	return cookieProj == project
}

// isWeWorkUserAllowed returns true if the userID is in allowedUsers, or if allowedUsers is empty (any user).
func isWeWorkUserAllowed(userID string, allowedUsers []string) bool {
	if len(allowedUsers) == 0 {
		return true
	}
	return slices.Contains(allowedUsers, userID)
}

func (s *Server) requestSiteTarget(r *http.Request) *model.SiteTarget {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	suffix := "." + s.baseDomain
	if !strings.HasSuffix(host, suffix) {
		return nil
	}
	label := strings.TrimSuffix(host, suffix)
	target, _ := s.store.GetSiteTarget(r.Context(), label)
	return target
}
