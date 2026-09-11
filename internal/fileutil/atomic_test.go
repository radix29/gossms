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

	// CreateTemp makes the temp file 0600, so a wider mode proves WriteAtomic
	// chmods before the rename. No POSIX mode bits on Windows.
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

// Callers pass a constant perm; applying it blindly would re-widen a script the
// user chmodded 0600 on every save.
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

// The existing mode is capped at perm, not preserved outright: a secrets file
// that reached 0644 must be tightened.
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

// A failed write must leave the original intact and no temp file. An unwritable
// directory discriminates: creating the temp file needs directory write
// permission, overwriting in place doesn't, so os.WriteFile would succeed here
// and destroy the original.
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

// The temp file must be a sibling of path: os.Rename refuses cross-filesystem
// renames. Observed via a directory that starts empty.
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

// assertOnlyFile fails unless dir holds exactly the named entry, catching a
// stray ".tmp".
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

// Without resolving first, saving a symlinked script would replace the link
// with a regular file and leave the target stale.
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

// A dangling link must be created through, not overwritten. Reading back
// through the link passes either way, so assert the link is still a link and
// the target exists.
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

// The fallback must follow a chain of dangling links, not just one hop.
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

// A link cycle must not hang the save; the hop limit makes it return.
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

// assertOnlyFile2 is assertOnlyFile for exactly two entries.
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
