package tui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// boolStr renders a bool as "True"/"False", the Static-row convention used
// throughout the Properties pages.
func boolStr(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// unreadableValue is what a value the login may not read renders as. "N/A"
// rather than blank or zero: without VIEW SERVER STATE, ServerInfo has
// SysInfoUnavailable and zeroed CPU count and memory, and printing those states
// positively that the machine has no CPUs.
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

// boolIdx maps a bool onto a two-item Select/Radio row's index (1 true, 0 false),
// matching the [off, on] ordering every such row uses.
func boolIdx(b bool) int {
	if b {
		return 1
	}
	return 0
}

// indexOf returns the index of value within items, or 0 if absent: the fallback
// a Select/Radio row needs when the server reports a value outside the row's
// options (not slices.Index's -1, which no row can show).
//
// Only safe when items[0] is a sentinel meaning "nothing" ((None), <All
// databases>). In a closed set it renders the first real option as though the
// server had reported it; use indexOfOK there.
func indexOf(items []string, value string) int {
	i, _ := indexOfOK(items, value)
	return i
}

// indexOfOK is indexOf plus whether value was found, for a row whose items
// can't absorb a miss. On false the caller shows the server's own value
// read-only rather than letting a plausible option stand in.
func indexOfOK(items []string, value string) (int, bool) {
	if i := slices.Index(items, value); i >= 0 {
		return i, true
	}
	return 0, false
}

// preservingItems returns base and the index of value within it, widening the
// list with value itself when base lacks it, so the index always points at value.
//
// Prefer this to indexOf for any server-supplied value: indexOf's not-found 0
// would display items[0] as fact, e.g. for a job owned by a dropped login, on
// exactly the objects an admin opened the page to investigate.
//
// value must be non-empty; pass orDefault(value, <sentinel>) for a field the
// server leaves blank. selectPreserving does that for you.
func preservingItems(base []string, value string) ([]string, int) {
	if i, ok := indexOfOK(base, value); ok {
		return base, i
	}
	items := append(slices.Clone(base), value)
	return items, len(items) - 1
}

// selectPreserving builds a Select row that can never misreport: it displays
// value whether or not base offers it, showing unset when the server reported
// nothing.
//
// Read it back with preservedValue, which maps unset to "" again; a stand-in must
// not be written as a real name.
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
// whether there is one: dirty, and not sitting on its stand-in.
//
// Gate every write behind it rather than on Dirty() alone. Dirty() suffices only
// because a stand-in is listed only when it is also the original selection, a
// property of how the list is built far from the write; otherwise "(unresolved
// owner)" could be sent to the server as a principal name.
//
// A nil row is a page that drew the value read-only for this object and never
// changes.
func changedTo(row *propsheet.SelectRow, unset string) (string, bool) {
	if row == nil || !row.Dirty() {
		return "", false
	}
	v := preservedValue(row, unset)
	return v, v != ""
}

// redrawGrid replaces a grid's rows while leaving the cell cursor, dragged column
// widths and scroll position where the user put them.
//
// DataGrid.SetData resets the cursor to 0,0: right for a fresh result set, wrong
// for a Properties page re-rendering state the user is navigating. The cursor
// would jump to the first row, and propsheet.GridRow (which detects movement by
// diffing SelectedCell around the key) would answer "not handled", so Form moves
// focus out of the grid on the first arrow key. See wireGridEditor.
//
// Restoring the cursor alone doesn't fix the scroll: SetSelectedCell ends in
// ensureVisible, which scrolls from zero to put the row at the bottom edge, so
// toggling a State halfway down a long grid would jump the list on every click.
//
// This is DataGrid.SetDataPreservingView, which documents the restore order.
func redrawGrid(grid *controls.DataGrid, headers []string, rows [][]string) {
	grid.SetDataPreservingView(headers, rows)
}

// resetGrid is redrawGrid for a change that alters the row *set* (Add, Remove,
// Revert), where the caller selects a row afterwards and the old cursor means
// nothing.
//
// SetData is wrong here too: SetSource clears colWidthOverride, so an Add threw
// away a column the user had dragged wider. The scroll is restored and then moved
// by SetSelectedRow's ensureVisible, so appending a row scrolls just far enough
// to show it.
func resetGrid(grid *controls.DataGrid, headers []string, rows [][]string, row int) {
	grid.SetDataPreservingView(headers, rows)
	grid.SetSelectedRow(row)
}

// wireGridEditor connects a detail editor to a DataGrid's selection (the
// grid-plus-detail shape of the AG General replica grid, Backup Preferences,
// Read-Only Routing, User Mapping, Attach and New Index/Statistics/AG pages).
// Moving off a row commits the editor, loads the new row, and redraws. Returns the
// redraw a page's RevertFn needs.
//
// The selected cell is saved and restored around SetData, which resets it to 0,0
// from inside OnSelectRow. Without that only the first row is reachable, and
// GridRow reports arrows unhandled, ejecting focus.
func wireGridEditor(grid *controls.DataGrid, headers []string, gridRows func() [][]string, commitCurrent, syncFromSelection func()) (reload func()) {
	redraw := func() { redrawGrid(grid, headers, gridRows()) }
	grid.OnSelectRow = func(int) {
		commitCurrent()
		syncFromSelection()
		redraw()
	}
	syncFromSelection()
	return func() {
		redraw()
		syncFromSelection()
	}
}

// compatLevelItems is the Compatibility level dropdown's base list, oldest
// accepted level to newest gosmo names. Don't use it directly: a server can report
// a level outside it and offers only its own version's levels, so build the list
// with compatItemsFor.
var compatLevelItems = []string{"100", "110", "120", "130", "140", "150", "160", "170"}

// maxCompatForMajor is the highest compatibility level each SQL Server major
// accepts. Offering a higher one gets "Valid values of the database compatibility
// level are 100, 110, 120, 130 or 140. (15048)" (2017 rejects 150, 160, 170).
//
// major*10 holds for every reachable version, but an explicit map makes a future
// irregular release a compile-time edit rather than a silent wrong answer.
var maxCompatForMajor = map[int]int{
	13: 130, // 2016
	14: 140, // 2017
	15: 150, // 2019
	16: 160, // 2022
	17: 170, // 2025
}

// compatItemsFor returns the Compatibility level items for a database at level on
// a server of the given major version.
//
// The list is capped at what major accepts; an unknown major (0, or newer than
// the map) is uncapped, gosmo's "treat it as newest" convention. level is inserted
// in numeric order when the capped list lacks it (a database restored from an
// older instance, or above the cap) because it must display as its real level
// though it can't be selected.
//
// indexOf's not-found 0 would show such a database as level 100, a real level
// read as fact. A level of 0 (an unpopulated lightweight handle) adds nothing.
func compatItemsFor(level, major int) []string {
	items := compatLevelItems
	if max, ok := maxCompatForMajor[major]; ok {
		items = slices.DeleteFunc(slices.Clone(items), func(s string) bool {
			n, _ := strconv.Atoi(s)
			return n > max
		})
	}
	s := strconv.Itoa(level)
	if level <= 0 || slices.Contains(items, s) {
		return items
	}
	items = append(slices.Clone(items), s)
	slices.SortFunc(items, func(a, b string) int {
		ai, _ := strconv.Atoi(a)
		bi, _ := strconv.Atoi(b)
		return cmp.Compare(ai, bi)
	})
	return items
}

// serverMajor is the connected instance's major version, or 0 when unknown, which
// every version-gated helper treats as newest.
//
// An Azure edition is 0 too (gosmo's serverMajorVersion): a Managed Instance
// reports 12 while running an 18.x engine, so believing it would cap
// compatibility levels at 120 and hide the 2019 enclave options.
func serverMajor(sc *db.ServerConn) int {
	if sc == nil || sc.Server == nil || sc.Server.Info() == nil || sc.Server.Info().IsAzure() {
		return 0
	}
	return sc.Server.Info().VersionMajor
}

// orDefault returns s, or def if s is empty: for server fields that come back
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
		if err := t.CheckWhereSyntax(ctx, filterDef); err != nil {
			return "", err
		}
		return "Valid", nil
	})
	estimateBtn := d.asyncStatusButton("Estimate Rows", rowsRow, "Estimating...", func(ctx context.Context) (string, error) {
		if filterDef == "" {
			return "", fmt.Errorf("no filter expression to estimate")
		}
		n, err := t.CountWhere(ctx, filterDef)
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

// Column headers shared by more than one grid. These pages read grids back
// positionally against the slice they were built from, and several rebuild a grid
// from three or four call sites, so headers spelled out at each can drift (a
// column labelled for its neighbour) in a way no test sees.
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

// platformText renders ServerInfo.Platform for a Static row. Platform is empty
// when @@VERSION names neither Windows nor Linux; "Unknown" beats a blank row.
// Never show ServerInfo.OSVersion here: it is @@VERSION verbatim, a multi-line
// banner that clips to a meaningless first line.
func platformText(info *gosmo.ServerInfo) string {
	if info.Platform == "" {
		return "Unknown"
	}
	return info.Platform
}

// versionBanner renders ServerInfo.OSVersion (@@VERSION verbatim) for a
// single-line row. Line breaks and tabs would break a grid row, so every
// whitespace run collapses to one space; the full text is still what Show Value
// and a Static row's Ctrl+C hand back.
func versionBanner(info *gosmo.ServerInfo) string {
	s := strings.Join(strings.Fields(info.OSVersion), " ")
	if s == "" {
		return "Unknown"
	}
	return s
}

// mustPropertyRowIndex returns the index of the label/value row carrying the
// given label. A loader that backfills a row after an async read addresses it
// this way rather than by constant, so inserting a row above cannot redirect the
// write into the wrong row. The label is a literal in the same function, so a
// miss is a typo and panics rather than backfilling nothing.
func mustPropertyRowIndex(rows [][]string, label string) int {
	for i, r := range rows {
		if len(r) > 0 && r[0] == label {
			return i
		}
	}
	panic("no property row labelled " + label)
}

// wireCellToggle wires the "activate a cell in one column to change that row's
// value" idiom: bounds-check the activation, mutate the page's own edit for that
// row, then re-render in place.
//
// col is the one editable column; every other column and an out-of-range row
// (header area, blank space past the last row) is ignored, which stops a write to
// edits[len(edits)-1]. count is read at activation time, not captured, because a
// filter row can shrink the visible slice after wiring.
//
// The redraw is redrawGrid, never SetData: these grids are navigated while
// toggled. Centralised here because it is easy to get wrong nine times.
func wireCellToggle(grid *controls.DataGrid, headers []string, col int,
	count func() int, change func(row int), rowsFor func() [][]string) {
	grid.OnActivateCell = func(row, c int) {
		if c != col || row < 0 || row >= count() {
			return
		}
		change(row)
		redrawGrid(grid, headers, rowsFor())
	}
}

// newCellToggleGrid is wireCellToggle for a page that builds the grid too: a
// cell-cursor DataGrid seeded from rowsFor, with column col editable.
//
// Pages whose grid is referenced by other closures before the toggle can be
// described build it themselves and call wireCellToggle.
func newCellToggleGrid(headers []string, col int,
	count func() int, change func(row int), rowsFor func() [][]string) *controls.DataGrid {
	grid := controls.NewDataGrid()
	grid.SetData(headers, rowsFor())
	grid.SetCellCursor(true)
	wireCellToggle(grid, headers, col, count, change, rowsFor)
	return grid
}

// staticBlock is a group of read-only detail rows filled from a grid's selected
// row (the "Selected alert"/"Selected schedule" half of a grid-plus-detail page).
//
// It exists for the out-of-range branch: clearing one SetValue("") per row risks
// a missed row that keeps describing the object the selection just left. set()
// with no values clears every row, and a short list clears the rest.
//
// Rows are constructed by the caller so each label stays a literal argument to
// propsheet.Static; TestNoPropertySheetLabelIsTruncated reads the call sites and
// a label moved into a slice would silently stop being checked.
type staticBlock struct {
	rows []*propsheet.StaticRow
}

func newStaticBlock(rows ...*propsheet.StaticRow) *staticBlock {
	return &staticBlock{rows: rows}
}

// set fills the block from values, one per row in construction order; rows past
// the end of values are cleared.
func (b *staticBlock) set(values ...string) {
	for i, r := range b.rows {
		if i < len(values) {
			r.SetValue(values[i])
		} else {
			r.SetValue("")
		}
	}
}

// sqlBodyRow is a read-only SQL editor row holding body, the "object's T-SQL"
// row every Definition page shows. A writable editor would offer edits the page
// can't save.
func sqlBodyRow(label, body string, height int) propsheet.Row {
	ed := controls.NewEditor(controls.SQLHighlighter(theme.Active()))
	ed.SetText(body)
	ed.SetReadOnly(true)
	return propsheet.NewEditorRow(label, ed, height)
}

// definitionPage is the read-only Definition page a DDL trigger shows at either
// scope. A database trigger's body comes from sys.sql_modules and a server
// trigger's from sys.server_sql_modules; load supplies the body and everything
// else, including the wording for an unreadable body, lives here once.
//
// An encrypted trigger, and a CLR trigger (no row in either view), report the
// absence on the page rather than failing it.
func definitionPage(load func(context.Context) (string, error)) propPage {
	return propPage{
		title: "Definition",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			body, err := load(ctx)
			if err != nil {
				return nil, nil, err
			}
			if strings.TrimSpace(body) == "" {
				return propsheet.NewForm(
					propsheet.Section("Definition"),
					propsheet.Note("The trigger's definition is not readable — it is either encrypted (WITH ENCRYPTION) or a CLR trigger, which stores no T-SQL body."),
				), nil, nil
			}
			return propsheet.NewForm(
				propsheet.Section("Definition"),
				sqlBodyRow("Body", body, 16),
			), nil, nil
		},
	}
}

// indexKeyColumnGrid is the key-column list an index and a key both show: the
// same ordinal/name/direction rows under indexKeyColumns, built once so the two
// can't disagree about what "Descending" means.
func indexKeyColumnGrid(idx *gosmo.Index) *controls.DataGrid {
	rows := make([][]string, len(idx.KeyColumns))
	for i, c := range idx.KeyColumns {
		order := "Ascending"
		if c.Descending {
			order = "Descending"
		}
		rows[i] = []string{strconv.Itoa(i + 1), c.Name, order}
	}
	grid := controls.NewDataGrid()
	grid.SetData(indexKeyColumns, rows)
	grid.SetCellCursor(true)
	return grid
}

// auditSelectRow builds the "Audit" dropdown both audit-specification pages show,
// from the audits offered and the one the specification is bound to.
//
// An orphaned specification (its audit dropped, which SQL Server allows) has a
// name in no list, and the database page also omits audits another specification
// holds. A dropdown preselecting the first real audit would let a stray Apply
// silently rebind the specification to it. The missing name is added as
// missingAuditItem and selected; the apply paths test the row's value against
// that constant before writing.
func auditSelectRow(names []string, current string) *propsheet.SelectRow {
	selected := slices.Index(names, current)
	if selected < 0 {
		names = append([]string{missingAuditItem}, names...)
		selected = 0
	}
	return propsheet.Select("Audit", names, selected)
}

// unionSorted returns list widened with every extra it does not already hold,
// sorted: the audit-specification pick lists' rule.
//
// A group the instance no longer defines but the specification still records is
// not in the server's list; dropping it would hide it from the only page that can
// stop recording it. Pages read grids back positionally against the returned
// slice, so the sort happens here, once.
func unionSorted(list, extra []string) []string {
	for _, v := range extra {
		if !slices.Contains(list, v) {
			list = append(list, v)
		}
	}
	slices.Sort(list)
	return list
}

// auditGroupGrid builds the "Record / Audit Action Group" toggle grid both
// audit-specification dialogs and Properties pages show: one row per group,
// ticked when recorded holds it. A New dialog passes nil. Read back
// positionally, so groups must be the slice the caller reads it against.
func auditGroupGrid(groups, recorded []string, height int) *propsheet.ToggleGridRow {
	grid := propsheet.NewToggleGrid([]string{"Record", "Audit Action Group"}, []int{0}, height)
	text := make([][]string, len(groups))
	values := make([][]bool, len(groups))
	for i, g := range groups {
		text[i] = []string{g}
		values[i] = []bool{slices.Contains(recorded, g)}
	}
	grid.SetRows(text, values)
	return grid
}

// tickedAuditGroups returns the groups whose Record box is ticked in a grid
// auditGroupGrid built from groups.
func tickedAuditGroups(grid *propsheet.ToggleGridRow, groups []string) []string {
	var ticked []string
	for i, g := range groups {
		if grid.Values()[i][0] {
			ticked = append(ticked, g)
		}
	}
	return ticked
}
