package fileutil

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
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

// A holder that takes the lock straight back after its release cannot starve
// a waiter: once the waiter has lost for lockPriorityAfter, its marker makes
// the holder stand back, so the release goes to the waiter. Without the marker
// the barger's retake, microseconds after the release, beats the waiter's
// next poll; on a slow Windows disk it did every time until lockWait.
func TestABargingHolderCannotStarveAWaiter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	lock, wait := path+".lock", path+".lock.wait"
	if err := os.WriteFile(lock, []byte("holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var order []string
	ran := func(who string) func() error {
		return func() error {
			mu.Lock()
			order = append(order, who)
			mu.Unlock()
			return nil
		}
	}
	waiterDone := make(chan error, 1)
	go func() { waiterDone <- WithLock(path, ran("waiter")) }()
	for deadline := time.Now().Add(lockWait); ; time.Sleep(time.Millisecond) {
		if _, err := os.Stat(wait); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the waiter never claimed priority")
		}
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := WithLock(path, ran("barger")); err != nil {
		t.Fatal(err)
	}
	if err := <-waiterDone; err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(order, []string{"waiter", "barger"}) {
		t.Errorf("order = %v, want the waiter first", order)
	}
	if _, err := os.Stat(wait); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("priority marker left behind: stat err %v", err)
	}
}

// Another waiter's fresh priority marker makes a caller stand back even with
// the lock free; giving up leaves the marker to its owner.
func TestAPriorityMarkerMakesOthersStandBack(t *testing.T) {
	defer func(w time.Duration) { lockWait = w }(lockWait)
	lockWait = 50 * time.Millisecond
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path+".lock.wait", []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	ran := false
	if err := WithLock(path, func() error { ran = true; return nil }); err == nil || ran {
		t.Fatalf("WithLock behind another's marker: err %v, ran %v; want an error and fn not run", err, ran)
	}
	if data, err := os.ReadFile(path + ".lock.wait"); err != nil || string(data) != "other" {
		t.Errorf("the other waiter's marker = %q, %v; want it kept", data, err)
	}
}

// A marker older than twice lockWait outlived its owner (a crash): it is
// removed and the lock taken.
func TestAnAbandonedPriorityMarkerIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path+".lock.wait", []byte("dead"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * lockWait)
	if err := os.Chtimes(path+".lock.wait", old, old); err != nil {
		t.Fatal(err)
	}
	ran := false
	if err := WithLock(path, func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("WithLock behind an abandoned marker: err %v, ran %v", err, ran)
	}
	if _, err := os.Stat(path + ".lock.wait"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("abandoned marker left behind: stat err %v", err)
	}
}
