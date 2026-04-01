package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
)

// newAnalyticsServer creates a test server and exposes the store for direct data insertion.
func newAnalyticsServer(t *testing.T) (*server.Server, *store.SQLiteStore) {
	t.Helper()
	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := server.New(st, "/tmp/sites", "droplydoc.com", nil, []byte("test-hmac-key-for-testing-1234"), "localhost:8081")
	return srv, st
}

// setupAnalyticsEnv creates a test user + subdomain and returns (token, subdomainName).
func setupAnalyticsEnv(t *testing.T, srv http.Handler) (string, string) {
	t.Helper()
	token := registerAndGetToken(t, srv, "analytics@example.com", "password123")

	body, _ := json.Marshal(map[string]string{"name": "mysite"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d", rr.Code)
	}

	return token, "mysite"
}

func TestGetStats(t *testing.T) {
	srv, st := newAnalyticsServer(t)
	token, subName := setupAnalyticsEnv(t, srv)

	// First subdomain = ID 1 in fresh DB
	st.RecordVisit(1, "blog", "/", "1.2.3.4", "", "")
	st.RecordVisit(1, "blog", "/", "5.6.7.8", "", "")
	st.RecordVisit(1, "blog", "/about", "1.2.3.4", "", "")

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/subdomains/%s/projects/blog/stats?period=7d", subName), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		TotalPV int `json:"total_pv"`
		TotalUV int `json:"total_uv"`
		Pages   []struct {
			Path string `json:"path"`
			PV   int    `json:"pv"`
			UV   int    `json:"uv"`
		} `json:"pages"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalPV != 3 {
		t.Fatalf("expected total_pv=3, got %d", resp.TotalPV)
	}
	// UV is summed per-page: "/" has 2 UVs (1.2.3.4, 5.6.7.8), "/about" has 1 UV (1.2.3.4)
	// Total = 2 + 1 = 3
	if resp.TotalUV != 3 {
		t.Fatalf("expected total_uv=3, got %d", resp.TotalUV)
	}
	if len(resp.Pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(resp.Pages))
	}
}

func TestGetStatsForbidden(t *testing.T) {
	srv, _ := newAnalyticsServer(t)
	_, _ = setupAnalyticsEnv(t, srv)

	otherToken := registerAndGetToken(t, srv, "other@example.com", "password456")

	req := httptest.NewRequest(http.MethodGet, "/subdomains/mysite/projects/blog/stats", nil)
	req.Header.Set("Authorization", "Bearer "+otherToken)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestGetLogs(t *testing.T) {
	srv, st := newAnalyticsServer(t)
	token, subName := setupAnalyticsEnv(t, srv)

	st.RecordVisit(1, "blog", "/hello", "1.2.3.4", "https://google.com", "Mozilla/5.0")

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/subdomains/%s/projects/blog/logs?limit=10", subName), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Logs  []json.RawMessage `json:"logs"`
		Total int               `json:"total"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected total=1, got %d", resp.Total)
	}
	if len(resp.Logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(resp.Logs))
	}
}

func TestGetStatsInvalidPeriod(t *testing.T) {
	srv, _ := newAnalyticsServer(t)
	token, subName := setupAnalyticsEnv(t, srv)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/subdomains/%s/projects/blog/stats?period=invalid", subName), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestGetLogsWithPathFilter(t *testing.T) {
	srv, st := newAnalyticsServer(t)
	token, subName := setupAnalyticsEnv(t, srv)

	st.RecordVisit(1, "blog", "/hello", "1.2.3.4", "", "")
	st.RecordVisit(1, "blog", "/world", "1.2.3.4", "", "")

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/subdomains/%s/projects/blog/logs?path=/hello", subName), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Logs  []json.RawMessage `json:"logs"`
		Total int               `json:"total"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected total=1 (path filter), got %d", resp.Total)
	}
}
