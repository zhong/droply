package backup

import (
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func restoreWorkload(b testing.TB, files, bytesPerFile int) Config {
	b.Helper()
	cfg := fixture(b)
	extra := filepath.Join(filepath.Dir(cfg.DataDir), "workload")
	must(b, os.Mkdir(extra, 0700))
	random := rand.New(rand.NewPCG(7, 11))
	data := make([]byte, bytesPerFile)
	for i := range data {
		data[i] = byte(random.Uint32())
	}
	for i := range files {
		must(b, os.WriteFile(filepath.Join(extra, fmt.Sprintf("file-%05d", i)), data, 0600))
	}
	cfg.Include = []string{extra}
	must(b, Create(b.Context(), cfg))
	return cfg
}

func BenchmarkRestore(b *testing.B) {
	for _, workload := range []struct {
		name         string
		files, bytes int
	}{
		{"many-files", 1000, 4096},
		{"large-file", 1, 32 << 20},
	} {
		b.Run(workload.name, func(b *testing.B) {
			cfg := restoreWorkload(b, workload.files, workload.bytes)
			target := filepath.Join(filepath.Dir(cfg.DataDir), "restored")
			b.ReportAllocs()
			for b.Loop() {
				must(b, Restore(b.Context(), RestoreConfig{Input: cfg.Output, DataDir: target}))
				b.StopTimer()
				must(b, os.RemoveAll(target))
				b.StartTimer()
			}
		})
	}
}

// This optional diagnostic measures scratch bytes separately from timed runs,
// since repeatedly walking the tree would distort the latency benchmark.
func TestRestoreFootprint(t *testing.T) {
	if os.Getenv("DROPLY_RESTORE_FOOTPRINT") != "1" {
		t.Skip("set DROPLY_RESTORE_FOOTPRINT=1 for scratch-space measurement")
	}
	for _, workload := range []struct {
		name         string
		files, bytes int
	}{
		{"many-files", 1000, 4096}, {"large-file", 1, 32 << 20},
	} {
		t.Run(workload.name, func(t *testing.T) {
			cfg := restoreWorkload(t, workload.files, workload.bytes)
			parent := filepath.Dir(cfg.DataDir)
			target := filepath.Join(parent, "restored")
			done := make(chan struct{})
			var wg sync.WaitGroup
			var peak int64
			wg.Go(func() {
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-done:
						return
					case <-ticker.C:
						entries, _ := os.ReadDir(parent)
						var size int64
						for _, entry := range entries {
							if !strings.HasPrefix(entry.Name(), ".droply-restore-") {
								continue
							}
							filepath.WalkDir(filepath.Join(parent, entry.Name()), func(path string, d fs.DirEntry, err error) error {
								if err == nil && d.Type().IsRegular() {
									if info, err := d.Info(); err == nil {
										size += info.Size()
									}
								}
								return nil
							})
						}
						peak = max(peak, size)
					}
				}
			})
			err := Restore(t.Context(), RestoreConfig{Input: cfg.Output, DataDir: target})
			close(done)
			wg.Wait()
			must(t, err)
			var restored int64
			filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
				if err == nil && d.Type().IsRegular() {
					if info, err := d.Info(); err == nil {
						restored += info.Size()
					}
				}
				return nil
			})
			t.Logf("observed_peak_scratch_bytes=%d final_regular_file_bytes=%d", peak, restored)
		})
	}
}
