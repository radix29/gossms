package tui

import (
	"context"
	"fmt"
	"strconv"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// permEntry is the common shape of gosmo.ServerPermissionEntry and
// gosmo.DatabasePermissionEntry (Principal/PrincipalType/Grantor/
// Permission/State, all strings) — normalized at the call site so the
// permissions-grid editor, identical for both scopes, is built once.
type permEntry struct {
	Principal     string
	PrincipalType string
	Grantor       string
	Permission    string
	State         string
}

// permEdit tracks one grid row's pending state through the cycle
// GRANT -> DENY -> "" (revoke) -> GRANT, driven by Space/Enter/click on
// the State column.
type permEdit struct {
	entry   permEntry
	orig    string
	current string
}

// permPrincipal is one row in the Permissions page's principal list —
// every login/user or role that could be granted a permission at this
// scope, not just the ones with an existing GRANT/DENY entry.
type permPrincipal struct {
	Name string
	Type string
}

func nextPermState(s string) string {
	switch s {
	case "GRANT", "GRANT_WITH_GRANT_OPTION":
		return "DENY"
	case "DENY":
		return ""
	default:
		return "GRANT"
	}
}

func displayPermState(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// buildPermissionsMatrix builds the Permissions page's two-pane editor: a
// principal list (top grid) and, for whichever principal is selected, the
// full catalog of grantable permissions at this scope (bottom grid) — not
// just the ones with an existing GRANT/DENY entry, so a principal with no
// prior ACL rows still shows every permission it could be granted. Tab
// switches focus between the two grids; the bottom grid's State column
// cycles Grant -> Deny -> (none) on activation, same as before. Wired to
// grant/deny/revokeFn for whichever scope (server- or database-level
// GRANT/DENY/REVOKE) the caller needs.
func buildPermissionsMatrix(
	principals []permPrincipal, catalog []string, entries []permEntry,
	principalsHeight, permsHeight int,
	grantFn, denyFn, revokeFn func(ctx context.Context, permission, principal string) error,
) (*propsheet.Form, propApply) {
	type entryKey struct{ principal, permission string }
	existing := make(map[entryKey]permEntry, len(entries))
	for _, e := range entries {
		existing[entryKey{e.Principal, e.Permission}] = e
	}

	// editsByPrincipal is built lazily, one principal at a time, as the
	// user browses the principal grid — a principal never selected has by
	// definition nothing to apply or check for dirtiness.
	editsByPrincipal := make(map[string][]*permEdit)
	editsFor := func(p permPrincipal) []*permEdit {
		if e, ok := editsByPrincipal[p.Name]; ok {
			return e
		}
		edits := make([]*permEdit, len(catalog))
		for i, perm := range catalog {
			ent, ok := existing[entryKey{p.Name, perm}]
			if !ok {
				ent = permEntry{Principal: p.Name, PrincipalType: p.Type, Permission: perm}
			}
			edits[i] = &permEdit{entry: ent, orig: ent.State, current: ent.State}
		}
		editsByPrincipal[p.Name] = edits
		return edits
	}

	principalRows := make([][]string, len(principals))
	for i, p := range principals {
		principalRows[i] = []string{p.Name, p.Type}
	}
	principalGrid := controls.NewDataGrid()
	principalGrid.SetData([]string{"Name", "Type"}, principalRows)

	permGrid := controls.NewDataGrid()
	permGrid.SetCellCursor(true)
	permRowsFor := func(edits []*permEdit) [][]string {
		rows := make([][]string, len(edits))
		for i, e := range edits {
			rows[i] = []string{e.entry.Permission, displayPermState(e.current)}
		}
		return rows
	}

	permSection := propsheet.Section("Explicit permissions")
	selected := -1
	loadPrincipal := func(row int) {
		if row < 0 || row >= len(principals) || row == selected {
			return
		}
		selected = row
		permGrid.SetData([]string{"Permission", "State"}, permRowsFor(editsFor(principals[row])))
		permGrid.SetSelectedRow(0)
		permSection.SetTitle("Explicit permissions for " + principals[row].Name)
	}
	principalGrid.OnSelectRow = loadPrincipal
	if len(principals) > 0 {
		loadPrincipal(0)
	}

	permGrid.OnActivateCell = func(row, col int) {
		if selected < 0 || col != 1 {
			return
		}
		edits := editsByPrincipal[principals[selected].Name]
		if row < 0 || row >= len(edits) {
			return
		}
		edits[row].current = nextPermState(edits[row].current)
		permGrid.SetData([]string{"Permission", "State"}, permRowsFor(edits))
		permGrid.SetSelectedRow(row)
	}

	principalsRow := propsheet.NewGridRow(principalGrid, principalsHeight)
	permsRow := propsheet.NewGridRow(permGrid, permsHeight)
	permsRow.DirtyFn = func() bool {
		for _, edits := range editsByPrincipal {
			for _, e := range edits {
				if e.current != e.orig {
					return true
				}
			}
		}
		return false
	}
	permsRow.RevertFn = func() {
		for _, edits := range editsByPrincipal {
			for _, e := range edits {
				e.current = e.orig
			}
		}
		if selected >= 0 {
			permGrid.SetData([]string{"Permission", "State"}, permRowsFor(editsByPrincipal[principals[selected].Name]))
		}
	}

	f := propsheet.NewForm(
		propsheet.Section("Principals"),
		principalsRow,
		permSection,
		permsRow,
		propsheet.Note("Space/Enter (or click) on State cycles Grant → Deny → (none). Tab switches between the principal list and its permissions."),
	)

	apply := func(ctx context.Context) error {
		for _, edits := range editsByPrincipal {
			for _, e := range edits {
				if e.current == e.orig {
					continue
				}
				var err error
				switch e.current {
				case "GRANT":
					err = grantFn(ctx, e.entry.Permission, e.entry.Principal)
				case "DENY":
					err = denyFn(ctx, e.entry.Permission, e.entry.Principal)
				case "":
					err = revokeFn(ctx, e.entry.Permission, e.entry.Principal)
				}
				if err != nil {
					return err
				}
			}
		}
		return nil
	}
	return f, apply
}

// pagePrincipalServerPermissions builds a "Securables" page listing every
// server-scoped permission (gosmo.ServerPermissionNames) with a
// Grant/Deny/(none) state cyclable per row, scoped to one principal — a
// login or server role. Shared by Login Properties and Server Role
// Properties, both server-level principals that can hold explicit
// server-scoped GRANT/DENY entries the same way. Unlike
// buildPermissionsMatrix (Server Properties' own Permissions page,
// server_props.go), which browses every principal at once, this only ever
// shows principalName's own entries — no principal picker.
func pagePrincipalServerPermissions(sc *db.ServerConn, principalName string) propPage {
	return propPage{
		title: "Securables",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			perms, err := sc.Server.ServerPermissionsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			states := make(map[string]string, len(perms))
			for _, p := range perms {
				if p.Principal == principalName {
					states[p.Permission] = p.State
				}
			}

			catalog := gosmo.ServerPermissionNames()
			edits := make([]*permEdit, len(catalog))
			for i, perm := range catalog {
				state := states[perm]
				entry := permEntry{Principal: principalName, Permission: perm, State: state}
				edits[i] = &permEdit{entry: entry, orig: state, current: state}
			}

			rowsFor := func() [][]string {
				rows := make([][]string, len(edits))
				for i, e := range edits {
					rows[i] = []string{e.entry.Permission, displayPermState(e.current)}
				}
				return rows
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Permission", "State"}, rowsFor())
			grid.SetCellCursor(true)
			grid.OnActivateCell = func(row, col int) {
				if col != 1 || row < 0 || row >= len(edits) {
					return
				}
				edits[row].current = nextPermState(edits[row].current)
				grid.SetData([]string{"Permission", "State"}, rowsFor())
				grid.SetSelectedRow(row)
			}

			gridRow := propsheet.NewGridRow(grid, 12)
			gridRow.DirtyFn = func() bool {
				for _, e := range edits {
					if e.current != e.orig {
						return true
					}
				}
				return false
			}
			gridRow.RevertFn = func() {
				for _, e := range edits {
					e.current = e.orig
				}
				grid.SetData([]string{"Permission", "State"}, rowsFor())
			}

			f := propsheet.NewForm(
				propsheet.Section("Explicit server-level permissions"),
				gridRow,
				propsheet.Note("Space/Enter (or click) on State cycles Grant → Deny → (none). Database and endpoint securables aren't modeled here yet."),
			)

			apply := func(ctx context.Context) error {
				for _, e := range edits {
					if e.current == e.orig {
						continue
					}
					var err error
					switch e.current {
					case "GRANT":
						err = sc.Server.GrantServerPermissionContext(ctx, e.entry.Permission, principalName)
					case "DENY":
						err = sc.Server.DenyServerPermissionContext(ctx, e.entry.Permission, principalName)
					case "":
						err = sc.Server.RevokeServerPermissionContext(ctx, e.entry.Permission, principalName)
					}
					if err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

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
func indexOf(items []string, value string) int {
	for i, it := range items {
		if it == value {
			return i
		}
	}
	return 0
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

// extPropEdit tracks one extended property's pending state: an existing
// property whose value changed, or a brand-new one pending Add.
type extPropEdit struct {
	name          string
	origValue     string
	value         string
	isNew         bool
	pendingRemove bool
}

// buildExtendedPropertiesForm builds the Extended Properties page every
// object-Properties dialog shares: a Name/Value grid, a "selected
// property" field pair below it for editing, and Add/Remove — level
// determines which object the Add/Set/DropExtendedPropertyContext calls
// target (empty for database-level, SCHEMA+TABLE for a table, also usable
// for column-level extended properties with SCHEMA+TABLE+COLUMN once a
// caller needs that). props is whatever the caller already fetched
// (DatabaseExtendedProperties for database-level, ExtendedProperties(level)
// for anything narrower) — this function only builds the UI and the
// apply closure, it doesn't decide how to read the initial list.
func buildExtendedPropertiesForm(sc *db.ServerConn, dbName string, level gosmo.ExtendedPropertyLevel, props []*gosmo.ExtendedProperty) (*propsheet.Form, propApply) {
	edits := make([]*extPropEdit, 0, len(props))
	for _, p := range props {
		edits = append(edits, &extPropEdit{name: p.Name, origValue: p.Value, value: p.Value})
	}

	visible := func() []*extPropEdit {
		out := make([]*extPropEdit, 0, len(edits))
		for _, e := range edits {
			if !e.pendingRemove {
				out = append(out, e)
			}
		}
		return out
	}
	rowsFor := func() [][]string {
		vis := visible()
		rows := make([][]string, len(vis))
		for i, e := range vis {
			rows[i] = []string{e.name, e.value}
		}
		return rows
	}

	grid := controls.NewDataGrid()
	grid.SetData([]string{"Name", "Value"}, rowsFor())

	nameField := propsheet.Text("Name", "", 24)
	valueField := propsheet.Text("Value", "", 30)
	selected := func() *extPropEdit {
		vis := visible()
		i := grid.SelectedRow()
		if i < 0 || i >= len(vis) {
			return nil
		}
		return vis[i]
	}
	// current tracks whichever edit the fields below the grid are
	// currently showing, so a value typed into valueField can be
	// committed back into it before the selection moves on (a plain
	// OnSelectRow callback only tells us the *new* selection, not which
	// edit the still-visible field text belongs to).
	var current *extPropEdit
	commitCurrent := func() {
		if current != nil {
			current.value = valueField.Value()
		}
	}
	syncFieldsFromSelection := func() {
		current = selected()
		if current != nil {
			nameField.SetValue(current.name)
			valueField.SetValue(current.value)
		} else {
			nameField.SetValue("")
			valueField.SetValue("")
		}
	}
	grid.OnSelectRow = func(row int) {
		commitCurrent()
		syncFieldsFromSelection()
	}
	syncFieldsFromSelection() // seed `current` for the initial selection (row 0)

	var addBtn, removeBtn *widgets.Button
	addBtn = widgets.NewButton("Add", func() {
		// Deliberately does NOT call commitCurrent(): valueField doubles as
		// the previously-selected property's live edit box, and
		// commitCurrent() writes its text into that property's .value — if
		// the user typed a value here meaning it for the brand-new property
		// being Added, that write would silently overwrite the previously
		// selected property's value instead. Any not-yet-applied edit to
		// the previously selected property is simply left as last synced
		// from its own selection (same trade-off as the Files page's Add).
		name := nameField.Value()
		if name == "" {
			return
		}
		for _, e := range visible() {
			if e.name == name {
				return // already present — edit its Value row instead
			}
		}
		edits = append(edits, &extPropEdit{name: name, value: valueField.Value(), isNew: true})
		grid.SetData([]string{"Name", "Value"}, rowsFor())
		grid.SetSelectedRow(len(visible()) - 1)
		syncFieldsFromSelection()
	})
	removeBtn = widgets.NewButton("Remove", func() {
		if e := selected(); e != nil {
			e.pendingRemove = true
			current = nil // its old value is void; don't let commitCurrent write back into it
			grid.SetData([]string{"Name", "Value"}, rowsFor())
			grid.SetSelectedRow(0)
			syncFieldsFromSelection()
		}
	})

	gridRow := propsheet.NewGridRow(grid, 12)
	dirty := func() bool {
		for _, e := range edits {
			if e.pendingRemove || e.isNew || e.value != e.origValue {
				return true
			}
		}
		return false
	}
	gridRow.DirtyFn = dirty
	gridRow.RevertFn = func() {
		kept := edits[:0]
		for _, e := range edits {
			if e.isNew {
				continue
			}
			e.value = e.origValue
			e.pendingRemove = false
			kept = append(kept, e)
		}
		edits = kept
		grid.SetData([]string{"Name", "Value"}, rowsFor())
	}

	f := propsheet.NewForm(
		propsheet.Section("Extended properties"),
		gridRow,
		propsheet.Section("Selected property"),
		nameField, valueField,
		propsheet.Buttons(addBtn, removeBtn),
		propsheet.Note("Extended properties are metadata only. They can be scripted via sp_addextendedproperty, sp_updateextendedproperty, and sp_dropextendedproperty."),
	)
	apply := func(ctx context.Context) error {
		commitCurrent()
		d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
		if err != nil {
			return err
		}
		for _, e := range edits {
			switch {
			case e.pendingRemove && !e.isNew:
				if err := d.DropExtendedPropertyContext(ctx, e.name, level); err != nil {
					return err
				}
			case e.isNew && !e.pendingRemove:
				if err := d.AddExtendedPropertyContext(ctx, e.name, e.value, level); err != nil {
					return err
				}
			case !e.isNew && !e.pendingRemove && e.value != e.origValue:
				if err := d.SetExtendedPropertyContext(ctx, e.name, e.value, level); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return f, apply
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

// securable identifies one thing a database role's Securables page can
// grant/deny permissions on: a table, a view, a schema, or the database
// itself.
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
// same GRANT -> DENY -> "" (revoke) -> GRANT cycle as permEdit.
type securableEdit struct {
	sec        securable
	permission string
	orig       string
	current    string
}

// buildSecurablesMatrix builds a Database Role Properties' Securables
// page: a securable-list grid (top) and, for whichever securable is
// selected, the full permission catalog for that securable's type x
// Grant/Deny/(none) state (bottom) — the inverse of buildPermissionsMatrix
// (that one is "one securable, every principal"; this is "one principal,
// every securable"). initial is every securable the principal already has
// at least one explicit grant/deny on (seeded from
// gosmo.PermissionsForPrincipalContext); available lists every other
// addable securable — picking one from the dropdown and clicking Add
// appends a new, all-"(none)" row. grantFn/denyFn/revokeFn are routed to
// the right gosmo Grant/Deny/RevokeXPermissionContext call based on the
// selected securable's Type.
func buildSecurablesMatrix(
	initial []securable, entries []*gosmo.PrincipalSecurable, available []securable,
	securablesHeight, permsHeight int,
	grantFn, denyFn, revokeFn func(ctx context.Context, s securable, permission string) error,
) (*propsheet.Form, propApply) {
	type entryKey struct{ secKey, permission string }
	existing := make(map[entryKey]string, len(entries))
	for _, e := range entries {
		k := securable{Type: e.SecurableType, Schema: e.Schema, Name: e.Name}
		existing[entryKey{k.key(), e.Permission}] = e.State
	}

	securables := append([]securable(nil), initial...)

	// editsBySecurable is built lazily, one securable at a time, as the
	// user browses the securable grid — a securable never selected has by
	// definition nothing to apply or check for dirtiness.
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

	securableRows := func() [][]string {
		rows := make([][]string, len(securables))
		for i, s := range securables {
			rows[i] = []string{s.label(), s.Type}
		}
		return rows
	}
	securableGrid := controls.NewDataGrid()
	securableGrid.SetData([]string{"Securable", "Type"}, securableRows())

	permGrid := controls.NewDataGrid()
	permGrid.SetCellCursor(true)
	permRowsFor := func(edits []*securableEdit) [][]string {
		rows := make([][]string, len(edits))
		for i, e := range edits {
			rows[i] = []string{e.permission, displayPermState(e.current)}
		}
		return rows
	}

	permSection := propsheet.Section("Permissions")
	selected := -1
	loadSecurable := func(row int) {
		if row < 0 || row >= len(securables) || row == selected {
			return
		}
		selected = row
		permGrid.SetData([]string{"Permission", "State"}, permRowsFor(editsFor(securables[row])))
		permGrid.SetSelectedRow(0)
		permSection.SetTitle("Permissions for " + securables[row].label())
	}
	securableGrid.OnSelectRow = loadSecurable
	if len(securables) > 0 {
		loadSecurable(0)
	}

	permGrid.OnActivateCell = func(row, col int) {
		if selected < 0 || col != 1 {
			return
		}
		edits := editsBySecurable[securables[selected].key()]
		if row < 0 || row >= len(edits) {
			return
		}
		edits[row].current = nextPermState(edits[row].current)
		permGrid.SetData([]string{"Permission", "State"}, permRowsFor(edits))
		permGrid.SetSelectedRow(row)
	}

	availableLabels := make([]string, len(available))
	for i, s := range available {
		availableLabels[i] = s.label()
	}
	if len(availableLabels) == 0 {
		availableLabels = []string{"(none available)"}
	}
	addSelect := propsheet.Select("Add securable", availableLabels, 0)
	addBtn := widgets.NewButton("Add", func() {
		if len(available) == 0 {
			return
		}
		s := available[addSelect.Selected()]
		for _, existingS := range securables {
			if existingS.key() == s.key() {
				return // already present — edit its Permissions row instead
			}
		}
		securables = append(securables, s)
		securableGrid.SetData([]string{"Securable", "Type"}, securableRows())
		securableGrid.SetSelectedRow(len(securables) - 1)
		loadSecurable(len(securables) - 1)
	})

	securablesRow := propsheet.NewGridRow(securableGrid, securablesHeight)
	permsRow := propsheet.NewGridRow(permGrid, permsHeight)
	permsRow.DirtyFn = func() bool {
		for _, edits := range editsBySecurable {
			for _, e := range edits {
				if e.current != e.orig {
					return true
				}
			}
		}
		return false
	}
	permsRow.RevertFn = func() {
		for _, edits := range editsBySecurable {
			for _, e := range edits {
				e.current = e.orig
			}
		}
		if selected >= 0 {
			permGrid.SetData([]string{"Permission", "State"}, permRowsFor(editsBySecurable[securables[selected].key()]))
		}
	}

	f := propsheet.NewForm(
		propsheet.Section("Securables"),
		securablesRow,
		addSelect,
		propsheet.Buttons(addBtn),
		permSection,
		permsRow,
		propsheet.Note("Space/Enter (or click) on State cycles Grant → Deny → (none). Tab switches between the securable list and its permissions. Pick a securable from the dropdown and click Add to give it its own row."),
	)

	apply := func(ctx context.Context) error {
		for _, edits := range editsBySecurable {
			for _, e := range edits {
				if e.current == e.orig {
					continue
				}
				var err error
				switch e.current {
				case "GRANT":
					err = grantFn(ctx, e.sec, e.permission)
				case "DENY":
					err = denyFn(ctx, e.sec, e.permission)
				case "":
					err = revokeFn(ctx, e.sec, e.permission)
				}
				if err != nil {
					return err
				}
			}
		}
		return nil
	}
	return f, apply
}

// grantSecurable, denySecurable, and revokeSecurable route a
// buildSecurablesMatrix grant/deny/revoke call to the right gosmo method
// based on s.Type — tables/views use the object-level trio, schemas the
// schema-level trio, and the database itself the database-scoped trio.
func grantSecurable(ctx context.Context, d *gosmo.Database, s securable, permission, principal string) error {
	switch s.Type {
	case "SCHEMA":
		return d.GrantSchemaPermissionContext(ctx, s.Name, gosmo.ObjectPermission(permission), principal)
	case "DATABASE":
		return d.GrantDatabasePermissionContext(ctx, permission, principal)
	default:
		return d.GrantPermissionContext(ctx, s.Schema, s.Name, gosmo.ObjectPermission(permission), principal)
	}
}

func denySecurable(ctx context.Context, d *gosmo.Database, s securable, permission, principal string) error {
	switch s.Type {
	case "SCHEMA":
		return d.DenySchemaPermissionContext(ctx, s.Name, gosmo.ObjectPermission(permission), principal)
	case "DATABASE":
		return d.DenyDatabasePermissionContext(ctx, permission, principal)
	default:
		return d.DenyPermissionContext(ctx, s.Schema, s.Name, gosmo.ObjectPermission(permission), principal)
	}
}

func revokeSecurable(ctx context.Context, d *gosmo.Database, s securable, permission, principal string) error {
	switch s.Type {
	case "SCHEMA":
		return d.RevokeSchemaPermissionContext(ctx, s.Name, gosmo.ObjectPermission(permission), principal)
	case "DATABASE":
		return d.RevokeDatabasePermissionContext(ctx, permission, principal)
	default:
		return d.RevokePermissionContext(ctx, s.Schema, s.Name, gosmo.ObjectPermission(permission), principal)
	}
}

// fixedRoleDescriptions gives each fixed database role a short blurb for
// a Database User Properties' Membership page — matching SSMS's own
// well-known descriptions. User-defined roles have no entry (blank).
var fixedRoleDescriptions = map[string]string{
	"db_owner":          "Full control over the database",
	"db_accessadmin":    "Add or remove access for logins",
	"db_securityadmin":  "Manage role membership and permissions",
	"db_ddladmin":       "Run any DDL command",
	"db_backupoperator": "Back up the database",
	"db_datareader":     "Read all data from all user tables",
	"db_datawriter":     "Add, delete, or change data in all user tables",
	"db_denydatareader": "Deny SELECT on all user tables",
	"db_denydatawriter": "Deny INSERT/UPDATE/DELETE on all user tables",
}

// fixedServerRoleDescriptions and serverRoleImpact give each fixed server
// role a short blurb and an impact level for Login Properties' Server
// Roles page, matching the mockup's own impact classification.
// User-defined server roles have no entry (blank).
var fixedServerRoleDescriptions = map[string]string{
	"sysadmin":      "Full control over the SQL Server instance",
	"securityadmin": "Manage logins and their properties",
	"serveradmin":   "Configure server-wide settings",
	"setupadmin":    "Add and remove linked servers",
	"processadmin":  "Manage processes running in SQL Server",
	"diskadmin":     "Manage disk files",
	"dbcreator":     "Create, alter, drop, and restore databases",
	"bulkadmin":     "Run BULK INSERT statements",
}

var serverRoleImpact = map[string]string{
	"sysadmin":      "Critical",
	"securityadmin": "Critical",
	"serveradmin":   "Critical",
	"setupadmin":    "High",
	"processadmin":  "High",
	"diskadmin":     "High",
	"dbcreator":     "High",
	"bulkadmin":     "Medium",
}
