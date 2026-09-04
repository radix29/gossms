package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// A Select row whose list begins with a synthetic "(None)"/"<all databases>"
// entry must be built by searching that whole list, not the bare names with a
// 1+ offset. With the offset, indexOf's not-found 0 becomes 1 — the first
// *real* item — so an alert whose category has since been deleted displays as
// though it belonged to whichever category sorts first, and applying the page
// silently moves it there.
//
// The six pages that used this shape now build the row with selectPreserving
// instead, which shows the deleted category's own name (see
// prop_select_preserving_pages_test.go). This stays because indexOf still
// builds the fixed-vocabulary rows and the New-X dialogs' defaults, where the
// value is one of the list by construction.
func TestIndexOfSentinelListFallsBackToSentinel(t *testing.T) {
	names := []string{"Alpha", "Beta", "Gamma"}
	items := append([]string{noneItem}, names...)

	t.Run("value the list does not contain selects the sentinel", func(t *testing.T) {
		if got := indexOf(items, "deleted-category"); got != 0 {
			t.Errorf("indexOf(items, %q) = %d (%q), want 0 (%q)",
				"deleted-category", got, items[got], noneItem)
		}
	})

	t.Run("empty value selects the sentinel", func(t *testing.T) {
		if got := indexOf(items, ""); got != 0 {
			t.Errorf("indexOf(items, \"\") = %d (%q), want 0 (%q)", got, items[got], noneItem)
		}
	})

	// The offset form this replaced: 1 + indexOf(names, missing) == 1, which
	// is "Alpha". Pinned so the old shape can't come back unnoticed.
	t.Run("the offset form it replaced picks the wrong item", func(t *testing.T) {
		if buggy := 1 + indexOf(names, "deleted-category"); items[buggy] != "Alpha" {
			t.Fatalf("expected the 1+offset form to land on Alpha, got %q", items[buggy])
		}
	})

	t.Run("a real value still resolves to itself", func(t *testing.T) {
		for i, want := range items {
			if got := indexOf(items, want); got != i {
				t.Errorf("indexOf(items, %q) = %d, want %d", want, got, i)
			}
		}
	})
}

// allDatabasesItem heads the alert Database row the same way noneItem heads
// the category rows, so it needs the same fallback.
func TestIndexOfAllDatabasesSentinel(t *testing.T) {
	items := append([]string{allDatabasesItem}, "master", "msdb")
	if got := indexOf(items, "dropped-db"); got != 0 {
		t.Errorf("indexOf(items, %q) = %d (%q), want 0 (%q)",
			"dropped-db", got, items[got], allDatabasesItem)
	}
	if got := indexOf(items, "msdb"); got != 2 {
		t.Errorf("indexOf(items, \"msdb\") = %d, want 2", got)
	}
}

// indexOfOK is for a list with no sentinel to absorb a miss, where indexOf's
// 0 would render the first real option as though the server had reported it.
func TestIndexOfOKReportsAMiss(t *testing.T) {
	items := []string{"NONE", "ROW", "PAGE"}

	if i, ok := indexOfOK(items, "COLUMNSTORE"); ok || i != 0 {
		t.Errorf(`indexOfOK(items, "COLUMNSTORE") = %d, %v; want 0, false`, i, ok)
	}
	if i, ok := indexOfOK(items, ""); ok || i != 0 {
		t.Errorf(`indexOfOK(items, "") = %d, %v; want 0, false`, i, ok)
	}
	for want, v := range items {
		if i, ok := indexOfOK(items, v); !ok || i != want {
			t.Errorf("indexOfOK(items, %q) = %d, %v; want %d, true", v, i, ok, want)
		}
	}
}

// A database at a level the dropdown doesn't list must show its real level,
// not fall back to 100 — which is itself a valid level and so reads as fact.
func TestCompatItemsForInsertsAnUnlistedLevel(t *testing.T) {
	t.Run("a level below the list is inserted first", func(t *testing.T) {
		items := compatItemsFor(90, 0)
		if items[0] != "90" {
			t.Fatalf("compatItemsFor(90, 0) = %v, want 90 first", items)
		}
		if got := indexOf(items, "90"); got != 0 {
			t.Errorf("indexOf(items, \"90\") = %d, want 0", got)
		}
	})

	t.Run("a level above the list is inserted last", func(t *testing.T) {
		items := compatItemsFor(180, 0)
		if items[len(items)-1] != "180" {
			t.Fatalf("compatItemsFor(180, 0) = %v, want 180 last", items)
		}
	})

	t.Run("a listed level leaves the list alone", func(t *testing.T) {
		for _, level := range []int{100, 150, 170} {
			if got := compatItemsFor(level, 0); len(got) != len(compatLevelItems) {
				t.Errorf("compatItemsFor(%d, 0) = %v, want the base list unchanged", level, got)
			}
		}
	})

	t.Run("an unpopulated level adds nothing", func(t *testing.T) {
		if got := compatItemsFor(0, 0); len(got) != len(compatLevelItems) {
			t.Errorf("compatItemsFor(0, 0) = %v, want the base list unchanged", got)
		}
	})

	// The base list must not be mutated by an insert — both call sites read it.
	t.Run("the base list is never mutated", func(t *testing.T) {
		compatItemsFor(90, 0)
		compatItemsFor(180, 0)
		if compatLevelItems[0] != "100" || len(compatLevelItems) != 8 {
			t.Fatalf("compatLevelItems was mutated: %v", compatLevelItems)
		}
	})
}

// A6: the dropdown must not offer a level the server rejects. On 2017 the
// fixed list offered 150, 160 and 170, and the server answered "Valid values
// of the database compatibility level are 100, 110, 120, 130 or 140. (15048)"
// on both New Database and Database Properties.
func TestCompatItemsForCapsTheListAtTheServerMajor(t *testing.T) {
	t.Run("each major caps at its own level", func(t *testing.T) {
		for major, want := range map[int]string{13: "130", 14: "140", 15: "150", 16: "160", 17: "170"} {
			items := compatItemsFor(0, major)
			if got := items[len(items)-1]; got != want {
				t.Errorf("major %d offers up to %q, want %q (%v)", major, got, want, items)
			}
		}
	})

	// The one case the cap must not swallow: a database restored from a newer
	// instance still has to display the level it really is, even though the
	// server cannot be asked to set it.
	t.Run("a current level above the cap is still shown", func(t *testing.T) {
		items := compatItemsFor(160, 14)
		if items[len(items)-1] != "160" {
			t.Fatalf("compatItemsFor(160, 14) = %v, want 160 present and last", items)
		}
		if got := indexOf(items, "160"); got != len(items)-1 {
			t.Errorf("indexOf(items, \"160\") = %d, want %d", got, len(items)-1)
		}
	})

	// An unread version is treated as newest, the convention gosmo's own
	// version gates follow — degrading to the oldest list would hide levels a
	// modern server does accept.
	t.Run("an unknown major is uncapped", func(t *testing.T) {
		for _, major := range []int{0, 99} {
			if got := compatItemsFor(0, major); len(got) != len(compatLevelItems) {
				t.Errorf("major %d = %v, want the full list", major, got)
			}
		}
	})

	t.Run("the base list is never mutated by a cap", func(t *testing.T) {
		compatItemsFor(160, 13)
		if compatLevelItems[0] != "100" || len(compatLevelItems) != 8 {
			t.Fatalf("compatLevelItems was mutated: %v", compatLevelItems)
		}
	})
}

// The rule preservingItems exists to enforce: the index it returns always
// points at the value that was asked for. indexOf cannot promise that, and
// every dropdown that misreported an owner did so by trusting it.
func TestPreservingItemsAlwaysPointsAtTheValue(t *testing.T) {
	base := []string{"alice", "bob", "carol"}
	for _, v := range []string{"alice", "carol", "mallory", "(unresolved owner)"} {
		items, i := preservingItems(base, v)
		if got := items[i]; got != v {
			t.Errorf("preservingItems(base, %q) selected %q, want %q", v, got, v)
		}
	}
}

func TestPreservingItemsDoesNotDisturbTheBaseList(t *testing.T) {
	base := []string{"alice", "bob"}
	items, _ := preservingItems(base, "mallory")
	if len(base) != 2 || base[0] != "alice" || base[1] != "bob" {
		t.Errorf("base list was mutated: %v", base)
	}
	// A widened list must keep every real option offered, not replace them.
	if len(items) != 3 || items[0] != "alice" || items[1] != "bob" {
		t.Errorf("widened list dropped real options: %v", items)
	}
}

// A value the list does have must not be duplicated onto the end — a second
// "alice" would make the same name selectable at two indices, and Dirty()
// compares indices.
func TestPreservingItemsReusesAPresentValue(t *testing.T) {
	base := []string{"alice", "bob"}
	items, i := preservingItems(base, "bob")
	if len(items) != 2 || i != 1 {
		t.Errorf("preservingItems(base, \"bob\") = %v, %d; want the original list and index 1", items, i)
	}
}

func TestSelectPreservingShowsAVanishedOwner(t *testing.T) {
	// A job owned by a login that has since been dropped: the owner is not in
	// the list, and must still be what the row displays.
	row := selectPreserving("Owner", []string{"alice", "bob"}, "ghost", unknownOwnerItem)
	if got := row.Value(); got != "ghost" {
		t.Errorf("Value() = %q, want %q", got, "ghost")
	}
	if row.Dirty() {
		t.Error("a freshly built row must not report itself dirty")
	}
}

func TestSelectPreservingStandsInForABlank(t *testing.T) {
	row := selectPreserving("Owner", []string{"alice", "bob"}, "", unknownOwnerItem)
	if got := row.Value(); got != unknownOwnerItem {
		t.Errorf("Value() = %q, want %q", got, unknownOwnerItem)
	}
	if got := preservedValue(row, unknownOwnerItem); got != "" {
		t.Errorf("preservedValue() = %q, want %q — the stand-in must not read back as a name", got, "")
	}
	if row.Dirty() {
		t.Error("a freshly built row must not report itself dirty")
	}
}

// changedTo is the write gate. These are the two answers that must be "no":
// an untouched row, and one still sitting on its stand-in.
func TestChangedToRefusesAnUntouchedRow(t *testing.T) {
	row := selectPreserving("Owner", []string{"alice", "bob"}, "alice", unknownOwnerItem)
	if v, ok := changedTo(row, unknownOwnerItem); ok {
		t.Errorf("changedTo on an untouched row = %q, true; want no write", v)
	}
}

func TestChangedToRefusesTheStandIn(t *testing.T) {
	// The row must be dirty *and* sitting on the stand-in, which needs the
	// stand-in offered alongside a real value — the shape a page produces if
	// it ever prepends one unconditionally. Reaching it any other way (
	// SetSelected, SetItems) resets the dirty baseline and tests nothing.
	row := selectPreserving("Owner", []string{unknownOwnerItem, "alice"}, "alice", unknownOwnerItem)
	focusSelect(t, row)

	press(row, tcell.KeyEnter) // open
	press(row, tcell.KeyUp)    // "alice" -> the stand-in
	press(row, tcell.KeyEnter) // close

	if !row.Dirty() {
		t.Fatal("the row did not go dirty — test premise is wrong, the rest proves nothing")
	}
	if row.Value() != unknownOwnerItem {
		t.Fatalf("row landed on %q, want the stand-in %q", row.Value(), unknownOwnerItem)
	}
	if v, ok := changedTo(row, unknownOwnerItem); ok {
		t.Errorf("changedTo returned %q for a row edited onto its stand-in; "+
			"want no write — %q is not a principal name", v, unknownOwnerItem)
	}
}

func TestChangedToReportsARealEdit(t *testing.T) {
	row := selectPreserving("Owner", []string{"alice", "bob"}, "", unknownOwnerItem)
	focusSelect(t, row)

	// Up, not Down: selectPreserving appends the stand-in, so it is the last
	// item and Down has nowhere to go.
	press(row, tcell.KeyEnter) // open
	press(row, tcell.KeyUp)    // move off the stand-in
	press(row, tcell.KeyEnter) // close

	if !row.Dirty() {
		t.Fatal("the row did not go dirty — test premise is wrong, the rest proves nothing")
	}
	v, ok := changedTo(row, unknownOwnerItem)
	if !ok {
		t.Fatal("changedTo refused a real edit")
	}
	if v != "bob" {
		t.Errorf("changedTo = %q, want %q", v, "bob")
	}
}

// focusSelect gives row's dropdown keyboard focus. SelectRow.Draw is what
// hands focus down to the widget (dd.Focus), and DropDown.HandleKey ignores
// every key while unfocused, so drawing once is unavoidable here. Reuses the
// package's existing fakeSizedScreen rather than adding a second fake.
func focusSelect(t *testing.T, row *propsheet.SelectRow) {
	t.Helper()
	row.Layout(0, 0, 40)
	row.Draw(&fakeSizedScreen{w: 100, h: 40}, true)
}

func press(row *propsheet.SelectRow, k tcell.Key) {
	row.HandleKey(tcell.NewEventKey(k, "", tcell.ModNone))
}

// A grid the user has dragged a column on keeps that width across redrawGrid.
// DataGrid.SetSource clears the overrides — right for a query grid handed a
// new result set, wrong for a Properties page redrawing its own fixed columns,
// where the drag simply vanishes on the next toggle or refresh.
func TestRedrawGridKeepsDraggedColumnWidths(t *testing.T) {
	headers := []string{"Attached", "Name", "Enabled"}
	rows := [][]string{{"[x]", "Nightly", "True"}, {"[ ]", "Weekly", "False"}}

	grid := controls.NewDataGrid()
	grid.SetCellCursor(true)
	grid.SetBounds(0, 0, 80, 10)
	grid.SetData(headers, rows)

	grid.SetColumnWidth(1, 40)
	widths := grid.ColumnWidthOverrides()
	if widths[1] != 40 {
		t.Fatalf("column 1 override = %d before the redraw, want 40", widths[1])
	}

	rows[1][0] = "[x]"
	redrawGrid(grid, headers, rows)

	if got := grid.ColumnWidthOverrides(); got[1] != 40 {
		t.Errorf("column 1 override = %d after the redraw, want 40", got[1])
	}
	// The columns the user did not touch stay on their computed default, so a
	// redraw cannot freeze the whole grid at whatever it happened to be.
	if got := grid.ColumnWidthOverrides(); got[0] != 0 || got[2] != 0 {
		t.Errorf("untouched columns picked up overrides: %v", got)
	}
}

// A redraw that reshapes the grid starts clean rather than applying the old
// widths to new columns.
func TestRedrawGridDropsWidthsPastTheNewColumnCount(t *testing.T) {
	grid := controls.NewDataGrid()
	grid.SetBounds(0, 0, 80, 10)
	grid.SetData([]string{"A", "B", "C"}, [][]string{{"1", "2", "3"}})
	grid.SetColumnWidth(2, 30)

	redrawGrid(grid, []string{"A", "B"}, [][]string{{"1", "2"}})

	if got := grid.ColumnWidthOverrides(); len(got) != 2 {
		t.Fatalf("overrides = %v, want two columns' worth", got)
	}
}
