package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zhong/droply/internal/store"
	"golang.org/x/time/rate"
)

// SetOpenRegistration must be called before serving. Private installations are closed by default.
func (s *Server) SetOpenRegistration(open bool) { s.openRegistration = open }

type authThrottle struct {
	mu      sync.Mutex
	entries map[string]*authThrottleEntry
}
type authThrottleEntry struct {
	limiter *rate.Limiter
	seen    time.Time
}

func (a *authThrottle) allow(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if a.entries == nil {
		a.entries = map[string]*authThrottleEntry{}
	}
	entry := a.entries[ip]
	if entry == nil {
		if len(a.entries) >= 4096 {
			oldestKey := ""
			oldest := now
			for key, value := range a.entries {
				if value.seen.Before(oldest) {
					oldestKey, oldest = key, value.seen
				}
			}
			delete(a.entries, oldestKey)
		}
		entry = &authThrottleEntry{limiter: rate.NewLimiter(rate.Every(6*time.Second), 10)}
		a.entries[ip] = entry
	}
	entry.seen = now
	return entry.limiter.Allow()
}
func (s *Server) allowAuthentication(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authThrottle.allow(getClientIP(r)) {
		w.Header().Set("Retry-After", "6")
		jsonError(w, "too many authentication attempts", 429)
		return false
	}
	return true
}
func (s *Server) requireAdministrator(w http.ResponseWriter, r *http.Request) bool {
	if r.Context().Value(projectTokenContextKey) != nil || !userFromContext(r.Context()).IsAdmin {
		jsonError(w, "administrator required", 403)
		return false
	}
	return true
}
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	w.Header().Set("Cache-Control", "no-store")
	jsonResponse(w, map[string]any{"id": user.ID, "email": user.Email, "is_admin": user.IsAdmin}, 200)
}
func (s *Server) handleCreateInvitation(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var input struct {
		Email     string    `json:"email"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input); err != nil {
		jsonError(w, "invalid invitation", 400)
		return
	}
	invitation, raw, err := s.store.CreateInvitation(r.Context(), userFromContext(r.Context()).ID, input.Email, input.ExpiresAt)
	if errors.Is(err, store.ErrIdentityInput) {
		jsonError(w, "email and expiration within 30 days required", 400)
		return
	}
	if err != nil {
		jsonError(w, "cannot create invitation", 500)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonResponse(w, struct {
		ID        int64     `json:"id"`
		Email     string    `json:"email"`
		CreatedAt time.Time `json:"created_at"`
		ExpiresAt time.Time `json:"expires_at"`
		Revoked   bool      `json:"revoked"`
		Used      bool      `json:"used"`
		Token     string    `json:"token"`
	}{invitation.ID, invitation.Email, invitation.CreatedAt, invitation.ExpiresAt, false, false, raw}, 201)
}
func (s *Server) handleListInvitations(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	items, err := s.store.ListInvitations(r.Context())
	if err != nil {
		jsonError(w, "cannot list invitations", 500)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonResponse(w, items, 200)
}
func (s *Server) handleRevokeInvitation(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id < 1 {
		jsonError(w, "invalid invitation ID", 400)
		return
	}
	err = s.store.RevokeInvitation(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		jsonError(w, "invitation not found", 404)
		return
	}
	if err != nil {
		jsonError(w, "cannot revoke invitation", 500)
		return
	}
	w.WriteHeader(204)
}
