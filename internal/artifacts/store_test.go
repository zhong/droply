package artifacts_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zhong/droply/internal/artifacts"
)

func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestStagePublishAndVerify(t *testing.T) {
	store, err := artifacts.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	info, err := store.Stage(t.Context(), "artifact-one", bytes.NewReader(archive(t, map[string]string{"index.html": "hello", "assets/style.css": "body {}"})), artifacts.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if info.FileCount != 2 || info.TotalSize != 12 || info.Checksum == "" {
		t.Fatalf("bad manifest info: %+v", info)
	}
	if err := store.Publish("artifact-one"); err != nil {
		t.Fatal(err)
	}
	verified, err := store.Verify(t.Context(), "artifact-one")
	if err != nil {
		t.Fatal(err)
	}
	if verified != info {
		t.Fatalf("verified = %+v, want %+v", verified, info)
	}
}

func rawArchive(t *testing.T, headers []tar.Header, contents []string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for i, h := range headers {
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if i < len(contents) {
			if _, err := io.WriteString(tw, contents[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestRejectUnsafeAndUnsupportedArchives(t *testing.T) {
	cases := []struct {
		name     string
		headers  []tar.Header
		contents []string
	}{
		{"traversal", []tar.Header{{Name: "../escape", Size: 1}}, []string{"x"}},
		{"absolute", []tar.Header{{Name: "/absolute", Size: 1}}, []string{"x"}},
		{"nested traversal", []tar.Header{{Name: "a/../escape", Size: 1}}, []string{"x"}},
		{"backslash", []tar.Header{{Name: `a\escape`, Size: 1}}, []string{"x"}},
		{"windows absolute", []tar.Header{{Name: "C:/escape", Size: 1}}, []string{"x"}},
		{"symlink", []tar.Header{{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../outside"}}, nil},
		{"hardlink", []tar.Header{{Name: "link", Typeflag: tar.TypeLink, Linkname: "index.html"}}, nil},
		{"device", []tar.Header{{Name: "device", Typeflag: tar.TypeChar}}, nil},
		{"fifo", []tar.Header{{Name: "pipe", Typeflag: tar.TypeFifo}}, nil},
		{"duplicate file", []tar.Header{{Name: "same", Size: 1}, {Name: "same", Size: 1}}, []string{"a", "b"}},
		{"duplicate dir", []tar.Header{{Name: "dir/", Typeflag: tar.TypeDir}, {Name: "dir/", Typeflag: tar.TypeDir}}, nil},
		{"file parent", []tar.Header{{Name: "parent", Size: 1}, {Name: "parent/child", Size: 1}}, []string{"a", "b"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := artifacts.New(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.Stage(t.Context(), "bad", bytes.NewReader(rawArchive(t, test.headers, test.contents)), artifacts.Limits{}); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			if err = store.Publish("bad"); err == nil {
				t.Fatal("failed stage published")
			}
			if _, err = os.Stat(store.Path("bad")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed stage visible: %v", err)
			}
			if err = store.RemoveStage("bad"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestArchiveLimitsAndTrailer(t *testing.T) {
	valid := archive(t, map[string]string{"index.html": "hello"})
	corrupted := bytes.Clone(valid)
	corrupted[len(corrupted)-8] ^= 0xff
	extra := append(bytes.Clone(valid), archive(t, map[string]string{"hidden": "data"})...)
	cases := []struct {
		name   string
		data   []byte
		limits artifacts.Limits
	}{
		{"byte limit", valid, artifacts.Limits{MaxBytes: 4}},
		{"file limit", archive(t, map[string]string{"a": "a", "b": "b"}), artifacts.Limits{MaxFiles: 1}},
		{"implicit directory limit", archive(t, map[string]string{"a/b/c": "x"}), artifacts.Limits{MaxFiles: 2}},
		{"empty directory limit", rawArchive(t, []tar.Header{{Name: "a/", Typeflag: tar.TypeDir}, {Name: "b/", Typeflag: tar.TypeDir}}, nil), artifacts.Limits{MaxFiles: 1}},
		{"gzip checksum", corrupted, artifacts.Limits{}},
		{"gzip truncated footer", valid[:len(valid)-8], artifacts.Limits{}},
		{"gzip truncated payload", valid[:len(valid)/2], artifacts.Limits{}},
		{"second tar stream", extra, artifacts.Limits{}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store, err := artifacts.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.Stage(t.Context(), "bad", bytes.NewReader(test.data), test.limits); err == nil {
				t.Fatal("bad archive accepted")
			}
			if err = store.Publish("bad"); err == nil {
				t.Fatal("bad archive published")
			}
		})
	}
	// Padding is bounded even when compressed to a tiny archive.
	var bomb bytes.Buffer
	gz := gzip.NewWriter(&bomb)
	gz.Write(make([]byte, 40000))
	gz.Close()
	store, err := artifacts.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Stage(t.Context(), "padding", &bomb, artifacts.Limits{MaxBytes: 1, MaxFiles: 1}); err == nil {
		t.Fatal("unbounded trailing padding accepted")
	}
}

func TestSafeIDsAndExclusivePublication(t *testing.T) {
	root := t.TempDir()
	store, err := artifacts.New(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", ".", "..", "../outside", "/tmp/escape", "a/b", `a\b`, "a.b", strings.Repeat("x", 129)} {
		if _, err = store.Stage(t.Context(), id, bytes.NewReader(nil), artifacts.Limits{}); err == nil {
			t.Fatalf("ID accepted: %q", id)
		}
		if store.Path(id) != "" {
			t.Fatalf("unsafe path for %q", id)
		}
		if err = store.Remove(id); err == nil {
			t.Fatalf("unsafe removal for %q", id)
		}
	}
	data := archive(t, map[string]string{"index.html": "one"})
	if _, err = store.Stage(t.Context(), "fixed", bytes.NewReader(data), artifacts.Limits{}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Stage(t.Context(), "fixed", bytes.NewReader(data), artifacts.Limits{}); err == nil {
		t.Fatal("stage overwritten")
	}
	if err = store.Publish("fixed"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Stage(t.Context(), "fixed", bytes.NewReader(data), artifacts.Limits{}); err == nil {
		t.Fatal("published ID reused")
	}
	got, err := os.ReadFile(filepath.Join(store.Path("fixed"), "index.html"))
	if err != nil || string(got) != "one" {
		t.Fatalf("published changed: %q %v", got, err)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	for _, mutation := range []string{"changed", "missing", "extra", "extra-dir", "symlink", "manifest", "metadata"} {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			store, err := artifacts.New(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.Stage(t.Context(), "one", bytes.NewReader(archive(t, map[string]string{"index.html": "hello"})), artifacts.Limits{}); err != nil {
				t.Fatal(err)
			}
			if err = store.Publish("one"); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(store.Path("one"), "index.html")
			switch mutation {
			case "changed":
				os.Chmod(file, 0600)
				err = os.WriteFile(file, []byte("world"), 0600)
			case "missing":
				err = os.Remove(file)
			case "extra":
				err = os.WriteFile(filepath.Join(store.Path("one"), "extra"), nil, 0600)
			case "extra-dir":
				err = os.Mkdir(filepath.Join(store.Path("one"), "extra"), 0700)
			case "symlink":
				if err = os.Remove(file); err == nil {
					err = os.Symlink(filepath.Join(t.TempDir(), "outside"), file)
				}
			case "manifest":
				manifest := filepath.Join(root, "one", "manifest.json")
				os.Chmod(manifest, 0600)
				err = os.WriteFile(manifest, []byte(`{"version":99,"entries":[]}`), 0600)
			case "metadata":
				err = os.WriteFile(filepath.Join(root, "one", "unexpected"), nil, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.Verify(t.Context(), "one"); err == nil {
				t.Fatal("tampered artifact verified")
			}
		})
	}
}

func TestImportCopiesAndRejectsLinks(t *testing.T) {
	legacy := t.TempDir()
	if err := os.Mkdir(filepath.Join(legacy, "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(legacy, "index.html")
	if err := os.WriteFile(source, []byte("legacy"), 0644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store, err := artifacts.New(root)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := store.Import(t.Context(), "imported", legacy, artifacts.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if imported.FileCount != 1 || imported.TotalSize != 6 {
		t.Fatal(imported)
	}
	if err = store.Publish("imported"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(source, []byte("edited"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Verify(t.Context(), "imported"); err != nil {
		t.Fatal("legacy edit changed copy", err)
	}
	if got, err := os.ReadFile(filepath.Join(store.Path("imported"), "index.html")); err != nil || string(got) != "legacy" {
		t.Fatalf("copy changed: %q %v", got, err)
	}
	if err = os.Symlink(source, filepath.Join(legacy, "symbolic")); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Import(t.Context(), "symlink", legacy, artifacts.Limits{}); err == nil {
		t.Fatal("legacy symlink imported")
	}
	if err = os.Remove(filepath.Join(legacy, "symbolic")); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(source, filepath.Join(legacy, "hard")); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Import(t.Context(), "hardlink", legacy, artifacts.Limits{}); err == nil {
		t.Fatal("legacy hardlink imported")
	}
}

type callbackReader struct {
	reader   io.Reader
	once     sync.Once
	callback func()
}

func (r *callbackReader) Read(p []byte) (int, error) { r.once.Do(r.callback); return r.reader.Read(p) }

func TestCancellationAndDiskFailureLeaveRecoverableStage(t *testing.T) {
	root := t.TempDir()
	store, err := artifacts.New(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	input := &callbackReader{reader: bytes.NewReader(archive(t, map[string]string{"a": "a"})), callback: cancel}
	if _, err = store.Stage(ctx, "canceled", input, artifacts.Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
	if err = store.Publish("canceled"); err == nil {
		t.Fatal("canceled stage published")
	}
	diskInput := &callbackReader{reader: bytes.NewReader(archive(t, map[string]string{"a": "a"})), callback: func() {
		if err := os.Mkdir(filepath.Join(root, ".staging", "disk", "manifest.json"), 0700); err != nil {
			t.Fatal(err)
		}
	}}
	if _, err = store.Stage(t.Context(), "disk", diskInput, artifacts.Limits{}); err == nil {
		t.Fatal("manifest disk failure ignored")
	}
	if err = store.Publish("disk"); err == nil {
		t.Fatal("failed disk stage published")
	}
	restarted, err := artifacts.New(root)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := restarted.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("orphan stages = %+v", entries)
	}
	for _, entry := range entries {
		if !entry.Staging {
			t.Fatal("orphan published")
		}
		if err = restarted.RemoveStage(entry.ID); err != nil {
			t.Fatal(err)
		}
	}
	usage, err := restarted.Usage()
	if err != nil || usage != 0 {
		t.Fatalf("remaining usage %d: %v", usage, err)
	}
}

func TestRestartUsageAndDeterministicManifest(t *testing.T) {
	root := t.TempDir()
	store, err := artifacts.New(root)
	if err != nil {
		t.Fatal(err)
	}
	one := rawArchive(t, []tar.Header{{Name: "a", Size: 1}, {Name: "b", Size: 2}}, []string{"x", "yz"})
	two := rawArchive(t, []tar.Header{{Name: "b", Size: 2}, {Name: "a", Size: 1}}, []string{"yz", "x"})
	a, err := store.Stage(t.Context(), "one", bytes.NewReader(one), artifacts.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Stage(t.Context(), "two", bytes.NewReader(two), artifacts.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("archive order changed manifest: %+v %+v", a, b)
	}
	if err = store.Publish("one"); err != nil {
		t.Fatal(err)
	}
	restarted, err := artifacts.New(root)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := restarted.Verify(t.Context(), "one")
	if err != nil || actual != a {
		t.Fatalf("restart = %+v %v", actual, err)
	}
	entries, err := restarted.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Staging || !entries[1].Staging {
		t.Fatalf("entries: %+v", entries)
	}
	var sum int64
	for _, entry := range entries {
		sum += entry.Size
		if entry.ModifiedAt.IsZero() || entry.Size <= 3 {
			t.Fatal(entry)
		}
	}
	usage, err := restarted.Usage()
	if err != nil || usage != sum {
		t.Fatalf("usage %d want %d: %v", usage, sum, err)
	}
	if err = restarted.Remove("one"); err != nil {
		t.Fatal(err)
	}
	if err = restarted.RemoveStage("two"); err != nil {
		t.Fatal(err)
	}
	if usage, err = restarted.Usage(); err != nil || usage != 0 {
		t.Fatalf("cleanup usage %d: %v", usage, err)
	}
}

func TestRejectTruncatedTarWithValidGzip(t *testing.T) {
	complete := archive(t, map[string]string{"index.html": "hello"})
	reader, err := gzip.NewReader(bytes.NewReader(complete))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	for _, test := range []struct {
		name   string
		remove int
	}{{"one missing block", 512}, {"both missing blocks", 1024}} {
		t.Run(test.name, func(t *testing.T) {
			var input bytes.Buffer
			gz := gzip.NewWriter(&input)
			if _, err := gz.Write(raw[:len(raw)-test.remove]); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			store, err := artifacts.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.Stage(t.Context(), "truncated", &input, artifacts.Limits{}); err == nil {
				t.Fatal("missing tar footer accepted")
			}
		})
	}
}
