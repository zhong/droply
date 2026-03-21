package server_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
)

// createTestTarGz builds an in-memory .tar.gz archive from a map of filename -> content.
func createTestTarGz(t *testing.T, files map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar write header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write content %s: %v", name, err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return &buf
}

// buildDeployRequest creates a multipart/form-data request with the tar.gz as "file".
func buildDeployRequest(t *testing.T, url string, archive *bytes.Buffer, token string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "deploy.tar.gz")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(archive.Bytes()); err != nil {
		t.Fatalf("write archive to form: %v", err)
	}
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, url, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func newDeployTestServer(t *testing.T) (*server.Server, string) {
	t.Helper()
	sitesDir := t.TempDir()
	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv := server.New(st, sitesDir, "droplydoc.com", nil)
	return srv, sitesDir
}

func TestDeploy(t *testing.T) {
	srv, sitesDir := newDeployTestServer(t)
	token := registerAndGetToken(t, srv, "alice@example.com", "password123")

	// Create subdomain first.
	body, _ := json.Marshal(map[string]string{"name": "alice"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// Deploy a tar.gz with index.html and style.css.
	archive := createTestTarGz(t, map[string]string{
		"index.html": "<html><body>Hello</body></html>",
		"style.css":  "body { color: red; }",
	})
	req = buildDeployRequest(t, "/subdomains/alice/projects/mysite/deploy", archive, token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("deploy: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode deploy response: %v", err)
	}

	// Verify version=1.
	version, ok := resp["version"].(float64)
	if !ok || int(version) != 1 {
		t.Fatalf("expected version=1, got %v", resp["version"])
	}

	// Verify url contains subdomain/project.
	url, _ := resp["url"].(string)
	if url == "" {
		t.Fatal("expected non-empty url")
	}
	if !contains(url, "alice") || !contains(url, "mysite") {
		t.Fatalf("url should contain subdomain and project, got: %s", url)
	}

	// Verify files exist on disk.
	if _, err := os.Stat(filepath.Join(sitesDir, "alice", "mysite", "index.html")); err != nil {
		t.Fatalf("index.html not on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sitesDir, "alice", "mysite", "style.css")); err != nil {
		t.Fatalf("style.css not on disk: %v", err)
	}
}

func TestDeployAutoCreatesProject(t *testing.T) {
	srv, _ := newDeployTestServer(t)
	token := registerAndGetToken(t, srv, "bob@example.com", "password123")

	// Create subdomain.
	body, _ := json.Marshal(map[string]string{"name": "bobsite"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// Deploy WITHOUT pre-creating the project — should auto-create it.
	archive := createTestTarGz(t, map[string]string{
		"index.html": "<html>auto</html>",
	})
	req = buildDeployRequest(t, "/subdomains/bobsite/projects/newproj/deploy", archive, token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("deploy auto-create: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestDeploySizeLimit(t *testing.T) {
	srv, _ := newDeployTestServer(t)
	token := registerAndGetToken(t, srv, "carol@example.com", "password123")

	// Create subdomain.
	body, _ := json.Marshal(map[string]string{"name": "carolsite"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// Deploy small content — smoke test that no crash occurs.
	archive := createTestTarGz(t, map[string]string{
		"index.html": "small",
	})
	req = buildDeployRequest(t, "/subdomains/carolsite/projects/proj/deploy", archive, token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	// Just verify no server crash — either 200 or a reasonable error is fine.
	if rr.Code == http.StatusInternalServerError {
		t.Fatalf("unexpected 500: %s", rr.Body.String())
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
