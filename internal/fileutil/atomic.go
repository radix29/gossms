// Package fileutil holds the file-writing helpers shared across gossms.
package fileutil

import (
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
// Both halves need flushing, not just the file: the rename is a change to
// the *directory*, so syncing only the temp file leaves durable bytes under
// a name that a post-crash directory may not carry yet, and the old contents
// come back. That is the same outcome the temp-file dance exists to prevent,
// just through a narrower window.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename below has succeeded

	// CreateTemp makes the file 0600; the caller's perm is what the file
	// ends up with, so a mode of its own never depends on that staying true.
	if err := f.Chmod(perm); err != nil {
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
