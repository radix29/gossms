package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

// Review step D2 (2026-11-16): what WithLock costs a save. Uncontended is
// every save's overhead; Contended has goroutines standing in for gossms
// instances saving the same file at once, each holding the lock for a
// read-merge-write's worth of file I/O, so a waiter's cost includes the
// lockPoll sleeps.
//
//	go test ./internal/fileutil -run XXX -bench WithLock -benchtime 2s

func BenchmarkWithLockUncontended(b *testing.B) {
	path := filepath.Join(b.TempDir(), "config.json")
	for b.Loop() {
		if err := WithLock(path, func() error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWithLockContended(b *testing.B) {
	path := filepath.Join(b.TempDir(), "config.json")
	data := make([]byte, 16<<10)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		b.Fatal(err)
	}
	b.SetParallelism(1) // GOMAXPROCS goroutines
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := WithLock(path, func() error {
				d, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				return WriteAtomic(path, d, 0o600)
			}); err != nil {
				b.Error(err)
				return
			}
		}
	})
}
