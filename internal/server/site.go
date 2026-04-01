package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
)

// loginPageTemplate is the HTML template for the password login page.
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
</style>
</head>
<body>
<div class="container">
  <h1>Password Required</h1>
  {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
  <form method="POST" action="/_droply/login">
    <input type="password" name="password" placeholder="Enter password" autofocus required>
    <input type="hidden" name="redirect" value="{{.Redirect}}">
    <input type="hidden" name="host" value="{{.Host}}">
    <button type="submit">Login</button>
  </form>
</div>
</body>
</html>`))

// rateLimiter tracks per-IP rate limiters.
type rateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rateLimiterEntry
}

type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newRateLimiter() *rateLimiter {
	rl := &rateLimiter{
		limiters: make(map[string]*rateLimiterEntry),
	}
	go rl.cleanup()
	return rl
}

func (rl *rateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, ok := rl.limiters[ip]
	if !ok {
		// 10 requests per minute = 1 every 6 seconds, burst of 10.
		limiter := rate.NewLimiter(rate.Every(6*time.Second), 10)
		rl.limiters[ip] = &rateLimiterEntry{limiter: limiter, lastSeen: time.Now()}
		return limiter
	}
	entry.lastSeen = time.Now()
	return entry.limiter
}

func (rl *rateLimiter) cleanup() {
	for {
		time.Sleep(5 * time.Minute)
		rl.mu.Lock()
		for ip, entry := range rl.limiters {
			if time.Since(entry.lastSeen) > 10*time.Minute {
				delete(rl.limiters, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// NewSiteHandler returns an http.Handler that serves site content with access control.
// It runs on a separate port from the API router.
func (s *Server) NewSiteHandler() http.Handler {
	rl := newRateLimiter()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/_droply/login" {
			s.siteLoginHandler(w, r, rl)
			return
		}
		s.siteHandler(w, r)
	})
}

// resolveHost resolves the Host header to a subdomain name and optional project name.
// For subdomain hosts like alice.droplydoc.com, it returns (alice, "", true).
// For custom domains, it looks up the domain in the store and returns (subdomain, project, true).
// For unknown hosts, it returns ("", "", false).
func (s *Server) resolveHost(host string) (subdomainName, projectName string, ok bool) {
	// Strip port if present.
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	// Check if it's a subdomain of baseDomain.
	suffix := "." + s.baseDomain
	if strings.HasSuffix(host, suffix) {
		sub := strings.TrimSuffix(host, suffix)
		if sub != "" && !strings.Contains(sub, ".") {
			return sub, "", true
		}
	}

	// Check custom domains.
	domains, err := s.store.ListAllVerifiedDomainsWithPaths()
	if err != nil {
		return "", "", false
	}
	for _, d := range domains {
		if strings.EqualFold(d.Domain, host) {
			return d.SubdomainName, d.ProjectName, true
		}
	}

	return "", "", false
}

// siteHandler serves site content, enforcing access rules.
func (s *Server) siteHandler(w http.ResponseWriter, r *http.Request) {
	host := r.Host

	subdomainName, customProject, ok := s.resolveHost(host)
	if !ok {
		http.NotFound(w, r)
		return
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
			http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
			return
		}
	}

	// Check access rule.
	rule, err := s.store.FindAccessRuleForSite(subdomainName, projectName)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if rule != nil {
		// Check IP whitelist first.
		if len(rule.AllowedIPs) > 0 {
			clientIP := getClientIP(r)
			if isIPAllowed(clientIP, rule.AllowedIPs) {
				// IP is whitelisted, serve directly.
				s.serveFile(w, r, sub.ID, subdomainName, projectName, servePath)
				return
			}
			// If there's no password, just block.
			if !rule.HasPassword {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		}

		// Check cookie.
		if rule.HasPassword {
			if s.isValidAccessCookie(r, subdomainName, projectName, isCustomDomain, rule.PasswordHash) {
				s.serveFile(w, r, sub.ID, subdomainName, projectName, servePath)
				return
			}
			// Show login page.
			s.renderLoginPage(w, r, "")
			return
		}

		// Has IP whitelist only and IP was not allowed.
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// No rule — serve directly.
	s.serveFile(w, r, sub.ID, subdomainName, projectName, servePath)
}

// serveFile serves a static file from the sites directory.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, subdomainID int64, subdomain, project, servePath string) {
	root := filepath.Join(s.sitesDir, subdomain, project)
	// Use http.Dir + http.FileServer for proper file serving.
	fs := http.FileServer(http.Dir(root))
	// Rewrite the request path to servePath.
	r.URL.Path = servePath
	fs.ServeHTTP(w, r)

	// Record visit asynchronously after serving the file.
	if shouldTrack(servePath) {
		normalizedPath := normalizePath(servePath)
		s.recordVisit(subdomainID, project, normalizedPath, getClientIP(r), r.Referer(), r.UserAgent())
	}
}

// renderLoginPage renders the login page template.
func (s *Server) renderLoginPage(w http.ResponseWriter, r *http.Request, errorMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := map[string]string{
		"Error":    errorMsg,
		"Redirect": r.URL.RequestURI(),
		"Host":     r.Host,
	}
	loginPageTemplate.Execute(w, data)
}

// siteLoginHandler handles POST /_droply/login.
func (s *Server) siteLoginHandler(w http.ResponseWriter, r *http.Request, rl *rateLimiter) {
	clientIP := getClientIP(r)
	limiter := rl.getLimiter(clientIP)
	if !limiter.Allow() {
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

	// Resolve the host to find subdomain/project.
	subdomainName, customProject, ok := s.resolveHost(host)
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
	rule, err := s.store.FindAccessRuleForSite(subdomainName, projectName)
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
		s.renderLoginPage(w, r, "Incorrect password")
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

	cookieValue := s.signCookie(cookieSubdomain, cookieProject, expiry, rule.PasswordHash)
	http.SetCookie(w, &http.Cookie{
		Name:     "_droply_access",
		Value:    cookieValue,
		Path:     "/",
		Expires:  expiry,
		Secure:   true,
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

// signCookie creates a signed cookie value in the format:
// {subdomain}:{project_or_empty}:{expiry_unix}:{hmac_sha256_hex}
// The passwordHash is included in the HMAC payload (not in the cookie value)
// so that changing the password automatically invalidates existing cookies.
func (s *Server) signCookie(subdomain, project string, expiry time.Time, passwordHash string) string {
	expiryStr := strconv.FormatInt(expiry.Unix(), 10)
	payload := fmt.Sprintf("%s:%s:%s:%s", subdomain, project, expiryStr, passwordHash)

	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))

	return fmt.Sprintf("%s:%s:%s:%s", subdomain, project, expiryStr, sig)
}

// isValidAccessCookie checks the _droply_access cookie for validity.
// passwordHash is from the rule returned by FindAccessRuleForSite.
// For subdomain-scoped cookies (cookieProj=="") where the passed rule is project-level,
// the function looks up the subdomain-level rule to get the correct hash.
func (s *Server) isValidAccessCookie(r *http.Request, subdomain, project string, isCustomDomain bool, passwordHash string) bool {
	cookie, err := r.Cookie("_droply_access")
	if err != nil {
		return false
	}

	parts := strings.SplitN(cookie.Value, ":", 4)
	if len(parts) != 4 {
		return false
	}

	cookieSub := parts[0]
	cookieProj := parts[1]
	expiryStr := parts[2]
	sig := parts[3]

	// Check expiry first (cheap, no DB needed).
	expiryUnix, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() > expiryUnix {
		return false
	}

	// Check scope: cookie subdomain must match.
	if cookieSub != subdomain {
		return false
	}

	// Determine the correct passwordHash for HMAC verification.
	hashForVerify := passwordHash
	if cookieProj == "" && project != "" {
		// Subdomain-scoped cookie but we were given a project-level rule's hash.
		// Look up the subdomain-level rule to get the correct hash.
		subRule, err := s.store.FindAccessRuleForSite(cookieSub, "")
		if err != nil || subRule == nil {
			return false
		}
		hashForVerify = subRule.PasswordHash
	}

	// Verify HMAC with passwordHash included in payload.
	payload := fmt.Sprintf("%s:%s:%s:%s", cookieSub, cookieProj, expiryStr, hashForVerify)
	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return false
	}

	// Subdomain-level cookie (empty project) grants access to all projects.
	if cookieProj == "" {
		return true
	}

	// Project-level cookie must match the requested project.
	return cookieProj == project
}
