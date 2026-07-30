package tui

import (
	"context"
	"strconv"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// ownedThing stands in for *gosmo.Schema / *gosmo.DatabaseRole so the page's
// widget wiring can be tested without a server.
type ownedThing struct {
	name    string
	members int
}

// transferPageParts are the widgets newOwnerTransferPage built, recovered by
// walking the form's focus chain — the page returns only the Form and apply,
// so this is how a test reaches the rows a user would interact with.
type transferPageParts struct {
	grid     *propsheet.GridRow
	statics  []*propsheet.StaticRow
	transfer *propsheet.SelectRow
}

func partsOf(t *testing.T, f *propsheet.Form) transferPageParts {
	t.Helper()
	var p transferPageParts
	f.SetBounds(0, 0, 80, 40)
	for ok := f.FocusFirst(); ok; ok = f.FocusNext() {
		switch r := f.Focused().(type) {
		case *propsheet.GridRow:
			p.grid = r
		case *propsheet.StaticRow:
			p.statics = append(p.statics, r)
		case *propsheet.SelectRow:
			p.transfer = r
		}
	}
	if p.grid == nil || p.transfer == nil {
		t.Fatalf("form is missing a grid row (%v) or the transfer select (%v)", p.grid, p.transfer)
	}
	return p
}

func newTestTransferPage(items []*ownerTransferItem[ownedThing], owners []string, changed *[][2]string) (*propsheet.Form, propApply) {
	return newOwnerTransferPage(items, owners, ownerTransferSpec[ownedThing]{
		Headers: []string{"Name", "Owner", "Members"},
		Cells: func(it *ownerTransferItem[ownedThing]) []string {
			return []string{it.name, it.newOwner, strconv.Itoa(it.obj.members)}
		},
		DetailLabels: []string{"Members"},
		DetailValues: func(it *ownerTransferItem[ownedThing]) []string {
			return []string{strconv.Itoa(it.obj.members)}
		},
		GridSection: "Owned things",
		ItemSection: "Selected thing",
		Note:        "note",
		ChangeOwner: func(_ context.Context, o ownedThing, newOwner string) error {
			*changed = append(*changed, [2]string{o.name, newOwner})
			return nil
		},
	})
}

func testItems() []*ownerTransferItem[ownedThing] {
	return []*ownerTransferItem[ownedThing]{
		{obj: ownedThing{"sales", 3}, name: "sales", origOwner: "alice", newOwner: "alice"},
		{obj: ownedThing{"hr", 7}, name: "hr", origOwner: "alice", newOwner: "alice"},
	}
}

// The page must apply exactly the rows the user retargeted, and leave the rest
// alone — an owner-transfer page that re-issues ALTER for every listed object
// would churn ownership it was never asked to touch.
func TestOwnerTransferPageAppliesOnlyChangedRows(t *testing.T) {
	items := testItems()
	var changed [][2]string
	owners := []string{"alice", "bob", "carol"}
	f, apply := newTestTransferPage(items, owners, &changed)
	p := partsOf(t, f)

	// Row 0 is selected on build; retarget it to carol, then move to row 1 so
	// the edit is committed the way OnSelectRow does for a real user.
	p.transfer.SetSelected(2)
	p.grid.Grid.OnSelectRow(1)

	if items[0].newOwner != "carol" {
		t.Errorf("row 0 newOwner = %q, want carol — moving off the row did not commit the edit", items[0].newOwner)
	}
	if items[1].newOwner != "alice" {
		t.Errorf("row 1 newOwner = %q, want alice — an untouched row was modified", items[1].newOwner)
	}

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(changed) != 1 {
		t.Fatalf("ChangeOwner calls = %v, want exactly one (sales -> carol)", changed)
	}
	if changed[0] != [2]string{"sales", "carol"} {
		t.Errorf("ChangeOwner called with %v, want {sales carol}", changed[0])
	}
}

// The edit sitting in the Select row when OK is pressed must be applied too:
// the user never moves off the last row they edited, so apply has to commit it
// rather than relying on OnSelectRow having fired.
func TestOwnerTransferPageAppliesTheUncommittedCurrentRow(t *testing.T) {
	items := testItems()
	var changed [][2]string
	f, apply := newTestTransferPage(items, []string{"alice", "bob"}, &changed)
	p := partsOf(t, f)

	p.transfer.SetSelected(1) // bob, and never leave row 0
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(changed) != 1 || changed[0] != [2]string{"sales", "bob"} {
		t.Errorf("ChangeOwner calls = %v, want one {sales bob} — the in-progress edit was dropped", changed)
	}
}

// Dirty drives the OK/Apply buttons, and Revert has to put every row back
// including the Select row showing the current one.
func TestOwnerTransferPageDirtyAndRevert(t *testing.T) {
	items := testItems()
	var changed [][2]string
	owners := []string{"alice", "bob", "carol"}
	f, _ := newTestTransferPage(items, owners, &changed)
	p := partsOf(t, f)

	if p.grid.Dirty() {
		t.Error("page reports dirty before any edit")
	}

	p.transfer.SetSelected(2)
	p.grid.Grid.OnSelectRow(1)
	if !p.grid.Dirty() {
		t.Error("page reports clean after retargeting a row")
	}

	p.grid.Revert()
	if p.grid.Dirty() {
		t.Error("page still reports dirty after Revert")
	}
	for i, it := range items {
		if it.newOwner != it.origOwner {
			t.Errorf("item %d newOwner = %q after Revert, want %q", i, it.newOwner, it.origOwner)
		}
	}
	if got := p.transfer.Value(); got != "alice" {
		t.Errorf("transfer row shows %q after Revert, want alice", got)
	}
}

// Selecting a grid row fills the detail rows, and clearing the selection
// blanks them rather than leaving the previous object's values on screen.
func TestOwnerTransferPageDetailRowsTrackSelection(t *testing.T) {
	items := testItems()
	var changed [][2]string
	f, _ := newTestTransferPage(items, []string{"alice", "bob"}, &changed)
	p := partsOf(t, f)

	// Name, Current owner, then the spec's one extra detail label.
	if len(p.statics) != 3 {
		t.Fatalf("static rows = %d, want 3 (Name, Current owner, Members)", len(p.statics))
	}
	p.grid.Grid.OnSelectRow(1)
	if got := []string{p.statics[0].Value(), p.statics[1].Value(), p.statics[2].Value()}; got[0] != "hr" || got[1] != "alice" || got[2] != "7" {
		t.Errorf("detail rows for row 1 = %v, want [hr alice 7]", got)
	}

	p.grid.Grid.OnSelectRow(-1)
	for i, s := range p.statics {
		if s.Value() != "" {
			t.Errorf("static %d = %q with no row selected, want empty", i, s.Value())
		}
	}
}

// The owner column has to show the pending owner, not the server's, or an
// edit is invisible until the dialog is reopened.
func TestOwnerTransferPageOwnerColumnTracksEdits(t *testing.T) {
	items := testItems()
	var changed [][2]string
	owners := []string{"alice", "bob", "carol"}
	f, _ := newTestTransferPage(items, owners, &changed)
	p := partsOf(t, f)

	p.transfer.SetSelected(2)
	p.grid.Grid.OnSelectRow(1)
	p.grid.Revert() // Revert re-runs Cells, so the grid data is rebuilt here

	if got := p.grid.Grid.Row(0)[1]; got != "alice" {
		t.Errorf("owner cell after revert = %q, want alice", got)
	}
}

// An owner the principal list doesn't contain — dropped since, or filtered
// out of the listing — must still be shown as the current owner. Falling back
// to the first entry instead is not merely a display bug: the page commits
// whatever the row shows, so opening this page and pressing OK would transfer
// ownership to whoever sorts first without the user asking.
func TestOwnerTransferPageKeepsAnOwnerMissingFromTheList(t *testing.T) {
	items := []*ownerTransferItem[ownedThing]{
		{obj: ownedThing{"sales", 1}, name: "sales", origOwner: "deleted-user", newOwner: "deleted-user"},
	}
	var changed [][2]string
	f, apply := newTestTransferPage(items, []string{"alice", "bob"}, &changed)
	p := partsOf(t, f)

	if got := p.transfer.Value(); got != "deleted-user" {
		t.Errorf("transfer row = %q, want deleted-user — the real owner is misreported as another principal", got)
	}
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("ChangeOwner calls = %v, want none — opening the page applied an ownership change on its own", changed)
	}

	// The unknown owner is offered alongside the real principals, so a genuine
	// transfer away from it still works.
	if got := selectValues(p.transfer, []string{"alice", "bob", "deleted-user"}); !got {
		t.Error("the owner list does not offer every real principal plus the current owner")
	}
}

// selectValues reports whether the row can select each of want by index, which
// is the only black-box way to see the list it was built with.
func selectValues(r *propsheet.SelectRow, want []string) bool {
	for i, w := range want {
		r.SetSelected(i)
		if r.Value() != w {
			return false
		}
	}
	return true
}
