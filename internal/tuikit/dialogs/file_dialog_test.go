package dialogs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

func key(k tcell.Key) *tcell.EventKey { return tcell.NewEventKey(k, "", tcell.ModNone) }
func rn(r rune) *tcell.EventKey       { return tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone) }

func newTestFileDialog(t *testing.T) (*FileDialog, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}
	d := NewFileDialog(nil)
	return d, dir
}

func TestFileDialogLoadDirSortsDirsBeforeFiles(t *testing.T) {
	d, dir := newTestFileDialog(t)
	d.loadDir(dir)

	if d.listErr != "" {
		t.Fatalf("listErr = %q, want empty", d.listErr)
	}
	// ".." (parent), then docs, src (alphabetical), then main.go, README.md
	// (case-insensitive: "main.go" < "readme.md").
	want := []string{"..", "docs", "src", "main.go", "README.md"}
	if len(d.entries) != len(want) {
		t.Fatalf("entries = %v, want names %v", d.entries, want)
	}
	for i, name := range want {
		if d.entries[i].name != name {
			t.Errorf("entries[%d].name = %q, want %q", i, d.entries[i].name, name)
		}
	}
	if !d.entries[0].isDir || !d.entries[1].isDir || !d.entries[2].isDir {
		t.Fatal("expected the first three entries to be directories")
	}
	if d.entries[3].isDir || d.entries[4].isDir {
		t.Fatal("expected the last two entries to be files")
	}
}

func TestFileDialogLoadDirAtRootOmitsParent(t *testing.T) {
	d := NewFileDialog(nil)
	root, err := filepath.Abs(string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}
	d.loadDir(root)
	for _, e := range d.entries {
		if e.name == ".." {
			t.Fatalf("filesystem root listing should not include a %q entry", "..")
		}
	}
}

func TestFileDialogShowOpenPreselectsName(t *testing.T) {
	d, dir := newTestFileDialog(t)
	d.ShowOpen("Open Query File", filepath.Join(dir, "README.md"), nil)

	if d.mode != FileDialogOpen {
		t.Fatalf("mode = %v, want FileDialogOpen", d.mode)
	}
	if got := d.nameField.Value(); got != "README.md" {
		t.Fatalf("nameField.Value() = %q, want %q", got, "README.md")
	}
	if d.entries[d.sel].name != "README.md" {
		t.Fatalf("selected entry = %q, want %q", d.entries[d.sel].name, "README.md")
	}
	if d.focus != ffList {
		t.Fatalf("focus = %d, want ffList", d.focus)
	}
}

func TestFileDialogShowSaveFocusesNameField(t *testing.T) {
	d, dir := newTestFileDialog(t)
	d.ShowSave("Save Query As", filepath.Join(dir, "new.sql"), nil)

	if d.mode != FileDialogSave {
		t.Fatalf("mode = %v, want FileDialogSave", d.mode)
	}
	if got := d.nameField.Value(); got != "new.sql" {
		t.Fatalf("nameField.Value() = %q, want %q", got, "new.sql")
	}
	if d.focus != ffName {
		t.Fatalf("focus = %d, want ffName", d.focus)
	}
	if got := d.buttonLabels()[0]; got != "Save" {
		t.Fatalf("primary button label = %q, want %q", got, "Save")
	}
}

func TestFileDialogEnterOnFileChooses(t *testing.T) {
	d, dir := newTestFileDialog(t)
	var chosen string
	d.ShowOpen("Open Query File", filepath.Join(dir, "README.md"), func(path string) { chosen = path })

	d.HandleKey(key(tcell.KeyEnter))

	want := filepath.Join(dir, "README.md")
	if chosen != want {
		t.Fatalf("OnChoose path = %q, want %q", chosen, want)
	}
	if d.Visible() {
		t.Fatal("dialog should hide itself once a choice is confirmed")
	}
}

func TestFileDialogEnterOnDirectoryDescends(t *testing.T) {
	d, dir := newTestFileDialog(t)
	d.ShowOpen("Open Query File", filepath.Join(dir, "README.md"), nil)
	d.selectByName("src")
	d.setFocus(ffList)

	d.HandleKey(key(tcell.KeyEnter))

	want, _ := filepath.EvalSymlinks(filepath.Join(dir, "src"))
	got, _ := filepath.EvalSymlinks(d.dir)
	if got != want {
		t.Fatalf("dir after descending = %q, want %q", d.dir, filepath.Join(dir, "src"))
	}
	if !d.Visible() {
		t.Fatal("descending into a directory should not close the dialog")
	}
}

func TestFileDialogParentEntryNavigatesUp(t *testing.T) {
	d, dir := newTestFileDialog(t)
	sub := filepath.Join(dir, "src")
	d.loadDir(sub)
	d.Show()
	d.selectByName("..")
	d.setFocus(ffList)

	d.HandleKey(key(tcell.KeyEnter))

	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(d.dir)
	if got != want {
		t.Fatalf("dir after '..' = %q, want %q", d.dir, dir)
	}
}

func TestFileDialogSaveOverwriteAsksBeforeChoosing(t *testing.T) {
	d, dir := newTestFileDialog(t)
	var confirmAskedFor string
	var proceedFn func()
	d.OnConfirmOverwrite = func(path string, proceed func()) {
		confirmAskedFor = path
		proceedFn = proceed
	}
	var chosen string
	d.ShowSave("Save Query", filepath.Join(dir, "README.md"), func(path string) { chosen = path })

	d.HandleKey(key(tcell.KeyEnter)) // ShowSave focuses the name field; Enter there confirms

	want := filepath.Join(dir, "README.md")
	if confirmAskedFor != want {
		t.Fatalf("OnConfirmOverwrite path = %q, want %q", confirmAskedFor, want)
	}
	if chosen != "" {
		t.Fatal("OnChoose must not fire until proceed() is called")
	}
	if !d.Visible() {
		t.Fatal("dialog must stay open while the overwrite prompt is pending")
	}

	proceedFn()
	if chosen != want {
		t.Fatalf("OnChoose path after proceed() = %q, want %q", chosen, want)
	}
	if d.Visible() {
		t.Fatal("dialog should hide once proceed() confirms the overwrite")
	}
}

func TestFileDialogSaveNewNameSkipsOverwritePrompt(t *testing.T) {
	d, dir := newTestFileDialog(t)
	d.OnConfirmOverwrite = func(path string, proceed func()) {
		t.Fatal("OnConfirmOverwrite must not fire for a name that doesn't already exist")
	}
	var chosen string
	d.ShowSave("Save Query As", filepath.Join(dir, "brand-new.sql"), func(path string) { chosen = path })

	d.HandleKey(key(tcell.KeyEnter))

	want := filepath.Join(dir, "brand-new.sql")
	if chosen != want {
		t.Fatalf("OnChoose path = %q, want %q", chosen, want)
	}
}

func TestFileDialogEscapeCancels(t *testing.T) {
	d, dir := newTestFileDialog(t)
	canceled := false
	d.ShowOpen("Open Query File", filepath.Join(dir, "README.md"), nil)
	d.OnCancel = func() { canceled = true }

	d.HandleKey(key(tcell.KeyEscape))

	if !canceled {
		t.Fatal("Escape should fire OnCancel")
	}
	if d.Visible() {
		t.Fatal("Escape should hide the dialog")
	}
}

func TestFileDialogTypeaheadJumpsToMatch(t *testing.T) {
	d, dir := newTestFileDialog(t)
	d.ShowOpen("Open Query File", filepath.Join(dir, "README.md"), nil)
	d.setFocus(ffList)

	d.HandleKey(rn('m')) // "main.go" is the only entry starting with 'm'

	if got := d.entries[d.sel].name; got != "main.go" {
		t.Fatalf("selected entry after typeahead 'm' = %q, want %q", got, "main.go")
	}
}

func TestFileDialogTabCompletesUniqueDirectory(t *testing.T) {
	d, dir := newTestFileDialog(t)
	d.ShowOpen("Open Query File", filepath.Join(dir, "README.md"), nil)
	d.setFocus(ffPath)
	d.pathField.SetValue("sr") // only "src" starts with "sr"

	if !d.completeField(d.pathField, true) {
		t.Fatal("completeField should complete an unambiguous directory prefix")
	}
	if got := d.pathField.Value(); got != "src"+string(filepath.Separator) {
		t.Fatalf("pathField.Value() = %q, want %q", got, "src"+string(filepath.Separator))
	}
}

func TestFileDialogTabCompletionNoMatchReturnsFalse(t *testing.T) {
	d, dir := newTestFileDialog(t)
	d.ShowOpen("Open Query File", filepath.Join(dir, "README.md"), nil)
	d.pathField.SetValue("zzz")

	if d.completeField(d.pathField, true) {
		t.Fatal("completeField should report no completion for a non-matching prefix")
	}
}

func TestCommonPrefixHelper(t *testing.T) {
	tests := []struct {
		in   []string
		want string
	}{
		{[]string{"src"}, "src"},
		{[]string{"src", "srv"}, "sr"},
		{[]string{"src", "docs"}, ""},
	}
	for _, tt := range tests {
		if got := commonPrefix(tt.in); got != tt.want {
			t.Errorf("commonPrefix(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestFileDialogHeldButtonOnUnselectedRowDoesNotAutoActivate pins the fix
// for tcell's all-motion mouse tracking resending Buttons()==Button1 on
// every motion event while the button stays down. Without listMouseDragging,
// a single physical click on a not-yet-selected row would set d.sel to that
// row, and the very next resent event — same physical click, no release —
// would then see idx == d.sel (just set) and mistake it for a second click
// on an already-selected row, auto-activating (choosing) a file the user
// only meant to select once.
func TestFileDialogHeldButtonOnUnselectedRowDoesNotAutoActivate(t *testing.T) {
	d, dir := newTestFileDialog(t)
	var chosen string
	d.ShowOpen("Open Query File", filepath.Join(dir, "README.md"), func(path string) { chosen = path })
	d.rect = core.Rect{X: 0, Y: 0, W: 60, H: 20}

	// Entries: "..", "docs", "src", "main.go", "README.md" (see
	// TestFileDialogLoadDirSortsDirsBeforeFiles). ShowOpen preselects
	// README.md (index 4), so main.go (index 3) starts out unselected.
	const mainGoIdx = 3
	if d.entries[mainGoIdx].name != "main.go" {
		t.Fatalf("entries[%d] = %q, want main.go", mainGoIdx, d.entries[mainGoIdx].name)
	}
	lr := d.listRect()
	x, y := lr.X+1, lr.Y+mainGoIdx

	// A single physical click selects the row but must not activate it.
	d.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	if d.sel != mainGoIdx {
		t.Fatalf("sel = %d, want %d", d.sel, mainGoIdx)
	}
	if chosen != "" {
		t.Fatal("a single click on a previously-unselected row must not activate it")
	}

	// Resent Button1 with no release in between must not auto-activate.
	d.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	if chosen != "" {
		t.Fatal("a resent Button1 with no release must not auto-activate the row it just selected")
	}

	// A genuine second click, after a release, does activate it.
	d.HandleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
	d.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	want := filepath.Join(dir, "main.go")
	if chosen != want {
		t.Fatalf("chosen = %q, want %q after a genuine second click", chosen, want)
	}
}

func TestFormatFileSizeHelper(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{500, "500 B"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
	}
	for _, tt := range tests {
		if got := formatFileSize(tt.in); got != tt.want {
			t.Errorf("formatFileSize(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
