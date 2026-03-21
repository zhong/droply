package caddy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildSubdomainRoute(t *testing.T) {
	c := NewClient("http://localhost:2019", "droplydoc.com", "/data/sites")
	route := c.buildSubdomainRoute("alice")

	if route.ID != "subdomain-alice" {
		t.Errorf("expected ID=subdomain-alice, got %s", route.ID)
	}
	if len(route.Match) != 1 || len(route.Match[0].Host) != 1 {
		t.Fatalf("expected 1 match with 1 host, got %v", route.Match)
	}
	if route.Match[0].Host[0] != "alice.droplydoc.com" {
		t.Errorf("expected host=alice.droplydoc.com, got %s", route.Match[0].Host[0])
	}
	if len(route.Handle) != 1 || route.Handle[0].Handler != "file_server" {
		t.Errorf("expected file_server handler, got %v", route.Handle)
	}
}

func TestAddSubdomainRouteHTTP(t *testing.T) {
	var gotPath string
	var gotBody map[string]interface{}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	c := NewClient(mock.URL, "droplydoc.com", "/data/sites")
	if err := c.AddSubdomainRoute("alice"); err != nil {
		t.Fatalf("AddSubdomainRoute: %v", err)
	}

	expectedPath := "/config/apps/http/servers/main/routes"
	if gotPath != expectedPath {
		t.Errorf("expected POST to %s, got %s", expectedPath, gotPath)
	}
	if id, ok := gotBody["@id"].(string); !ok || id != "subdomain-alice" {
		t.Errorf("expected @id=subdomain-alice in body, got %v", gotBody["@id"])
	}
}

func TestRemoveSubdomainRouteHTTP(t *testing.T) {
	var gotMethod string
	var gotPath string

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	c := NewClient(mock.URL, "droplydoc.com", "/data/sites")
	if err := c.RemoveSubdomainRoute("alice"); err != nil {
		t.Fatalf("RemoveSubdomainRoute: %v", err)
	}

	if gotMethod != http.MethodDelete {
		t.Errorf("expected DELETE, got %s", gotMethod)
	}
	expectedPath := "/id/subdomain-alice"
	if gotPath != expectedPath {
		t.Errorf("expected path %s, got %s", expectedPath, gotPath)
	}
}
