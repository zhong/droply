package server

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zhong/droply/internal/staticweb"
)

func TestSiteCacheBoundedAndRetry(t *testing.T) {
	root := t.TempDir()
	var cache siteCache
	missing := filepath.Join(root, "missing")
	if _, err := cache.load("retry", missing); err == nil {
		t.Fatal("missing root accepted")
	}
	if len(cache.sites) != 0 {
		t.Fatal("cached an error")
	}
	if err := os.Mkdir(missing, 0700); err != nil {
		t.Fatal(err)
	}
	first, err := cache.load("retry", missing)
	if err != nil {
		t.Fatal(err)
	}
	same, err := cache.load("retry", missing)
	if err != nil || first != same {
		t.Fatal("cache did not reuse immutable site")
	}
	for i := range siteCacheCapacity {
		if _, err := cache.load(fmt.Sprint(i), root); err != nil {
			t.Fatal(err)
		}
	}
	if len(cache.sites) != siteCacheCapacity || cache.sites["retry"] != nil {
		t.Fatal("FIFO capacity not enforced")
	}
	reloaded, err := cache.load("retry", missing)
	if err != nil || reloaded == first {
		t.Fatal("evicted entry not reloaded")
	}
	cache.forget("retry")
	if cache.sites["retry"] != nil {
		t.Fatal("forgotten entry retained")
	}
	for _, id := range cache.order {
		if id == "retry" {
			t.Fatal("forgotten FIFO reference retained")
		}
	}
	if err := os.WriteFile(filepath.Join(missing, "_headers"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.load("retry", missing); err == nil {
		t.Fatal("invalid configuration accepted after invalidation")
	}
	if err := os.Remove(filepath.Join(missing, "_headers")); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.load("retry", missing); err != nil {
		t.Fatal("parse error was cached:", err)
	}
}

func TestSiteCacheConcurrentRequestPolicy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "asset.txt"), []byte("asset"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "_headers"), []byte("/*\n  X-Site: immutable\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var cache siteCache
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			site, err := cache.load("artifact", root)
			if err != nil {
				t.Error(err)
				return
			}
			private := i%2 == 0
			w := httptest.NewRecorder()
			site.ServeHTTP(w, httptest.NewRequest("GET", "/asset.txt", nil), staticweb.Options{Private: private, Preview: !private})
			if w.Code != 200 || w.Body.String() != "asset" || w.Header().Get("X-Site") != "immutable" {
				t.Error("incorrect static response")
			}
			if (w.Header().Get("Cache-Control") == "private, no-store") != private {
				t.Error("private policy crossed requests")
			}
			if (w.Header().Get("X-Robots-Tag") == "noindex, nofollow") == private {
				t.Error("preview policy crossed requests")
			}
			w.Header().Set("X-Site", "changed response")
		})
	}
	wg.Wait()
	if len(cache.sites) != 1 {
		t.Fatal("concurrent misses retained duplicate entries")
	}
}
