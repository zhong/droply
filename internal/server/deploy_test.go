package server_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
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
	req.Host = "api.droplydoc.com"
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
	srv := server.New(st, sitesDir, "droplydoc.com", []byte("test-hmac-key-for-testing-1234"))
	return srv, sitesDir
}

func TestDeploy(t *testing.T) {
	srv, _ := newDeployTestServer(t)
	token := registerAndGetToken(t, srv, "alice@example.com", "password123")

	// Create subdomain first.
	body, _ := json.Marshal(map[string]string{"name": "alice"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Host = "api.droplydoc.com"
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

	// The primary URL is rooted at the project host; the old URL stays explicit.
	url, _ := resp["url"].(string)
	if url == "" {
		t.Fatal("expected non-empty url")
	}
	if url != resp["project_url"] || resp["legacy_url"] != "https://alice.droplydoc.com/mysite" {
		t.Fatalf("unexpected project/legacy URLs: %v", resp)
	}

	// Verify files exist on disk.
	if _, err := readSiteFile(srv, "alice.droplydoc.com", "/mysite/index.html"); err != nil {
		t.Fatalf("index.html not on disk: %v", err)
	}
	if _, err := readSiteFile(srv, "alice.droplydoc.com", "/mysite/style.css"); err != nil {
		t.Fatalf("style.css not on disk: %v", err)
	}
}

func TestRedeployOverwritesFiles(t *testing.T) {
	srv, _ := newDeployTestServer(t)
	token := registerAndGetToken(t, srv, "redeploy@example.com", "password123")

	// Create subdomain.
	body, _ := json.Marshal(map[string]string{"name": "redeployer"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// First deploy with original content.
	archive1 := createTestTarGz(t, map[string]string{
		"index.html": "<html><body>Version 1</body></html>",
		"style.css":  "body { color: red; }",
	})
	req = buildDeployRequest(t, "/subdomains/redeployer/projects/mysite/deploy", archive1, token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("first deploy: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp1 map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp1)
	if int(resp1["version"].(float64)) != 1 {
		t.Fatalf("first deploy: expected version=1, got %v", resp1["version"])
	}
	if int(resp1["file_count"].(float64)) != 2 {
		t.Fatalf("first deploy: expected file_count=2, got %v", resp1["file_count"])
	}

	// Verify first deploy content.
	content1, err := readSiteFile(srv, "redeployer.droplydoc.com", "/mysite/index.html")
	if err != nil {
		t.Fatalf("read index.html after first deploy: %v", err)
	}
	if string(content1) != "<html><body>Version 1</body></html>" {
		t.Fatalf("first deploy content mismatch: got %q", string(content1))
	}

	// Second deploy with UPDATED content.
	archive2 := createTestTarGz(t, map[string]string{
		"index.html": "<html><body>Version 2 UPDATED</body></html>",
		"style.css":  "body { color: blue; }",
	})
	req = buildDeployRequest(t, "/subdomains/redeployer/projects/mysite/deploy", archive2, token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("second deploy: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp2 map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp2)
	if int(resp2["version"].(float64)) != 2 {
		t.Fatalf("second deploy: expected version=2, got %v", resp2["version"])
	}
	if int(resp2["file_count"].(float64)) != 2 {
		t.Fatalf("second deploy: expected file_count=2, got %v", resp2["file_count"])
	}

	// Verify files were ACTUALLY overwritten with new content.
	content2, err := readSiteFile(srv, "redeployer.droplydoc.com", "/mysite/index.html")
	if err != nil {
		t.Fatalf("read index.html after second deploy: %v", err)
	}
	if string(content2) != "<html><body>Version 2 UPDATED</body></html>" {
		t.Fatalf("REDEPLOY BUG: index.html was NOT updated!\n  expected: %q\n  got:      %q", "<html><body>Version 2 UPDATED</body></html>", string(content2))
	}

	cssContent, err := readSiteFile(srv, "redeployer.droplydoc.com", "/mysite/style.css")
	if err != nil {
		t.Fatalf("read style.css after second deploy: %v", err)
	}
	if string(cssContent) != "body { color: blue; }" {
		t.Fatalf("REDEPLOY BUG: style.css was NOT updated!\n  expected: %q\n  got:      %q", "body { color: blue; }", string(cssContent))
	}
}

// createTarGzWithFileInfoHeader creates a tar.gz using tar.FileInfoHeader,
// exactly like the real CLI client does, to test compatibility.
func createTarGzWithFileInfoHeader(t *testing.T, files map[string]string) *bytes.Buffer {
	t.Helper()

	// Write files to a temp dir first, then tar them using FileInfoHeader.
	tmpDir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(tmpDir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for name := range files {
		p := filepath.Join(tmpDir, name)
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			t.Fatalf("FileInfoHeader %s: %v", name, err)
		}
		hdr.Name = name
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %s: %v", name, err)
		}
		f, err := os.Open(p)
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		if _, err := io.Copy(tw, f); err != nil {
			f.Close()
			t.Fatalf("copy %s: %v", name, err)
		}
		f.Close()
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return &buf
}

func TestDeployWithFileInfoHeader(t *testing.T) {
	srv, _ := newDeployTestServer(t)
	token := registerAndGetToken(t, srv, "fih@example.com", "password123")

	body, _ := json.Marshal(map[string]string{"name": "fihtest"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Host = "api.droplydoc.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// Create tar.gz using FileInfoHeader (like the real client).
	archive := createTarGzWithFileInfoHeader(t, map[string]string{
		"index.html": "<html><body>FileInfoHeader test</body></html>",
		"style.css":  "body { color: green; }",
		"i18n.js":    "const i18n = {};",
	})

	req = buildDeployRequest(t, "/subdomains/fihtest/projects/site/deploy", archive, token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("deploy: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp)
	t.Logf("deploy response: %+v", resp)

	fileCount := int(resp["file_count"].(float64))
	if fileCount != 3 {
		t.Fatalf("expected file_count=3, got %d — FileInfoHeader tar not compatible with extractTarGz!", fileCount)
	}

	// Verify files on disk.
	content, err := readSiteFile(srv, "fihtest.droplydoc.com", "/site/index.html")
	if err != nil {
		t.Fatalf("index.html not on disk: %v", err)
	}
	if string(content) != "<html><body>FileInfoHeader test</body></html>" {
		t.Fatalf("content mismatch: %q", string(content))
	}
}

func TestDeployAutoCreatesProject(t *testing.T) {
	srv, _ := newDeployTestServer(t)
	token := registerAndGetToken(t, srv, "bob@example.com", "password123")

	// Create subdomain.
	body, _ := json.Marshal(map[string]string{"name": "bobsite"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Host = "api.droplydoc.com"
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
	req.Host = "api.droplydoc.com"
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

// Observe published content through the supported HTTP boundary.
func readSiteFile(srv *server.Server, host, path string) ([]byte, error) {
	req := httptest.NewRequest("GET", path, nil)
	req.Host = host
	w := httptest.NewRecorder()
	srv.NewSiteHandler().ServeHTTP(w, req)
	if w.Code == 301 && len(path) >= 10 && path[len(path)-10:] == "index.html" {
		return readSiteFile(srv, host, path[:len(path)-10])
	}
	if w.Code != 200 {
		return nil, fmt.Errorf("site returned %d: %s", w.Code, w.Body.String())
	}
	return w.Body.Bytes(), nil
}
