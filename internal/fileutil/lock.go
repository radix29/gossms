package fileutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// Lock timings. Variables so tests can shorten them.
var (
	// lockWait is how long WithLock waits for a live holder before giving up.
	lockWait = 2 * time.Second
	// lockStale is the age past which a lock file is taken to be left behind
	// by a crashed process. A holder does one read-merge-write of a small
	// file, milliseconds, so anything this old has no owner.
	lockStale = 10 * time.Second
	// lockPoll is the retry interval while the lock is held.
	lockPoll = 5 * time.Millisecond
)

// WithLock runs fn while holding path's lock file, path + ".lock", so two
// gossms instances doing a read-merge-write of the same file take turns. A
// replayed merge (config.Save, TrackedQueries.Save) only works if nothing
// writes between its read and its write; without the lock two saves in the
// same instant both read the old file and the later write drops the earlier
// one's change.
//
// The lock is an O_CREATE|O_EXCL create, which every OS and filesystem gossms
// runs on makes atomic: portable, with no flock and no GOOS branch. It is
// advisory — only callers of WithLock honour it.
//
// A lock older than lockStale is removed as abandoned. Two waiters that both
// find the same stale lock can race so that the second removes the lock the
// first just took; that needs a crash first and then two saves inside one
// poll interval, and costs only the race this lock narrows, so it is
// accepted rather than closed with anything OS-specific.
//
// If a live holder keeps the lock past lockWait, WithLock returns an error
// without running fn; the callers keep their pending changes for the next
// save.
func WithLock(path string, fn func() error) error {
	lock := resolveSymlink(path) + ".lock"
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		if fi, serr := os.Stat(lock); serr == nil && time.Since(fi.ModTime()) > lockStale {
			os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s is locked by another gossms instance (remove %s if none is running)", path, lock)
		}
		time.Sleep(lockPoll)
	}
	defer os.Remove(lock)
	return fn()
}
