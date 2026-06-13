package server

import (
	"log"
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

	// Choose the right authorization endpoint based on the User-Agent.
	// Inside the WeCom mobile app the in-app browser is already signed in,
	// so we use snsapi_base for silent authorization (no QR code). Outside
	// (desktop Chrome/Safari, mobile Safari, etc.) we use the SSO endpoint
	// that renders a QR code page.
	var authURL string
	if isWeComUserAgent(r.UserAgent()) {
		authURL = s.wework.GetMobileAuthorizeURL(state)
	} else {
		authURL = s.wework.GetAuthorizeURL(state)
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

// isWeComUserAgent reports whether the request comes from inside the WeCom
// mobile app's embedded browser. The WeCom UA contains "wxwork/" (the
// trailing slash is important — it distinguishes WeCom from regular WeChat,
// which only has "MicroMessenger" and cannot use snsapi_base for WeCom OAuth).
func isWeComUserAgent(ua string) bool {
	return strings.Contains(ua, "wxwork/")
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
		log.Printf("wework callback: invalid or expired state (state=%s)", state)
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	// Exchange code for user_id.
	userID, err := s.wework.GetUserIDByCode(code)
	if err != nil {
		log.Printf("wework callback: failed to get user ID (code=%s, err=%v)", truncate(code, 8), err)
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
		log.Printf("wework callback: user %q not in allow-list (subdomain=%s, project=%s, allowed=%v)",
			userID, stateData.Subdomain, stateData.Project, rule.AllowedWeWorkUsers)
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

	// Compute cookie Domain so the session is readable from the original
	// subdomain after we redirect back. If the OAuth callback host differs
	// from the requesting host (e.g. login.docs.paratera.co handling the
	// callback for its.docs.paratera.co), scope the cookie to the shared
	// parent domain so both subdomains see it.
	cookieDomain := cookieParentDomain(r.Host, stateData.Host, s.baseDomain)

	http.SetCookie(w, &http.Cookie{
		Name:     "_droply_access",
		Value:    cookieValue,
		Path:     "/",
		Domain:   cookieDomain,
		Expires:  expiry,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	// Build the absolute redirect URL. If the callback host differs from the
	// requesting host (state was generated on a different subdomain), redirect
	// back to the original host using the stored path; otherwise use the path
	// directly so it resolves against the current host.
	target := stateData.Redirect
	if stateData.Host != "" && !sameHost(stateData.Host, r.Host) {
		target = "https://" + stateData.Host + stateData.Redirect
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// truncate shortens s to at most n characters, appending "..." if truncated.
// Safe for any length input (no panic on short strings).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// sameHost compares two host strings case-insensitively, ignoring an optional port.
func sameHost(a, b string) bool {
	return strings.EqualFold(stripPort(a), stripPort(b))
}

func stripPort(host string) string {
	if i := strings.LastIndex(host, ":"); i >= 0 {
		// Ignore IPv6 brackets edge case — droply hosts are domain names.
		return host[:i]
	}
	return host
}

// cookieParentDomain decides what to set on the Cookie's Domain attribute so
// that the session survives a redirect from the OAuth callback host back to
// the original requesting subdomain. When both hosts share baseDomain as a
// suffix and they differ from each other, scope the cookie to ".baseDomain"
// so every subdomain can read it. When they match (or no cross-host hop is
// happening), return "" to keep the cookie host-only (safer default).
func cookieParentDomain(callbackHost, originHost, baseDomain string) string {
	if baseDomain == "" {
		return ""
	}
	cb := stripPort(callbackHost)
	or := stripPort(originHost)
	if or == "" || sameHost(cb, or) {
		return ""
	}
	suffix := "." + baseDomain
	if !strings.HasSuffix(strings.ToLower(cb), suffix) || !strings.HasSuffix(strings.ToLower(or), suffix) {
		return ""
	}
	return baseDomain
}

// CookieParentDomainForTest exposes cookieParentDomain for the external test package.
func CookieParentDomainForTest(callbackHost, originHost, baseDomain string) string {
	return cookieParentDomain(callbackHost, originHost, baseDomain)
}
