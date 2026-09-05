package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zhong/droply/internal/model"
)

func TestAuditPendingRetentionAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	st, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	event := model.AuditEvent{ActorKind: "user", ActorID: 1, UserID: 1, ProjectID: 3, SubdomainID: 2, Action: "deployment.create", Target: "project:3"}
	id, err := st.BeginAuditEvent(t.Context(), event)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	list, err := st.ListAuditEvents(t.Context(), 3, 2, 0, 10)
	if err != nil || len(list) != 1 || list[0].Result != "pending" {
		t.Fatalf("restart=%+v %v", list, err)
	}
	if _, err := st.db.Exec(`CREATE TRIGGER fail_audit_finalize BEFORE UPDATE ON audit_events BEGIN SELECT RAISE(ABORT,'storage fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishAuditEvent(t.Context(), id, 3, "version:1", 200, "success"); err == nil {
		t.Fatal("fault ignored")
	}
	list, err = st.ListAuditEvents(t.Context(), 3, 2, 0, 10)
	if err != nil || list[0].Result != "pending" {
		t.Fatal("invented success after failed finalization")
	}
	if _, err := st.db.Exec(`DROP TRIGGER fail_audit_finalize`); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishAuditEvent(t.Context(), id, 3, "version:1", 200, "success"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishAuditEvent(t.Context(), id, 3, "version:2", 500, "failure"); err == nil {
		t.Fatal("final event overwritten")
	}
	if _, err := st.db.Exec(`UPDATE audit_events SET created_at=? WHERE id=?`, time.Now().UTC().AddDate(0, 0, -91).Format(dtLayout), id); err != nil {
		t.Fatal(err)
	}
	shared := event
	shared.ProjectID = 0
	shared.Action = "subdomain.access.set"
	if _, err := st.BeginAuditEvent(t.Context(), shared); err != nil {
		t.Fatal(err)
	}
	other := event
	other.ProjectID = 4
	other.SubdomainID = 5
	if _, err := st.BeginAuditEvent(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	list, err = st.ListAuditEvents(t.Context(), 3, 2, 0, 10)
	if err != nil || len(list) != 2 {
		t.Fatal("project query omitted inherited policy or leaked another project", err)
	}
	removed, err := st.CleanupAuditEvents(t.Context(), 90)
	if err != nil || removed != 1 {
		t.Fatal(removed, err)
	}
	list, err = st.ListAuditEvents(t.Context(), 0, 0, 0, 10)
	if err != nil || len(list) != 2 {
		t.Fatal("retention lost recent rows", err)
	}
}
