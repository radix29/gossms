package tui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// boolStr renders a bool as "True"/"False", the Static-row convention used
// throughout the Properties pages.
func boolStr(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// engineEditionNames maps SERVERPROPERTY('EngineEdition') (gosmo's
// ServerInfo.EngineEdition) to the name SSMS's General page shows.
var engineEditionNames = map[int]string{
	1:  "Personal/Desktop Engine",
	2:  "Standard",
	3:  "Enterprise",
	4:  "Express",
	5:  "SQL Database",
	6:  "SQL Data Warehouse",
	8:  "Managed Instance",
	9:  "SQL Edge",
	11: "Azure Synapse serverless SQL pool",
}

// engineEditionName renders an EngineEdition code as SSMS would, falling
// back to the raw number for a code this build doesn't recognize yet.
func engineEditionName(code int) string {
	if name, ok := engineEditionNames[code]; ok {
		return name
	}
	return strconv.Itoa(code)
}

// boolIdx maps a bool onto a two-item Select/Radio row's index — 1 for
// true, 0 for false — matching the [off, on] / [enabled, disabled] item
// ordering every such row on these pages uses.
func boolIdx(b bool) int {
	if b {
		return 1
	}
	return 0
}

// indexOf returns the index of value within items, or 0 (the first item)
// if it isn't present — the fallback a Select/Radio row's "selected" index
// needs when the server reports a value outside the row's known options.
// Note the 0, not slices.Index's -1: no Select row can show a negative index.
//
// The 0 is only a safe fallback when items[0] is a sentinel that means
// "nothing" — a leading (None) or <All databases>. Where items is a closed
// set with no such sentinel, falling back renders the first real option as
// though the server had reported it; use indexOfOK there instead.
func indexOf(items []string, value string) int {
	i, _ := indexOfOK(items, value)
	return i
}

// indexOfOK is indexOf plus whether value was actually found, for a row whose
// items can't absorb a miss. A caller that gets false shows the server's own
// value (read-only) rather than letting a wrong-but-plausible option stand in
// for it.
func indexOfOK(items []string, value string) (int, bool) {
	if i := slices.Index(items, value); i >= 0 {
		return i, true
	}
	return 0, false
}

// preservingItems returns base and the index of value within it, widening the
// list with value itself when base doesn't contain it. The returned index
// therefore always points at value — which is the whole point, and the one
// thing indexOf cannot promise.
//
// Prefer this to indexOf for any value the *server* supplied. indexOf's
// not-found 0 renders items[0] as though the server had reported it, so a job
// owned by a dropped login, a login whose default database no longer exists,
// or a schema owned by an unresolvable principal all display the first real
// option as fact — on exactly the objects an admin opened the page to
// investigate. A widened list cannot lie: the value shown is the value read.
//
// value must be non-empty; pass orDefault(value, <sentinel>) for a field the
// server leaves blank, so the stand-in is named at the call site rather than
// guessed here. selectPreserving does that for you.
func preservingItems(base []string, value string) ([]string, int) {
	if i, ok := indexOfOK(base, value); ok {
		return base, i
	}
	items := append(slices.Clone(base), value)
	return items, len(items) - 1
}

// selectPreserving builds a Select row that can never misreport: it displays
// value, whether or not base offers it, showing unset in value's place when
// the server reported nothing.
//
// Read it back with preservedValue, which maps unset to "" again — a row left
// showing the stand-in must not write the stand-in as if it were a real name.
func selectPreserving(label string, base []string, value, unset string) *propsheet.SelectRow {
	items, i := preservingItems(base, orDefault(value, unset))
	return propsheet.Select(label, items, i)
}

// preservedValue reads a selectPreserving row back as a value to write,
// undoing its stand-in.
func preservedValue(row *propsheet.SelectRow, unset string) string {
	if v := row.Value(); v != unset {
		return v
	}
	return ""
}

// changedTo reports the real value a selectPreserving row was edited to, and
// whether there is one — dirty, and not left sitting on its stand-in.
//
// Gate every write behind it rather than on Dirty() alone. Dirty() happens to
// be enough today, because a stand-in is only ever in the list when it is also
// the original selection, so returning to it clears the flag; that is a
// property of how the list is built, three files away from the write, and a
// page that ever offers a stand-in alongside a real value would send
// "(unresolved owner)" to the server as a principal name.
func changedTo(row *propsheet.SelectRow, unset string) (string, bool) {
	if !row.Dirty() {
		return "", false
	}
	v := preservedValue(row, unset)
	return v, v != ""
}

// redrawGrid replaces a grid's rows while leaving the cell cursor — and any
// column the user has dragged to a width of their own — where they put them.
//
// `DataGrid.SetData` resets the cursor to 0,0 — correct for a fresh result set,
// wrong for the redraws a Properties page does to re-render state the user is
// still navigating. Two things go wrong without the restore, and the second is
// the one that makes a page unusable rather than merely jumpy: the cursor jumps
// to the first row, and `propsheet.GridRow` — which reports movement by diffing
// `SelectedCell` either side of the key — then answers "not handled", so `Form`
// moves focus straight out of the grid on the first arrow key. See
// `wireGridEditor` (ag_props.go) for the worked example and the bug it fixed.
//
// The dragged widths and the scroll position go the same way and for the same
// reason, and the scroll is the one that is not fixed by restoring the cursor:
// `SetSelectedCell` ends in `ensureVisible`, which scrolls from wherever it
// finds the view — from the zero `SetSource` left, just far enough to reach
// the selected row, putting it against the bottom edge. Toggling a State half
// way down a long Securables or Scoped Configuration grid therefore jumped the
// whole list on every click.
//
// This is the application layer's name for `DataGrid.SetDataPreservingView`,
// which does the restoring; the mechanics, and why the three go back in the
// order they do, are documented there. Kept as a wrapper because a Properties
// page saying `redrawGrid` reads as the page-level idiom it is, and because
// CLAUDE.md's rule names it.
func redrawGrid(grid *controls.DataGrid, headers []string, rows [][]string) {
	grid.SetDataPreservingView(headers, rows)
}

// compatLevelItems is the Compatibility level dropdown's base list: the
// oldest level SQL Server still accepts through the newest gosmo names
// (gosmo.CompatLevel2025 == 170). Don't use it directly — a server can report
// a level outside it in either direction, so build the list with
// compatItemsFor.
var compatLevelItems = []string{"100", "110", "120", "130", "140", "150", "160", "170"}

// compatItemsFor returns the Compatibility level items for a database
// currently at level, with level inserted in numeric order when the base list
// doesn't already have it — a database restored from an older instance (90 or
// below), or a level a newer SQL Server adds.
//
// Selecting into the fixed list with indexOf's not-found 0 displayed such a
// database as level 100, which is itself a real level and so read as fact.
// A level of 0 (an unpopulated lightweight handle) adds nothing.
func compatItemsFor(level int) []string {
	s := strconv.Itoa(level)
	if level <= 0 || slices.Contains(compatLevelItems, s) {
		return compatLevelItems
	}
	items := append(slices.Clone(compatLevelItems), s)
	slices.SortFunc(items, func(a, b string) int {
		ai, _ := strconv.Atoi(a)
		bi, _ := strconv.Atoi(b)
		return cmp.Compare(ai, bi)
	})
	return items
}

// orDefault returns s, or def if s is empty — for server fields that come
// back blank when unset but need a concrete default for indexOf/Select.
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// credNames extracts the Name field of every credential, for building a
// Select row's item list.
func credNames(creds []*gosmo.Credential) []string {
	names := make([]string, len(creds))
	for i, c := range creds {
		names[i] = c.Name
	}
	return names
}

// buildFilterInfoForm builds the read-only Filter page shared by Index and
// Statistics Properties: SQL Server only accepts a filtered predicate at
// CREATE time, so on an existing index/statistic it's shown read-only here
// rather than editable, with Check Syntax/Estimate Rows running the real
// predicate against the table live via t's own CheckWhereSyntax/CountWhere.
func buildFilterInfoForm(d *PropDialog, t *gosmo.Table, hasFilter bool, filterDef string) *propsheet.Form {
	statusRow := propsheet.Static("Status", "Not checked")
	rowsRow := propsheet.Static("Estimated qualifying rows", "")

	checkBtn := d.asyncStatusButton("Check Syntax", statusRow, "Checking...", func(ctx context.Context) (string, error) {
		if filterDef == "" {
			return "", fmt.Errorf("no filter expression to check")
		}
		if err := t.CheckWhereSyntaxContext(ctx, filterDef); err != nil {
			return "", err
		}
		return "Valid", nil
	})
	estimateBtn := d.asyncStatusButton("Estimate Rows", rowsRow, "Estimating...", func(ctx context.Context) (string, error) {
		if filterDef == "" {
			return "", fmt.Errorf("no filter expression to estimate")
		}
		n, err := t.CountWhereContext(ctx, filterDef)
		if err != nil {
			return "", err
		}
		return strconv.FormatInt(n, 10), nil
	})

	return propsheet.NewForm(
		propsheet.Section("Filtered predicate"),
		propsheet.Static("Filtered", boolStr(hasFilter)),
		propsheet.Section("Filter expression"),
		propsheet.Static("Expression", orDefault(filterDef, "(none)")),
		propsheet.Section("Validation"),
		statusRow, rowsRow,
		propsheet.Buttons(checkBtn, estimateBtn),
		propsheet.Note("The predicate can only be set when the index or statistic is created — use Script Changes, or DROP + CREATE, to change it. Check Syntax and Estimate Rows run the expression against the live table."),
	)
}

// Column headers shared by more than one grid. Each of these pages reads its
// grid back positionally against the slice it was built from, and several
// rebuild the same grid from three or four call sites, so a header list
// spelled out at each one is a list that can drift at one of them — the
// misalignment then shows as a column labelled for its neighbour, which no
// test that also works in indices can see.
var (
	// permissionStateColumns heads the grant/deny/revoke matrices.
	permissionStateColumns = []string{"Permission", "State"}
	// propertyValueColumns heads the Detail Browser's two-column property
	// readouts.
	propertyValueColumns = []string{"Property", "Value"}
	// indexKeyColumns heads the key-column list an index, key or statistic
	// is built on.
	indexKeyColumns = []string{"Ord", "Column name", "Sort order"}
)
