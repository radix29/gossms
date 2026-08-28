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

// unreadableValue is what a value the connected login is not allowed to read
// renders as. "N/A" rather than a blank or a zero: a login without VIEW SERVER
// STATE gets ServerInfo.SysInfoUnavailable and a zeroed CPU count and memory
// size, and printing those as numbers states positively that the machine has
// no CPUs, which is worse than saying nothing.
const unreadableValue = "N/A"

// sysInfoInt renders one of the two sys.dm_os_sys_info values ServerInfo
// carries, honouring SysInfoUnavailable. Never format either field directly.
func sysInfoInt(info *gosmo.ServerInfo, v int64) string {
	if info.SysInfoUnavailable {
		return unreadableValue
	}
	return strconv.FormatInt(v, 10)
}

// sysInfoMB renders ServerInfo.PhysicalMemoryMB the way the Object Explorer
// Details pane shows a size, honouring SysInfoUnavailable.
func sysInfoMB(info *gosmo.ServerInfo) string {
	if info.SysInfoUnavailable {
		return unreadableValue
	}
	return formatMB(float64(info.PhysicalMemoryMB))
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

// engineEditionName renders an EngineEdition code as SSMS would, falling back to
// the raw number for a code this build doesn't recognise.
func engineEditionName(code int) string {
	if name, ok := engineEditionNames[code]; ok {
		return name
	}
	return strconv.Itoa(code)
}

// boolIdx maps a bool onto a two-item Select/Radio row's index — 1 for true, 0
// for false — matching the [off, on] ordering every such row uses.
func boolIdx(b bool) int {
	if b {
		return 1
	}
	return 0
}

// indexOf returns the index of value within items, or 0 if it isn't present —
// the fallback a Select/Radio row's index needs when the server reports a value
// outside the row's options. 0, not slices.Index's -1: no Select row can show a
// negative index.
//
// The 0 is only safe when items[0] is a sentinel meaning "nothing" — a leading
// (None) or <All databases>. In a closed set with no such sentinel it renders
// the first real option as though the server had reported it; use indexOfOK
// there.
func indexOf(items []string, value string) int {
	i, _ := indexOfOK(items, value)
	return i
}

// indexOfOK is indexOf plus whether value was found, for a row whose items can't
// absorb a miss. On false the caller shows the server's own value read-only
// rather than letting a plausible option stand in.
func indexOfOK(items []string, value string) (int, bool) {
	if i := slices.Index(items, value); i >= 0 {
		return i, true
	}
	return 0, false
}

// preservingItems returns base and the index of value within it, widening the
// list with value itself when base doesn't contain it, so the returned index
// always points at value — the one thing indexOf cannot promise.
//
// Prefer this to indexOf for any value the *server* supplied. indexOf's
// not-found 0 renders items[0] as though the server had reported it, so a job
// owned by a dropped login, or a schema owned by an unresolvable principal,
// displays the first real option as fact — on exactly the objects an admin
// opened the page to investigate.
//
// value must be non-empty; pass orDefault(value, <sentinel>) for a field the
// server leaves blank, so the stand-in is named at the call site.
// selectPreserving does that for you.
func preservingItems(base []string, value string) ([]string, int) {
	if i, ok := indexOfOK(base, value); ok {
		return base, i
	}
	items := append(slices.Clone(base), value)
	return items, len(items) - 1
}

// selectPreserving builds a Select row that can never misreport: it displays
// value whether or not base offers it, showing unset in its place when the
// server reported nothing.
//
// Read it back with preservedValue, which maps unset to "" again — a row showing
// the stand-in must not write it as a real name.
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
// whether there is one — dirty, and not sitting on its stand-in.
//
// Gate every write behind it rather than on Dirty() alone. Dirty() happens to
// suffice today only because a stand-in is in the list only when it is also the
// original selection; that is a property of how the list is built, three files
// away from the write, and a page offering a stand-in alongside a real value
// would send "(unresolved owner)" to the server as a principal name.
func changedTo(row *propsheet.SelectRow, unset string) (string, bool) {
	if !row.Dirty() {
		return "", false
	}
	v := preservedValue(row, unset)
	return v, v != ""
}

// redrawGrid replaces a grid's rows while leaving the cell cursor — and any
// column dragged to a width of its own — where the user put them.
//
// DataGrid.SetData resets the cursor to 0,0, right for a fresh result set and
// wrong for the redraws a Properties page does to re-render state the user is
// still navigating. The cursor jumps to the first row, and propsheet.GridRow —
// which reports movement by diffing SelectedCell either side of the key — then
// answers "not handled", so Form moves focus straight out of the grid on the
// first arrow key. See wireGridEditor (ag_props.go) for the worked example.
//
// The dragged widths and the scroll position go the same way, and the scroll is
// not fixed by restoring the cursor: SetSelectedCell ends in ensureVisible,
// which scrolls from the zero SetSource left just far enough to reach the
// selected row, putting it against the bottom edge. Toggling a State half way
// down a long grid therefore jumped the whole list on every click.
//
// This is the application layer's name for DataGrid.SetDataPreservingView, which
// does the restoring and documents why the three go back in the order they do.
func redrawGrid(grid *controls.DataGrid, headers []string, rows [][]string) {
	grid.SetDataPreservingView(headers, rows)
}

// resetGrid is redrawGrid for a change that alters the row *set* — an Add, a
// Remove, a Revert — where the caller has a row it wants selected afterwards
// and the old cursor no longer means anything.
//
// It exists because SetData is not the right half of that pair either:
// SetSource clears colWidthOverride along with the cursor, so an Add threw away
// a column the user had dragged wider. The cursor being set explicitly hid
// that, which is why the seventeen sites that hand-rolled SetData +
// SetSelectedRow never showed the GridRow keyboard trap redrawGrid documents —
// only the widths were lost.
//
// The scroll is restored and then moved by SetSelectedRow's own ensureVisible,
// so appending a row scrolls just far enough to show it rather than jumping the
// list from zero.
func resetGrid(grid *controls.DataGrid, headers []string, rows [][]string, row int) {
	grid.SetDataPreservingView(headers, rows)
	grid.SetSelectedRow(row)
}

// compatLevelItems is the Compatibility level dropdown's base list, from the
// oldest level SQL Server still accepts to the newest gosmo names. Don't use it
// directly — a server can report a level outside it in either direction, so
// build the list with compatItemsFor.
var compatLevelItems = []string{"100", "110", "120", "130", "140", "150", "160", "170"}

// compatItemsFor returns the Compatibility level items for a database currently
// at level, inserting level in numeric order when the base list lacks it — a
// database restored from an older instance, or a level a newer SQL Server adds.
//
// Selecting into the fixed list with indexOf's not-found 0 displays such a
// database as level 100, itself a real level and so read as fact. A level of 0
// (an unpopulated lightweight handle) adds nothing.
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

// orDefault returns s, or def if s is empty — for server fields that come back
// blank when unset but need a concrete default for indexOf/Select.
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
// Statistics Properties. SQL Server accepts a filtered predicate only at CREATE
// time, so on an existing index or statistic it is read-only, with Check
// Syntax/Estimate Rows running the predicate against the table live.
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

// Column headers shared by more than one grid. These pages read their grids back
// positionally against the slice they were built from, and several rebuild the
// same grid from three or four call sites, so a header list spelled out at each
// can drift at one of them — showing as a column labelled for its neighbour,
// which no test that also works in indices can see.
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
