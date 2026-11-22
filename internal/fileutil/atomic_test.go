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
	setUmask(t, 0o022)
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

// A rename needs only directory permission, so without the writability probe a
// save would replace a file the user made read-only, which os.WriteFile refuses.
func TestWriteAtomicRefusesAReadOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits do not set the read-only attribute the same way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores file write permission")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "query.sql")
	if err := os.WriteFile(path, []byte("original"), 0o444); err != nil {
		t.Fatal(err)
	}

	if err := WriteAtomic(path, []byte("replacement"), 0o644); err == nil {
		t.Fatal("WriteAtomic over a read-only file succeeded, want an error")
	} else if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("WriteAtomic error = %v, want a permission error", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "original" {
		t.Errorf("contents = %q after a refused write, want the original untouched", got)
	}
	assertOnlyFile(t, dir, "query.sql")
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

func TestCreateAtomicNeverReplacesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key")

	created, err := CreateAtomic(path, []byte("first"), 0o600)
	if err != nil || !created {
		t.Fatalf("first create: created %v, err %v", created, err)
	}
	created, err = CreateAtomic(path, []byte("second"), 0o600)
	if err != nil || created {
		t.Fatalf("second create: created %v, err %v; want false, nil", created, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "first" {
		t.Errorf("contents = %q, want the first writer's", got)
	}
	assertOnlyFile(t, dir, "key")
}

// setUmask makes WriteAtomic see mask for the rest of the test, without
// touching the process's real umask.
func setUmask(t *testing.T, mask os.FileMode) {
	t.Helper()
	saved := umask
	umask = func() os.FileMode { return mask }
	t.Cleanup(func() { umask = saved })
}

// A new file honours the umask as os.WriteFile's would: under umask 077 a
// script saved with perm 0644 is the user's alone (K14). The explicit chmod
// after CreateTemp used to bypass it.
func TestWriteAtomicNewFileHonoursTheUmask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX mode bits on Windows")
	}
	setUmask(t, 0o077)
	dir := t.TempDir()

	path := filepath.Join(dir, "script.sql")
	if err := WriteAtomic(path, []byte("SELECT 1\n"), 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if fi, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("WriteAtomic under umask 077: mode = %04o, want 0600", got)
	}

	key := filepath.Join(dir, "gossms.key")
	if _, err := CreateAtomic(key, []byte("k"), 0o644); err != nil {
		t.Fatalf("CreateAtomic: %v", err)
	}
	if fi, err := os.Stat(key); err != nil {
		t.Fatal(err)
	} else if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("CreateAtomic under umask 077: mode = %04o, want 0600", got)
	}
}

// The probe agrees with what the kernel does to an ordinary create, so a new
// file from WriteAtomic gets the mode os.WriteFile would have given it.
func TestProbeUmaskMatchesAnOrdinaryCreate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX mode bits on Windows")
	}
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(plain)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := 0o666&^probeUmask(), fi.Mode().Perm(); got != want {
		t.Errorf("0666 less the probed umask = %04o, os.WriteFile gave %04o", got, want)
	}
}

// ageFile backdates path's modification time by d.
func ageFile(t *testing.T, path string, d time.Duration) {
	t.Helper()
	old := time.Now().Add(-d)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
}

// Only the target's own temp files past StaleTempAge go: a fresh one may be a
// save in progress in another process, and anything else in the folder is
// someone's file.
func TestRemoveStaleTempsRemovesOnlyOldTempsOfThatFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	files := map[string]struct {
		age  time.Duration
		gone bool
	}{
		"config.json.tmp123456":   {time.Hour, true},
		"config.json.tmp987":      {StaleTempAge + time.Minute, true},
		"config.json.tmp555":      {time.Second, false}, // in progress
		"config.json.tmpnotes":    {time.Hour, false},   // not CreateTemp's digits
		"config.json.tmp":         {time.Hour, false},
		"other.json.tmp123":       {time.Hour, false}, // another file's
		"config.json":             {time.Hour, false},
		"config.json.corrupt-1":   {time.Hour, false},
		"xconfig.json.tmp1234567": {time.Hour, false},
	}
	for name, f := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		ageFile(t, p, f.age)
	}

	RemoveStaleTemps(path)

	for name, f := range files {
		_, err := os.Stat(filepath.Join(dir, name))
		if gone := errors.Is(err, fs.ErrNotExist); gone != f.gone {
			t.Errorf("%s: removed = %v, want %v", name, gone, f.gone)
		}
	}
}

// A save sweeps the leftovers of an earlier one the process died in.
func TestWriteAtomicSweepsAnEarlierSavesStaleTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "query.sql")
	stale := filepath.Join(dir, "query.sql.tmp4242")
	if err := os.WriteFile(stale, []byte("select 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	ageFile(t, stale, time.Hour)

	if err := WriteAtomic(path, []byte("select 2"), 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the stale temp file is still there (err %v)", err)
	}
}

// corruptCopies lists the KeepCorrupt copies of path, by name.
func corruptCopies(t *testing.T, path string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), filepath.Base(path)+".corrupt") {
			data, err := os.ReadFile(filepath.Join(filepath.Dir(path), e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			got[e.Name()] = string(data)
		}
	}
	return got
}

// A second corruption must not replace the first one's copy, even within the
// same second.
func TestKeepCorruptKeepsEachCorruptionInItsOwnCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	now := time.Date(2026, 10, 15, 10, 30, 5, 0, time.Local)

	first, err := KeepCorrupt(path, []byte("{first"), now)
	if err != nil {
		t.Fatalf("KeepCorrupt: %v", err)
	}
	if want := path + ".corrupt-20261015_103005"; first != want {
		t.Errorf("copy = %s, want %s", first, want)
	}
	second, err := KeepCorrupt(path, []byte("{second"), now)
	if err != nil {
		t.Fatalf("KeepCorrupt: %v", err)
	}
	if second == first {
		t.Fatalf("the second corruption reused %s", first)
	}
	got := corruptCopies(t, path)
	if len(got) != 2 || got[filepath.Base(first)] != "{first" || got[filepath.Base(second)] != "{second" {
		t.Errorf("copies = %v, want both corruptions kept", got)
	}
}

// The same broken bytes re-read (Load, then each Save) are kept once, and a
// copy under the old fixed .corrupt name counts.
func TestKeepCorruptReusesACopyOfTheSameBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := path + ".corrupt"
	if err := os.WriteFile(legacy, []byte("{old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := KeepCorrupt(path, []byte("{old"), time.Now()); err != nil || got != legacy {
		t.Errorf("KeepCorrupt = %s, %v; want the existing %s", got, err, legacy)
	}
	a, err := KeepCorrupt(path, []byte("{new"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if b, err := KeepCorrupt(path, []byte("{new"), time.Now().Add(time.Hour)); err != nil || b != a {
		t.Errorf("KeepCorrupt again = %s, %v; want %s reused", b, err, a)
	}
	if got := corruptCopies(t, path); len(got) != 2 {
		t.Errorf("copies = %v, want the legacy one and one new", got)
	}
}

func TestKeepCorruptReportsACopyItCouldNotMake(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "config.json")
	if _, err := KeepCorrupt(path, []byte("{x"), time.Now()); err == nil {
		t.Error("KeepCorrupt into a folder that doesn't exist reported success")
	}
}

func TestStreamAbortLeavesTheOldContentsAndNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.csv")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := CreateStream(path, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	st.Abort()
	if got, _ := os.ReadFile(path); string(got) != "original" {
		t.Errorf("contents = %q after Abort, want the original", got)
	}
	assertOnlyFile(t, dir, "results.csv")
}

func TestStreamCommitReplacesTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.csv")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := CreateStream(path, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	st.Write([]byte("new "))
	st.Write([]byte("rows"))
	if err := st.Commit(); err != nil {
		t.Fatal(err)
	}
	st.Abort() // after Commit: a no-op
	if got, _ := os.ReadFile(path); string(got) != "new rows" {
		t.Errorf("contents = %q after Commit, want the new rows", got)
	}
	assertOnlyFile(t, dir, "results.csv")
}

func TestCreateStreamRefusesAReadOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits do not set the read-only attribute the same way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores file write permission")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "results.csv")
	if err := os.WriteFile(path, []byte("original"), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateStream(path, 0o644); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("CreateStream error = %v, want a permission error", err)
	}
	assertOnlyFile(t, dir, "results.csv")
}

// Rename returns a persistent denial as itself once renameRetry has passed,
// not as a timeout or a nil.
func TestRenameReturnsAPersistentDenial(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the POSIX mode bits can make read-only, as non-root")
	}
	defer func(d time.Duration) { renameRetry = d }(renameRetry)
	renameRetry = 20 * time.Millisecond
	dir := t.TempDir()
	src := filepath.Join(dir, "a")
	if err := os.WriteFile(src, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if err := Rename(src, filepath.Join(dir, "b")); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Rename = %v, want a permission error", err)
	}
}
