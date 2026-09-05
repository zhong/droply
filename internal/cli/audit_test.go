//go:build integration

package cli

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/zhong/droply/internal/model"
)

func TestAuditCommandRealAPI(t *testing.T) {
	run, selectUser, _ := deploymentCommandFixture(t)
	output, err := run("audit", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Events []model.AuditEvent `json:"events"`
		Next   int64              `json:"next_cursor"`
	}
	if err := json.Unmarshal([]byte(output), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].Action != "deployment.create" || page.Events[0].Result != "success" || page.Next == 0 {
		t.Fatalf("audit page: %s", output)
	}
	first := page.Events[0].ID
	output, err = run("audit", "--limit", "1", "--before", strconv.FormatInt(page.Next, 10))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(output), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].ID >= first {
		t.Fatalf("cursor: %s", output)
	}
	for _, args := range [][]string{{"audit", "--admin"}, {"audit", "--limit", "101"}, {"audit", "--before", "-1"}} {
		if out, err := run(args...); err == nil || out != "" {
			t.Fatalf("invalid/unauthorized query %v: %s %v", args, out, err)
		}
	}
	selectUser("stranger")
	if out, err := run("audit"); err == nil || out != "" {
		t.Fatal("stranger read audit")
	}
}
