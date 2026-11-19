package tui

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/propsheet"
	"github.com/radix29/tuikit/widgets"
)

// securableListColumns heads the list of securables a principal can be
// granted on.
var securableListColumns = []string{"Securable", "Type"}

// securableColumnColumns heads the per-column permission editor.
var securableColumnColumns = []string{"Column", "State"}

// securable identifies one thing a database principal's Securables page can
// grant or deny permissions on — the page a user and a database role share:
// the database itself, or any securable below it gosmo has a class for.
type securable struct {
	Type   gosmo.SecurableClass
	Schema string // empty for a class that is not schema-scoped
	Name   string // empty for DATABASE
}

func (s securable) key() string { return string(s.Type) + "\x00" + s.Schema + "\x00" + s.Name }

func (s securable) label() string {
	if s.Type == gosmo.SecurableDatabase {
		return "(database)"
	}
	// gosmo owns which classes live in a schema: a class added there later
	// labels by its own rule, not as [].[name].
	if s.Type.SchemaScoped() {
		return gosmo.QualifiedName(s.Schema, s.Name)
	}
	return gosmo.QuoteName(s.Name)
}

// hasColumns reports whether this securable's columns can carry permissions of
// their own — tables, views and table-valued functions can.
func (s securable) hasColumns() bool { return s.Type.HasColumns() }

// catalog returns every permission name grantable on this securable's type.
// A view's differs from a table's: it has no VIEW CHANGE TRACKING.
func (s securable) catalog() []string { return s.Type.PermissionNames() }

// columnCatalog returns every permission name grantable on one of this
// securable's columns: a multi-statement table function's lacks UPDATE.
func (s securable) columnCatalog() []string { return s.Type.ColumnPermissionNames() }

// gosmo is the securable as ApplyPermission addresses it, narrowed to columns
// when they are given. The database's own name stays off it, since the
// receiver already names the database.
func (s securable) gosmo(columns []string) gosmo.Securable {
	sec := gosmo.Securable{Class: s.Type, Schema: s.Schema, Name: s.Name, Columns: columns}
	if sec.Class == gosmo.SecurableDatabase {
		sec.Name, sec.Schema = "", ""
	}
	return sec
}

// securableEdit tracks one (securable, permission) cell's pending state,
// same cycle as permEdit.
type securableEdit struct {
	sec        securable
	permission string
	orig       gosmo.PermissionState
	current    gosmo.PermissionState
}

// columnEdit tracks one (securable, permission, column) cell's pending state —
// the column-level grants behind SSMS's "Column Permissions..." button. Keyed on
// all three because the editor keeps edits made against one securable/permission
// pair while the user browses to another, and Apply writes them all.
type columnEdit struct {
	sec        securable
	permission string
	column     string
	orig       gosmo.PermissionState
	current    gosmo.PermissionState
}

func columnEditKey(sec securable, permission, column string) string {
	return sec.key() + "\x00" + permission + "\x00" + column
}

// pageDatabasePrincipalSecurables builds a "Securables" page for one database
// principal — a user or a database role, which hold explicit
// object/schema/database-scoped GRANT/DENY entries the same way, so both
// Properties dialogs use this page. principal is read through a pointer because
// a rename on the General page changes it while the dialog is open. Its
// server-level counterpart is pagePrincipalServerPermissions.
//
// d runs the page's own round trips: the Add picker's search and the
// column-permissions editor's column fetch.
func pageDatabasePrincipalSecurables(d *PropDialog, sc *db.ServerConn, dbName string, principal *string) propPage {
	return propPage{
		title: "Securables",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			database, err := sc.Server.DatabaseByName(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			entries, err := database.PermissionsForPrincipal(ctx, *principal)
			if err != nil {
				return nil, nil, err
			}
			colEntries, err := database.ColumnPermissionsForPrincipal(ctx, *principal)
			if err != nil {
				return nil, nil, err
			}
			seen := make(map[string]bool)
			var initial []securable
			for _, e := range entries {
				s := securable{Type: e.SecurableType, Schema: e.Schema, Name: e.Name}
				if !seen[s.key()] {
					seen[s.key()] = true
					initial = append(initial, s)
				}
			}
			// A securable the principal holds only *column* grants on has no
			// object-level entry, so without this it isn't listed and its
			// column grants stay invisible until the user adds it back by
			// hand.
			for _, e := range colEntries {
				s := securable{Type: e.ObjectType, Schema: e.Schema, Name: e.Object}
				if !seen[s.key()] {
					seen[s.key()] = true
					initial = append(initial, s)
				}
			}

			// The Add picker's candidates come from a name search against the
			// server, not the whole catalog: loading every table into a
			// dropdown is slow to open and useless to pick from. One capped
			// search gives the picker something to show before the user
			// types.
			find := securableFinder(database)
			candidates, err := find(ctx, "")
			if err != nil {
				return nil, nil, err
			}

			f, apply := buildSecurablesMatrix(d, database, initial, entries, colEntries, candidates, find, 8, 12,
				func(ctx context.Context, verb gosmo.PermissionVerb, opts gosmo.PermissionOptions, s securable, permission string) error {
					return applySecurable(ctx, database, verb, opts, s, permission, *principal)
				},
				func(ctx context.Context, verb gosmo.PermissionVerb, opts gosmo.PermissionOptions, s securable, permission, column string) error {
					return applyColumnSecurable(ctx, database, verb, opts, s, permission, column, *principal)
				},
			)
			return f, apply, nil
		},
	}
}

// securableFinder is the Add picker's search against database: securables whose
// name contains term, capped one past securableSearchLimit.
func securableFinder(database *gosmo.Database) securableFindFn {
	return func(ctx context.Context, term string) ([]securable, error) {
		refs, err := database.FindSecurables(ctx,
			gosmo.SecurableSearch{Name: term, Limit: securableSearchLimit + 1})
		if err != nil {
			return nil, err
		}
		out := make([]securable, len(refs))
		for i, r := range refs {
			out[i] = securable{Type: r.Class, Schema: r.Schema, Name: r.Name}
		}
		return out, nil
	}
}

// pageActionRunner runs a page's own round trip off the UI goroutine and hands
// the result back on it, latched against a second one in flight — the search and
// column fetch of a Securables page. PropDialog and newObjectDialog both are one,
// so Role Properties and New Database Role share the page.
type pageActionRunner interface {
	runPageActionOnce(inFlight *bool, fn func(ctx context.Context) error, onDone func(err error))
}

// securableApplyFn applies one object/schema/database-scoped state change, and
// columnApplyFn one column-scoped change — the securables-page counterparts of
// permApplyFn, which carries no securable.
type securableApplyFn func(ctx context.Context, verb gosmo.PermissionVerb, opts gosmo.PermissionOptions, s securable, permission string) error
type columnApplyFn func(ctx context.Context, verb gosmo.PermissionVerb, opts gosmo.PermissionOptions, s securable, permission, column string) error

// securableFindFn searches the server for addable securables whose name contains
// term, returning at most securableSearchLimit+1 — the extra row is how the page
// knows to say the list was truncated.
type securableFindFn func(ctx context.Context, term string) ([]securable, error)

// securableSearchLimit is how many candidates the Add picker lists. A dropdown
// is a keyboard list, not a catalogue browser, and past a few hundred entries
// scrolling to one is slower than typing its name — so the page says the list
// was truncated rather than silently showing a subset.
const securableSearchLimit = 200

// noSecurableCandidates is what the Add picker shows when the search returned
// nothing: an empty item list draws as a box the user opens to find nothing in,
// which reads as broken.
const noSecurableCandidates = "(no matches)"

// securableEntryKey addresses one explicit grant or deny the page was seeded
// with: a securable's key and a permission name.
type securableEntryKey struct{ secKey, permission string }

// securablesMatrix is one Securables page's state, shared by the three grids, the
// Add picker and the apply. The grids' row indices address the visible* slices,
// which the filters rebuild; the full lists stay in securables and the edit maps.
type securablesMatrix struct {
	d          pageActionRunner
	database   *gosmo.Database
	find       securableFindFn
	applyFn    securableApplyFn
	colApplyFn columnApplyFn

	existing   map[securableEntryKey]gosmo.PermissionState
	securables []securable

	// editsBySecurable is built lazily, one securable at a time, as the user
	// browses the grid: a securable never selected has nothing to apply.
	editsBySecurable map[string][]*securableEdit

	// colEdits holds every column-level edit made this session, keyed by
	// securable+permission+column and seeded from the server. A column never
	// looked at is absent.
	colEdits map[string]*columnEdit

	// visibleSecurables is the filtered view the top grid's row indices address;
	// securables stays the full list.
	securableFilter   string
	visibleSecurables []securable
	securableGrid     *controls.DataGrid

	permGrid     *controls.DataGrid
	permFilter   string
	visiblePerms []*securableEdit
	permSection  *propsheet.SectionRow

	colSection    *propsheet.SectionRow
	colPermSelect *propsheet.SelectRow
	colHint       *propsheet.HintRow
	colGrid       *controls.DataGrid
	// loadedCols is the column list the bottom grid is showing. Each entry
	// carries its own securable and permission, so a cycled cell writes back to
	// what the grid was loaded for rather than whatever is selected when the
	// click lands.
	loadedCols   []*columnEdit
	loadColsBusy bool

	selected int
	// selectedSec is the securable the middle grid is showing, held by value
	// rather than as an index into visibleSecurables, which the filter rebuilds
	// underneath it.
	selectedSec   securable
	selectedEdits []*securableEdit

	addSelect *propsheet.SelectRow
	hint      *propsheet.HintRow
	// available is what the Add picker lists, in its order.
	available []securable

	// searchTerm is what the search box holds, searchedFor what the picker was
	// last populated from; see runSearch.
	searchTerm, searchedFor string
	searchRunning           bool
}

// buildSecurablesMatrix builds a Securables page: a securable-list grid on top,
// the selected securable's full permission catalog x state in the middle, and
// for a table or view the per-column grants for one permission below — the
// inverse of buildPermissionsMatrix, which is "one securable, every principal"
// to this one's "one principal, every securable".
//
// initial is every securable the principal already has an explicit grant or deny
// on; candidates seeds the Add picker and find repopulates it from the server as
// the user types. applyFn and colApplyFn route to the right gosmo call for the
// selected securable's Type. database is only read, for a table's columns.
func buildSecurablesMatrix(
	d pageActionRunner, database *gosmo.Database,
	initial []securable, entries []*gosmo.PrincipalSecurable, colEntries []*gosmo.ColumnPermissionEntry,
	candidates []securable, find securableFindFn, securablesHeight, permsHeight int,
	applyFn securableApplyFn, colApplyFn columnApplyFn,
) (*propsheet.Form, propApply) {
	p := &securablesMatrix{
		d: d, database: database, find: find, applyFn: applyFn, colApplyFn: colApplyFn,
		existing:         make(map[securableEntryKey]gosmo.PermissionState, len(entries)),
		securables:       append([]securable(nil), initial...),
		editsBySecurable: make(map[string][]*securableEdit),
		colEdits:         make(map[string]*columnEdit),
		selected:         -1,
	}
	for _, e := range entries {
		k := securable{Type: e.SecurableType, Schema: e.Schema, Name: e.Name}
		p.existing[securableEntryKey{k.key(), e.Permission}] = e.State
	}
	for _, e := range colEntries {
		// e.ObjectType, not a hardcoded "TABLE": a view's column grants key under
		// VIEW, the Type the securable list gives them, and keying them as TABLE
		// makes columnEditKey miss so an existing grant shows as "(none)".
		sec := securable{Type: e.ObjectType, Schema: e.Schema, Name: e.Object}
		state := e.State
		p.colEdits[columnEditKey(sec, string(e.Permission), e.Column)] = &columnEdit{
			sec: sec, permission: string(e.Permission), column: e.Column,
			orig: state, current: state,
		}
	}

	p.securableGrid = controls.NewDataGrid()
	p.securableGrid.SetData(securableListColumns, p.securableRows())

	p.permGrid = controls.NewDataGrid()
	p.permGrid.SetCellCursor(true)

	// -- column-permissions editor ------------------------------------
	p.colSection = propsheet.Section("Column permissions")
	p.colPermSelect = propsheet.Select("Column permission", gosmo.ColumnPermissionNames(), 0)
	p.colHint = propsheet.Hint()
	p.colGrid = controls.NewDataGrid()
	p.colGrid.SetCellCursor(true)
	p.colGrid.SetData(securableColumnColumns, nil)
	propsheet.BindCellToggle(p.colGrid, securableColumnColumns, 1, func() int { return len(p.loadedCols) },
		func(row int) { p.loadedCols[row].current = nextPermState(p.loadedCols[row].current) }, p.colRows)

	p.permSection = propsheet.Section("Permissions")
	p.securableGrid.OnSelectRow = p.loadSecurable
	p.loadSecurable(0)

	propsheet.BindCellToggle(p.permGrid, permissionStateColumns, 1, func() int { return len(p.visiblePerms) },
		func(row int) { p.visiblePerms[row].current = nextPermState(p.visiblePerms[row].current) },
		func() [][]string { return p.permRowsFor(p.selectedEdits) })

	loadColsBtn := widgets.NewButton("Load Columns", p.loadColumns)

	p.addSelect = propsheet.Select("Add securable", []string{noSecurableCandidates}, 0)
	p.addSelect.SetDirtyTracked(false)
	p.hint = propsheet.Hint()
	p.setCandidates(candidates, "")

	addBtn := widgets.NewButton("Add", p.addSelected)

	searchRow := propsheet.Text("Search to add", "", 28)
	searchRow.SetDirtyTracked(false)
	searchRow.SetOnChange(func(term string) {
		p.searchTerm = term
		p.runSearch()
	})

	securableFilterRow := propsheet.Text("Filter securables", "", 28)
	securableFilterRow.SetDirtyTracked(false)
	securableFilterRow.SetOnChange(func(term string) {
		p.securableFilter = term
		p.selected = -1
		resetGrid(p.securableGrid, securableListColumns, p.securableRows(), 0)
		p.loadSecurable(0)
	})

	permFilterRow := propsheet.Text("Filter permissions", "", 28)
	permFilterRow.SetDirtyTracked(false)
	permFilterRow.SetOnChange(func(term string) {
		p.permFilter = term
		if p.selectedEdits != nil {
			resetGrid(p.permGrid, permissionStateColumns, p.permRowsFor(p.selectedEdits), 0)
		}
	})

	securablesRow := propsheet.NewGridRow(p.securableGrid, securablesHeight)
	permsRow := propsheet.NewGridRow(p.permGrid, permsHeight)
	colsRow := propsheet.NewGridRow(p.colGrid, 8)

	// Both grids report the page's whole dirty state: the sheet asks each row,
	// and a column edit made while the middle grid is clean would otherwise
	// leave Apply disabled.
	permsRow.DirtyFn, permsRow.RevertFn = p.anyDirty, p.revertAll
	colsRow.DirtyFn, colsRow.RevertFn = p.anyDirty, p.revertAll

	f := propsheet.NewForm(
		propsheet.Section("Securables"),
		securableFilterRow,
		securablesRow,
		searchRow,
		p.addSelect,
		propsheet.Buttons(addBtn),
		p.hint,
		p.permSection,
		permFilterRow,
		permsRow,
		p.colSection,
		p.colPermSelect,
		propsheet.Buttons(loadColsBtn),
		p.colHint,
		colsRow,
		propsheet.Note(permStateCycleNote+" Tab switches between the grids, and the filter boxes narrow them as you type. \"Search to add\" looks names up on the server — type part of the name of a schema, object, type or certificate, then pick it from the dropdown and click Add to give it its own row. For a table, view or table-valued function, Load Columns fetches its columns so single-column grants can be edited below; a column DENY overrides an object-level GRANT."),
	)
	return f, p.apply
}

// editsFor returns s's permission edits, building them from its catalog and the
// seeded grants the first time s is selected.
func (p *securablesMatrix) editsFor(s securable) []*securableEdit {
	if e, ok := p.editsBySecurable[s.key()]; ok {
		return e
	}
	catalog := s.catalog()
	edits := make([]*securableEdit, len(catalog))
	for i, perm := range catalog {
		state := p.existing[securableEntryKey{s.key(), perm}]
		edits[i] = &securableEdit{sec: s, permission: perm, orig: state, current: state}
	}
	p.editsBySecurable[s.key()] = edits
	return edits
}

// securableRows rebuilds visibleSecurables from the filter and returns the top
// grid's rows.
func (p *securablesMatrix) securableRows() [][]string {
	p.visibleSecurables = p.visibleSecurables[:0]
	for _, s := range p.securables {
		if matchesFilter(p.securableFilter, s.label(), string(s.Type)) {
			p.visibleSecurables = append(p.visibleSecurables, s)
		}
	}
	rows := make([][]string, len(p.visibleSecurables))
	for i, s := range p.visibleSecurables {
		rows[i] = []string{s.label(), string(s.Type)}
	}
	return rows
}

// permRowsFor rebuilds visiblePerms from edits and the filter and returns the
// middle grid's rows.
func (p *securablesMatrix) permRowsFor(edits []*securableEdit) [][]string {
	p.visiblePerms = p.visiblePerms[:0]
	for _, e := range edits {
		if matchesFilter(p.permFilter, e.permission) {
			p.visiblePerms = append(p.visiblePerms, e)
		}
	}
	rows := make([][]string, len(p.visiblePerms))
	for i, e := range p.visiblePerms {
		rows[i] = []string{e.permission, displayPermState(e.current)}
	}
	return rows
}

func (p *securablesMatrix) colRows() [][]string {
	rows := make([][]string, len(p.loadedCols))
	for i, c := range p.loadedCols {
		rows[i] = []string{c.column, displayPermState(c.current)}
	}
	return rows
}

// clearSelection empties both lower grids. A filter matching nothing must
// reach this: leaving them on the previously selected securable means
// cycling a State there edits, and Apply writes, permissions on a securable
// the page no longer lists.
func (p *securablesMatrix) clearSelection() {
	p.selected = -1
	p.selectedSec = securable{}
	p.selectedEdits = nil
	p.visiblePerms = p.visiblePerms[:0]
	resetGrid(p.permGrid, permissionStateColumns, nil, 0)
	p.loadedCols = nil
	resetGrid(p.colGrid, securableColumnColumns, nil, 0)
	p.permSection.SetTitle("Permissions")
	p.colSection.SetTitle("Column permissions")
	p.colHint.Set("No securable selected.")
}

// loadSecurable points the lower grids at the top grid's row.
func (p *securablesMatrix) loadSecurable(row int) {
	if row < 0 || row >= len(p.visibleSecurables) {
		p.clearSelection()
		return
	}
	if row == p.selected {
		return
	}
	p.selected = row
	p.selectedSec = p.visibleSecurables[row]
	p.selectedEdits = p.editsFor(p.selectedSec)
	resetGrid(p.permGrid, permissionStateColumns, p.permRowsFor(p.selectedEdits), 0)
	p.permSection.SetTitle("Permissions for " + p.selectedSec.label())
	if p.selectedSec.hasColumns() {
		// The column permissions offered are the securable's own: a
		// multi-statement table function refuses UPDATE (col). The
		// permission picked last stays picked when this one has it too.
		names := p.selectedSec.columnCatalog()
		picked := p.colPermSelect.Value()
		p.colPermSelect.SetItems(names)
		if i := slices.Index(names, picked); i >= 0 {
			p.colPermSelect.SetSelected(i)
		}
		p.colHint.Set("Click Load Columns to edit per-column grants on " + p.selectedSec.label() + ".")
	} else {
		p.colHint.Set(string(p.selectedSec.Type) + " securables have no columns.")
	}
}

// loadColumns is Load Columns: fetch the selected securable's columns and show
// the picked column permission's state on each.
func (p *securablesMatrix) loadColumns() {
	if p.selected < 0 || !p.selectedSec.hasColumns() {
		p.colHint.Set("Select a table, view or table-valued function above first — only those have columns.")
		return
	}
	sec := p.selectedSec
	perm := p.colPermSelect.Value()
	p.colHint.Set("Loading columns for " + sec.label() + "...")

	var cols []*gosmo.Column
	p.d.runPageActionOnce(&p.loadColsBusy, func(ctx context.Context) error {
		// ObjectColumns, not TableByName + Table.Columns: the latter reads
		// sys.tables and so fails on a view, which carries column
		// permissions exactly like a table.
		var err error
		cols, err = p.database.ObjectColumns(ctx, sec.Schema, sec.Name)
		return err
	}, func(err error) {
		if err != nil {
			p.colHint.Set("Error: " + err.Error())
			return
		}
		p.loadedCols = p.loadedCols[:0]
		for _, c := range cols {
			k := columnEditKey(sec, perm, c.Name)
			e, ok := p.colEdits[k]
			if !ok {
				// Absent means no explicit grant on that column: seed a
				// "(none)" edit so cycling it produces a real statement.
				e = &columnEdit{sec: sec, permission: perm, column: c.Name}
				p.colEdits[k] = e
			}
			p.loadedCols = append(p.loadedCols, e)
		}
		p.colSection.SetTitle(fmt.Sprintf("Column permissions — %s %s", perm, sec.label()))
		resetGrid(p.colGrid, securableColumnColumns, p.colRows(), 0)
		p.colHint.Set(fmt.Sprintf("%d columns. Cycling a State here grants on that column only.", len(p.loadedCols)))
	})
}

// setCandidates repopulates the Add picker from a search for term.
//
// The database itself is a securable but not an object, so no name search
// returns it. It is offered whenever the typed term matches its label, as
// the top grid's own filter does.
func (p *securablesMatrix) setCandidates(found []securable, term string) {
	p.available = p.available[:0]
	if dbSec := (securable{Type: gosmo.SecurableDatabase}); matchesFilter(term, dbSec.label(), string(dbSec.Type)) {
		p.available = append(p.available, dbSec)
	}
	truncated := len(found) > securableSearchLimit
	if truncated {
		found = found[:securableSearchLimit]
	}
	p.available = append(p.available, found...)

	labels := make([]string, len(p.available))
	for i, s := range p.available {
		labels[i] = s.label()
	}
	if len(labels) == 0 {
		labels = []string{noSecurableCandidates}
	}
	p.addSelect.SetItems(labels)
	switch {
	case truncated:
		p.hint.Set(fmt.Sprintf("More than %d matches — type more to narrow the list.", securableSearchLimit))
	case len(p.available) == 0:
		p.hint.Set("Nothing on the server matches that name.")
	default:
		p.hint.Clear()
	}
}

// addSelected is Add: list the picked securable and select its row.
func (p *securablesMatrix) addSelected() {
	if len(p.available) == 0 {
		p.hint.Set("Nothing to add — search for a securable by name first.")
		return
	}
	s := p.available[p.addSelect.Selected()]
	if slices.ContainsFunc(p.securables, func(e securable) bool { return e.key() == s.key() }) {
		// Already present — say so and move to its row rather than leaving
		// the button looking broken.
		p.hint.Set(s.label() + " is already listed — edit its permissions below.")
	} else {
		p.hint.Clear()
		p.securables = append(p.securables, s)
	}
	rows := p.securableRows()
	row := slices.IndexFunc(p.visibleSecurables, func(e securable) bool { return e.key() == s.key() })
	if row < 0 {
		// Present, but the filter box is hiding it — selecting row 0 would
		// point the grids at a different securable.
		redrawGrid(p.securableGrid, securableListColumns, rows)
		p.hint.Set(s.label() + " is listed, but the filter above is hiding it.")
		return
	}
	p.selected = -1
	resetGrid(p.securableGrid, securableListColumns, rows, row)
	p.loadSecurable(row)
}

// runSearch re-queries the server as the user types, coalescing rather than
// queueing: at most one search is in flight, and if the term moved on while it
// was out the completion starts the next. A query per keystroke would put five
// round trips behind "Order" and let an early one land last, repopulating the
// picker from a term already backspaced away.
//
// runPageActionOnce owns the latch: a panicking search releases it, where a
// hand-cleared flag stayed set and killed search for the rest of the showing.
func (p *securablesMatrix) runSearch() {
	if p.searchRunning || p.searchTerm == p.searchedFor {
		return
	}
	want := p.searchTerm
	var found []securable
	p.d.runPageActionOnce(&p.searchRunning, func(ctx context.Context) error {
		var err error
		found, err = p.find(ctx, want)
		return err
	}, func(err error) {
		p.searchedFor = want
		if err != nil {
			p.hint.SetError("Search failed: " + err.Error())
		} else {
			p.setCandidates(found, want)
		}
		p.runSearch()
	})
}

func (p *securablesMatrix) anyDirty() bool {
	for _, edits := range p.editsBySecurable {
		for _, e := range edits {
			if e.current != e.orig {
				return true
			}
		}
	}
	for _, e := range p.colEdits {
		if e.current != e.orig {
			return true
		}
	}
	return false
}

func (p *securablesMatrix) revertAll() {
	for _, edits := range p.editsBySecurable {
		for _, e := range edits {
			e.current = e.orig
		}
	}
	for _, e := range p.colEdits {
		e.current = e.orig
	}
	if p.selectedEdits != nil {
		resetGrid(p.permGrid, permissionStateColumns, p.permRowsFor(p.selectedEdits), 0)
	}
	resetGrid(p.colGrid, securableColumnColumns, p.colRows(), 0)
}

// apply writes every changed state. Both loops walk a stable order rather than
// ranging their map: map order changes every run, so which statements landed
// before a mid-apply failure isn't reproducible and Script Changes reorders its
// output between presses. editsFor is only called with a securable from the
// securables slice.
func (p *securablesMatrix) apply(ctx context.Context) error {
	for _, s := range p.securables {
		for _, e := range p.editsBySecurable[s.key()] {
			if e.current == e.orig {
				continue
			}
			verb, opts := permTransition(e.orig, e.current)
			if err := p.applyFn(ctx, verb, opts, e.sec, e.permission); err != nil {
				return err
			}
		}
	}
	for _, k := range slices.Sorted(maps.Keys(p.colEdits)) {
		e := p.colEdits[k]
		if e.current == e.orig {
			continue
		}
		verb, opts := permTransition(e.orig, e.current)
		if err := p.colApplyFn(ctx, verb, opts, e.sec, e.permission, e.column); err != nil {
			return err
		}
	}
	return nil
}

// applySecurable issues one object/schema/database-scoped state change.
func applySecurable(ctx context.Context, d *gosmo.Database, verb gosmo.PermissionVerb, opts gosmo.PermissionOptions, s securable, permission, principal string) error {
	return securablePermApply(d, s.gosmo(nil))(ctx, verb, opts, permission, principal)
}

// applyColumnSecurable issues one column-level state change. Only tables and
// views reach it — securable.hasColumns gates the editor that produces these
// edits.
func applyColumnSecurable(ctx context.Context, d *gosmo.Database, verb gosmo.PermissionVerb, opts gosmo.PermissionOptions, s securable, permission, column, principal string) error {
	return securablePermApply(d, s.gosmo([]string{column}))(ctx, verb, opts, permission, principal)
}
