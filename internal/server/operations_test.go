package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/zhong/droply/internal/store"
)

// Walk the registered surface so a new route cannot accidentally omit its
// operation/authentication wrapper while existing endpoint tests still pass.
func TestEveryManagementRouteRequiresAuthentication(t *testing.T) {
	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := New(st, t.TempDir(), "example.test", []byte("operations-test-key"))
	if err := chi.Walk(srv.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/console") || route == "/auth/register" || route == "/auth/login" || route == "/healthz" || route == "/_droply/tls-check" {
			return nil
		}
		t.Run(method+" "+route, func(t *testing.T) {
			request := httptest.NewRequest(method, route, nil)
			request.Host = "api.example.test"
			result := httptest.NewRecorder()
			srv.ServeHTTP(result, request)
			if result.Code != http.StatusUnauthorized {
				t.Fatalf("unprotected management route: %d %s", result.Code, result.Body.String())
			}
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
