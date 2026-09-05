package store

import (
	"testing"
)

func createTestSubdomain(t *testing.T, st *SQLiteStore) int64 {
	t.Helper()
	user, err := st.CreateUser("test@example.com", "hash", "dp_test_token")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	sub, err := st.CreateSubdomain(user.ID, "mysub")
	if err != nil {
		t.Fatalf("create subdomain: %v", err)
	}
	return sub.ID
}

func TestRecordVisit(t *testing.T) {
	st := newTestStore(t)
	subID := createTestSubdomain(t, st)

	err := st.RecordVisit(subID, "blog", "/hello", "1.2.3.4", "https://google.com", "Mozilla/5.0")
	if err != nil {
		t.Fatalf("RecordVisit: %v", err)
	}

	logs, total, err := st.GetVisitLogs(subID, "blog", 10, 0, "")
	if err != nil {
		t.Fatalf("GetVisitLogs: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1 log, got %d", total)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(logs))
	}
	if logs[0].Path != "/hello" {
		t.Fatalf("expected path /hello, got %s", logs[0].Path)
	}
	if logs[0].IP != "1.2.3.4" {
		t.Fatalf("expected IP 1.2.3.4, got %s", logs[0].IP)
	}
}

func TestRecordVisitUV(t *testing.T) {
	st := newTestStore(t)
	subID := createTestSubdomain(t, st)

	st.RecordVisit(subID, "blog", "/hello", "1.2.3.4", "", "")
	st.RecordVisit(subID, "blog", "/hello", "1.2.3.4", "", "")
	st.RecordVisit(subID, "blog", "/hello", "5.6.7.8", "", "")

	stats, err := st.GetPageStats(subID, "blog", "all")
	if err != nil {
		t.Fatalf("GetPageStats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 page, got %d", len(stats))
	}
	if stats[0].PV != 3 {
		t.Fatalf("expected PV=3, got %d", stats[0].PV)
	}
	if stats[0].UV != 2 {
		t.Fatalf("expected UV=2, got %d", stats[0].UV)
	}
}

func TestGetPageStatsPeriod(t *testing.T) {
	st := newTestStore(t)
	subID := createTestSubdomain(t, st)

	st.RecordVisit(subID, "blog", "/", "1.2.3.4", "", "")
	st.RecordVisit(subID, "blog", "/about", "1.2.3.4", "", "")

	stats, err := st.GetPageStats(subID, "blog", "7d")
	if err != nil {
		t.Fatalf("GetPageStats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(stats))
	}
}

func TestGetPageStatsEmpty(t *testing.T) {
	st := newTestStore(t)
	subID := createTestSubdomain(t, st)

	stats, err := st.GetPageStats(subID, "blog", "all")
	if err != nil {
		t.Fatalf("GetPageStats: %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("expected 0 pages for unvisited project, got %d", len(stats))
	}
}

func TestGetVisitLogsWithPathFilter(t *testing.T) {
	st := newTestStore(t)
	subID := createTestSubdomain(t, st)

	st.RecordVisit(subID, "blog", "/hello", "1.2.3.4", "", "")
	st.RecordVisit(subID, "blog", "/world", "1.2.3.4", "", "")
	st.RecordVisit(subID, "blog", "/hello/world", "1.2.3.4", "", "")

	logs, total, err := st.GetVisitLogs(subID, "blog", 10, 0, "/hello")
	if err != nil {
		t.Fatalf("GetVisitLogs: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2 logs matching /hello, got %d", total)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(logs))
	}
}

func TestGetVisitLogsPagination(t *testing.T) {
	st := newTestStore(t)
	subID := createTestSubdomain(t, st)

	for range 5 {
		st.RecordVisit(subID, "blog", "/page", "1.2.3.4", "", "")
	}

	logs, total, err := st.GetVisitLogs(subID, "blog", 2, 0, "")
	if err != nil {
		t.Fatalf("GetVisitLogs: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected total=5, got %d", total)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 log entries (limit), got %d", len(logs))
	}
}

func TestCleanupVisitLogsRetention(t *testing.T) {
	st := newTestStore(t)
	subID := createTestSubdomain(t, st)

	st.RecordVisit(subID, "blog", "/hello", "1.2.3.4", "", "")

	// With 30-day retention, recent visits should NOT be deleted.
	n, err := st.CleanupVisitLogs(30)
	if err != nil {
		t.Fatalf("CleanupVisitLogs: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 deleted (within retention), got %d", n)
	}

	_, total, err := st.GetVisitLogs(subID, "blog", 10, 0, "")
	if err != nil {
		t.Fatalf("GetVisitLogs: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1 log remaining, got %d", total)
	}
}

func TestRecordVisitDifferentProjects(t *testing.T) {
	st := newTestStore(t)
	subID := createTestSubdomain(t, st)

	st.RecordVisit(subID, "blog", "/", "1.2.3.4", "", "")
	st.RecordVisit(subID, "docs", "/", "1.2.3.4", "", "")

	blogStats, _ := st.GetPageStats(subID, "blog", "all")
	if len(blogStats) != 1 || blogStats[0].PV != 1 {
		t.Fatalf("expected blog PV=1, got %v", blogStats)
	}

	docsStats, _ := st.GetPageStats(subID, "docs", "all")
	if len(docsStats) != 1 || docsStats[0].PV != 1 {
		t.Fatalf("expected docs PV=1, got %v", docsStats)
	}
}
