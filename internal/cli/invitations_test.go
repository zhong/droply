//go:build integration

package cli

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func TestInvitationCommandRealAPI(t *testing.T) {
	withTempHome(t)
	t.Chdir(t.TempDir())
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "droply.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.BootstrapAdmin(t.Context(), "admin@example.com", string(hash), "admin-token"); err != nil {
		t.Fatal(err)
	}
	app := server.New(st, t.TempDir(), "example.test", []byte("test-key"))
	defer app.ShutdownAnalytics()
	api := httptest.NewServer(app)
	defer api.Close()
	t.Setenv("DROPLY_API_URL", api.URL)
	t.Setenv("DROPLY_TOKEN", "admin-token")
	run := func(args ...string) (string, error) {
		cmd := NewRootCmd("test")
		var out, diagnostics bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&diagnostics)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), err
	}
	output, err := run("invitation", "create", "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(output), &created); err != nil {
		t.Fatal(err)
	}
	if created.Token == "" {
		t.Fatal("missing one-time invitation")
	}
	listed, err := run("invitation", "list")
	if err != nil || !json.Valid([]byte(listed)) || strings.Contains(listed, created.Token) {
		t.Fatalf("list=%q err=%v", listed, err)
	}
	var account struct {
		Token string `json:"api_token"`
	}
	client := NewAPIClient(&Context{APIURL: api.URL})
	body := map[string]string{"email": "member@example.com", "password": "test-password", "invite": created.Token}
	if err := client.doJSONContext(t.Context(), "POST", "/auth/register", body, &account); err != nil {
		t.Fatal(err)
	}
	if err := client.doJSONContext(t.Context(), "POST", "/auth/register", body, nil); err == nil {
		t.Fatal("invitation reused")
	}
	t.Setenv("DROPLY_TOKEN", account.Token)
	if out, err := run("invitation", "list"); err == nil || out != "" {
		t.Fatal("nonadmin listed invitations")
	}
	t.Setenv("DROPLY_TOKEN", "admin-token")
	output, err = run("invitation", "create", "other@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(output), &created); err != nil {
		t.Fatal(err)
	}
	if _, err := run("invitation", "revoke", strconv.FormatInt(created.ID, 10)); err != nil {
		t.Fatal(err)
	}
	body["email"] = "other@example.com"
	body["invite"] = created.Token
	if err := client.doJSONContext(t.Context(), "POST", "/auth/register", body, nil); err == nil {
		t.Fatal("revoked invitation accepted")
	}
}
