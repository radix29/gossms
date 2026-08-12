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
		if d.entries[i].Name != name {
			t.Errorf("entries[%d].Name = %q, want %q", i, d.entries[i].Name, name)
		}
	}
	if !d.entries[0].IsDir || !d.entries[1].IsDir || !d.entries[2].IsDir {
		t.Fatal("expected the first three entries to be directories")
	}
	if d.entries[3].IsDir || d.entries[4].IsDir {
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
		if e.Name == ".." {
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
	if d.entries[d.sel].Name != "README.md" {
		t.Fatalf("selected entry = %q, want %q", d.entries[d.sel].Name, "README.md")
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

	if got := d.entries[d.sel].Name; got != "main.go" {
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

// TestFileDialogScrollbarDragScrolls confirms dragging the file list's own
// scrollbar scrolls the listing instead of being read as a click that
// selects/activates whatever entry sits under it.
func TestFileDialogScrollbarDragScrolls(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		name := filepath.Join(dir, "file"+string(rune('a'+i))+".txt")
		if err := os.WriteFile(name, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	d := NewFileDialog(nil)
	d.ShowOpen("Open File", filepath.Join(dir, "filea.txt"), func(string) {})
	d.rect = core.Rect{X: 0, Y: 0, W: 60, H: 20}
	if len(d.entries) <= fileListRows {
		t.Fatalf("test needs more entries than fileListRows (%d) to exercise scrolling, got %d", fileListRows, len(d.entries))
	}
	selBefore := d.sel

	lr := d.listRect()
	sbX := lr.Right() - 1
	if !d.HandleMouse(tcell.NewEventMouse(sbX, lr.Y+lr.H-1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse on the list's scrollbar column should be handled")
	}
	if !d.sbDragging {
		t.Fatal("sbDragging should be true after pressing on the scrollbar")
	}
	if d.scroll == 0 {
		t.Error("scroll should have jumped forward when clicking near the bottom of the track")
	}
	if d.sel != selBefore {
		t.Errorf("sel changed to %d, clicking the scrollbar must not select an entry", d.sel)
	}

	d.HandleMouse(tcell.NewEventMouse(sbX, lr.Y, tcell.ButtonNone, tcell.ModNone))
	if d.sbDragging {
		t.Error("sbDragging should reset on release")
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

// TestFileDialogHeldButtonOnUnselectedRowDoesNotAutoActivate covers tcell's
// all-motion mouse tracking resending Buttons()==Button1 on every motion
// event while the button stays down. Without listMouseDragging, a single
// physical click on a not-yet-selected row sets d.sel to that row, and the
// next resent event — same physical click, no release — sees idx == d.sel
// and mistakes it for a second click on an already-selected row,
// auto-activating a file meant only to be selected.
func TestFileDialogHeldButtonOnUnselectedRowDoesNotAutoActivate(t *testing.T) {
	d, dir := newTestFileDialog(t)
	var chosen string
	d.ShowOpen("Open Query File", filepath.Join(dir, "README.md"), func(path string) { chosen = path })
	d.rect = core.Rect{X: 0, Y: 0, W: 60, H: 20}

	// Entries: "..", "docs", "src", "main.go", "README.md" (see
	// TestFileDialogLoadDirSortsDirsBeforeFiles). ShowOpen preselects
	// README.md (index 4), so main.go (index 3) starts out unselected.
	const mainGoIdx = 3
	if d.entries[mainGoIdx].Name != "main.go" {
		t.Fatalf("entries[%d] = %q, want main.go", mainGoIdx, d.entries[mainGoIdx].Name)
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

// newPlacedFileDialog is newTestFileDialog with a screen size behind it, so
// the dialog gets a real centred rect and its fields land where Draw would
// put them. The plain helper's nil screen leaves the rect at 0x0, which is
// fine for the list-geometry tests above but puts the button row on top of
// the path field — enough to make a mouse-routing test assert nonsense.
func newPlacedFileDialog(t *testing.T) (*FileDialog, string) {
	t.Helper()
	_, dir := newTestFileDialog(t)
	d := NewFileDialog(&sizedScreen{w: 120, h: 40})
	d.ShowOpen("Open", dir, func(string) {})
	// Mirrors the two SetBounds calls in file_dialog_draw.go, which only run
	// under Draw — if those move, this must move with them.
	inner := d.InnerRect()
	d.pathField.SetBounds(inner.X+1, inner.Y)
	d.nameField.SetBounds(inner.X+1, inner.Y+fileListRows+5)
	return d, dir
}

// A text-selection drag belongs to the field that claimed its press until
// the release, wherever the pointer goes — invariant 1 in ARCHITECTURE.md
// § The mouseDragging idiom. InputField honours this itself, but only if its
// host forwards the off-rect motion; hit-testing every Button1 before
// forwarding, as this dialog used to, froze the selection at the box edge.
func TestFileDialogDragOutOfPathFieldKeepsExtending(t *testing.T) {
	d, _ := newPlacedFileDialog(t)
	d.pathField.SetValue("abcdefgh")

	ix, y := d.pathField.InputX(), d.pathField.RectY()
	if !d.HandleMouse(tcell.NewEventMouse(ix+1, y, tcell.Button1, tcell.ModNone)) {
		t.Fatal("the press inside the path field was refused — test premise is wrong")
	}
	if d.dragField != d.pathField {
		t.Fatal("the press did not claim the gesture for the path field")
	}

	// Drag down into the file list, which owns that region and would
	// otherwise select a row out from under the drag.
	lr := d.listRect()
	selBefore := d.sel
	dragX, dragY := ix+6, lr.Y+1
	if d.pathField.HitTest(dragX, dragY) {
		t.Fatal("the drag point is still inside the field — test premise is wrong")
	}
	if !d.HandleMouse(tcell.NewEventMouse(dragX, dragY, tcell.Button1, tcell.ModNone)) {
		t.Fatal("the dialog refused motion the path field owns the gesture for")
	}
	if got := d.pathField.SelectedText(); got != "abcde" {
		t.Errorf("SelectedText() = %q, want %q — the drag stopped at the box edge", got, "abcde")
	}
	if d.sel != selBefore {
		t.Errorf("sel = %d, want %d — the list stole a gesture the path field owned", d.sel, selBefore)
	}

	// The release ends it and clears the latch, so the next press routes
	// positionally again.
	d.HandleMouse(tcell.NewEventMouse(dragX, dragY, tcell.ButtonNone, tcell.ModNone))
	if d.dragField != nil {
		t.Error("the release left the gesture latched")
	}
	d.HandleMouse(tcell.NewEventMouse(lr.X+1, lr.Y+1, tcell.Button1, tcell.ModNone))
	if d.sel == selBefore {
		t.Error("a press in the list after the release did not select a row")
	}
}

// A release outside the dialog is eaten by ConsumeOutsideClick, so the latch
// has to be cleared ahead of it or the field swallows the next press.
func TestFileDialogReleaseOutsideTheDialogClearsTheDragLatch(t *testing.T) {
	d, _ := newPlacedFileDialog(t)

	ix, y := d.pathField.InputX(), d.pathField.RectY()
	d.HandleMouse(tcell.NewEventMouse(ix+1, y, tcell.Button1, tcell.ModNone))
	d.HandleMouse(tcell.NewEventMouse(0, 0, tcell.ButtonNone, tcell.ModNone))

	if d.dragField != nil {
		t.Fatal("a release outside the dialog left the gesture latched")
	}
	if d.pathField.HandleMouse(tcell.NewEventMouse(ix+3, y+9, tcell.Button1, tcell.ModNone)) {
		t.Error("the field is still latched — its next off-rect press was accepted")
	}
}

// A latch must not survive into the next showing (tuikit invariant 4): a
// dialog dismissed mid-drag would reopen routing every click to that field.
func TestFileDialogShowClearsTheDragLatch(t *testing.T) {
	d, dir := newPlacedFileDialog(t)
	d.dragField = d.pathField
	d.ShowOpen("Open", dir, func(string) {})
	if d.dragField != nil {
		t.Error("Show left a drag latch armed from the previous showing")
	}
}
