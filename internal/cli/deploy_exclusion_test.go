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

func TestCreateTarGzExcludesConfiguredPathsAndFiles(t *testing.T) {
	projectRoot := t.TempDir()
	srcDir := filepath.Join(projectRoot, "dist")

	files := map[string]string{
		"dist/index.html":               "<html>home</html>",
		"dist/assets/app.js":            "console.log('ok')",
		"dist/assets/draft.html":        "draft asset",
		"dist/private/secret.txt":       "secret",
		"dist/public/secret.txt":        "top secret",
		"dist/content/draft.html":       "draft page",
		"dist/content/keep.html":        "keep me",
		"dist/robots-local.txt":         "disallow",
		"dist/nested/robots.txt":        "allow",
		"public/outside.txt":            "outside deploy dir",
		"public/secret.txt":             "configured file outside deploy dir",
		"docs/robots-local.txt":         "outside file name match",
		"docs/draft.html":               "outside file name match",
		"dist/content/robots-local.txt": "exclude by file name",
	}

	for rel, content := range files {
		path := filepath.Join(projectRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	var buf bytes.Buffer
	count, err := createTarGz(&buf, srcDir, projectRoot, &ProjectConfig{
		ExcludePaths: []string{"dist/private", "dist/public/secret.txt"},
		ExcludeFiles: []string{"draft.html", "robots-local.txt"},
	})
	if err != nil {
		t.Fatalf("createTarGz: %v", err)
	}

	if count != 4 {
		t.Fatalf("expected 4 files packaged, got %d", count)
	}

	found := readTarEntries(t, &buf)

	expectedIncluded := map[string]string{
		"index.html":        "<html>home</html>",
		"assets/app.js":     "console.log('ok')",
		"content/keep.html": "keep me",
		"nested/robots.txt": "allow",
	}

	for name, expected := range expectedIncluded {
		content, ok := found[name]
		if !ok {
			t.Fatalf("expected %q in archive", name)
		}
		if content != expected {
			t.Fatalf("expected %q content %q, got %q", name, expected, content)
		}
	}

	excluded := []string{
		"assets/draft.html",
		"private/secret.txt",
		"public/secret.txt",
		"content/draft.html",
		"robots-local.txt",
		"content/robots-local.txt",
	}
	for _, name := range excluded {
		if _, ok := found[name]; ok {
			t.Fatalf("did not expect %q in archive", name)
		}
	}
}

func readTarEntries(t *testing.T, buf *bytes.Buffer) map[string]string {
	t.Helper()

	gz, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
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
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", hdr.Name, err)
		}
		found[hdr.Name] = string(data)
	}

	return found
}
