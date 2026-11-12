package dialogs

import (
	"os"
	"path/filepath"
	"testing"
)

// "Not there" is an answer, including a path through a file (ENOTDIR).
func TestLocalFileSystemExistsMissingIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, "missing"), filepath.Join(file, "child")} {
		exists, isDir, err := LocalFileSystem{}.Exists(p)
		if exists || isDir || err != nil {
			t.Errorf("Exists(%q) = (%v, %v, %v), want (false, false, nil)", p, exists, isDir, err)
		}
	}
}

// Any other Stat failure is "couldn't ask" and must reach the caller: the
// save-overwrite guard prompts on it, and swallowing it skipped the prompt.
func TestLocalFileSystemExistsReportsPermissionError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })

	exists, _, err := LocalFileSystem{}.Exists(filepath.Join(locked, "x"))
	if err == nil {
		t.Fatalf("Exists under an unreadable directory = (%v, nil), want an error", exists)
	}
}
