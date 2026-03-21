package caddy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSetSubdomainProtected(t *testing.T) {
	var receivedRoutes []caddyRoute
	var deletedPaths []string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			deletedPaths = append(deletedPaths, r.URL.Path)
			w.WriteHeader(http.StatusOK)
		case http.MethodPost:
			var route caddyRoute
			json.NewDecoder(r.Body).Decode(&route)
			receivedRoutes = append(receivedRoutes, route)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "droplydoc.com", "/data/sites")
	if err := c.SetSubdomainProtected("alice", "localhost:8081"); err != nil {
		t.Fatalf("SetSubdomainProtected: %v", err)
	}

	if len(deletedPaths) != 1 || deletedPaths[0] != "/id/subdomain-alice" {
		t.Errorf("expected delete of /id/subdomain-alice, got %v", deletedPaths)
	}
	if len(receivedRoutes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(receivedRoutes))
	}
	route := receivedRoutes[0]
	if route.ID != "subdomain-alice" {
		t.Errorf("unexpected route ID: %s", route.ID)
	}
	if route.Handle[0].Handler != "reverse_proxy" {
		t.Errorf("expected reverse_proxy handler, got %s", route.Handle[0].Handler)
	}
}

func TestSetSubdomainUnprotected(t *testing.T) {
	var receivedRoutes []caddyRoute
	var deletedPaths []string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			deletedPaths = append(deletedPaths, r.URL.Path)
			w.WriteHeader(http.StatusOK)
		case http.MethodPost:
			var route caddyRoute
			json.NewDecoder(r.Body).Decode(&route)
			receivedRoutes = append(receivedRoutes, route)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "droplydoc.com", "/data/sites")
	if err := c.SetSubdomainUnprotected("alice"); err != nil {
		t.Fatalf("SetSubdomainUnprotected: %v", err)
	}

	if len(deletedPaths) != 1 {
		t.Fatalf("expected 1 delete, got %d", len(deletedPaths))
	}
	if len(receivedRoutes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(receivedRoutes))
	}
	if receivedRoutes[0].Handle[0].Handler != "file_server" {
		t.Errorf("expected file_server handler, got %s", receivedRoutes[0].Handle[0].Handler)
	}
}
