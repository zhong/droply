package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
)

func TestHealthDatabaseAvailability(t *testing.T) {
	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(st, t.TempDir(), "example.com", []byte("test-health-key"))
	check := func(status int, body string) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "http://api.example.com/healthz", nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code != status || w.Body.String() != body+"\n" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("health %d %s %v", w.Code, w.Body.String(), w.Header())
		}
	}
	check(200, `{"status":"ok"}`)
	st.Close()
	check(503, `{"status":"unavailable"}`)
}
