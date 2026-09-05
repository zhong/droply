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
	openRegistration  bool
	authThrottle      ipLimiter
	uploadMu          sync.Mutex
	deploymentMu      sync.RWMutex
	prepareOnce       sync.Once
	prepareError      error
	artifacts         *artifacts.Store
	staticSites       siteCache
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
	sitesHandler      http.Handler
	hmacKey           []byte
	visitCh           chan visitRecord
	done              chan struct{}
	wework            *wework.Client
	weworkState       *wework.StateStore
}

// New creates a new Server and registers all routes.
func New(s store.Store, sitesDir, baseDomain string, hmacKey []byte) *Server {
	srv := &Server{
		authThrottle:      ipLimiter{capacity: 4096},
		deploymentOptions: DeploymentOptions{RetainCount: 10, RetainDays: 30, OrphanGrace: time.Hour},
		store:             s,
		sitesDir:          sitesDir,
		baseDomain:        baseDomain,
		hmacKey:           hmacKey,
		visitCh:           make(chan visitRecord, 1000),
		done:              make(chan struct{}),
	}
	srv.router = srv.buildRouter()
	srv.sitesHandler = srv.NewSiteHandler()
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

// serveAPI handles management routes after the public entry validates the Host.
func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Server) buildRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	s.registerConsoleRoutes(r)

	// Public auth routes
	r.Method("POST", "/auth/register", s.withTrustedProxy(http.HandlerFunc(s.handleRegister)))
	r.Method("POST", "/auth/login", s.withTrustedProxy(http.HandlerFunc(s.handleLogin)))

	r.Get("/healthz", s.handleHealth)

	// Hostname authorization for optional external gateways
	r.Get("/_droply/tls-check", s.handleTLSCheck)

	// Authenticated routes
	r.Group(func(r chi.Router) {
		s.registerAuditRoutes(r)
		r.With(s.forOperation(opMe)).Get("/auth/me", s.handleMe)
		r.With(s.forOperation(opAccessibleProjects)).Get("/projects", s.handleAccessibleProjects)
		r.With(s.forOperation(opListInvitations)).Get("/admin/invitations", s.handleListInvitations)
		r.With(s.forOperation(opCreateInvitation)).Post("/admin/invitations", s.handleCreateInvitation)
		r.With(s.forOperation(opRevokeInvitation)).Delete("/admin/invitations/{id}", s.handleRevokeInvitation)

		r.With(s.forOperation(opCreateSubdomain)).Post("/subdomains", s.handleCreateSubdomain)
		r.With(s.forOperation(opListSubdomains)).Get("/subdomains", s.handleListSubdomains)
		r.With(s.forOperation(opCertificateStatus)).Get("/certificates/{domain}", s.handleCertificateStatus)
		r.With(s.forOperation(opDeleteSubdomain)).Delete("/subdomains/{sub}", s.handleDeleteSubdomain)

		r.With(s.forOperation(opListProjects)).Get("/subdomains/{sub}/projects", s.handleListProjects)
		r.With(s.forOperation(opDeleteProject)).Delete("/subdomains/{sub}/projects/{project}", s.handleDeleteProject)

		r.With(s.forOperation(opDeploy)).Post("/subdomains/{sub}/projects/{project}/deploy", s.handleDeploy)
		r.With(s.forOperation(opListDeployments)).Get("/subdomains/{sub}/projects/{project}/deployments", s.handleListDeployments)
		r.With(s.forOperation(opRollback)).Post("/subdomains/{sub}/projects/{project}/rollback/{version}", s.handleRollback)
		r.With(s.forOperation(opPromote)).Post("/subdomains/{sub}/projects/{project}/promote/{version}", s.handlePromote)
		r.With(s.forOperation(opPublicationEvents)).Get("/subdomains/{sub}/projects/{project}/events", s.handlePublicationEvents)
		r.With(s.forOperation(opListMembers)).Get("/subdomains/{sub}/projects/{project}/members", s.handleListMembers)
		r.With(s.forOperation(opPutMember)).Put("/subdomains/{sub}/projects/{project}/members", s.handlePutMember)
		r.With(s.forOperation(opRemoveMember)).Delete("/subdomains/{sub}/projects/{project}/members/{id}", s.handleRemoveMember)
		r.With(s.forOperation(opListProjectTokens)).Get("/subdomains/{sub}/projects/{project}/tokens", s.handleListProjectTokens)
		r.With(s.forOperation(opCreateProjectToken)).Post("/subdomains/{sub}/projects/{project}/tokens", s.handleCreateProjectToken)
		r.With(s.forOperation(opRevokeProjectToken)).Delete("/subdomains/{sub}/projects/{project}/tokens/{id}", s.handleRevokeProjectToken)
		r.With(s.forOperation(opPreviewCleanup)).Get("/subdomains/{sub}/projects/{project}/cleanup", s.handleDeploymentCleanup)
		r.With(s.forOperation(opApplyCleanup)).Post("/subdomains/{sub}/projects/{project}/cleanup", s.handleDeploymentCleanup)

		r.With(s.forOperation(opCreateDomain)).Post("/subdomains/{sub}/projects/{project}/domains", s.handleCreateDomain)
		r.With(s.forOperation(opListDomains)).Get("/subdomains/{sub}/projects/{project}/domains", s.handleListDomains)
		r.With(s.forOperation(opDeleteDomain)).Delete("/subdomains/{sub}/projects/{project}/domains/{domain}", s.handleDeleteDomain)
		r.With(s.forOperation(opVerifyDomain)).Post("/subdomains/{sub}/projects/{project}/domains/{domain}/verify", s.handleVerifyDomain)

		r.With(s.forOperation(opSetSubdomainAccess)).Put("/subdomains/{sub}/access", s.handleSetAccess)
		r.With(s.forOperation(opGetSubdomainAccess)).Get("/subdomains/{sub}/access", s.handleGetAccess)
		r.With(s.forOperation(opDeleteSubdomainAccess)).Delete("/subdomains/{sub}/access", s.handleDeleteAccess)

		r.With(s.forOperation(opSetProjectAccess)).Put("/subdomains/{sub}/projects/{project}/access", s.handleSetProjectAccess)
		r.With(s.forOperation(opGetProjectAccess)).Get("/subdomains/{sub}/projects/{project}/access", s.handleGetProjectAccess)
		r.With(s.forOperation(opDeleteProjectAccess)).Delete("/subdomains/{sub}/projects/{project}/access", s.handleDeleteProjectAccess)

		r.With(s.forOperation(opGetStats)).Get("/subdomains/{sub}/projects/{project}/stats", s.handleGetStats)
		r.With(s.forOperation(opGetLogs)).Get("/subdomains/{sub}/projects/{project}/logs", s.handleGetLogs)
	})

	return r
}

// authMiddleware extracts the Bearer token from the Authorization header,
// looks up the user, and stores it in the request context.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ctx context.Context
		var ok bool
		if r.Header.Get("Authorization") == "" {
			ctx, ok = s.authenticateConsoleSession(w, r)
		} else {
			ctx, ok = s.authenticateBearer(w, r)
		}
		if ok {
			s.auditMiddleware(next).ServeHTTP(w, r.WithContext(ctx))
		}
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
