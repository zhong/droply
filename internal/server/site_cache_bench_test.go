package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/zhong/droply/internal/staticweb"
)

// Compare parsing on every request with sharing an immutable Site. This isolates
// static serving; it does not measure SQLite authorization, TLS or the network.
func BenchmarkCachedSite(b *testing.B) {
	for _, rules := range []bool{false, true} {
		name := "no_rules"
		if rules {
			name = "rules"
		}
		b.Run(name, func(b *testing.B) {
			dir := b.TempDir()
			files := map[string]string{"asset.txt": "0123456789"}
			if rules {
				files["_droply.toml"] = `[site]
mode = "static"
`
				files["_headers"] = "/*\n  X-Site: benchmark\n"
				files["_redirects"] = "/old /asset.txt 302\n"
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
					b.Fatal(err)
				}
			}
			for _, reuse := range []bool{false, true} {
				mode := "load"
				if reuse {
					mode = "cache"
				}
				b.Run(mode, func(b *testing.B) {
					var cache siteCache
					site, err := cache.load("artifact", dir)
					if err != nil {
						b.Fatal(err)
					}
					r := httptest.NewRequest("GET", "/asset.txt", nil)
					b.ReportAllocs()
					for b.Loop() {
						if reuse {
							site, err = cache.load("artifact", dir)
							if err != nil {
								b.Fatal(err)
							}
						} else {
							site, err = staticweb.Load(dir)
							if err != nil {
								b.Fatal(err)
							}
						}
						w := httptest.NewRecorder()
						site.ServeHTTP(w, r, staticweb.Options{ETagSeed: "benchmark"})
						if w.Code != 200 {
							b.Fatal(w.Code)
						}
					}
				})
			}
		})
	}
}
