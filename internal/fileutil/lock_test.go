package fileutil

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// A read-modify-write under the lock never loses an increment.
func TestWithLockSerializesReadModifyWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counter")
	if err := os.WriteFile(path, []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	const workers, rounds = 4, 50
	var wg sync.WaitGroup
	errs := make(chan error, workers*rounds)
	for range workers {
		wg.Go(func() {
			for range rounds {
				errs <- WithLock(path, func() error {
					data, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					n, err := strconv.Atoi(string(data))
					if err != nil {
						return err
					}
					time.Sleep(50 * time.Microsecond) // widen the window
					return WriteAtomic(path, []byte(strconv.Itoa(n+1)), 0o600)
				})
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(path)
	if got := string(data); got != strconv.Itoa(workers*rounds) {
		t.Errorf("counter = %s, want %d — an increment was lost", got, workers*rounds)
	}
}

// The lock file is gone afterwards, whether fn succeeded or failed.
func TestWithLockRemovesTheLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	boom := errors.New("boom")
	for _, want := range []error{nil, boom} {
		if err := WithLock(path, func() error { return want }); !errors.Is(err, want) {
			t.Fatalf("WithLock = %v, want fn's %v", err, want)
		}
		if _, err := os.Stat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("lock file after fn returned %v: stat err %v, want not-exist", want, err)
		}
	}
}

// A lock left by a crashed process is taken over once it is stale.
func TestWithLockTakesOverAStaleLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * lockStale)
	if err := os.Chtimes(path+".lock", old, old); err != nil {
		t.Fatal(err)
	}
	ran := false
	if err := WithLock(path, func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("WithLock over a stale lock: err %v, ran %v", err, ran)
	}
}

// A live holder past lockWait makes WithLock fail without running fn, and
// leaves the holder's lock alone.
func TestWithLockGivesUpOnALiveHolder(t *testing.T) {
	defer func(w time.Duration) { lockWait = w }(lockWait)
	lockWait = 50 * time.Millisecond

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ran := false
	if err := WithLock(path, func() error { ran = true; return nil }); err == nil || ran {
		t.Fatalf("WithLock under a live lock: err %v, ran %v; want an error and fn not run", err, ran)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Errorf("the live holder's lock was removed: %v", err)
	}
}
