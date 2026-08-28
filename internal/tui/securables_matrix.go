package tui

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// securableListColumns heads the list of securables a principal can be
// granted on.
var securableListColumns = []string{"Securable", "Type"}

// securableColumnColumns heads the per-column permission editor.
var securableColumnColumns = []string{"Column", "State"}

// securable identifies one thing a database role's Securables page can grant or
// deny permissions on: a table, a view, a schema, or the database itself.
type securable struct {
	Type   string // "TABLE", "VIEW", "SCHEMA", "DATABASE"
	Schema string // empty for DATABASE
	Name   string // empty for DATABASE
}

func (s securable) key() string { return s.Type + "\x00" + s.Schema + "\x00" + s.Name }

func (s securable) label() string {
	switch s.Type {
	case "DATABASE":
		return "(database)"
	case "SCHEMA":
		return "[" + s.Name + "]"
	default:
		return "[" + s.Schema + "].[" + s.Name + "]"
	}
}

// hasColumns reports whether this securable's columns can carry permissions of
// their own — only tables and views can.
func (s securable) hasColumns() bool { return s.Type == "TABLE" || s.Type == "VIEW" }

// catalog returns every permission name grantable on this securable's type.
func (s securable) catalog() []string {
	switch s.Type {
	case "SCHEMA":
		return gosmo.SchemaPermissionNames()
	case "DATABASE":
		return gosmo.DatabasePermissionNames()
	default:
		return gosmo.ObjectPermissionNames()
	}
}

// securableEdit tracks one (securable, permission) cell's pending state,
// same cycle as permEdit.
type securableEdit struct {
	sec        securable
	permission string
	orig       string
	current    string
}

// columnEdit tracks one (securable, permission, column) cell's pending state —
// the column-level grants behind SSMS's "Column Permissions..." button. Keyed on
// all three because the editor keeps edits made against one securable/permission
// pair while the user browses to another, and Apply writes them all.
type columnEdit struct {
	sec        securable
	permission string
	column     string
	orig       string
	current    string
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
// d is needed only for the column-permissions editor, which fetches a table's
// columns on demand through d.runPageAction.
func pageDatabasePrincipalSecurables(d *PropDialog, sc *db.ServerConn, dbName string, principal *string) propPage {
	return propPage{
		title: "Securables",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			database, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			entries, err := database.PermissionsForPrincipalContext(ctx, *principal)
			if err != nil {
				return nil, nil, err
			}
			colEntries, err := database.ColumnPermissionsForPrincipalContext(ctx, *principal)
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
			find := func(ctx context.Context, term string) ([]securable, error) {
				refs, err := database.FindSecurablesContext(ctx,
					gosmo.SecurableSearch{Name: term, Limit: securableSearchLimit + 1})
				if err != nil {
					return nil, err
				}
				out := make([]securable, len(refs))
				for i, r := range refs {
					out[i] = securable{Type: r.Type, Schema: r.Schema, Name: r.Name}
				}
				return out, nil
			}
			candidates, err := find(ctx, "")
			if err != nil {
				return nil, nil, err
			}

			f, apply := buildSecurablesMatrix(d, database, initial, entries, colEntries, candidates, find, 8, 12,
				func(ctx context.Context, verb string, opts gosmo.PermissionOptions, s securable, permission string) error {
					return applySecurable(ctx, database, verb, opts, s, permission, *principal)
				},
				func(ctx context.Context, verb string, opts gosmo.PermissionOptions, s securable, permission, column string) error {
					return applyColumnSecurable(ctx, database, verb, opts, s, permission, column, *principal)
				},
			)
			return f, apply, nil
		},
	}
}

// securableApplyFn applies one object/schema/database-scoped state change, and
// columnApplyFn one column-scoped change — the securables-page counterparts of
// permApplyFn, which carries no securable.
type securableApplyFn func(ctx context.Context, verb string, opts gosmo.PermissionOptions, s securable, permission string) error
type columnApplyFn func(ctx context.Context, verb string, opts gosmo.PermissionOptions, s securable, permission, column string) error

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

// buildSecurablesMatrix builds a Securables page: a securable-list grid on top,
// the selected securable's full permission catalog x state in the middle, and
// for a table or view the per-column grants for one permission below — the
// inverse of buildPermissionsMatrix, which is "one securable, every principal"
// to this one's "one principal, every securable".
//
// initial is every securable the principal already has an explicit grant or deny
// on; candidates seeds the Add picker and find repopulates it from the server as
// the user types. applyFn and colApplyFn route to the right gosmo call for the
// selected securable's Type.
func buildSecurablesMatrix(
	d *PropDialog, database *gosmo.Database,
	initial []securable, entries []*gosmo.PrincipalSecurable, colEntries []*gosmo.ColumnPermissionEntry,
	candidates []securable, find securableFindFn, securablesHeight, permsHeight int,
	applyFn securableApplyFn, colApplyFn columnApplyFn,
) (*propsheet.Form, propApply) {
	type entryKey struct{ secKey, permission string }
	existing := make(map[entryKey]string, len(entries))
	for _, e := range entries {
		k := securable{Type: e.SecurableType, Schema: e.Schema, Name: e.Name}
		existing[entryKey{k.key(), e.Permission}] = e.State
	}

	securables := append([]securable(nil), initial...)

	// editsBySecurable is built lazily, one securable at a time, as the user
	// browses the grid: a securable never selected has nothing to apply.
	editsBySecurable := make(map[string][]*securableEdit)
	editsFor := func(s securable) []*securableEdit {
		if e, ok := editsBySecurable[s.key()]; ok {
			return e
		}
		catalog := s.catalog()
		edits := make([]*securableEdit, len(catalog))
		for i, perm := range catalog {
			state := existing[entryKey{s.key(), perm}]
			edits[i] = &securableEdit{sec: s, permission: perm, orig: state, current: state}
		}
		editsBySecurable[s.key()] = edits
		return edits
	}

	// colEdits holds every column-level edit made this session, keyed by
	// securable+permission+column and seeded from the server. A column never
	// looked at is absent.
	colEdits := make(map[string]*columnEdit)
	for _, e := range colEntries {
		// e.ObjectType, not a hardcoded "TABLE": a view's column grants key under
		// VIEW, the Type the securable list gives them, and keying them as TABLE
		// makes columnEditKey miss so an existing grant shows as "(none)".
		sec := securable{Type: e.ObjectType, Schema: e.Schema, Name: e.Object}
		state := string(e.State)
		colEdits[columnEditKey(sec, string(e.Permission), e.Column)] = &columnEdit{
			sec: sec, permission: string(e.Permission), column: e.Column,
			orig: state, current: state,
		}
	}

	// visibleSecurables is the filtered view the top grid's row indices address;
	// securables stays the full list.
	securableFilter := ""
	var visibleSecurables []securable
	securableRows := func() [][]string {
		visibleSecurables = visibleSecurables[:0]
		for _, s := range securables {
			if matchesFilter(securableFilter, s.label(), s.Type) {
				visibleSecurables = append(visibleSecurables, s)
			}
		}
		rows := make([][]string, len(visibleSecurables))
		for i, s := range visibleSecurables {
			rows[i] = []string{s.label(), s.Type}
		}
		return rows
	}
	securableGrid := controls.NewDataGrid()
	securableGrid.SetData(securableListColumns, securableRows())

	permGrid := controls.NewDataGrid()
	permGrid.SetCellCursor(true)
	permFilter := ""
	var visiblePerms []*securableEdit
	permRowsFor := func(edits []*securableEdit) [][]string {
		visiblePerms = visiblePerms[:0]
		for _, e := range edits {
			if matchesFilter(permFilter, e.permission) {
				visiblePerms = append(visiblePerms, e)
			}
		}
		rows := make([][]string, len(visiblePerms))
		for i, e := range visiblePerms {
			rows[i] = []string{e.permission, displayPermState(e.current)}
		}
		return rows
	}

	// -- column-permissions editor ------------------------------------
	colSection := propsheet.Section("Column permissions")
	colPermSelect := propsheet.Select("Column permission", gosmo.ColumnPermissionNames(), 0)
	colHint := propsheet.Hint()
	colGrid := controls.NewDataGrid()
	colGrid.SetCellCursor(true)
	colGrid.SetData(securableColumnColumns, nil)
	// loadedCols is the column list the bottom grid is showing. Each entry
	// carries its own securable and permission, so a cycled cell writes back to
	// what the grid was loaded for rather than whatever is selected when the
	// click lands.
	var loadedCols []*columnEdit

	colRows := func() [][]string {
		rows := make([][]string, len(loadedCols))
		for i, c := range loadedCols {
			rows[i] = []string{c.column, displayPermState(c.current)}
		}
		return rows
	}
	colGrid.OnActivateCell = func(row, col int) {
		if col != 1 || row < 0 || row >= len(loadedCols) {
			return
		}
		loadedCols[row].current = nextPermState(loadedCols[row].current)
		redrawGrid(colGrid, securableColumnColumns, colRows())
	}

	permSection := propsheet.Section("Permissions")
	selected := -1
	// selectedSec is the securable the middle grid is showing, held by value
	// rather than as an index into visibleSecurables, which the filter rebuilds
	// underneath it.
	var selectedSec securable
	var selectedEdits []*securableEdit
	// clearSelection empties both lower grids. A filter matching nothing must
	// reach this: leaving them on the previously selected securable means
	// cycling a State there edits, and Apply writes, permissions on a securable
	// the page no longer lists.
	clearSelection := func() {
		selected = -1
		selectedSec = securable{}
		selectedEdits = nil
		visiblePerms = visiblePerms[:0]
		permGrid.SetData(permissionStateColumns, nil)
		loadedCols = nil
		colGrid.SetData(securableColumnColumns, nil)
		permSection.SetTitle("Permissions")
		colSection.SetTitle("Column permissions")
		colHint.Set("No securable selected.")
	}
	loadSecurable := func(row int) {
		if row < 0 || row >= len(visibleSecurables) {
			clearSelection()
			return
		}
		if row == selected {
			return
		}
		selected = row
		selectedSec = visibleSecurables[row]
		selectedEdits = editsFor(selectedSec)
		resetGrid(permGrid, permissionStateColumns, permRowsFor(selectedEdits), 0)
		permSection.SetTitle("Permissions for " + selectedSec.label())
		if selectedSec.hasColumns() {
			colHint.Set("Click Load Columns to edit per-column grants on " + selectedSec.label() + ".")
		} else {
			colHint.Set(selectedSec.Type + " securables have no columns.")
		}
	}
	securableGrid.OnSelectRow = loadSecurable
	loadSecurable(0)

	permGrid.OnActivateCell = func(row, col int) {
		if col != 1 || row < 0 || row >= len(visiblePerms) {
			return
		}
		visiblePerms[row].current = nextPermState(visiblePerms[row].current)
		redrawGrid(permGrid, permissionStateColumns, permRowsFor(selectedEdits))
	}

	var loadColsBusy bool
	loadColsBtn := widgets.NewButton("Load Columns", func() {
		if selected < 0 || !selectedSec.hasColumns() {
			colHint.Set("Select a table or view above first — only those have columns.")
			return
		}
		sec := selectedSec
		perm := gosmo.ColumnPermissionNames()[colPermSelect.Selected()]
		colHint.Set("Loading columns for " + sec.label() + "...")

		var cols []*gosmo.Column
		d.runPageActionOnce(&loadColsBusy, func(ctx context.Context) error {
			// ObjectColumns, not TableByName + Table.Columns: the latter reads
			// sys.tables and so fails on a view, which carries column
			// permissions exactly like a table.
			var err error
			cols, err = database.ObjectColumnsContext(ctx, sec.Schema, sec.Name)
			return err
		}, func(err error) {
			if err != nil {
				colHint.Set("Error: " + err.Error())
				return
			}
			loadedCols = loadedCols[:0]
			for _, c := range cols {
				k := columnEditKey(sec, perm, c.Name)
				e, ok := colEdits[k]
				if !ok {
					// Absent means no explicit grant on that column: seed a
					// "(none)" edit so cycling it produces a real statement.
					e = &columnEdit{sec: sec, permission: perm, column: c.Name}
					colEdits[k] = e
				}
				loadedCols = append(loadedCols, e)
			}
			colSection.SetTitle(fmt.Sprintf("Column permissions — %s %s", perm, sec.label()))
			resetGrid(colGrid, securableColumnColumns, colRows(), 0)
			colHint.Set(fmt.Sprintf("%d columns. Cycling a State here grants on that column only.", len(loadedCols)))
		})
	})

	addSelect := propsheet.Select("Add securable", []string{noSecurableCandidates}, 0)
	addSelect.SetDirtyTracked(false)
	hint := propsheet.Hint()

	// The database itself is a securable but not an object, so no name search
	// returns it. It is offered whenever the typed term matches its label, as
	// the top grid's own filter does.
	var available []securable
	setCandidates := func(found []securable, term string) {
		available = available[:0]
		if dbSec := (securable{Type: "DATABASE"}); matchesFilter(term, dbSec.label(), dbSec.Type) {
			available = append(available, dbSec)
		}
		truncated := len(found) > securableSearchLimit
		if truncated {
			found = found[:securableSearchLimit]
		}
		available = append(available, found...)

		labels := make([]string, len(available))
		for i, s := range available {
			labels[i] = s.label()
		}
		if len(labels) == 0 {
			labels = []string{noSecurableCandidates}
		}
		addSelect.SetItems(labels)
		switch {
		case truncated:
			hint.Set(fmt.Sprintf("More than %d matches — type more to narrow the list.", securableSearchLimit))
		case len(available) == 0:
			hint.Set("Nothing on the server matches that name.")
		default:
			hint.Clear()
		}
	}
	setCandidates(candidates, "")

	addBtn := widgets.NewButton("Add", func() {
		if len(available) == 0 {
			hint.Set("Nothing to add — search for a securable by name first.")
			return
		}
		s := available[addSelect.Selected()]
		if slices.ContainsFunc(securables, func(e securable) bool { return e.key() == s.key() }) {
			// Already present — say so and move to its row rather than leaving
			// the button looking broken.
			hint.Set(s.label() + " is already listed — edit its permissions below.")
		} else {
			hint.Clear()
			securables = append(securables, s)
		}
		redrawGrid(securableGrid, securableListColumns, securableRows())
		row := slices.IndexFunc(visibleSecurables, func(e securable) bool { return e.key() == s.key() })
		if row < 0 {
			// Present, but the filter box is hiding it — selecting row 0 would
			// point the grids at a different securable.
			hint.Set(s.label() + " is listed, but the filter above is hiding it.")
			return
		}
		selected = -1
		securableGrid.SetSelectedRow(row)
		loadSecurable(row)
	})

	// The search box re-queries the server as the user types, coalescing rather
	// than queueing: at most one search is in flight, and if the term moved on
	// while it was out the completion starts the next. A query per keystroke
	// would put five round trips behind "Order" and let an early one land last,
	// repopulating the picker from a term already backspaced away.
	searchTerm, searchedFor := "", ""
	searchRunning := false
	var runSearch func()
	runSearch = func() {
		if searchRunning || searchTerm == searchedFor {
			return
		}
		searchRunning = true
		want := searchTerm
		var found []securable
		d.runPageAction(func(ctx context.Context) error {
			var err error
			found, err = find(ctx, want)
			return err
		}, func(err error) {
			searchRunning = false
			searchedFor = want
			if err != nil {
				hint.SetError("Search failed: " + err.Error())
			} else {
				setCandidates(found, want)
			}
			runSearch()
		})
	}
	searchRow := propsheet.Text("Search to add", "", 28)
	searchRow.SetDirtyTracked(false)
	searchRow.SetOnChange(func(term string) {
		searchTerm = term
		runSearch()
	})

	securableFilterRow := propsheet.Text("Filter securables", "", 28)
	securableFilterRow.SetDirtyTracked(false)
	securableFilterRow.SetOnChange(func(term string) {
		securableFilter = term
		redrawGrid(securableGrid, securableListColumns, securableRows())
		selected = -1
		securableGrid.SetSelectedRow(0)
		loadSecurable(0)
	})

	permFilterRow := propsheet.Text("Filter permissions", "", 28)
	permFilterRow.SetDirtyTracked(false)
	permFilterRow.SetOnChange(func(term string) {
		permFilter = term
		if selectedEdits != nil {
			resetGrid(permGrid, permissionStateColumns, permRowsFor(selectedEdits), 0)
		}
	})

	securablesRow := propsheet.NewGridRow(securableGrid, securablesHeight)
	permsRow := propsheet.NewGridRow(permGrid, permsHeight)
	colsRow := propsheet.NewGridRow(colGrid, 8)

	anyDirty := func() bool {
		for _, edits := range editsBySecurable {
			for _, e := range edits {
				if e.current != e.orig {
					return true
				}
			}
		}
		for _, e := range colEdits {
			if e.current != e.orig {
				return true
			}
		}
		return false
	}
	revertAll := func() {
		for _, edits := range editsBySecurable {
			for _, e := range edits {
				e.current = e.orig
			}
		}
		for _, e := range colEdits {
			e.current = e.orig
		}
		if selectedEdits != nil {
			resetGrid(permGrid, permissionStateColumns, permRowsFor(selectedEdits), 0)
		}
		resetGrid(colGrid, securableColumnColumns, colRows(), 0)
	}
	// Both grids report the page's whole dirty state: the sheet asks each row,
	// and a column edit made while the middle grid is clean would otherwise
	// leave Apply disabled.
	permsRow.DirtyFn, permsRow.RevertFn = anyDirty, revertAll
	colsRow.DirtyFn, colsRow.RevertFn = anyDirty, revertAll

	f := propsheet.NewForm(
		propsheet.Section("Securables"),
		securableFilterRow,
		securablesRow,
		searchRow,
		addSelect,
		propsheet.Buttons(addBtn),
		hint,
		permSection,
		permFilterRow,
		permsRow,
		colSection,
		colPermSelect,
		propsheet.Buttons(loadColsBtn),
		colHint,
		colsRow,
		propsheet.Note(permStateCycleNote+" Tab switches between the grids, and the filter boxes narrow them as you type. \"Search to add\" looks names up on the server — type part of a table, view or schema name, then pick it from the dropdown and click Add to give it its own row. For a table or view, Load Columns fetches its columns so single-column grants can be edited below; a column DENY overrides an object-level GRANT."),
	)

	// Both loops walk a stable order rather than ranging their map: map order
	// changes every run, so which statements landed before a mid-apply failure
	// isn't reproducible and Script Changes reorders its output between presses.
	// editsFor is only called with a securable from the securables slice.
	apply := func(ctx context.Context) error {
		for _, s := range securables {
			for _, e := range editsBySecurable[s.key()] {
				if e.current == e.orig {
					continue
				}
				verb, opts := permTransition(e.orig, e.current)
				if err := applyFn(ctx, verb, opts, e.sec, e.permission); err != nil {
					return err
				}
				commitApplied(ctx, &e.orig, e.current)
			}
		}
		for _, k := range slices.Sorted(maps.Keys(colEdits)) {
			e := colEdits[k]
			if e.current == e.orig {
				continue
			}
			verb, opts := permTransition(e.orig, e.current)
			if err := colApplyFn(ctx, verb, opts, e.sec, e.permission, e.column); err != nil {
				return err
			}
			commitApplied(ctx, &e.orig, e.current)
		}
		return nil
	}
	return f, apply
}

// applySecurable routes a buildSecurablesMatrix state change to the right gosmo
// method by s.Type: tables and views use the object-level trio, schemas the
// schema-level trio, the database itself the database-scoped trio.
func applySecurable(ctx context.Context, d *gosmo.Database, verb string, opts gosmo.PermissionOptions, s securable, permission, principal string) error {
	switch s.Type {
	case "SCHEMA":
		return schemaPermApply(d, s.Name)(ctx, verb, opts, permission, principal)
	case "DATABASE":
		return databasePermApply(d)(ctx, verb, opts, permission, principal)
	default:
		return objectPermApply(d, s.Schema, s.Name)(ctx, verb, opts, permission, principal)
	}
}

// applyColumnSecurable routes a column-level state change. Only tables and views
// reach it — securable.hasColumns gates the editor that produces these edits.
func applyColumnSecurable(ctx context.Context, d *gosmo.Database, verb string, opts gosmo.PermissionOptions, s securable, permission, column, principal string) error {
	p := gosmo.ObjectPermission(permission)
	cols := []string{column}
	switch verb {
	case "GRANT":
		return d.GrantColumnPermissionWithOptionsContext(ctx, s.Schema, s.Name, p, cols, principal, opts)
	case "DENY":
		return d.DenyColumnPermissionWithOptionsContext(ctx, s.Schema, s.Name, p, cols, principal, opts)
	default:
		return d.RevokeColumnPermissionWithOptionsContext(ctx, s.Schema, s.Name, p, cols, principal, opts)
	}
}
