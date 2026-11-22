package fileutil

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
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
	// Each hold costs two fsyncs (~25 ms on a slow Windows disk) and the lock
	// is unfair, so a waiter can starve past the 2 s default under load.
	defer func(w time.Duration) { lockWait = w }(lockWait)
	lockWait = time.Minute
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

// A waiter that saw the dead holder's lock stale, but reaches the break after
// another waiter broke it and took a fresh one, leaves the fresh lock alone.
func TestBreakingAStaleLockRechecksUnderTheBreakLock(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "config.json.lock")
	if err := os.WriteFile(lock, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if breakStaleLock(lock, "me") {
		t.Error("breakStaleLock removed a fresh lock")
	}
	if data, err := os.ReadFile(lock); err != nil || string(data) != "other" {
		t.Errorf("the fresh lock = %q, %v; want it untouched", data, err)
	}
	if _, err := os.Stat(lock + ".break"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("break lock left behind: stat err %v", err)
	}
}

// A break in progress (a fresh break lock) makes other waiters stand back, and
// a break lock left by a crash is cleared so the stale lock can be broken.
func TestABreakLockSerializesBreakers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	lock := path + ".lock"
	old := time.Now().Add(-2 * lockStale)
	for _, f := range []string{lock, lock + ".break"} {
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if breakStaleLock(lock, "me") {
		t.Fatal("breakStaleLock broke the lock while another break was in progress")
	}
	if err := os.Chtimes(lock+".break", old, old); err != nil {
		t.Fatal(err)
	}
	ran := false
	if err := WithLock(path, func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("WithLock behind a crashed break: err %v, ran %v", err, ran)
	}
	for _, f := range []string{lock, lock + ".break"} {
		if _, err := os.Stat(f); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left behind: stat err %v", filepath.Base(f), err)
		}
	}
}

// A holder whose lock was broken and retaken while it ran (it overran
// lockStale) does not remove its successor's lock on the way out.
func TestReleaseLeavesASuccessorsLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	lock := path + ".lock"
	err := WithLock(path, func() error {
		return os.WriteFile(lock, []byte("successor"), 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(lock); err != nil || string(data) != "successor" {
		t.Errorf("successor's lock = %q, %v; want it kept", data, err)
	}
}

// Many waiters arriving at one stale lock still take turns: none runs fn while
// another holds the lock.
func TestWaitersOnAStaleLockTakeTurns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for range 50 {
		if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-2 * lockStale)
		if err := os.Chtimes(path+".lock", old, old); err != nil {
			t.Fatal(err)
		}
		var inside, overlaps atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				err := WithLock(path, func() error {
					if inside.Add(1) > 1 {
						overlaps.Add(1)
					}
					time.Sleep(200 * time.Microsecond)
					inside.Add(-1)
					return nil
				})
				if err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if n := overlaps.Load(); n > 0 {
			t.Fatalf("%d waiters ran fn while another held the lock", n)
		}
	}
}
