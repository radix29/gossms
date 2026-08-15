package fileutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWriteAtomicCreatesTheFileWithItsContentsAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := WriteAtomic(path, []byte(`{"connections":[]}`), 0o600); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != `{"connections":[]}` {
		t.Errorf("contents = %q, want the bytes written", got)
	}

	// CreateTemp makes the temp file 0600, so a caller asking for anything
	// wider only gets it because WriteAtomic chmods before the rename.
	// Skipped on Windows, which has no POSIX mode bits to assert.
	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %04o, want 0600", got)
	}
}

func TestWriteAtomicAppliesAWiderModeThanCreateTempsOwn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX mode bits on Windows")
	}
	path := filepath.Join(t.TempDir(), "script.sql")
	if err := WriteAtomic(path, []byte("SELECT 1\n"), 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o644 {
		t.Errorf("mode = %04o, want 0644 — the chmod before the rename is what "+
			"stops every file inheriting CreateTemp's 0600", got)
	}
}

// The bug this pins: every caller passes a constant perm, so applying it
// blindly re-widened a script the user had chmodded 0600 back to 0644 on every
// save. os.WriteFile never did that — it doesn't create a new inode — and a
// rename-based write must not either.
func TestWriteAtomicKeepsAnExistingFilesNarrowerMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX mode bits on Windows")
	}
	path := filepath.Join(t.TempDir(), "script.sql")
	if err := os.WriteFile(path, []byte("SELECT 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil { // defeat any umask
		t.Fatal(err)
	}

	if err := WriteAtomic(path, []byte("SELECT 2\n"), 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %04o after saving over a 0600 file, want 0600 — the "+
			"caller's 0644 re-widened it", got)
	}
}

// The other direction, and the reason the existing mode is capped at perm
// rather than preserved outright: a secrets-adjacent file that somehow reached
// 0644 must be tightened back, not kept wide forever.
func TestWriteAtomicTightensAnExistingFileWiderThanPerm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX mode bits on Windows")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteAtomic(path, []byte(`{"connections":[]}`), 0o600); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %04o after saving a 0644 config with perm 0600, want "+
			"0600 — a world-readable config must not stay that way", got)
	}
}

func TestWriteAtomicReplacesAnExistingFileWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("the-old-and-much-longer-contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("contents = %q, want %q — the old contents were not fully replaced", got, "new")
	}
}

func TestWriteAtomicLeavesNoTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := WriteAtomic(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	assertOnlyFile(t, dir, "config.json")
}

// A failed write must leave the original intact and clean up after itself.
//
// An unwritable directory is what makes this discriminating rather than
// merely reassuring: creating the temp file needs write permission on the
// *directory*, where overwriting an existing file does not. So os.WriteFile
// would succeed here and destroy the original, and only a write that goes
// through a new directory entry fails — which is exactly the property the
// temp-file-plus-rename exists to provide. Proven by A/B on 2026-08-14.
func TestWriteAtomicFailureKeepsTheOriginalAndLeavesNoTempFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory mode bits do not deny writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory write permission")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	if err := WriteAtomic(path, []byte("replacement"), 0o600); err == nil {
		t.Fatal("WriteAtomic into an unwritable directory succeeded, want an error")
	} else if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("WriteAtomic error = %v, want a permission error", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "original" {
		t.Errorf("contents = %q after a failed write, want the original untouched", got)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	assertOnlyFile(t, dir, "config.json")
}

// The temp file must be a sibling of path: a rename across filesystems is not
// atomic, and os.Rename refuses one outright. Nothing else in the package
// exposes where it lands, so this reads it off a directory that starts empty.
func TestWriteAtomicWritesItsTempFileBesidePath(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "config.json")
	if err := WriteAtomic(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	assertOnlyFile(t, dir, "sub")
	assertOnlyFile(t, sub, "config.json")
}

func TestWriteAtomicWritesAnEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.sql")
	if err := WriteAtomic(path, nil, 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Size() != 0 {
		t.Errorf("size = %d, want 0", fi.Size())
	}
}

// assertOnlyFile fails unless dir contains exactly the named entry — the way
// a stray ".tmp" left by a failed or interrupted write shows up.
func assertOnlyFile(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if len(got) != 1 || got[0] != name {
		t.Errorf("%s contains %v, want only %q — a temp file was left behind",
			dir, got, name)
	}
	for _, n := range got {
		if strings.Contains(n, ".tmp") {
			t.Errorf("%s still holds temp file %q", dir, n)
		}
	}
}

// A rename replaces a directory entry, so without resolving first, saving a
// symlinked script would turn the link into a regular file and leave the real
// file with its old contents. A plain os.WriteFile follows the link for free;
// this has to be made to.
func TestWriteAtomicWritesThroughASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need elevation on Windows")
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "real.sql")
	link := filepath.Join(dir, "link.sql")
	if err := os.WriteFile(real, []byte("SELECT 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	if err := WriteAtomic(link, []byte("SELECT 2\n"), 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a regular file")
	}
	got, err := os.ReadFile(real)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "SELECT 2\n" {
		t.Errorf("the link's target reads %q, want the new contents — the write "+
			"landed on the link instead of through it", got)
	}
	assertOnlyFile2(t, dir, "link.sql", "real.sql")
}

// A link pointing at a file that doesn't exist yet must be *created through*,
// not written over. EvalSymlinks fails on a dangling link, and reading the
// contents back through the link passes either way — so this asserts the link
// is still a link and the target now exists, which is what separates the two.
func TestWriteAtomicCreatesThroughADanglingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need elevation on Windows")
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "link.sql")
	target := filepath.Join(dir, "not-there-yet.sql")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := WriteAtomic(link, []byte("SELECT 1\n"), 0o644); err != nil {
		t.Fatalf("WriteAtomic through a dangling symlink: %v", err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		t.Error("the dangling symlink was replaced by a regular file")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile of the link's target: %v", err)
	}
	if string(got) != "SELECT 1\n" {
		t.Errorf("the target reads %q, want the bytes written", got)
	}
	assertOnlyFile2(t, dir, "link.sql", "not-there-yet.sql")
}

// A link whose target is itself a dangling link: the by-hand fallback has to
// follow the chain, not just one hop.
func TestWriteAtomicFollowsAChainOfDanglingSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need elevation on Windows")
	}
	dir := t.TempDir()
	outer := filepath.Join(dir, "outer.sql")
	inner := filepath.Join(dir, "inner.sql")
	target := filepath.Join(dir, "real.sql")
	if err := os.Symlink(inner, outer); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.sql", inner); err != nil { // relative, on purpose
		t.Fatal(err)
	}

	if err := WriteAtomic(outer, []byte("SELECT 1\n"), 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile of the chain's target: %v", err)
	}
	if string(got) != "SELECT 1\n" {
		t.Errorf("the target reads %q, want the bytes written", got)
	}
	for _, l := range []string{outer, inner} {
		fi, err := os.Lstat(l)
		if err != nil {
			t.Fatalf("Lstat %s: %v", l, err)
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			t.Errorf("%s was replaced by a regular file", filepath.Base(l))
		}
	}
}

// A link cycle must not hang the save. The hop limit gives up and writes at
// whatever the walk last reached, which is a link — so the save fails or
// replaces a link, but it returns.
func TestWriteAtomicSurvivesASymlinkCycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need elevation on Windows")
	}
	dir := t.TempDir()
	a := filepath.Join(dir, "a.sql")
	b := filepath.Join(dir, "b.sql")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- WriteAtomic(a, []byte("x"), 0o644) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("WriteAtomic did not return on a symlink cycle")
	}
}

// assertOnlyFile2 is assertOnlyFile for a directory expected to hold exactly
// two named entries.
func assertOnlyFile2(t *testing.T, dir, a, b string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("%s still holds temp file %q", dir, e.Name())
		}
	}
	if len(got) != 2 {
		t.Errorf("%s contains %v, want exactly %q and %q", dir, got, a, b)
	}
}
