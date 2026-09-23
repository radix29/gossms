// Package fileutil holds shared file-writing helpers.
package fileutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteAtomic writes data to a temp file in path's directory and renames it
// over path, so path is only ever replaced whole. os.WriteFile truncates in
// place: a crash, full disk or power loss mid-write leaves a half file and the
// original is gone (a truncated config.json loses every saved connection). The
// temp file must share path's directory; a cross-filesystem rename isn't
// atomic.
//
// perm is the mode a new file gets and the widest an existing one keeps; see
// modeFor.
//
// Both the file and the directory are synced: the rename changes the directory,
// and without syncing it a crash can bring the old contents back.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	path = resolveSymlink(path)
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename below has succeeded

	// CreateTemp makes the file 0600; set the final mode explicitly rather than
	// rely on that.
	if err := f.Chmod(modeFor(path, perm)); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	// Sync before the rename so a crash can't leave the new name pointing at a
	// partial file.
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// CreateAtomic is WriteAtomic for a file that must never be replaced once it
// exists: it writes data to a temp file beside path and hard-links it into
// place, which fails rather than overwrites if path appeared meanwhile.
// created is false, with no error, when path already existed — the caller
// then reads the winner's file instead of its own data.
//
// It exists for two processes creating the same file at once. With
// WriteAtomic's rename both "succeed" and the later rename silently wins, so
// the earlier writer goes on using data that is no longer on disk.
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

	if err := f.Chmod(perm); err != nil {
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

// resolveSymlink returns the file path names, following symlinks, so
// WriteAtomic writes through a link instead of replacing it. A rename replaces
// the directory entry, so without this saving a symlinked script would turn the
// link into a regular file and leave the target untouched. Resolving also puts
// the temp file in the target's directory, where the rename stays atomic.
//
// EvalSymlinks fails when the target doesn't exist — including a dangling link,
// which must still create the target, not overwrite the link. So the fallback
// walks the last component by hand; a relative link resolves against its own
// directory, and a hop limit stops link cycles.
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

// modeFor returns the mode path already has, capped at perm; a missing or
// unreadable path gets perm.
//
// Preserving the mode keeps rename-based writes behaving like os.WriteFile:
// callers pass a constant (0600 for config.json, gossms.key,
// tracked_queries.json; 0644 for scripts), and applying it blindly would
// re-widen a script the user chmodded 0600 on every save.
//
// Capping at perm tightens a config.json or gossms.key that somehow reached
// 0644 back to 0600 instead of keeping it wide forever.
//
// The mode read is the symlink target's; WriteAtomic resolved it already.
//
// Ownership is not preserved (the new file belongs to the running user). That
// only matters when running as root over another user's file, which isn't
// supported, and fixing it would need OS branching.
func modeFor(path string, perm os.FileMode) os.FileMode {
	fi, err := os.Stat(path)
	if err != nil {
		return perm
	}
	return fi.Mode().Perm() & perm
}

// syncDir flushes the directory entry WriteAtomic's rename created, so the
// rename survives a crash.
//
// Best-effort and silent: it must not fail a save that already succeeded.
// Directory sync is POSIX-only — Windows FlushFileBuffers rejects a read handle
// — so reporting errors would break saving on Windows.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	d.Close()
}
