package fileutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
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

// withLockTimings sets lockWait and lockMaxWait for one test.
func withLockTimings(t *testing.T, wait, maxWait time.Duration) {
	oldWait, oldMax := lockWait, lockMaxWait
	lockWait, lockMaxWait = wait, maxWait
	t.Cleanup(func() { lockWait, lockMaxWait = oldWait, oldMax })
}

// A live holder that keeps the lock past lockWait makes WithLock fail without
// running fn, and leaves the holder's lock alone. It fails at lockWait, not
// lockMaxWait: an unchanged lock is no progress.
func TestWithLockGivesUpOnALiveHolder(t *testing.T) {
	withLockTimings(t, 50*time.Millisecond, 5*time.Second)

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ran := false
	start := time.Now()
	err := WithLock(path, func() error { ran = true; return nil })
	if err == nil || ran || !strings.Contains(err.Error(), "locked by another gossms instance") {
		t.Fatalf("WithLock under a live lock: err %v, ran %v; want the locked error and fn not run", err, ran)
	}
	if took := time.Since(start); took >= lockMaxWait/2 {
		t.Errorf("WithLock took %v to give up on a stuck holder; want about lockWait (%v), well under lockMaxWait (%v)", took, lockWait, lockMaxWait)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Errorf("the live holder's lock was removed: %v", err)
	}
}

// Issue #56: a waiter behind a moving queue does not time out, however long
// the queue makes it wait in all. Each of 24 workers waits behind about 23
// 100 ms holds, past lockWait, so timing the caller's own wait fails this on
// every OS with no load needed. lockWait is fifteen holds, not one: on a
// loaded Windows host a 30 ms hold took over 100 ms, and a 100 ms one over
// 400 ms, which is a stuck holder by that lockWait's measure.
func TestWithLockWaitsBehindAMovingQueue(t *testing.T) {
	withLockTimings(t, 1500*time.Millisecond, 30*time.Second)
	path := filepath.Join(t.TempDir(), "config.json")
	const workers, rounds = 24, 2
	var inside, overlaps atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, workers*rounds)
	for range workers {
		wg.Go(func() {
			for range rounds {
				errs <- WithLock(path, func() error {
					if inside.Add(1) > 1 {
						overlaps.Add(1)
					}
					time.Sleep(100 * time.Millisecond)
					inside.Add(-1)
					return nil
				})
			}
		})
	}
	wg.Wait()
	close(errs)
	failed := 0
	for err := range errs {
		if err != nil {
			if failed++; failed == 1 {
				t.Error(err)
			}
		}
	}
	if failed > 0 {
		t.Errorf("%d of %d takes failed behind a moving queue", failed, workers*rounds)
	}
	if n := overlaps.Load(); n > 0 {
		t.Errorf("%d holders ran fn while another held the lock", n)
	}
}

// A queue that keeps moving without ever reaching this waiter (another
// waiter's marker always stands it back) fails at lockMaxWait with the busy
// error, not the stuck-holder one.
func TestWithLockGivesUpAtTheCapWhenBusy(t *testing.T) {
	withLockTimings(t, 100*time.Millisecond, 500*time.Millisecond)
	path := filepath.Join(t.TempDir(), "config.json")
	lock, wait := path+".lock", path+".lock.wait"
	// take refreshes the marker, so blockedByPriority never drops it as
	// abandoned (2*lockWait), and takes the lock as a new holder.
	take := func() error {
		if err := os.WriteFile(wait, []byte("other"), 0o600); err != nil {
			return err
		}
		if took, err := createLockFile(lock, "churn"); err != nil || !took {
			return fmt.Errorf("churn create: took %v, err %v", took, err)
		}
		return nil
	}
	if err := take(); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	churned := make(chan struct{})
	go func() {
		defer close(churned)
		for {
			time.Sleep(20 * time.Millisecond)
			releaseLockFile(lock, "churn")
			select {
			case <-stop:
				return
			default:
			}
			if err := take(); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	start := time.Now()
	ran := false
	err := WithLock(path, func() error { ran = true; return nil })
	took := time.Since(start)
	close(stop)
	<-churned
	if err == nil || ran || !strings.Contains(err.Error(), "is busy") {
		t.Fatalf("WithLock behind a moving queue it never reaches: err %v, ran %v; want the busy error", err, ran)
	}
	if took < lockMaxWait {
		t.Errorf("WithLock gave up after %v; want at least lockMaxWait (%v)", took, lockMaxWait)
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
// the lock free, until the marker is old enough to count as abandoned
// (2*lockWait): a free lock is progress, so the caller does not give up first.
func TestAPriorityMarkerMakesOthersStandBack(t *testing.T) {
	withLockTimings(t, 50*time.Millisecond, 5*time.Second)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path+".lock.wait", []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	ran := false
	start := time.Now()
	if err := WithLock(path, func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("WithLock behind an owner-less marker: err %v, ran %v; want fn run once the marker aged out", err, ran)
	}
	// The marker's ModTime predates start, by a timestamp tick at most.
	if took := time.Since(start); took < 3*lockWait/2 {
		t.Errorf("WithLock ran fn after %v; want it to stand back for about 2*lockWait (%v)", took, 2*lockWait)
	}
}

// Giving up on a stuck holder while another waiter owns the marker leaves the
// marker to its owner.
func TestGivingUpLeavesAnothersMarker(t *testing.T) {
	withLockTimings(t, 50*time.Millisecond, 5*time.Second)
	path := filepath.Join(t.TempDir(), "config.json")
	for _, f := range []string{path + ".lock", path + ".lock.wait"} {
		if err := os.WriteFile(f, []byte("other"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ran := false
	if err := WithLock(path, func() error { ran = true; return nil }); err == nil || ran {
		t.Fatalf("WithLock behind a stuck holder and another's marker: err %v, ran %v; want an error and fn not run", err, ran)
	}
	if data, err := os.ReadFile(path + ".lock.wait"); err != nil || string(data) != "other" {
		t.Errorf("the other waiter's marker = %q, %v; want it kept", data, err)
	}
}

// A directory that refuses the lock's create, with no lock file in it, is not
// contention: WithLock returns the denial at once, not after lockWait.
func TestWithLockFailsFastInAnUnwritableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the POSIX mode bits can make read-only, as non-root")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	start := time.Now()
	ran := false
	err := WithLock(filepath.Join(dir, "config.json"), func() error { ran = true; return nil })
	if !errors.Is(err, fs.ErrPermission) || ran {
		t.Fatalf("WithLock = %v, ran %v; want a permission error and fn not run", err, ran)
	}
	if took := time.Since(start); took >= lockWait/2 {
		t.Errorf("WithLock took %v to report the denial; want well under lockWait (%v)", took, lockWait)
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

// An outside handle on the lock file at release (on Windows, os.Open shares
// no delete, as an antivirus scan's may not) delays the remove; release
// retries rather than leaving the lock for every saver to stall behind until
// lockStale. On other OSes the remove succeeds at once.
func TestReleaseOutlastsABriefOutsideHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	lock := path + ".lock"
	closed := make(chan struct{})
	err := WithLock(path, func() error {
		f, err := os.Open(lock)
		if err != nil {
			return err
		}
		go func() {
			defer close(closed)
			time.Sleep(50 * time.Millisecond)
			f.Close()
		}()
		return nil
	})
	<-closed
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("lock file after a briefly held outside handle: stat err %v, want not-exist", err)
	}
}
