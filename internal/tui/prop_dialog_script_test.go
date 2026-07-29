package tui

import (
	"context"
	"fmt"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// commitRename is the whole of the Script Changes fix: under
// gosmo.WithScript a rename is recorded rather than run, so the boxed name
// every other page resolves by must keep pointing at what the server still
// has.
func TestCommitRenameHeldBackWhileScripting(t *testing.T) {
	name := "old"
	commitRename(context.Background(), &name, "new")
	if name != "new" {
		t.Errorf("a real rename left the boxed name %q, want %q", name, "new")
	}

	scripted := "old"
	ctx, _ := gosmo.WithScript(context.Background())
	commitRename(ctx, &scripted, "new")
	if scripted != "old" {
		t.Errorf("a scripted rename advanced the boxed name to %q — the server still has %q, so every sibling page's lookup would miss", scripted, "old")
	}
}

// dirtyForm returns a one-row Form holding edited, reported as an edit
// away from an empty baseline so the form is Dirty. The value is pasted
// rather than SetValue'd — SetValue resets the dirty baseline along with
// the text, being the post-load/post-Apply setter — and pasted rather than
// typed, since InputField ignores keys until it is focused, and focus is
// only applied at Draw time.
func dirtyForm(t *testing.T, edited string) (*propsheet.Form, *propsheet.TextRow) {
	t.Helper()
	row := propsheet.Text("Name", "", 40)
	row.Paste(edited)
	f := propsheet.NewForm(row)
	if row.Value() != edited {
		t.Fatalf("setup: row holds %q, want %q", row.Value(), edited)
	}
	if !f.Dirty() {
		t.Fatal("setup: form did not become dirty after its row was edited")
	}
	return f, row
}

// newSheetDialog builds a PropDialog whose every page is loaded and dirty,
// without touching a server: forms are installed from OnLoadPage exactly
// as PropDialog.onLoadPage does, minus its goroutine. rowFor, if non-nil,
// receives each page's edited row.
func newSheetDialog(t *testing.T, pages []propPage, applies map[int]propApply, rowFor func(int, *propsheet.TextRow)) *PropDialog {
	t.Helper()
	sheet := propsheet.NewPropertySheet(&fakeSizedScreen{w: 120, h: 40}, "Test Properties")
	d := &PropDialog{PropertySheet: sheet, pages: pages, applyFn: applies}

	titles := make([]string, len(pages))
	for i, p := range pages {
		titles[i] = p.title
	}
	d.SetPages(titles)
	d.OnLoadPage = func(page, seq int) {
		f, row := dirtyForm(t, "edited-"+titles[page])
		d.SetPageForm(page, seq, f)
		if rowFor != nil {
			rowFor(page, row)
		}
	}
	d.Show()
	for i := range pages {
		d.SelectPage(i)
	}
	if got := len(d.DirtyPages()); got != len(pages) {
		t.Fatalf("setup: %d dirty pages, want %d", got, len(pages))
	}
	return d
}

// The renaming page's apply has to run last. Every other page addresses
// the object by the boxed name, so a rename emitted before them leaves
// their statements naming something that no longer exists by the time the
// generated script runs.
func TestDirtyApplyFnsRunsRenamingPageLast(t *testing.T) {
	pages := []propPage{
		{title: "General", renames: true},
		{title: "Server Roles"},
		{title: "Securables"},
	}
	var order []string
	applies := map[int]propApply{}
	for i, p := range pages {
		title := p.title
		applies[i] = func(context.Context) error {
			order = append(order, title)
			return nil
		}
	}

	d := newSheetDialog(t, pages, applies, nil)
	for _, fn := range d.dirtyApplyFns() {
		if err := fn(context.Background()); err != nil {
			t.Fatalf("apply: %v", err)
		}
	}

	want := []string{"Server Roles", "Securables", "General"}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Errorf("applies ran %v, want %v", order, want)
	}
}

// End-to-end over both halves together, in the shape the nine real dialogs
// have: a General page that renames, and a sibling that re-resolves the
// object by the boxed name on every apply. serverName stands in for what
// the server actually holds — under WithScript it never changes, because
// the rename was only recorded.
//
// Before the fix this run failed outright: General ran first and advanced
// the boxed name, so the sibling's lookup missed, runPipeline aborted on
// that error, and Script Changes produced nothing at all.
func TestScriptedRenameLeavesSiblingPagesResolvable(t *testing.T) {
	const serverName = "old_login"
	boxed := serverName

	var renameRow *propsheet.TextRow
	var statements []string
	// resolve stands in for findLogin and friends: a read, which WithScript
	// does not intercept, so it only ever succeeds for the name the server
	// really has.
	resolve := func() error {
		if boxed != serverName {
			return fmt.Errorf("gosmo: login %q not found", boxed)
		}
		return nil
	}

	pages := []propPage{
		{title: "General", renames: true},
		{title: "Server Roles"},
	}
	applies := map[int]propApply{
		0: func(ctx context.Context) error {
			if err := resolve(); err != nil {
				return err
			}
			statements = append(statements, "ALTER LOGIN ["+boxed+"] WITH NAME = ["+renameRow.Value()+"]")
			commitRename(ctx, &boxed, renameRow.Value())
			return nil
		},
		1: func(ctx context.Context) error {
			if err := resolve(); err != nil {
				return err
			}
			statements = append(statements, "ALTER SERVER ROLE [dbcreator] ADD MEMBER ["+boxed+"]")
			return nil
		},
	}

	d := newSheetDialog(t, pages, applies, func(page int, row *propsheet.TextRow) {
		if page == 0 {
			renameRow = row
		}
	})
	// The General page's edited row is the new login name.
	if renameRow == nil {
		t.Fatal("setup: General page's row was never captured")
	}
	newName := renameRow.Value()

	ctx, _ := gosmo.WithScript(context.Background())
	for _, fn := range d.dirtyApplyFns() {
		if err := fn(ctx); err != nil {
			t.Fatalf("scripted apply failed: %v", err)
		}
	}

	// The rename is last, so everything above it names the object the
	// server still has and the script is valid top to bottom.
	want := []string{
		"ALTER SERVER ROLE [dbcreator] ADD MEMBER [" + serverName + "]",
		"ALTER LOGIN [" + serverName + "] WITH NAME = [" + newName + "]",
	}
	if fmt.Sprint(statements) != fmt.Sprint(want) {
		t.Errorf("scripted statements:\n got %v\nwant %v", statements, want)
	}
	if boxed != serverName {
		t.Errorf("boxed name is %q after scripting — it must stay %q, or a following real Apply looks up an object that was never created", boxed, serverName)
	}
}
