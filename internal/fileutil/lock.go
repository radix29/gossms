package fileutil

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"time"
)

// Lock timings. Variables so tests can shorten them.
var (
	// lockWait is how long WithLock waits for a live holder before giving up.
	lockWait = 2 * time.Second
	// lockStale is the age past which a lock file is taken as left by a
	// crashed process (a holder's read-merge-write takes milliseconds).
	lockStale = 10 * time.Second
	// lockPoll is the retry interval while the lock is held.
	lockPoll = 5 * time.Millisecond
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
// If a live holder keeps the lock past lockWait, WithLock returns an error
// without running fn; callers keep their pending changes for the next save.
func WithLock(path string, fn func() error) error {
	lock := resolveSymlink(path) + ".lock"
	token := strconv.Itoa(os.Getpid()) + " " + rand.Text()
	deadline := time.Now().Add(lockWait)
	for {
		took, err := createLockFile(lock, token)
		if err != nil {
			return err
		}
		if took {
			break
		}
		if isStaleLock(lock) && breakStaleLock(lock, token) {
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s is locked by another gossms instance (remove %s if none is running)", path, lock)
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
func releaseLockFile(lock, token string) {
	if data, err := os.ReadFile(lock); err == nil && string(data) == token {
		os.Remove(lock)
	}
}
