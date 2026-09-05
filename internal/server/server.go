package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/zhong/droply/internal/artifacts"
	"github.com/zhong/droply/internal/certificates"
	"github.com/zhong/droply/internal/model"
	"github.com/zhong/droply/internal/store"
	"github.com/zhong/droply/internal/wework"
)

type contextKey string

const userContextKey contextKey = "user"

// Server holds all dependencies for the HTTP server.
type Server struct {
	uploadMu          sync.Mutex
	deploymentMu      sync.RWMutex
	prepareOnce       sync.Once
	prepareError      error
	artifacts         *artifacts.Store
	deploymentOptions DeploymentOptions
	certificates      *certificates.Manager
	analyticsStart    sync.Once
	analyticsStop     sync.Once
	visitsMu          sync.RWMutex
	visitsClosed      bool
	store             store.Store
	dnsResolver       DNSResolver
	trustedProxies    []netip.Prefix
	sitesDir          string
	baseDomain        string
	router            *chi.Mux
	hmacKey           []byte
	visitCh           chan visitRecord
	done              chan struct{}
	wework            *wework.Client
	weworkState       *wework.StateStore
}

// New creates a new Server and registers all routes.
func New(s store.Store, sitesDir, baseDomain string, hmacKey []byte) *Server {
	srv := &Server{
		deploymentOptions: DeploymentOptions{RetainCount: 10, RetainDays: 30, OrphanGrace: time.Hour},
		store:             s,
		sitesDir:          sitesDir,
		baseDomain:        baseDomain,
		hmacKey:           hmacKey,
		visitCh:           make(chan visitRecord, 1000),
		done:              make(chan struct{}),
	}
	srv.router = srv.buildRouter()
	return srv
}

// SetWeWork configures the WeWork OAuth client. State tokens expire after 10 minutes.
// Call before starting the site handler. Passing nil disables WeWork login.
func (s *Server) SetWeWork(client *wework.Client) {
	s.wework = client
	if client != nil {
		s.weworkState = wework.NewStateStore(10 * time.Minute)
	}
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

	// Hostname authorization for optional external gateways
	r.Get("/_droply/tls-check", s.handleTLSCheck)

	// Authenticated routes
	r.Group(func(r chi.Router) {
		r.Use(s.authMiddleware)

		r.Post("/subdomains", s.handleCreateSubdomain)
		r.Get("/subdomains", s.handleListSubdomains)
		r.Get("/certificates/{domain}", s.handleCertificateStatus)
		r.Delete("/subdomains/{sub}", s.handleDeleteSubdomain)

		r.Get("/subdomains/{sub}/projects", s.handleListProjects)
		r.Delete("/subdomains/{sub}/projects/{project}", s.handleDeleteProject)

		r.Post("/subdomains/{sub}/projects/{project}/deploy", s.handleDeploy)
		r.Get("/subdomains/{sub}/projects/{project}/deployments", s.handleListDeployments)
		r.Post("/subdomains/{sub}/projects/{project}/rollback/{version}", s.handleRollback)
		r.Get("/subdomains/{sub}/projects/{project}/cleanup", s.handleDeploymentCleanup)
		r.Post("/subdomains/{sub}/projects/{project}/cleanup", s.handleDeploymentCleanup)

		r.Post("/subdomains/{sub}/projects/{project}/domains", s.handleCreateDomain)
		r.Get("/subdomains/{sub}/projects/{project}/domains", s.handleListDomains)
		r.Delete("/subdomains/{sub}/projects/{project}/domains/{domain}", s.handleDeleteDomain)
		r.Post("/subdomains/{sub}/projects/{project}/domains/{domain}/verify", s.handleVerifyDomain)

		r.Put("/subdomains/{sub}/access", s.handleSetAccess)
		r.Get("/subdomains/{sub}/access", s.handleGetAccess)
		r.Delete("/subdomains/{sub}/access", s.handleDeleteAccess)

		r.Put("/subdomains/{sub}/projects/{project}/access", s.handleSetProjectAccess)
		r.Get("/subdomains/{sub}/projects/{project}/access", s.handleGetProjectAccess)
		r.Delete("/subdomains/{sub}/projects/{project}/access", s.handleDeleteProjectAccess)

		r.Get("/subdomains/{sub}/projects/{project}/stats", s.handleGetStats)
		r.Get("/subdomains/{sub}/projects/{project}/logs", s.handleGetLogs)
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
