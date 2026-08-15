// Package fileutil holds the file-writing helpers shared across gossms.
package fileutil

import (
	"io/fs"
	"os"
	"path/filepath"
)

// WriteAtomic writes data to path by way of a temp file in the same
// directory plus a rename, so path is only ever replaced whole. A plain
// os.WriteFile truncates in place: a crash, a full disk, or a power loss
// partway through leaves a half-written file behind, and the original is
// already gone. config.Save is the worked example — a truncated config.json
// is invalid JSON, which Load discards entirely, silently taking every saved
// connection with it — but a half-written .sql script the user spent an hour
// on is the same loss. The temp file has to share path's directory for the
// rename to be atomic, since a rename across filesystems isn't.
//
// perm is the mode a *new* file gets, and the widest an existing one is left
// with — an existing file otherwise keeps the mode it already has. See modeFor.
//
// Both halves need flushing, not just the file: the rename is a change to
// the *directory*, so syncing only the temp file leaves durable bytes under
// a name that a post-crash directory may not carry yet, and the old contents
// come back. That is the same outcome the temp-file dance exists to prevent,
// just through a narrower window.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	path = resolveSymlink(path)
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename below has succeeded

	// CreateTemp makes the file 0600; the mode below is what the file ends up
	// with, so a mode of its own never depends on that staying true.
	if err := f.Chmod(modeFor(path, perm)); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	// Flush to disk before the rename, so a crash right after it can't
	// leave the new name pointing at an empty or partial file.
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

// resolveSymlink returns the file path actually names, following symlinks, so
// WriteAtomic writes *through* a link instead of replacing it.
//
// The rename is the reason this is needed at all. A write-in-place follows a
// symlink for free; a rename does not — it replaces the directory entry, so
// saving a script that was symlinked into a repo would silently turn the link
// into a regular file and leave the real file untouched. Resolving first moves
// both the temp file and the rename to the link's target directory, which is
// also where they have to be for the rename to stay atomic.
//
// EvalSymlinks does the whole job when every component exists. It fails on a
// path whose target doesn't — an ordinary new file, but also a *dangling* link,
// which is the case that has to keep working: a script symlinked to a name not
// written yet must be created at the target, not on top of the link. So the
// fallback walks the last component by hand, and only a path that isn't a link
// at all is used as given. A relative link resolves against the link's own
// directory, matching EvalSymlinks; the hop limit is what stops a link cycle
// spinning here rather than failing the save.
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

// modeFor decides what mode WriteAtomic's replacement file gets: the mode path
// already has, with perm as a ceiling. A path that doesn't exist yet (or can't
// be read) simply gets perm.
//
// Both halves are load-bearing, in opposite directions.
//
// Preserving the existing mode is what stops a rename-based write behaving
// differently from a write-in-place one. Every caller passes a constant — 0600
// for config.json and gossms.key, 0644 for a saved script — and applying it
// blindly re-widens a file on every save: a .sql the user chmodded 0600
// silently came back 0644 the next time Ctrl+S ran. os.WriteFile never did that,
// because it doesn't create a new inode; this must not either.
//
// Capping at perm is what stops that preservation becoming a security bug. The
// caller's perm is the widest the file is ever allowed to be, so a config.json
// or gossms.key that somehow reached 0644 — a legacy write, a stray chmod, a
// restore from a backup taken elsewhere — is tightened back to 0600 on the next
// save rather than kept wide forever. Preserving the mode *exactly* would make
// that permanent, which is the one outcome worse than the bug this fixes.
//
// The cost is narrow and accepted: a script at 0664 in a group-writable tree
// comes back 0644, losing group write. Erring toward the tighter mode is the
// right direction for a file that may hold credentials, and no caller passes a
// perm wider than it means.
//
// WriteAtomic has already resolved any symlink by the time this runs, so the
// mode read here is the target's — the file actually being replaced.
func modeFor(path string, perm os.FileMode) os.FileMode {
	fi, err := os.Stat(path)
	if err != nil {
		return perm
	}
	return fi.Mode().Perm() & perm
}

// syncDir flushes a directory's own contents — the entry WriteAtomic's
// rename just created — so the rename survives a crash rather than only the
// bytes it points at.
//
// Best-effort, and deliberately returning nothing: this must not be able to
// fail a save that has already succeeded. Syncing a directory is a POSIX
// notion, and on Windows FlushFileBuffers rejects a handle opened for
// reading — so reporting the error would turn a durability nicety into "the
// config never saves" on one of the three platforms this single binary
// targets. Where the call works it closes the crash window; where it doesn't
// the file's own Sync above is still what it always was.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	d.Close()
}
