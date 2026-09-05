package server_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zhong/droply/internal/model"
	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
)

type auditDNS struct{}

func (auditDNS) LookupTXT(context.Context, string) ([]string, error) {
	return []string{"wrong-proof"}, nil
}

func TestAuditRealManagementAndSecretBoundaries(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "droply.db")
	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := server.New(st, t.TempDir(), "droplydoc.com", []byte("audit-key"))
	defer srv.ShutdownAnalytics()
	owner := registerAndGetToken(t, srv, "audit@example.test", "account-secret")
	viewer := registerAndGetToken(t, srv, "viewer@example.test", "viewer-secret")
	if _, err := st.ClaimAdmin(t.Context(), "audit@example.test"); err != nil {
		t.Fatal(err)
	}
	createSubdomain(t, srv, owner, "audited")
	base := "/subdomains/audited/projects/site"
	request := func(token, method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Host = "api.droplydoc.com"
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		return w
	}
	upload := func(token, query string, files map[string]string, status int) {
		t.Helper()
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, buildDeployRequest(t, base+"/deploy"+query, createTestTarGz(t, files), token))
		if w.Code != status {
			t.Fatalf("deploy %d %s", w.Code, w.Body)
		}
	}
	upload(owner, "", map[string]string{"index.html": "A"}, 200)
	upload(owner, "?environment=preview", map[string]string{"index.html": "B"}, 200)
	request(owner, "POST", base+"/promote/2", "", 200)
	request(owner, "POST", base+"/rollback/1", "", 200)
	upload(owner, "?environment=preview", map[string]string{"_redirects": "unsupported", "index.html": "bad"}, 400)
	request(owner, "PUT", base+"/access", `{"password":"visitor-secret","session_ttl":3600}`, 200)
	request(owner, "POST", base+"/domains", `{"domain":"audit.other.test"}`, 201)
	srv.SetDNSResolver(auditDNS{})
	request(owner, "POST", base+"/domains/AUDIT.other.test./verify", "", 200)
	request(owner, "DELETE", base+"/domains/AUDIT.other.test.", "", 204)
	request(owner, "PUT", base+"/members", `{"email":"viewer@example.test","role":"viewer"}`, 200)
	request(viewer, "POST", base+"/rollback/2", "", 403)
	issued := request(owner, "POST", base+"/tokens", `{"name":"ci"}`, 201)
	var token struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(issued.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	upload(token.Token, "?environment=preview", map[string]string{"index.html": "CI"}, 200)
	request(owner, "DELETE", base+"/tokens/"+strconv.FormatInt(token.ID, 10), "", 204)
	response := request(owner, "GET", base+"/audit?limit=100", "", 200)
	var page struct {
		Events []model.AuditEvent `json:"events"`
		Next   int64              `json:"next_cursor"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	actions := map[string]bool{}
	failed, ci := false, false
	for _, event := range page.Events {
		actions[event.Action] = true
		if event.Action == "domain.remove" || event.Action == "domain.verify" {
			if !strings.HasPrefix(event.Target, "domain:") {
				t.Fatalf("lost domain identity: %+v", event)
			}
		}
		if event.Action == "domain.verify" && (event.Result != "failure" || event.StatusCode != 200) {
			t.Fatalf("unverified domain falsely succeeded: %+v", event)
		}
		if event.Result == "pending" {
			t.Fatalf("event not finalized: %+v", event)
		}
		if event.Action == "deployment.create" && event.Result == "failure" {
			failed = true
		}
		if event.ActorKind == "project_token" && event.ActorID == token.ID && event.Result == "success" {
			ci = true
		}
		if event.CreatedAt.IsZero() || event.Target == "" || event.UserID == 0 {
			t.Fatalf("incomplete event %+v", event)
		}
	}
	for _, action := range []string{"deployment.create", "deployment.promote", "deployment.rollback", "access.set", "domain.create", "domain.remove", "member.set", "token.create", "token.revoke"} {
		if !actions[action] {
			t.Fatalf("missing %s: %s", action, response.Body)
		}
	}
	if !failed || !ci {
		t.Fatal("failure or CI actor missing")
	}
	for _, secret := range []string{owner, viewer, token.Token, "account-secret", "visitor-secret", "viewer-secret"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("secret persisted in audit")
		}
	}
	first := request(viewer, "GET", base+"/audit?limit=2", "", 200)
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 || page.Next == 0 {
		t.Fatal("pagination absent")
	}
	cursor := page.Next
	second := request(viewer, "GET", base+"/audit?limit=2&before="+strconv.FormatInt(cursor, 10), "", 200)
	if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	for _, event := range page.Events {
		if event.ID >= cursor {
			t.Fatal("cursor repeated event")
		}
	}
	request(viewer, "GET", "/admin/audit", "", 403)
	request(owner, "GET", "/admin/audit", "", 200)
	request(viewer, "GET", base+"/audit?limit=101", "", 400)
	member, err := st.GetUserByEmail("viewer@example.test")
	if err != nil {
		t.Fatal(err)
	}
	request(owner, "DELETE", base+"/members/"+strconv.FormatInt(member.ID, 10), "", 204)
	request(viewer, "GET", base+"/audit", "", 403)

	// Losing the audit store must prevent a new mutation, not silently omit it.
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TRIGGER fail_cleanup_metadata BEFORE UPDATE OF artifact_state ON deployments WHEN NEW.artifact_state='deleted' BEGIN SELECT RAISE(ABORT,'cleanup metadata fault'); END`); err != nil {
		t.Fatal(err)
	}
	request(owner, "POST", base+"/cleanup?keep=0&days=0", "", 200)
	cleanupPage := request(owner, "GET", base+"/audit?limit=1", "", 200)
	if err := json.Unmarshal(cleanupPage.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].Action != "deployment.cleanup" || page.Events[0].Result != "failure" || page.Events[0].StatusCode != 200 {
		t.Fatalf("cleanup fault falsely succeeded: %s", cleanupPage.Body)
	}
	if _, err := raw.Exec(`DROP TABLE audit_events`); err != nil {
		t.Fatal(err)
	}
	request(owner, "POST", base+"/rollback/2", "", 503)
	sub, _ := st.GetSubdomainByName("audited")
	project, _ := st.GetProject(sub.ID, "site")
	active, err := st.GetActiveDeployment(t.Context(), project.ID)
	if err != nil || active.Version != 1 {
		t.Fatal("audit failure changed production", err)
	}
}

// Simulate losing the database acknowledgment after a durable commit. HTTP 500
// cannot establish whether publication happened, so the audit must stay pending.
type auditCommitFault struct {
	store.Store
}

func (s auditCommitFault) CommitDeployment(ctx context.Context, id int64, files int, size int64, checksum string) error {
	if err := s.Store.CommitDeployment(ctx, id, files, size, checksum); err != nil {
		return err
	}
	return errors.New("commit acknowledgment lost")
}

func TestAuditUnknownPublicationAndPrecommitFailure(t *testing.T) {
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := server.New(auditCommitFault{st}, t.TempDir(), "droplydoc.com", []byte("audit-key"))
	defer srv.ShutdownAnalytics()
	token := registerAndGetToken(t, srv, "fault@example.test", "password-123")
	createSubdomain(t, srv, token, "fault")
	for _, tc := range []struct {
		files  map[string]string
		status int
		result string
	}{
		{map[string]string{"index.html": "bad", "_redirects": "unsupported"}, 400, "failure"},
		{map[string]string{"index.html": "committed"}, 500, "pending"},
	} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, buildDeployRequest(t, "/subdomains/fault/projects/site/deploy", createTestTarGz(t, tc.files), token))
		if w.Code != tc.status {
			t.Fatalf("response=%d %s", w.Code, w.Body)
		}
		events, err := st.ListAuditEvents(t.Context(), 0, 0, 0, 1)
		if err != nil || len(events) != 1 {
			t.Fatalf("events=%+v err=%v", events, err)
		}
		if events[0].Result != tc.result || events[0].StatusCode != tc.status || events[0].ProjectID == 0 || !strings.HasPrefix(events[0].Target, "version:") {
			t.Fatalf("event=%+v", events[0])
		}
	}
	sub, err := st.GetSubdomainByName("fault")
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.GetProject(sub.ID, "site")
	if err != nil {
		t.Fatal(err)
	}
	active, err := st.GetActiveDeployment(t.Context(), project.ID)
	if err != nil || active.Version != 2 {
		t.Fatalf("durable publication=%+v err=%v", active, err)
	}
}
