// Package fileutil holds shared file-writing helpers.
package fileutil

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// WriteAtomic writes data to a temp file in path's directory and renames it over
// path, so path is only ever replaced whole. os.WriteFile truncates in place: a
// crash mid-write leaves a half file (a truncated config.json loses every saved
// connection). The temp file must share path's directory; a cross-filesystem
// rename is not atomic.
//
// perm is the mode a new file gets, less the umask, and the widest an existing
// one keeps; see modeFor.
//
// Both the file and the directory are synced: without syncing the directory a
// crash can bring the old contents back.
//
// An existing file that os.WriteFile could not open for writing (read-only mode
// or attribute, ACL) is refused: a rename needs only the directory's write
// permission, so it would otherwise replace the file the user protected.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	st, err := CreateStream(path, perm)
	if err != nil {
		return err
	}
	if _, err := st.Write(data); err != nil {
		st.Abort()
		return err
	}
	return st.Commit()
}

// Stream is WriteAtomic for output produced piecemeal: it writes to a temp file
// beside the target and replaces the target only at Commit, so a run that fails
// or is abandoned (Abort) leaves the old contents untouched. Not safe for
// concurrent use.
type Stream struct {
	f    *os.File
	path string // the resolved target
	done bool
}

// CreateStream opens a Stream for path, applying WriteAtomic's checks (symlink
// resolution, the read-only refusal, the mode rule) up front so a destination
// that can't be replaced fails before the caller starts producing output.
func CreateStream(path string, perm os.FileMode) (*Stream, error) {
	path = resolveSymlink(path)
	if err := checkWritable(path); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp")
	if err != nil {
		return nil, err
	}
	// CreateTemp makes the file 0600; set the final mode explicitly rather than
	// rely on that.
	if err := f.Chmod(modeFor(path, perm)); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, err
	}
	return &Stream{f: f, path: path}, nil
}

// Write appends p to the temp file.
func (s *Stream) Write(p []byte) (int, error) { return s.f.Write(p) }

// Commit syncs and closes the temp file, renames it over the target and syncs
// the directory. On any error the temp file is removed and the target is left as
// it was. Commit after Commit or Abort is a no-op returning nil.
func (s *Stream) Commit() error {
	if s.done {
		return nil
	}
	s.done = true
	tmp := s.f.Name()
	// Sync before the rename so a crash can't leave the new name pointing at a
	// partial file.
	if err := s.f.Sync(); err != nil {
		s.f.Close()
		os.Remove(tmp)
		return err
	}
	if err := s.f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return err
	}
	syncDir(filepath.Dir(s.path))
	RemoveStaleTemps(s.path)
	return nil
}

// Abort discards the temp file, leaving the target as it was. Abort after
// Commit or Abort is a no-op.
func (s *Stream) Abort() {
	if s.done {
		return
	}
	s.done = true
	s.f.Close()
	os.Remove(s.f.Name())
}

// CreateAtomic is WriteAtomic for a file that must never be replaced once it
// exists: it writes data to a temp file beside path and hard-links it into
// place, which fails rather than overwrites if path appeared meanwhile. created
// is false, with no error, when path already existed — the caller then reads the
// winner's file instead of its own data.
//
// For two processes creating the same file at once: with WriteAtomic's rename
// both "succeed" and the later one silently wins.
//
// A filesystem that cannot hard-link falls back to an O_EXCL create written in
// place: still create-if-absent, but a concurrent reader can see it partly
// written.
func CreateAtomic(path string, data []byte, perm os.FileMode) (created bool, err error) {
	path = resolveSymlink(path)
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp")
	if err != nil {
		return false, err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // the link, if made, keeps the data alive under path

	if err := f.Chmod(perm &^ umask()); err != nil {
		f.Close()
		return false, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return false, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return false, err
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	switch err := os.Link(tmp, path); {
	case err == nil:
		syncDir(filepath.Dir(path))
		return true, nil
	case errors.Is(err, fs.ErrExist):
		return false, nil
	}
	return createExclusiveInPlace(path, data, perm)
}

// createExclusiveInPlace is CreateAtomic's fallback where os.Link fails for a
// reason other than the target existing (FAT, some network filesystems).
func createExclusiveInPlace(path string, data []byte, perm os.FileMode) (bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, werr := f.Write(data)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		os.Remove(path)
		return false, err
	}
	syncDir(filepath.Dir(path))
	return true, nil
}

// StaleTempAge is how old a temp file left beside a target must be before
// RemoveStaleTemps deletes it. A save takes milliseconds; the margin keeps a
// write in progress in another gossms process (or over a slow share) safe.
const StaleTempAge = 5 * time.Minute

// RemoveStaleTemps deletes the temp files WriteAtomic and CreateAtomic left
// beside path — path's base + ".tmp" + CreateTemp's digits — older than
// StaleTempAge. The deferred remove never runs when the process dies between
// CreateTemp and the rename (watchdog os.Exit, a kill, a crash), and each
// leftover of config.json is a full copy of it, encrypted passwords included.
//
// Only that exact pattern is matched, so in a user's script folder nothing but
// gossms's own leftovers for this one file can go. Best-effort and silent: a
// file it can't read or remove (held open on Windows) is left for next time.
func RemoveStaleTemps(path string) {
	path = resolveSymlink(path)
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return
	}
	prefix := filepath.Base(path) + ".tmp"
	cutoff := time.Now().Add(-StaleTempAge)
	for _, e := range entries {
		digits, ok := strings.CutPrefix(e.Name(), prefix)
		if !ok || !allDigits(digits) || !e.Type().IsRegular() {
			continue
		}
		if fi, err := e.Info(); err == nil && fi.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(filepath.Dir(path), e.Name()))
		}
	}
}

// allDigits reports whether s is a non-empty run of ASCII digits, the random
// part os.CreateTemp puts in place of no "*".
func allDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// isTempName reports whether name is a WriteAtomic/CreateAtomic temp file,
// possibly one still being written.
func isTempName(name string) bool {
	_, after, ok := strings.CutLast(name, ".tmp")
	return ok && allDigits(after)
}

// KeepCorrupt saves data — path's contents, which did not parse — beside it as
// path.corrupt-<yyyymmdd_hhmmss>, so what the file held stays readable by hand
// after the caller starts empty. It returns the copy's path.
//
// Each corruption gets its own copy: one fixed name would replace the first
// recovery copy when the file is corrupted again. A copy already holding the
// same bytes (an older one, or the plain .corrupt name) is reused, since Load
// and every Save re-read the same broken file. The copy is created, never
// overwritten (CreateAtomic), so two processes can't replace each other's.
//
// An error means data exists nowhere but in path: the caller must not write
// over path.
func KeepCorrupt(path string, data []byte, now time.Time) (string, error) {
	path = resolveSymlink(path)
	dir, base := filepath.Dir(path), filepath.Base(path)+".corrupt"
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), base) || !e.Type().IsRegular() || isTempName(e.Name()) {
				continue
			}
			old := filepath.Join(dir, e.Name())
			if held, err := os.ReadFile(old); err == nil && bytes.Equal(held, data) {
				return old, nil
			}
		}
	}
	name := filepath.Join(dir, base+"-"+now.Format("20060102_150405"))
	for i := range 100 {
		dst := name
		if i > 0 {
			dst = fmt.Sprintf("%s-%d", name, i)
		}
		created, err := CreateAtomic(dst, data, 0o600)
		if err != nil {
			return "", err
		}
		if created {
			return dst, nil
		}
	}
	return "", fmt.Errorf("no free name for a copy of %s beside it", base)
}

// resolveSymlink returns the file path names, following symlinks, so WriteAtomic
// writes through a link instead of replacing it. A rename replaces the directory
// entry, so otherwise saving a symlinked script would turn the link into a
// regular file and leave the target untouched. Resolving also puts the temp file
// in the target's directory, where the rename stays atomic.
//
// EvalSymlinks fails when the target doesn't exist — including a dangling link,
// which must still create the target, not overwrite the link. So the fallback
// walks the last component by hand; a relative link resolves against its own
// directory, and a hop limit stops cycles.
func resolveSymlink(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	for range 16 {
		fi, err := os.Lstat(path)
		if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
			return path
		}
		target, err := os.Readlink(path)
		if err != nil {
			return path
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = target
	}
	return path
}

// checkWritable opens an existing path for writing, without O_TRUNC, and closes
// it at once, so the OS's own rules (root, ACLs, the Windows read-only attribute)
// decide, as they would for os.WriteFile. A missing path is fine: WriteAtomic
// creates it. Any other open failure is returned as is.
func checkWritable(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}

// modeFor returns the mode path already has, capped at perm; a missing or
// unreadable path gets perm less the umask, as os.WriteFile's would.
//
// Preserving the mode keeps rename-based writes behaving like os.WriteFile: a
// script the user chmodded 0600 stays 0600 across saves.
//
// The umask matters because the chmod is explicit: the kernel applies the umask
// only at create, and CreateTemp creates at 0600, so an explicit 0644 chmod
// would bypass a umask of 077.
//
// Capping at perm tightens a config.json or gossms.key that reached 0644 back to
// 0600. The mode read is the symlink target's (WriteAtomic resolved it already).
//
// Ownership is not preserved: the new file belongs to the running user.
// Preserving it needs OS branching and matters only for root writing another
// user's file, which is unsupported.
func modeFor(path string, perm os.FileMode) os.FileMode {
	fi, err := os.Stat(path)
	if err != nil {
		return perm &^ umask()
	}
	return fi.Mode().Perm() & perm
}

// umask is the process's file-creation mask. A variable so tests can set one
// without changing the process's own.
var umask = sync.OnceValue(probeUmask)

// probeUmask reads the umask by creating a file at 0777 and seeing which bits the
// kernel kept: Go has no portable read of it (syscall.Umask sets the mask to read
// it, racing other goroutines' creates). Read once: gossms never changes its
// umask. Any failure reads as no mask. On Windows the probe reads 0666 back, a
// mask of 0111 that no caller's perm has bits in.
func probeUmask() os.FileMode {
	dir, err := os.MkdirTemp("", "gossms-umask")
	if err != nil {
		return 0
	}
	defer os.RemoveAll(dir)
	f, err := os.OpenFile(filepath.Join(dir, "probe"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o777)
	if err != nil {
		return 0
	}
	fi, err := f.Stat()
	f.Close()
	if err != nil {
		return 0
	}
	return 0o777 &^ fi.Mode().Perm()
}

// syncDir flushes the directory entry WriteAtomic's rename created, so the
// rename survives a crash. Best-effort and silent: it must not fail a save that
// already succeeded. Directory sync is POSIX-only (Windows FlushFileBuffers
// rejects a read handle), so reporting errors would break saving on Windows.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	d.Close()
}
