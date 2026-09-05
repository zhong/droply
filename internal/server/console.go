package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zhong/droply/internal/model"
)

//go:embed console_assets/*
var consoleAssets embed.FS

const consoleCookieName = "__Host-droply_console"
const consoleSessionContextKey contextKey = "console-session"

func (s *Server) registerConsoleRoutes(r chi.Router) {
	r.Get("/console", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/console/", 307) })
	r.Method("GET", "/console/*", s.withTrustedProxy(http.HandlerFunc(s.serveConsole)))
	r.Method("POST", "/console/login", s.withTrustedProxy(http.HandlerFunc(s.consoleLogin)))
	r.Method("GET", "/console/session", s.withTrustedProxy(http.HandlerFunc(s.consoleSession)))
	r.Method("POST", "/console/logout", s.withTrustedProxy(http.HandlerFunc(s.consoleLogout)))
}
func canonicalHost(authority string) string {
	host := authority
	if h, _, err := net.SplitHostPort(authority); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}
func (s *Server) consoleOrigin(r *http.Request) string {
	host := canonicalHost(r.Host)
	if host != "api."+strings.ToLower(s.baseDomain) {
		return ""
	}
	secure := r.TLS != nil
	s.withTrustedProxy(http.HandlerFunc(func(_ http.ResponseWriter, clean *http.Request) {
		secure = secure || clean.Header.Get("X-Forwarded-Proto") == "https"
	})).ServeHTTP(nil, r)
	if !secure {
		return ""
	}
	if _, port, err := net.SplitHostPort(r.Host); err == nil && port != "443" {
		host = net.JoinHostPort(host, port)
	}
	return "https://" + host
}
func (s *Server) validConsoleOrigin(r *http.Request) bool {
	expected := s.consoleOrigin(r)
	if expected == "" {
		return false
	}
	u, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && u.Scheme == "https" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && strings.EqualFold(u.Host, strings.TrimPrefix(expected, "https://"))
}
func (s *Server) csrfForSession(raw string) string {
	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write([]byte("droply-console-csrf-v1\x00"))
	mac.Write([]byte(raw))
	return hex.EncodeToString(mac.Sum(nil))
}
func (s *Server) authenticateConsoleSession(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	w.Header().Set("Cache-Control", "no-store")
	cookie, err := r.Cookie(consoleCookieName)
	if err != nil {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return nil, false
	}
	if s.consoleOrigin(r) == "" {
		jsonError(w, "secure management origin required", http.StatusUnauthorized)
		return nil, false
	}
	session, err := s.store.GetConsoleSession(r.Context(), cookie.Value)
	if err != nil {
		jsonError(w, "session expired; sign in again", 401)
		return nil, false
	}
	user, err := s.store.GetUserByID(r.Context(), session.UserID)
	if err != nil {
		jsonError(w, "session expired; sign in again", 401)
		return nil, false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if !s.validConsoleOrigin(r) || !hmac.Equal([]byte(r.Header.Get("X-CSRF-Token")), []byte(s.csrfForSession(cookie.Value))) {
			jsonError(w, "invalid request origin or CSRF token", 403)
			return nil, false
		}
	}
	ctx := context.WithValue(r.Context(), userContextKey, user)
	return context.WithValue(ctx, consoleSessionContextKey, true), true
}
func (s *Server) serveConsole(w http.ResponseWriter, r *http.Request) {
	if canonicalHost(r.Host) != "api."+strings.ToLower(s.baseDomain) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	assets, _ := fs.Sub(consoleAssets, "console_assets")
	http.StripPrefix("/console/", http.FileServer(http.FS(assets))).ServeHTTP(w, r)
}
func (s *Server) consoleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.validConsoleOrigin(r) {
		jsonError(w, "secure management origin required", 403)
		return
	}
	if !s.allowAuthentication(w, r) {
		return
	}
	var input loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input); err != nil {
		jsonError(w, "invalid login request", 400)
		return
	}
	user, err := s.authenticateCredentials(input.Email, input.Password)
	if err != nil {
		jsonError(w, "invalid credentials", 401)
		return
	}
	session, raw, err := s.store.CreateConsoleSession(r.Context(), user.ID)
	if err != nil {
		jsonError(w, "cannot create session", 500)
		return
	}
	if old, err := r.Cookie(consoleCookieName); err == nil {
		_ = s.store.RevokeConsoleSession(r.Context(), old.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: consoleCookieName, Value: raw, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: session.ExpiresAt, MaxAge: int(time.Until(session.ExpiresAt).Seconds())})
	s.writeConsoleSession(w, user, raw, session.ExpiresAt)
}
func (s *Server) writeConsoleSession(w http.ResponseWriter, user *model.User, raw string, expires time.Time) {
	w.Header().Set("Cache-Control", "no-store")
	jsonResponse(w, map[string]any{"user": map[string]any{"id": user.ID, "email": user.Email}, "csrf_token": s.csrfForSession(raw), "expires_at": expires, "base_domain": s.baseDomain}, 200)
}
func (s *Server) consoleSession(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.authenticateConsoleSession(w, r)
	if !ok {
		return
	}
	cookie, _ := r.Cookie(consoleCookieName)
	session, err := s.store.GetConsoleSession(r.Context(), cookie.Value)
	if err != nil {
		jsonError(w, "session expired; sign in again", 401)
		return
	}
	s.writeConsoleSession(w, userFromContext(ctx), cookie.Value, session.ExpiresAt)
}
func (s *Server) consoleLogout(w http.ResponseWriter, r *http.Request) {
	_, ok := s.authenticateConsoleSession(w, r)
	if !ok {
		return
	}
	cookie, _ := r.Cookie(consoleCookieName)
	if err := s.store.RevokeConsoleSession(r.Context(), cookie.Value); err != nil {
		jsonError(w, "cannot end session", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: consoleCookieName, Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(204)
}
