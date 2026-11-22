package fileutil

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"strconv"
	"time"
)

// Lock timings. Variables so tests can shorten them.
var (
	// lockWait is how long WithLock waits while the lock makes no progress
	// (the same holder keeps it) before giving up on a stuck holder.
	lockWait = 2 * time.Second
	// lockMaxWait caps WithLock's whole wait, however much the queue moves.
	lockMaxWait = 10 * time.Second
	// lockStale is the age past which a lock file is taken as left by a
	// crashed process (a holder's read-merge-write takes milliseconds).
	lockStale = 10 * time.Second
	// lockPoll is the retry interval while the lock is held.
	lockPoll = 5 * time.Millisecond
	// lockPriorityAfter is how long a waiter loses before it claims the
	// priority marker (claimPriority).
	lockPriorityAfter = 100 * time.Millisecond
)

// WithLock runs fn while holding path's lock file, path + ".lock", so two gossms
// instances doing a read-merge-write of the same file take turns. Without it,
// two simultaneous saves (config.Save, TrackedQueries.Save) both read the old
// file and the later write drops the earlier change.
//
// The lock is an O_CREATE|O_EXCL create, atomic on every OS and filesystem
// gossms runs on: portable, no flock, no GOOS branch. Advisory: only WithLock
// callers honour it.
//
// A lock older than lockStale is removed as abandoned, under a second lock
// (breakStaleLock): a waiter that saw the old lock stale must not remove the
// lock another waiter has since taken in its place. The lock file holds the
// holder's token, and release removes it only while it still holds that token,
// so a holder that overran lockStale leaves its successor's lock alone.
//
// A waiter that has lost for lockPriorityAfter claims priority: it creates
// lock + ".wait" holding its token, and every other caller stands back while
// that marker is there (blockedByPriority), so the next release goes to it.
// Polling alone was unfair: a holder that saves again took the lock straight
// back on release, before the sleeping waiter's next poll, and on a slow disk
// (two fsyncs per hold) a waiter lost every race until lockWait. A longer or
// jittered poll made that worse — it only cuts the waiter's tries. One waiter
// holds the marker at a time; the owner removes it once it has the lock, or
// when it gives up. The marker decides who goes next, never mutual exclusion
// (the lock file alone does that), so its races cost fairness, not safety.
//
// WithLock gives up, without running fn, when the lock has made no progress
// for lockWait — the same holder has kept it, so it is stuck or crashed — or
// when the whole wait passes lockMaxWait; callers keep their pending changes
// for the next save. Progress is the lock seen absent, or changed since the
// last poll (it appeared, or is a new file with a new ModTime): every new
// holder creates a fresh one. Absent counts on every poll, not only as a
// change: a waiter that polls rarely under load can see it absent twice with
// holders in between, and a free lock is never a stuck holder. Timing this
// caller's own wait instead failed waiters behind a moving queue (issue #56):
// the marker holds one waiter at a time and its next owner is whoever wins
// the race, so with several waiters on a loaded Windows host one could lose
// for lockWait while every hold was short. Progress is read with os.Stat only, never by reading the token: a
// read opens a handle without FILE_SHARE_DELETE on Windows, and polling
// waiters would make the holder's remove fail.
func WithLock(path string, fn func() error) error {
	lock := resolveSymlink(path) + ".lock"
	token := strconv.Itoa(os.Getpid()) + " " + rand.Text()
	wait := lock + ".wait"
	start := time.Now()
	lastProgress := start
	seen := statLock(lock)
	// timedOut watches the lock for progress and reports which deadline,
	// if any, has passed: stuck (no progress for lockWait) or busy (the cap).
	timedOut := func() (stuck, busy bool) {
		s := statLock(lock)
		now := time.Now() // after the stat: a stat that stalls under load must not date what it saw
		if s != seen || s == (lockState{}) {
			seen, lastProgress = s, now
		}
		return now.Sub(lastProgress) > lockWait, now.Sub(start) > lockMaxWait
	}
	giveUp := func(stuck bool) error {
		if stuck {
			return fmt.Errorf("%s is locked by another gossms instance (remove %s if none is running)", path, lock)
		}
		return fmt.Errorf("%s is busy: waited %v behind other gossms instances", path, lockMaxWait)
	}
	var denied error
	absentDenials := 0 // consecutive denied creates with no lock file there
	prioritized := false
	defer func() {
		if prioritized {
			releaseLockFile(wait, token)
		}
	}()
	for {
		if !prioritized && blockedByPriority(wait) {
			if stuck, busy := timedOut(); stuck || busy {
				return giveUp(stuck)
			}
			time.Sleep(lockPoll)
			continue
		}
		took, err := createLockFile(lock, token)
		if err != nil && !errors.Is(err, fs.ErrPermission) {
			return err
		}
		if took {
			if prioritized {
				releaseLockFile(wait, token)
				prioritized = false
			}
			break
		}
		// Windows reports ERROR_ACCESS_DENIED, not "exists", for an
		// O_EXCL create against a lock file that is delete-pending (the
		// holder just removed it, or a handle is still open). That is
		// contention, so keep waiting; only a denial that outlasts the
		// deadlines is returned, as itself. A denial with no lock file there
		// at all is the directory refusing the create (read-only, anywhere),
		// not contention: seen on two tries in a row (the second rules out a
		// delete-pending lock finishing in between), it is returned at once
		// rather than after lockWait — a CLI command's audit write is
		// synchronous, and would stall that long on every run.
		denied = err
		if err != nil && lockAbsent(lock) {
			if absentDenials++; absentDenials >= 2 {
				return err
			}
		} else {
			absentDenials = 0
		}
		if isStaleLock(lock) && breakStaleLock(lock, token) {
			continue
		}
		if stuck, busy := timedOut(); stuck || busy {
			if denied != nil {
				return denied
			}
			return giveUp(stuck)
		}
		if !prioritized && time.Since(start) >= lockPriorityAfter {
			prioritized = claimPriority(wait, token)
		}
		time.Sleep(lockPoll)
	}
	defer releaseLockFile(lock, token)
	return fn()
}

// createLockFile creates lock holding token, reporting false (and no error)
// when it already exists.
func createLockFile(lock, token string) (bool, error) {
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, werr := f.WriteString(token)
	if err := errors.Join(werr, f.Close()); err != nil {
		os.Remove(lock)
		return false, err
	}
	return true, nil
}

// blockedByPriority reports whether another waiter holds the priority marker
// wait. Called only by a caller that does not hold it. A marker older than
// twice lockWait outlived its owner (everyone else stands back, so the owner
// takes the next release, or gives up after lockWait with no progress), so it
// is removed and ignored — that costs fairness, never exclusion. Until then a
// waiter that crashed holding it delays saves: over a free lock they wait the
// marker out (a free lock is progress), behind a held one they fail as
// "locked" once that holder is stuck. A stat error counts as absent: on
// Windows a marker being deleted refuses opens.
func blockedByPriority(wait string) bool {
	fi, err := os.Stat(wait)
	if err != nil {
		return false
	}
	if time.Since(fi.ModTime()) > 2*lockWait {
		os.Remove(wait)
		return false
	}
	return true
}

// lockState is what statLock saw of a lock file. Two holders' files differ in
// ModTime unless the filesystem's timestamps are too coarse to tell them
// apart; then a waiter falls back to failing after lockWait, never to two
// holders at once (the O_EXCL create alone decides that).
type lockState struct {
	exists, unknown bool
	mod             time.Time
}

// statLock reads lock's state with a stat, which opens no handle. Any error
// but not-exist is unknown: on Windows a delete-pending lock refuses opens,
// so a change into it reads as progress. Only the change: a stat that keeps
// failing (a directory that refuses it) must not pass for a moving queue.
func statLock(lock string) lockState {
	fi, err := os.Stat(lock)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return lockState{}
	case err != nil:
		return lockState{unknown: true}
	}
	return lockState{exists: true, mod: fi.ModTime()}
}

// lockAbsent reports whether lock certainly does not exist. Any other stat
// error counts as present: on Windows a delete-pending lock refuses opens.
func lockAbsent(lock string) bool {
	_, err := os.Stat(lock)
	return errors.Is(err, fs.ErrNotExist)
}

// claimPriority creates the priority marker wait holding token, reporting
// whether this caller now holds it.
func claimPriority(wait, token string) bool {
	took, err := createLockFile(wait, token)
	return err == nil && took
}

// isStaleLock reports whether lock exists and is older than lockStale.
func isStaleLock(lock string) bool {
	fi, err := os.Stat(lock)
	return err == nil && time.Since(fi.ModTime()) > lockStale
}

// breakStaleLock removes lock if it is still stale, reporting whether it did.
// Every break runs under lock + ".break" and checks staleness again inside it.
// Without that, two waiters that both saw the dead holder's lock stale could
// interleave so the second removes the fresh lock the first just took, and
// both run fn at once.
//
// The break lock is held for a stat and a remove, so a stale one means a crash
// inside that window; it is removed outright, the one race left.
func breakStaleLock(lock, token string) bool {
	brk := lock + ".break"
	took, err := createLockFile(brk, token)
	if err != nil {
		return false
	}
	if !took {
		if isStaleLock(brk) {
			os.Remove(brk)
		}
		return false
	}
	defer os.Remove(brk)
	if !isStaleLock(lock) {
		return false
	}
	return os.Remove(lock) == nil
}

// releaseLockFile removes lock if it still holds token. A holder that overran
// lockStale may have had its lock broken and retaken; that lock is the new
// holder's to remove.
//
// gossms's waiters only create and stat a lock, never open it, so a failed
// remove means an outside process has a handle open without
// FILE_SHARE_DELETE — on a loaded Windows host, the antivirus scanning the
// file just created (issue #56: the remove failed, and so did one retry 5 ms
// later). It is retried every lockPoll for up to lockWait and logged if it
// still fails: a lock left behind stalls every saver until it goes stale
// (lockStale), and waiters read the unchanged lock as a stuck holder.
func releaseLockFile(lock, token string) {
	data, err := os.ReadFile(lock)
	if err != nil || string(data) != token {
		return
	}
	for deadline := time.Now().Add(lockWait); ; time.Sleep(lockPoll) {
		err := os.Remove(lock)
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			log.Printf("fileutil: releasing %s: %v; left in place until it is taken as abandoned", lock, err)
			return
		}
	}
}
