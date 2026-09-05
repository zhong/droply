//go:build integration

package server_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhong/droply/internal/artifacts"
	"github.com/zhong/droply/internal/server"
)

const recoveryDeployURL = "/subdomains/recovery/projects/site/deploy"

func assertRejectedDeployment(t *testing.T, f *recoveryFixture, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code < 400 {
		t.Fatalf("rejected deployment reported success: %d %s", w.Code, w.Body.String())
	}
	f.assertContent(t, "old")
	history := f.history(t)
	if len(history) != 2 || history[0].Version != 2 || history[0].Status != "failed" || history[0].FailureReason == "" || history[0].Production || history[0].Available || !history[1].Production || !history[1].Available {
		t.Fatalf("failed deployment history: %+v", history)
	}
	storage, err := artifacts.New(filepath.Join(f.sites, ".artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := storage.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Staging {
		t.Fatalf("failed deployment leaked staging or published data: %+v", entries)
	}
}

func TestDeployExpandedLimitsPreserveProduction(t *testing.T) {
	for _, test := range []struct {
		name    string
		options server.DeploymentOptions
		files   map[string]string
	}{
		{"expanded bytes", server.DeploymentOptions{MaxExpandedBytes: 8}, map[string]string{"index.html": strings.Repeat("new", 4096)}},
		{"file count", server.DeploymentOptions{MaxFiles: 1}, map[string]string{"index.html": "new", "extra.txt": "extra"}},
		{"implicit directories", server.DeploymentOptions{MaxFiles: 1}, map[string]string{"nested/index.html": "new"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newRecoveryFixture(t)
			if err := f.srv.SetDeploymentOptions(test.options); err != nil {
				t.Fatal(err)
			}
			if w := f.upload(t, "old"); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			w := httptest.NewRecorder()
			f.srv.ServeHTTP(w, buildDeployRequest(t, recoveryDeployURL, createTestTarGz(t, test.files), f.token))
			assertRejectedDeployment(t, f, w)
		})
	}
}

func TestDeployUnsafeArchivePreservesProduction(t *testing.T) {
	for _, test := range []struct {
		name   string
		header tar.Header
	}{
		{"symlink", tar.Header{Name: "index.html", Typeflag: tar.TypeSymlink, Linkname: "../../outside"}},
		{"hardlink", tar.Header{Name: "index.html", Typeflag: tar.TypeLink, Linkname: "outside"}},
		{"traversal", tar.Header{Name: "../escape", Size: 3}},
		{"absolute", tar.Header{Name: "/escape", Size: 3}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newRecoveryFixture(t)
			if w := f.upload(t, "old"); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			var archive bytes.Buffer
			gz := gzip.NewWriter(&archive)
			tw := tar.NewWriter(gz)
			if err := tw.WriteHeader(&test.header); err != nil {
				t.Fatal(err)
			}
			if test.header.Size > 0 {
				if _, err := tw.Write([]byte("new")); err != nil {
					t.Fatal(err)
				}
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			f.srv.ServeHTTP(w, buildDeployRequest(t, recoveryDeployURL, &archive, f.token))
			assertRejectedDeployment(t, f, w)
		})
	}
}

type zeroStream struct{}

func (zeroStream) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestDeployEnforcesActualRequestSizeAfterMultipartEnd(t *testing.T) {
	f := newRecoveryFixture(t)
	if w := f.upload(t, "old"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	req := buildDeployRequest(t, recoveryDeployURL, createTestTarGz(t, map[string]string{"index.html": "new"}), f.token)
	original := req.Body
	defer original.Close()
	// Stream a valid small archive followed by 51 MiB of MIME epilogue. ContentLength
	// is deliberately unknown: enforcement must count bytes, not trust this header.
	req.Body = io.NopCloser(io.MultiReader(original, io.LimitReader(zeroStream{}, 51<<20)))
	req.ContentLength = -1
	w := httptest.NewRecorder()
	f.srv.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized request: %d %s", w.Code, w.Body.String())
	}
	assertRejectedDeployment(t, f, w)
}

type interruptedUpload struct {
	body      io.ReadCloser
	remaining int
	cancel    context.CancelFunc
}

func (r *interruptedUpload) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		if r.cancel != nil {
			r.cancel()
			return 0, context.Canceled
		}
		return 0, io.ErrUnexpectedEOF
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.body.Read(p)
	r.remaining -= n
	return n, err
}
func (r *interruptedUpload) Close() error { return r.body.Close() }

func TestDeployInterruptedStreamPreservesProduction(t *testing.T) {
	randomContent := make([]byte, 128<<10)
	if _, err := rand.Read(randomContent); err != nil {
		t.Fatal(err)
	}
	for _, cancelContext := range []bool{false, true} {
		name := "stream error"
		if cancelContext {
			name = "request context canceled"
		}
		t.Run(name, func(t *testing.T) {
			f := newRecoveryFixture(t)
			if w := f.upload(t, "old"); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			req := buildDeployRequest(t, recoveryDeployURL, createTestTarGz(t, map[string]string{"index.html": string(randomContent)}), f.token)
			interrupted := &interruptedUpload{body: req.Body, remaining: 8192}
			if cancelContext {
				ctx, cancel := context.WithCancel(req.Context())
				defer cancel()
				req = req.WithContext(ctx)
				interrupted.cancel = cancel
			}
			req.Body = interrupted
			req.ContentLength = -1
			w := httptest.NewRecorder()
			f.srv.ServeHTTP(w, req)
			assertRejectedDeployment(t, f, w)
		})
	}
}

func TestDeployManifestQuotaIsCheckedBeforePublication(t *testing.T) {
	f := newRecoveryFixture(t)
	if w := f.upload(t, "old"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	storage, err := artifacts.New(filepath.Join(f.sites, ".artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	used, err := storage.Usage()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a restart with enough quota for new file bytes but no room for its
	// durable manifest. The second check must account for the whole staged artifact.
	f.srv = server.New(f.st, f.sites, "droplydoc.com", []byte("recovery-test-signing-key"))
	if err = f.srv.SetDeploymentOptions(server.DeploymentOptions{MaxStorageBytes: used + 16}); err != nil {
		t.Fatal(err)
	}
	w := f.upload(t, "new")
	if w.Code != http.StatusInsufficientStorage {
		t.Fatalf("manifest quota: %d %s", w.Code, w.Body.String())
	}
	assertRejectedDeployment(t, f, w)
}
