package dialogs

import (
	"errors"
	"strings"
	"testing"
)

// fakeWindowsFS is a stand-in for a remote Windows host: backslash paths and
// drive-letter roots, with a canned directory tree. It exists to pin the
// behaviour on a client whose own path rules are different — this test
// package runs on Linux, so every assertion below fails if FileDialog reaches
// for path/filepath anywhere.
type fakeWindowsFS struct {
	WindowsPathRules
	tree      map[string][]FileEntry
	existsErr error
	// listCalls/existsCalls count round trips. On a remote host each one is
	// a network call inside the event handler, so the tests below assert on
	// how many the dialog makes, not just on what it ends up showing.
	listCalls, existsCalls int
}

func newFakeWindowsFS() *fakeWindowsFS {
	return &fakeWindowsFS{tree: map[string][]FileEntry{
		"": {{Name: "C:", IsDir: true}},
		`C:\`: {
			{Name: "Backup", IsDir: true},
			{Name: "Windows", IsDir: true},
			{Name: "pagefile.sys", Size: 100},
		},
		`C:\Backup`: {
			{Name: "backup_test_full.bak", Size: 4096},
			{Name: "HealthClinic_full.bak", Size: 8192},
			{Name: "old", IsDir: true},
		},
	}}
}

func (f *fakeWindowsFS) List(dir string) ([]FileEntry, error) {
	f.listCalls++
	entries, ok := f.tree[dir]
	if !ok {
		return nil, errors.New("no such directory: " + dir)
	}
	return entries, nil
}

func (f *fakeWindowsFS) Default() string { return `C:\Backup` }

// existsErr, when set, is what Exists reports instead of an answer — the
// unreachable-server case.
func (f *fakeWindowsFS) Exists(path string) (bool, bool, error) {
	f.existsCalls++
	if f.existsErr != nil {
		return false, false, f.existsErr
	}
	if _, ok := f.tree[path]; ok {
		return true, true, nil
	}
	dir, name := f.Split(path)
	for _, e := range f.tree[strings.TrimSuffix(dir, `\`)] {
		if e.Name == name {
			return true, e.IsDir, nil
		}
	}
	return false, false, nil
}

// The path the dialog hands back must be the server's, byte for byte. Before
// FileDialog took a FileSystem it built every path with path/filepath, so on
// a Linux client `C:\Backup\db.bak` was not absolute and Save returned
// "/current/working/dir/C:\Backup\db.bak" — a destination BACKUP cannot
// write, reported without any error.
func TestShowSaveOnReturnsServerPathUnchanged(t *testing.T) {
	fs := newFakeWindowsFS()
	d := NewFileDialog(nil)

	var chosen string
	d.ShowSaveOn(fs, "Backup Destination", `C:\Backup\backup_test_full.bak`, func(p string) { chosen = p })

	if d.dir != `C:\Backup` {
		t.Fatalf("dir = %q, want %q", d.dir, `C:\Backup`)
	}
	if got := d.nameField.Value(); got != "backup_test_full.bak" {
		t.Fatalf("nameField = %q, want %q", got, "backup_test_full.bak")
	}
	d.confirmChoice()
	if chosen != `C:\Backup\backup_test_full.bak` {
		t.Fatalf("chosen = %q, want %q", chosen, `C:\Backup\backup_test_full.bak`)
	}
}

// Picking a file out of the listing must produce that file's server path,
// not the client-joined mixture of the two conventions.
func TestActivateSelectedOnRemoteFileReturnsServerPath(t *testing.T) {
	fs := newFakeWindowsFS()
	d := NewFileDialog(nil)

	var chosen string
	d.ShowOpenOn(fs, "Select Backup File", `C:\Backup\`, func(p string) { chosen = p })

	d.selectByName("HealthClinic_full.bak")
	d.activateSelected()

	if chosen != `C:\Backup\HealthClinic_full.bak` {
		t.Fatalf("chosen = %q, want %q", chosen, `C:\Backup\HealthClinic_full.bak`)
	}
}

// Navigation has to walk the server's tree: down into a subdirectory, back
// up through "..", and up again from the drive root into the drive list.
func TestRemoteNavigation(t *testing.T) {
	fs := newFakeWindowsFS()
	d := NewFileDialog(nil)
	d.ShowOpenOn(fs, "Select Backup File", `C:\Backup\`, nil)

	if d.entries[0].Name != ".." {
		t.Fatalf("entries[0] = %q, want %q", d.entries[0].Name, "..")
	}
	d.activateSelected() // ".."
	if d.dir != `C:\` {
		t.Fatalf("after .. dir = %q, want %q", d.dir, `C:\`)
	}
	if d.listErr != "" {
		t.Fatalf("after .. listErr = %q", d.listErr)
	}

	d.selectByName("Backup")
	d.activateSelected()
	if d.dir != `C:\Backup` {
		t.Fatalf("after descending dir = %q, want %q", d.dir, `C:\Backup`)
	}

	d.sel = 0 // ".."
	d.activateSelected()
	d.sel = 0 // ".." out of C:\ into the drive list
	d.activateSelected()
	if d.dir != "" {
		t.Fatalf("at drive list dir = %q, want %q", d.dir, "")
	}
	if len(d.entries) != 1 || d.entries[0].Name != "C:" {
		t.Fatalf("drive list = %+v, want a single C: entry (no .. row above it)", d.entries)
	}

	d.sel = 0
	d.activateSelected() // back down into the drive
	if d.dir != `C:\` {
		t.Fatalf("after picking a drive dir = %q, want %q", d.dir, `C:\`)
	}
}

// A local browse opened after a remote one must be local again — the two
// share one FileDialog instance, and a leaked remote FileSystem would send
// File > Open Query File to the server's disks.
func TestShowOpenResetsToLocalFileSystem(t *testing.T) {
	d := NewFileDialog(nil)
	d.ShowOpenOn(newFakeWindowsFS(), "Select Backup File", `C:\Backup\`, nil)
	d.ShowOpen("Open Query File", t.TempDir(), nil)

	if _, ok := d.FileSystem().(LocalFileSystem); !ok {
		t.Fatalf("FileSystem() = %T, want LocalFileSystem", d.FileSystem())
	}
}

// A remote Exists that fails must not be read as "no file there": that
// silently skips the save-overwrite guard, so an existing backup is picked
// with no prompt on exactly the connection that is already in trouble.
func TestSaveOverwritePromptsWhenExistsCannotAnswer(t *testing.T) {
	fs := newFakeWindowsFS()
	fs.existsErr = errors.New("i/o timeout")
	d := NewFileDialog(nil)

	prompted, chosen := "", ""
	d.OnConfirmOverwrite = func(path string, proceed func()) { prompted = path }
	// A name the tree does not hold — under the old (bool, bool) signature
	// the timeout and "not there" were the same answer, so this went
	// straight through.
	d.ShowSaveOn(fs, "Backup Destination", `C:\Backup\brand_new.bak`, func(p string) { chosen = p })
	d.confirmChoice()

	if prompted != `C:\Backup\brand_new.bak` {
		t.Errorf("OnConfirmOverwrite path = %q, want the dialog to prompt when Exists failed", prompted)
	}
	if chosen != "" {
		t.Errorf("chosen = %q, want the choice to wait on the prompt", chosen)
	}
}

// The other direction: a filesystem that can answer must still skip the
// prompt for a name that genuinely isn't there.
func TestSaveNewNameSkipsPromptOnRemote(t *testing.T) {
	fs := newFakeWindowsFS()
	d := NewFileDialog(nil)

	prompted, chosen := "", ""
	d.OnConfirmOverwrite = func(path string, proceed func()) { prompted = path }
	d.ShowSaveOn(fs, "Backup Destination", `C:\Backup\brand_new.bak`, func(p string) { chosen = p })
	d.confirmChoice()

	if prompted != "" {
		t.Errorf("OnConfirmOverwrite fired for %q, want no prompt for a new name", prompted)
	}
	if chosen != `C:\Backup\brand_new.bak` {
		t.Errorf("chosen = %q, want %q", chosen, `C:\Backup\brand_new.bak`)
	}
}

// Tab completion against the directory already on screen must reuse that
// listing. It runs on every Tab keypress, and FileSystem is synchronous, so
// a re-list here is a network round trip the user waits out mid-word.
func TestCompletionInCurrentDirCostsNoExtraList(t *testing.T) {
	fs := newFakeWindowsFS()
	d := NewFileDialog(nil)
	d.ShowSaveOn(fs, "Backup Destination", `C:\Backup\`, nil)

	afterLoad := fs.listCalls
	d.nameField.SetValue("backup_")
	if !d.completeField(d.nameField, false) {
		t.Fatal("completeField returned false, want the unique match completed")
	}
	if got := d.nameField.Value(); got != "backup_test_full.bak" {
		t.Errorf("nameField = %q, want %q", got, "backup_test_full.bak")
	}
	if fs.listCalls != afterLoad {
		t.Errorf("List called %d extra time(s) completing in the current directory, want 0",
			fs.listCalls-afterLoad)
	}
}

// The dialog's own ".." row is not a real entry and must stay out of the
// candidate set — as a candidate it drags the common prefix down to "" and
// completion silently stops working in any directory.
func TestCompletionIgnoresTheParentRow(t *testing.T) {
	fs := newFakeWindowsFS()
	d := NewFileDialog(nil)
	d.ShowSaveOn(fs, "Backup Destination", `C:\Backup\`, nil)

	d.nameField.SetValue("")
	if !d.completeField(d.nameField, true) {
		t.Fatal("completeField returned false, want the two directories' prefix")
	}
	// "old" is the only directory in C:\Backup once ".." is excluded.
	if got := d.nameField.Value(); got != `old\` {
		t.Errorf("nameField = %q, want %q", got, `old\`)
	}
}

// Enter in the path bar on a name the listing already carries must not cost
// an Exists probe: the dialog knows whether it is a file from the row it is
// displaying.
func TestNavigateTypedUsesTheListingItAlreadyHas(t *testing.T) {
	fs := newFakeWindowsFS()
	d := NewFileDialog(nil)
	d.ShowOpenOn(fs, "Select Backup File", `C:\Backup\`, nil)

	before := fs.existsCalls
	d.pathField.SetValue(`C:\Backup\HealthClinic_full.bak`)
	d.navigateTyped()

	if fs.existsCalls != before {
		t.Errorf("Exists called %d extra time(s), want 0 for a name already listed",
			fs.existsCalls-before)
	}
	if d.dir != `C:\Backup` {
		t.Errorf("dir = %q, want it to stay in %q", d.dir, `C:\Backup`)
	}
	if got := d.nameField.Value(); got != "HealthClinic_full.bak" {
		t.Errorf("nameField = %q, want the typed file preselected", got)
	}
}

func TestWindowsPathRules(t *testing.T) {
	var w WindowsPathRules
	if got := w.Clean(`C:\Backup\..\Data\.\x`); got != `C:\Data\x` {
		t.Errorf("Clean = %q, want %q", got, `C:\Data\x`)
	}
	if got := w.Clean(`C:\`); got != `C:\` {
		t.Errorf("Clean(root) = %q, want %q", got, `C:\`)
	}
	if got := w.Parent(`C:\Backup\old`); got != `C:\Backup` {
		t.Errorf("Parent = %q, want %q", got, `C:\Backup`)
	}
	if got := w.Parent(`C:\Backup`); got != `C:\` {
		t.Errorf("Parent(depth 1) = %q, want %q", got, `C:\`)
	}
	if got := w.Parent(`C:\`); got != "" {
		t.Errorf("Parent(root) = %q, want %q", got, "")
	}
	if got := w.Join(`C:\Backup`, "db.bak"); got != `C:\Backup\db.bak` {
		t.Errorf("Join = %q, want %q", got, `C:\Backup\db.bak`)
	}
	if !w.IsAbs(`C:\Backup`) || !w.IsAbs(`\\host\share`) || w.IsAbs("db.bak") {
		t.Errorf("IsAbs misclassified a Windows path")
	}
	if dir, name := w.Split(`C:\Backup\db.bak`); dir != `C:\Backup\` || name != "db.bak" {
		t.Errorf("Split = (%q, %q)", dir, name)
	}
}

// A UNC path's share root is the shallowest listable level, so Up from it
// goes straight to the drive list. Walking it by separator produced
// `\\server` and then `\` first, two levels that enumerate nothing.
func TestWindowsPathRulesUNCBottomsOutAtTheShare(t *testing.T) {
	var w WindowsPathRules
	if got := w.Parent(`\\host\share\Backups\old`); got != `\\host\share\Backups` {
		t.Errorf("Parent = %q, want %q", got, `\\host\share\Backups`)
	}
	if got := w.Parent(`\\host\share\Backups`); got != `\\host\share` {
		t.Errorf("Parent(depth 1) = %q, want %q", got, `\\host\share`)
	}
	if got := w.Parent(`\\host\share`); got != "" {
		t.Errorf("Parent(share root) = %q, want %q — the drive list, not %q", got, "", `\\host`)
	}
	if got := w.Parent(`\\host`); got != "" {
		t.Errorf("Parent(server) = %q, want %q", got, "")
	}
}

func TestPosixPathRules(t *testing.T) {
	var p PosixPathRules
	if got := p.Clean("/var/opt/mssql/../data//x"); got != "/var/opt/data/x" {
		t.Errorf("Clean = %q, want %q", got, "/var/opt/data/x")
	}
	if got := p.Parent("/var/opt"); got != "/var" {
		t.Errorf("Parent = %q, want %q", got, "/var")
	}
	if got := p.Parent("/var"); got != "/" {
		t.Errorf("Parent(depth 1) = %q, want %q", got, "/")
	}
	if got := p.Parent("/"); got != "/" {
		t.Errorf("Parent(root) = %q, want %q — root must equal itself so the dialog drops its .. row", got, "/")
	}
	if got := p.Join("/var/opt", "db.bak"); got != "/var/opt/db.bak" {
		t.Errorf("Join = %q, want %q", got, "/var/opt/db.bak")
	}
}
