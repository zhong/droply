package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/chenzhong/droply/internal/model"
	"github.com/chenzhong/droply/internal/store"
)

type contextKey string

const userContextKey contextKey = "user"

// CaddyClient is the interface for interacting with the Caddy admin API.
type CaddyClient interface {
	AddSubdomain(subdomain, baseDomain, sitesDir string) error
	RemoveSubdomain(subdomain string) error
	AddCustomDomain(domain, subdomain, project string) error
	RemoveCustomDomain(domain string) error
}

// Server holds all dependencies for the HTTP server.
type Server struct {
	store      store.Store
	sitesDir   string
	baseDomain string
	caddy      CaddyClient
	router     *chi.Mux
}

// New creates a new Server and registers all routes.
func New(s store.Store, sitesDir, baseDomain string, caddy CaddyClient) *Server {
	srv := &Server{
		store:      s,
		sitesDir:   sitesDir,
		baseDomain: baseDomain,
		caddy:      caddy,
	}
	srv.router = srv.buildRouter()
	return srv
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Server) buildRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Public auth routes
	r.Post("/auth/register", s.handleRegister)
	r.Post("/auth/login", s.handleLogin)

	// Authenticated routes
	r.Group(func(r chi.Router) {
		r.Use(s.authMiddleware)

		r.Post("/subdomains", s.stubHandler)
		r.Get("/subdomains", s.stubHandler)
		r.Delete("/subdomains/{sub}", s.stubHandler)

		r.Get("/subdomains/{sub}/projects", s.stubHandler)
		r.Delete("/subdomains/{sub}/projects/{project}", s.stubHandler)

		r.Post("/subdomains/{sub}/projects/{project}/deploy", s.stubHandler)
		r.Get("/subdomains/{sub}/projects/{project}/deployments", s.stubHandler)

		r.Post("/subdomains/{sub}/projects/{project}/domains", s.stubHandler)
		r.Get("/subdomains/{sub}/projects/{project}/domains", s.stubHandler)
		r.Delete("/subdomains/{sub}/projects/{project}/domains/{domain}", s.stubHandler)
	})

	return r
}

// authMiddleware extracts the Bearer token from the Authorization header,
// looks up the user, and stores it in the request context.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		auth := r.Header.Get("Authorization")
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix {
			jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		token := auth[len(prefix):]
		user, err := s.store.GetUserByToken(token)
		if err != nil {
			jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// userFromContext retrieves the authenticated user from the request context.
func userFromContext(ctx context.Context) *model.User {
	u, _ := ctx.Value(userContextKey).(*model.User)
	return u
}

// stubHandler returns 501 Not Implemented for endpoints not yet built.
func (s *Server) stubHandler(w http.ResponseWriter, r *http.Request) {
	jsonError(w, "not implemented", http.StatusNotImplemented)
}

// jsonResponse writes a JSON response with the given status code.
func jsonResponse(w http.ResponseWriter, v any, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// jsonError writes a JSON error response.
func jsonError(w http.ResponseWriter, msg string, status int) {
	jsonResponse(w, map[string]string{"error": msg}, status)
}
