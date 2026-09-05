//go:build integration

package server_test

import (
	"html"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
	"github.com/zhong/droply/internal/wework"
	"golang.org/x/crypto/bcrypt"
)

func visitorFixture(t *testing.T) (*server.Server, []string) {
	t.Helper()
	srv, st := consoleFixture(t)
	sub, err := st.GetSubdomainByName("team")
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.GetProject(sub.ID, "site")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("visitor-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutAccessRule(t.Context(), store.AccessRuleInput{
		SubdomainID: sub.ID, ProjectID: &project.ID, PasswordHash: string(hash), SessionTTL: 3600,
		WeWorkEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	// Only the login link is exercised; no external OAuth requests are needed.
	srv.SetWeWork(wework.NewClient(wework.Config{CorpID: "visitor-test", AgentID: "test", Secret: "fixture-only"}))
	if _, err := st.CreateCustomDomain(project.ID, "custom.visitor.localhost"); err != nil {
		t.Fatal(err)
	}
	if err := st.VerifyCustomDomain("custom.visitor.localhost"); err != nil {
		t.Fatal(err)
	}
	deployments, err := st.ListDeployments(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview := ""
	for _, deployment := range deployments {
		if deployment.Environment == "preview" {
			preview = deployment.PreviewLabel
		}
	}
	if preview == "" {
		t.Fatal("missing preview fixture")
	}
	return srv, []string{
		project.HostLabel + ".console.localhost",
		"team.console.localhost",
		preview + ".console.localhost",
		"custom.visitor.localhost",
	}
}

func TestVisitorAssetsAndLoginRetry(t *testing.T) {
	srv, hosts := visitorFixture(t)
	for _, tc := range []struct {
		name, method, path string
		status             int
	}{
		{name: "script before login", method: "GET", path: "/_droply/ui/visitor.js", status: 200},
		{name: "stylesheet before login", method: "GET", path: "/_droply/ui/visitor.css", status: 200},
		{name: "head", method: "HEAD", path: "/_droply/ui/visitor.js", status: 200},
		{name: "template hidden", method: "GET", path: "/_droply/ui/index.html", status: 404},
		{name: "no listing", method: "GET", path: "/_droply/ui/", status: 404},
		{name: "no source", method: "GET", path: "/_droply/ui/main.tsx", status: 404},
		{name: "no traversal", method: "GET", path: "/_droply/ui/%2e%2e/index.html", status: 404},
		{name: "no writes", method: "POST", path: "/_droply/ui/visitor.js", status: 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "https://"+hosts[0]+tc.path, nil)
			response := httptest.NewRecorder()
			srv.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d", response.Code, tc.status)
			}
			if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
				t.Fatal("UI response must not be cached")
			}
			if len(response.Result().Cookies()) != 0 {
				t.Fatal("assets must not authenticate a visitor")
			}
			if tc.method == "HEAD" && response.Body.Len() != 0 {
				t.Fatal("HEAD returned body")
			}
		})
	}
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			destination := "/?lang=zh&next=%2Fintro%3Fa%3D1"
			if host == "team.console.localhost" {
				destination = "/site" + destination
			}
			form := url.Values{"password": {"incorrect"}, "host": {host}, "redirect": {destination}}
			request := httptest.NewRequest("POST", "https://"+host+"/_droply/login", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			srv.ServeHTTP(response, request)
			if response.Code != 200 {
				t.Fatal(response.Code, response.Body.String())
			}
			body := response.Body.String()
			if !strings.Contains(body, "密码不正确，请重试。") {
				t.Fatal("missing login error")
			}
			if !strings.Contains(response.Header().Get("Content-Security-Policy"), "form-action 'self'") {
				t.Fatal("missing form CSP")
			}
			match := regexp.MustCompile(`name="redirect"\s+value="([^"]+)"`).FindStringSubmatch(body)
			if len(match) != 2 || html.UnescapeString(match[1]) != destination {
				t.Fatalf("retry changed destination: %v", match)
			}
			if strings.Contains(body, "visitor-password") || len(response.Result().Cookies()) != 0 {
				t.Fatal("incorrect login leaked credentials")
			}
			form.Set("password", "visitor-password")
			form.Set("redirect", html.UnescapeString(match[1]))
			request = httptest.NewRequest("POST", "https://"+host+"/_droply/login", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response = httptest.NewRecorder()
			srv.ServeHTTP(response, request)
			if response.Code != 302 || response.Header().Get("Location") != destination {
				t.Fatal("retry did not return to original URL", response.Code, response.Header())
			}
		})
	}
}

func TestVisitorBrowser(t *testing.T) {
	if os.Getenv("DROPLY_VISITOR_BROWSER") != "1" {
		t.Skip("set DROPLY_VISITOR_BROWSER=1 and DROPLY_PLAYWRIGHT_PATH for Chromium acceptance")
	}
	srv, hosts := visitorFixture(t)
	listener := httptest.NewTLSServer(srv)
	defer listener.Close()
	port := strings.TrimPrefix(listener.URL, "https://127.0.0.1:")
	script, err := filepath.Abs("../../console/tests/visitor.mjs")
	if err != nil {
		t.Fatal(err)
	}
	args := []string{script}
	for _, host := range hosts {
		args = append(args, "https://"+host+":"+port)
	}
	cmd := exec.CommandContext(t.Context(), "node", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser: %v\n%s", err, output)
	}
	t.Log(string(output))
}
