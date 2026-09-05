package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginUsesCommandStreams(t *testing.T) {
	withTempHome(t)
	t.Chdir(t.TempDir())
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var credentials map[string]string
		if err := json.NewDecoder(r.Body).Decode(&credentials); err != nil {
			t.Error(err)
		}
		if credentials["email"] != "user@example.test" || credentials["password"] != "password" {
			t.Errorf("unexpected credentials")
		}
		io.WriteString(w, `{"api_token":"saved-token"}`)
	}))
	defer api.Close()
	cmd := NewRootCmd("test")
	var out, diagnostics bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&diagnostics)
	cmd.SetIn(strings.NewReader("user@example.test\npassword\n"))
	cmd.SetArgs([]string{"login", "--context", "test", "--api-url", api.URL})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "Logged in successfully on context") || diagnostics.String() != "Email: Password: " {
		t.Fatalf("stdout=%q stderr=%q", out.String(), diagnostics.String())
	}
	if mustLoadFullConfig(t).Contexts["test"].Token != "saved-token" {
		t.Fatal("token not saved")
	}
}
