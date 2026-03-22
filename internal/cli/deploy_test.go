package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateTarGzFromDot(t *testing.T) {
	// Regression test: createTarGz with srcDir="." must not skip the
	// root directory as a hidden directory. The root "." has Name()="."
	// which starts with ".", but it must be handled as the root entry,
	// not as a hidden directory.
	tmpDir := t.TempDir()

	// Create test files.
	files := map[string]string{
		"index.html": "<html><body>Hello</body></html>",
		"style.css":  "body { color: red; }",
		"app.js":     "console.log('hello');",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Also create a hidden file that should be excluded.
	os.WriteFile(filepath.Join(tmpDir, ".droply.toml"), []byte("subdomain = \"test\""), 0644)

	// Save original working directory and change to tmpDir.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	var buf bytes.Buffer
	count, err := createTarGz(&buf, ".")
	if err != nil {
		t.Fatalf("createTarGz: %v", err)
	}

	if count != 3 {
		t.Fatalf("expected 3 files packaged, got %d", count)
	}

	// Verify tar contents.
	gz, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	found := make(map[string]string)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		data, _ := io.ReadAll(tr)
		found[hdr.Name] = string(data)
	}

	for name, expected := range files {
		content, ok := found[name]
		if !ok {
			t.Errorf("file %q missing from tar", name)
			continue
		}
		if content != expected {
			t.Errorf("file %q: expected %q, got %q", name, expected, content)
		}
	}

	// Hidden file should not be in the tar.
	if _, ok := found[".droply.toml"]; ok {
		t.Error(".droply.toml should be excluded from tar")
	}
}

func TestCreateTarGzExcludesHiddenDirs(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a visible file and a hidden directory with a file.
	os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte("hello"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, ".hidden"), 0755)
	os.WriteFile(filepath.Join(tmpDir, ".hidden", "secret.txt"), []byte("secret"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "public"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "public", "page.html"), []byte("page"), 0644)

	var buf bytes.Buffer
	count, err := createTarGz(&buf, tmpDir)
	if err != nil {
		t.Fatalf("createTarGz: %v", err)
	}

	if count != 2 {
		t.Fatalf("expected 2 files, got %d", count)
	}
}
