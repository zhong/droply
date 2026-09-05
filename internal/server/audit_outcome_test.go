package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/zhong/droply/internal/model"
	"github.com/zhong/droply/internal/store"
)

type failingAuditWriter struct{ *httptest.ResponseRecorder }

func (w failingAuditWriter) Write([]byte) (int, error) { return 0, errors.New("connection lost") }

func TestAuditOutcomeIndependentOfResponse(t *testing.T) {
	for _, tc := range []struct {
		name         string
		handler      http.HandlerFunc
		result       string
		status       int
		brokenWriter bool
	}{
		{"large non JSON", func(w http.ResponseWriter, r *http.Request) {
			auditResourceTarget(r, auditVersion, 7)
			recordAudit(r, auditSuccess)
			_, _ = w.Write([]byte(strings.Repeat("secret", 2000)))
		}, "success", 200, false},
		{"misleading JSON", func(w http.ResponseWriter, r *http.Request) {
			auditResourceTarget(r, auditUser, 9)
			recordAudit(r, auditSuccess)
			_, _ = w.Write([]byte(`{"id":42,"version":99}`))
		}, "success", 200, false},
		{"semantic failure", func(w http.ResponseWriter, r *http.Request) { recordAudit(r, auditFailure); w.WriteHeader(200) }, "failure", 200, false},
		{"no declared result", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }, "pending", 200, false},
		{"before commit failure", func(w http.ResponseWriter, r *http.Request) { recordAudit(r, auditFailure); w.WriteHeader(500) }, "failure", 500, false},
		{"unknown commit", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }, "pending", 500, false},
		{"panic before header", func(w http.ResponseWriter, r *http.Request) { panic("interrupted") }, "pending", 500, false},
		{"panic after header", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); panic("interrupted") }, "pending", 200, false},
		{"committed then abort", func(w http.ResponseWriter, r *http.Request) {
			recordAudit(r, auditSuccess)
			panic(http.ErrAbortHandler)
		}, "success", 0, false},
		{"abort before header", func(w http.ResponseWriter, r *http.Request) { panic(http.ErrAbortHandler) }, "pending", 0, false},
		{"abort after header", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); panic(http.ErrAbortHandler) }, "pending", 200, false},
		{"committed response failure", func(w http.ResponseWriter, r *http.Request) {
			recordAudit(r, auditSuccess)
			_, _ = w.Write([]byte("ok"))
		}, "success", 200, true},
		{"informational header", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(103)
			recordAudit(r, auditSuccess)
			w.WriteHeader(201)
		}, "success", 201, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.NewSQLiteStore(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			srv := &Server{store: st}
			r := httptest.NewRequest("POST", "/", nil)
			ctx := context.WithValue(r.Context(), operationContextKey, opDeploy)
			ctx = context.WithValue(ctx, userContextKey, &model.User{ID: 1})
			r = r.WithContext(ctx)
			w := httptest.NewRecorder()
			var writer http.ResponseWriter = w
			if tc.brokenWriter {
				writer = failingAuditWriter{w}
			}
			func() {
				defer func() {
					if p := recover(); p != nil && p != http.ErrAbortHandler {
						t.Fatalf("unexpected panic: %v", p)
					}
				}()
				middleware.Recoverer(srv.auditMiddleware(tc.handler)).ServeHTTP(writer, r)
			}()
			events, err := st.ListAuditEvents(t.Context(), 0, 0, 0, 10)
			if err != nil || len(events) != 1 {
				t.Fatalf("events=%+v error=%v", events, err)
			}
			e := events[0]
			if e.Result != tc.result || e.StatusCode != tc.status {
				t.Fatalf("event=%+v want %s/%d", e, tc.result, tc.status)
			}
			target := "request"
			if tc.name == "large non JSON" {
				target = "version:7"
			}
			if tc.name == "misleading JSON" {
				target = "user:9"
			}
			if e.Target != target {
				t.Fatalf("target=%q want %q", e.Target, target)
			}
		})
	}
}
