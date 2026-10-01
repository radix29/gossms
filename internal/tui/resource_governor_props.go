package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// resource_governor_props.go is Resource Governor Properties: one dialog for
// the whole configuration, as SSMS has it, paged — General, Resource Pools,
// Workload Groups (resource_governor_props_groups.go) and External Resource
// Pools. A pool, group or external pool leaf opens the same dialog on its own
// page with its row selected; there is no separate Pool Properties, so there
// is one editor per concept.
//
// # One Apply, in dependency order, finished once
//
// The pages plan their statements rather than send them (applyPlan): a group
// cannot move into a pool before the pool exists, nor a pool be dropped while
// a group still uses it, and those edits sit on different pages. The plan
// runs in rgPhase order, and runResourceGovernorPlan then ends every Apply
// with exactly one ALTER RESOURCE GOVERNOR RECONFIGURE — or DISABLE, when the
// governor is to stay off: RECONFIGURE enables a disabled governor (W1), and
// briefly enabling one with a classifier in place would classify every login
// that arrived meanwhile.
//
// Not one transaction. Pool and group DDL is transactional (W1), but gosmo
// issues each write on its own pooled connection, and a transaction spanning
// them needs a write API gosmo does not have. A failure part-way leaves the
// earlier statements stored but not in force — RECONFIGURE is the last step
// and has not run — and the dialog reloads every page, so what it shows is
// what the server has.

// The pages of Resource Governor Properties, in order — what rgFocus.page
// selects.
const (
	rgPageGeneral = iota
	rgPagePools
	rgPageGroups
	rgPageExternalPools
)

// The phases a Resource Governor Apply runs in. Everything that will be
// referred to is created first, groups are created and moved next, and drops
// come after the moves that empty a pool. Pool limits are altered last, after
// the drops: a dropped pool's MIN_CPU_PERCENT and MIN_MEMORY_PERCENT are then
// free for a raised one, since the minimums across pools may not exceed 100.
const (
	rgPhaseGovernor = iota
	rgPhaseCreatePools
	rgPhaseCreateGroups
	rgPhaseAlterGroups
	rgPhaseDropGroups
	rgPhaseDropPools
	rgPhaseAlterPools
)

// rgEnabledKey is the plan value General's Enabled box sets: whether the run
// ends in RECONFIGURE or DISABLE. Absent when General was not edited.
const rgEnabledKey = "resource governor enabled"

// rgFocus is where Resource Governor Properties opens: the page, and the row
// to select on it. Pool also picks the Workload Groups page's pool when no
// group is named.
type rgFocus struct {
	page                  int
	pool, group, external string
}

// showResourceGovernorPropertiesFor opens Resource Governor Properties on sc,
// at focus.
func (a *App) showResourceGovernorPropertiesFor(sc *db.ServerConn, focus rgFocus) {
	d := a.propDialog
	opened := d.showPlanned(sc, "", "Resource Governor Properties", "Resource Governor", "Server: "+sc.Opts.Server,
		func() []propPage { return rgPropPages(d, sc, focus) },
		func(ctx context.Context, plan *applyPlan) error { return runResourceGovernorPlan(ctx, sc, plan) })
	if !opened {
		return
	}
	// The label carries the governor's state, and the folders its pools.
	d.onSaved = func() { a.refreshResourceGovernorNodes(sc) }
	if focus.page != rgPageGeneral {
		d.SelectPage(focus.page)
	}
}

// rgPropPages builds the page set. Every write is Resource Governor DDL,
// which needs CONTROL SERVER and nothing less (W1: ALTER SETTINGS is refused).
func rgPropPages(d *PropDialog, sc *db.ServerConn, focus rgFocus) []propPage {
	return []propPage{
		withRequires(pageRGGeneral(d, sc), "", gate.ControlServer),
		withRequires(pageRGPools(sc, focus.pool), "", gate.ControlServer),
		withRequires(pageRGGroups(sc, focus), "", gate.ControlServer),
		withRequires(pageRGExternalPools(sc, focus.external), "", gate.ControlServer),
	}
}

// runResourceGovernorPlan carries out a Resource Governor Apply: the pages'
// statements in phase order, then RECONFIGURE, or DISABLE when the governor
// is to be off. With General unedited, that is whatever the governor is now
// — read here, under Script Changes too, since reads still reach the server.
//
// A disabled governor left disabled gets DISABLE, which clears the pending
// flag without applying anything: nothing is in force while it is off, and
// the next RECONFIGURE (Enabled ticked) applies every stored change.
func runResourceGovernorPlan(ctx context.Context, sc *db.ServerConn, plan *applyPlan) error {
	if err := plan.run(ctx); err != nil {
		return err
	}
	v, ok := plan.value(rgEnabledKey)
	if !ok {
		return applyResourceGovernor(ctx, sc)
	}
	rg := sc.Server.ResourceGovernorRef()
	if enabled, _ := v.(bool); enabled {
		return rg.Reconfigure(ctx)
	}
	return rg.Disable(ctx)
}

// rgPlanFrom is applyPlanFrom for a Resource Governor page, as an error when
// the page was applied without one.
func rgPlanFrom(ctx context.Context) (*applyPlan, error) {
	if plan := applyPlanFrom(ctx); plan != nil {
		return plan, nil
	}
	return nil, errNotPlanned
}

// -- General -----------------------------------------------------------------

// rgNoClassifier is the Classifier function choice for none.
const rgNoClassifier = "(none)"

// rgClassifierTemplate is what New classifier... opens on master: the shape
// the server accepts as a classifier, ready to fill in.
const rgClassifierTemplate = `-- A Resource Governor classifier runs for every new session, in master,
-- and returns the name of the workload group the session belongs in.
-- It must be schema-bound, take no parameters and return sysname; an
-- unknown name or NULL puts the session in the default group.
-- Keep it fast: logins wait for it.
CREATE FUNCTION dbo.rg_classifier()
RETURNS sysname
WITH SCHEMABINDING
AS
BEGIN
    DECLARE @group sysname = N'default';
    -- IF SUSER_SNAME() = N'report_reader' SET @group = N'reporting';
    -- IF APP_NAME() LIKE N'%Management Studio%' SET @group = N'adhoc';
    RETURN @group;
END;
GO
-- Then reopen Resource Governor Properties and pick it under General >
-- Classifier function.
`

// rgNewClassifier is New classifier...: it closes the dialog, then opens the
// template on master. The dialog is modal, so a template opened behind it
// could not be typed in, run, or even reached until the dialog closed — the
// first version left it open and told the user to "create the function, then
// F5 here", which no key could do (W7). Edits on any page would be lost with
// the dialog, so they are confirmed first.
func rgNewClassifier(d *PropDialog, sc *db.ServerConn) {
	open := func() {
		d.Dismiss()
		d.app.openQueryWithText(sc, "master", rgClassifierTemplate)
	}
	if !d.Dirty() {
		open()
		return
	}
	d.app.confirmDialog.ShowConfirm("New Classifier",
		"Writing a classifier closes Resource Governor Properties, and its unsaved changes are lost. Close it and open the template?",
		func(confirmed bool) {
			if confirmed {
				open()
			}
		})
}

// pageRGGeneral is the governor itself: enabled, the classifier, and the
// stored I/O limit, with what is in force beside them when the login may
// read it.
//
// The classifier is a picker (docs/decisions.md): the dialog lists the
// functions master already has that the server would accept, and New
// classifier... opens a template to write one. Writing T-SQL is a query
// window's job, not a property sheet's.
func pageRGGeneral(d *PropDialog, sc *db.ServerConn) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			rg, err := sc.Server.ResourceGovernor(ctx)
			if errors.Is(err, gosmo.ErrNotFound) {
				return propsheet.NewForm(
					propsheet.Section("Resource Governor"),
					propsheet.Note("The Resource Governor configuration is not visible to this login — VIEW ANY DEFINITION is needed to read it, and CONTROL SERVER to change it."),
				), nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			candidates, err := sc.Server.ClassifierFunctionCandidates(ctx)
			if err != nil {
				return nil, nil, err
			}
			// In force needs VIEW SERVER STATE; without it the page shows the
			// stored half only.
			status, _ := sc.Server.ResourceGovernorStatus(ctx)

			choices, selected := rgClassifierChoices(candidates, rg.ClassifierSchema, rg.ClassifierName, rg.ClassifierFunctionID)
			items := make([]string, len(choices))
			for i, c := range choices {
				items[i] = rgClassifierItem(c)
			}

			enabledRow := propsheet.Check("Enabled", rg.IsEnabled)
			classifierRow := propsheet.Select("Classifier function", items, selected)
			classifierRow.SetFitItems(true)
			ioRow := propsheet.Int("Max outstanding I/O per volume", int64(rg.MaxOutstandingIOPerVolume), 0, 100, "")
			newClassifier := widgets.NewButton("New classifier...", func() { rgNewClassifier(d, sc) })

			rows := []propsheet.Row{
				propsheet.Section("Configuration"),
				enabledRow,
				classifierRow,
				propsheet.Buttons(newClassifier),
				ioRow,
				propsheet.Note("Max outstanding I/O per volume: 0 lets the server choose. The classifier list holds the schema-bound, parameterless functions in master that return sysname."),
			}
			if status != nil {
				rows = append(rows,
					propsheet.Section("In force"),
					propsheet.Static("Classifier function", classifierText(status.ClassifierSchema, status.ClassifierName, status.ClassifierFunctionID)),
					propsheet.Static("Max outstanding I/O per volume", ioPerVolumeText(status.MaxOutstandingIOPerVolume)),
				)
				if status.IsReconfigurationPending {
					rows = append(rows, propsheet.Note(rgPendingNote(rg.IsEnabled)))
				}
			}
			rows = append(rows, propsheet.Note("OK and Apply end with ALTER RESOURCE GOVERNOR RECONFIGURE, which puts every stored change in force — or with DISABLE while Enabled is cleared."))

			apply := func(ctx context.Context) error {
				plan, err := rgPlanFrom(ctx)
				if err != nil {
					return err
				}
				if enabledRow.Dirty() {
					plan.set(rgEnabledKey, enabledRow.Checked())
				}
				if classifierRow.Dirty() {
					c := choices[classifierRow.Selected()]
					plan.add(rgPhaseGovernor, func(ctx context.Context) error {
						return sc.Server.ResourceGovernorRef().SetClassifier(ctx, c.Schema, c.Name)
					})
				}
				if ioRow.Dirty() {
					n, err := ioRow.IntValue()
					if err != nil {
						return err
					}
					plan.add(rgPhaseGovernor, func(ctx context.Context) error {
						return sc.Server.ResourceGovernorRef().SetMaxOutstandingIOPerVolume(ctx, int(n))
					})
				}
				return nil
			}
			return propsheet.NewForm(rows...), apply, nil
		},
	}
}

// rgPendingNote explains a set pending flag, which means different things
// either side of DISABLE (see resourceGovernorState).
func rgPendingNote(enabled bool) string {
	if enabled {
		return "Stored changes are waiting for RECONFIGURE; any Apply here puts them in force."
	}
	return "Changes were stored since the governor was disabled; they take effect when it is enabled."
}

// rgClassifierChoices is the Classifier function list — none, then each
// candidate — and the index of the stored classifier in it. A stored
// classifier missing from the candidates (its definition no longer
// qualifies, or the login cannot see it) is appended rather than displayed as
// some other choice; an unresolvable one is shown by id and can only be
// replaced.
func rgClassifierChoices(candidates []gosmo.ClassifierFunction, schema, name string, id int) ([]gosmo.ClassifierFunction, int) {
	choices := append([]gosmo.ClassifierFunction{{}}, candidates...)
	if id == 0 && name == "" {
		return choices, 0
	}
	if i := slices.Index(choices, gosmo.ClassifierFunction{Schema: schema, Name: name}); i > 0 {
		return choices, i
	}
	if name == "" {
		// Unresolvable: the item text says so, and choosing it again sends
		// nothing, since the row is only dirty off it.
		name = fmt.Sprintf("object_id %d (not visible)", id)
	}
	choices = append(choices, gosmo.ClassifierFunction{Schema: schema, Name: name})
	return choices, len(choices) - 1
}

// rgClassifierItem is a classifier choice's text in the list.
func rgClassifierItem(c gosmo.ClassifierFunction) string {
	switch {
	case c.Name == "":
		return rgNoClassifier
	case c.Schema == "":
		return c.Name
	}
	return c.Schema + "." + c.Name
}

// -- Resource pools and external resource pools -------------------------------

// rgIntField is one integer limit of a pool kind: its grid column, its detail
// row, its range and default, and where it goes in the kind's options. O is
// gosmo.ResourcePoolOptions or gosmo.ExternalResourcePoolOptions.
type rgIntField[O any] struct {
	header   string
	label    string
	min, max int64
	unit     string
	def      int
	set      func(o *O, v *int)
}

// rgIntEdit is one pool's row on a pool page: what the server has, what the
// page now says, and whether it is new or going.
type rgIntEdit struct {
	name     string
	system   bool // built in: never dropped
	locked   bool // the internal pool: no ALTER either
	isNew    bool
	removing bool
	orig     []int
	cur      []int
	affinity string
}

func (e *rgIntEdit) dirty() bool { return e.isNew || e.removing || !slices.Equal(e.cur, e.orig) }

// rgIntPageSpec describes a pool page — the two kinds differ only in their
// fields and their gosmo calls.
type rgIntPageSpec[O any] struct {
	title  string
	noun   string // "resource pool"
	fields []rgIntField[O]
	read   func(ctx context.Context) ([]*rgIntEdit, error)
	create func(ctx context.Context, name string, o O) error
	alter  func(ctx context.Context, name string, o O) error
	drop   func(ctx context.Context, name string) error
	focus  string
	notes  []string
}

// options is the kind's options for every field cur differs from base in.
func (s rgIntPageSpec[O]) options(cur, base []int) O {
	var o O
	for i, f := range s.fields {
		if cur[i] != base[i] {
			f.set(&o, new(cur[i]))
		}
	}
	return o
}

func (s rgIntPageSpec[O]) defaults() []int {
	out := make([]int, len(s.fields))
	for i, f := range s.fields {
		out[i] = f.def
	}
	return out
}

// rgPoolFields are a resource pool's limits, in grid order.
var rgPoolFields = []rgIntField[gosmo.ResourcePoolOptions]{
	{"Min CPU %", "Minimum CPU %", 0, 100, "", 0, func(o *gosmo.ResourcePoolOptions, v *int) { o.MinCPUPercent = v }},
	{"Max CPU %", "Maximum CPU %", 1, 100, "", 100, func(o *gosmo.ResourcePoolOptions, v *int) { o.MaxCPUPercent = v }},
	{"Cap CPU %", "CPU cap %", 1, 100, "", 100, func(o *gosmo.ResourcePoolOptions, v *int) { o.CapCPUPercent = v }},
	{"Min mem %", "Minimum memory %", 0, 100, "", 0, func(o *gosmo.ResourcePoolOptions, v *int) { o.MinMemoryPercent = v }},
	{"Max mem %", "Maximum memory %", 1, 100, "", 100, func(o *gosmo.ResourcePoolOptions, v *int) { o.MaxMemoryPercent = v }},
	{"Min IOPS", "Minimum IOPS per volume", 0, 2147483647, "", 0, func(o *gosmo.ResourcePoolOptions, v *int) { o.MinIOPSPerVolume = v }},
	{"Max IOPS", "Maximum IOPS per volume", 0, 2147483647, "", 0, func(o *gosmo.ResourcePoolOptions, v *int) { o.MaxIOPSPerVolume = v }},
}

// rgExternalPoolFields are an external resource pool's limits, in grid order.
var rgExternalPoolFields = []rgIntField[gosmo.ExternalResourcePoolOptions]{
	{"Max CPU %", "Maximum CPU %", 1, 100, "", 100, func(o *gosmo.ExternalResourcePoolOptions, v *int) { o.MaxCPUPercent = v }},
	{"Max mem %", "Maximum memory %", 1, 100, "", 100, func(o *gosmo.ExternalResourcePoolOptions, v *int) { o.MaxMemoryPercent = v }},
	{"Max processes", "Maximum processes", 0, 2147483647, "", 0, func(o *gosmo.ExternalResourcePoolOptions, v *int) { o.MaxProcesses = v }},
}

func pageRGPools(sc *db.ServerConn, focus string) propPage {
	return rgIntPage(rgIntPageSpec[gosmo.ResourcePoolOptions]{
		title:  "Resource Pools",
		noun:   "resource pool",
		fields: rgPoolFields,
		read: func(ctx context.Context) ([]*rgIntEdit, error) {
			pools, err := sc.Server.ResourcePools(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]*rgIntEdit, len(pools))
			for i, p := range pools {
				vals := []int{p.MinCPUPercent, p.MaxCPUPercent, p.CapCPUPercent,
					p.MinMemoryPercent, p.MaxMemoryPercent, p.MinIOPSPerVolume, p.MaxIOPSPerVolume}
				out[i] = &rgIntEdit{name: p.Name, system: p.IsSystem(), locked: p.Name == "internal",
					orig: vals, cur: slices.Clone(vals), affinity: poolAffinityText(p.Affinity)}
			}
			return out, nil
		},
		create: func(ctx context.Context, name string, o gosmo.ResourcePoolOptions) error {
			_, err := sc.Server.CreateResourcePool(ctx, gosmo.CreateResourcePoolRequest{Name: name, Options: o})
			return err
		},
		alter: func(ctx context.Context, name string, o gosmo.ResourcePoolOptions) error {
			return sc.Server.ResourcePoolRef(name).Alter(ctx, o)
		},
		drop:  func(ctx context.Context, name string) error { return sc.Server.ResourcePoolRef(name).Drop(ctx) },
		focus: focus,
		notes: []string{
			"internal accepts no changes; default and internal cannot be removed. A pool is removed only once no workload group uses it — move or remove its groups on the Workload Groups page in the same Apply.",
			"A pool added here is offered on the Workload Groups page after Apply. Scheduler affinity is shown, not edited.",
		},
	})
}

func pageRGExternalPools(sc *db.ServerConn, focus string) propPage {
	return rgIntPage(rgIntPageSpec[gosmo.ExternalResourcePoolOptions]{
		title:  "External Resource Pools",
		noun:   "external resource pool",
		fields: rgExternalPoolFields,
		read: func(ctx context.Context) ([]*rgIntEdit, error) {
			pools, err := sc.Server.ExternalResourcePools(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]*rgIntEdit, len(pools))
			for i, p := range pools {
				vals := []int{p.MaxCPUPercent, p.MaxMemoryPercent, p.MaxProcesses}
				out[i] = &rgIntEdit{name: p.Name, system: p.IsSystem(),
					orig: vals, cur: slices.Clone(vals), affinity: externalPoolAffinityText(p.Affinity)}
			}
			return out, nil
		},
		create: func(ctx context.Context, name string, o gosmo.ExternalResourcePoolOptions) error {
			_, err := sc.Server.CreateExternalResourcePool(ctx, gosmo.CreateExternalResourcePoolRequest{Name: name, Options: o})
			return err
		},
		alter: func(ctx context.Context, name string, o gosmo.ExternalResourcePoolOptions) error {
			return sc.Server.ExternalResourcePoolRef(name).Alter(ctx, o)
		},
		drop:  func(ctx context.Context, name string) error { return sc.Server.ExternalResourcePoolRef(name).Drop(ctx) },
		focus: focus,
		notes: []string{
			"External pools govern external scripts (Machine Learning Services). default cannot be removed, nor a pool a workload group uses. Maximum processes: 0 is unlimited. CPU affinity is shown, not edited.",
		},
	})
}

// rgIntPage is a pool page: the pools in a grid, the selected one's limits
// below it, and Add/Remove.
func rgIntPage[O any](spec rgIntPageSpec[O]) propPage {
	return propPage{
		title: spec.title,
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			loaded, err := spec.read(ctx)
			if err != nil {
				return nil, nil, err
			}
			if len(loaded) == 0 {
				return nil, nil, errResourceGovernorNotVisible
			}
			edits := slices.Clone(loaded)

			visible := func() []*rgIntEdit {
				out := make([]*rgIntEdit, 0, len(edits))
				for _, e := range edits {
					if !e.removing {
						out = append(out, e)
					}
				}
				return out
			}
			headers := []string{"Name"}
			for _, f := range spec.fields {
				headers = append(headers, f.header)
			}
			headers = append(headers, "Affinity")
			gridRows := func() [][]string {
				vis := visible()
				rows := make([][]string, len(vis))
				for i, e := range vis {
					row := []string{rgEditName(e.name, e.system, e.isNew)}
					for _, v := range e.cur {
						row = append(row, strconv.Itoa(v))
					}
					rows[i] = append(row, orDefault(e.affinity, "Auto"))
				}
				return rows
			}
			grid := controls.NewDataGrid()
			grid.SetData(headers, gridRows())
			grid.SetCellCursor(true)

			detail := make([]*propsheet.TextRow, len(spec.fields))
			for i, f := range spec.fields {
				detail[i] = propsheet.Int(f.label, int64(f.def), f.min, f.max, f.unit)
			}
			selectedRow := propsheet.Static("Name", "")

			var current *rgIntEdit
			// commitCurrent folds the detail rows into the selected pool. A
			// value the row would not validate stays what it was.
			commitCurrent := func() {
				if current == nil || current.locked {
					return
				}
				for i, row := range detail {
					if n, err := row.IntValue(); err == nil && row.Validate() == nil {
						current.cur[i] = int(n)
					}
				}
			}
			syncFromSelection := func() {
				vis := visible()
				current = nil
				if i := grid.SelectedRow(); i >= 0 && i < len(vis) {
					current = vis[i]
				}
				if current == nil {
					selectedRow.SetValue("")
				} else {
					selectedRow.SetValue(rgEditName(current.name, current.system, current.isNew))
				}
				for i, row := range detail {
					if current != nil {
						row.SetValue(strconv.Itoa(current.cur[i]))
					}
					row.SetReadOnly(current == nil || current.locked)
				}
			}
			if i := slices.IndexFunc(visible(), func(e *rgIntEdit) bool { return e.name == spec.focus }); i > 0 {
				grid.SetSelectedRow(i)
			}
			reload := wireGridEditor(grid, headers, gridRows, commitCurrent, syncFromSelection)
			// reselect moves the cursor to row after a change to the row set.
			reselect := func(row int) {
				current = nil
				resetGrid(grid, headers, gridRows(), row)
				syncFromSelection()
			}

			gridRow := propsheet.NewGridRow(grid, min(len(edits)+4, 10))
			gridRow.DirtyFn = func() bool {
				return slices.ContainsFunc(edits, (*rgIntEdit).dirty)
			}
			gridRow.RevertFn = func() {
				edits = edits[:0]
				for _, e := range loaded {
					e.cur, e.removing = slices.Clone(e.orig), false
					edits = append(edits, e)
				}
				current = nil
				reload()
			}

			nameField := propsheet.Text("New "+spec.noun, "", 24)
			hint := propsheet.Hint()
			addBtn := widgets.NewButton("Add", func() {
				commitCurrent()
				name := nameField.Value()
				if name == "" {
					hint.Set("Type a name for the new " + spec.noun + " first.")
					return
				}
				if slices.ContainsFunc(edits, func(e *rgIntEdit) bool { return e.name == name }) {
					hint.Set("A " + spec.noun + " named " + name + " is already listed.")
					return
				}
				hint.Clear()
				def := spec.defaults()
				edits = append(edits, &rgIntEdit{name: name, isNew: true, orig: def, cur: slices.Clone(def)})
				nameField.SetValue("")
				reselect(len(visible()) - 1)
			})
			removeBtn := widgets.NewButton("Remove", func() {
				commitCurrent()
				vis := visible()
				i := grid.SelectedRow()
				if i < 0 || i >= len(vis) {
					hint.Set("Select a " + spec.noun + " in the grid above to remove it.")
					return
				}
				e := vis[i]
				if e.system {
					hint.Set(e.name + " is built in and cannot be removed.")
					return
				}
				hint.Clear()
				if e.isNew {
					edits = slices.DeleteFunc(edits, func(x *rgIntEdit) bool { return x == e })
				} else {
					e.removing = true
				}
				reselect(min(i, len(visible())-1))
			})

			rows := []propsheet.Row{
				propsheet.Section(spec.title),
				gridRow,
				propsheet.Section("Selected " + spec.noun),
				selectedRow,
			}
			for _, row := range detail {
				rows = append(rows, row)
			}
			rows = append(rows,
				propsheet.Section("Add or remove"),
				nameField,
				propsheet.Buttons(addBtn, removeBtn),
				hint,
			)
			for _, n := range spec.notes {
				rows = append(rows, propsheet.Note(n))
			}

			apply := func(ctx context.Context) error {
				commitCurrent()
				plan, err := rgPlanFrom(ctx)
				if err != nil {
					return err
				}
				def := spec.defaults()
				for _, e := range edits {
					name := e.name
					switch {
					case e.isNew:
						o := spec.options(e.cur, def)
						plan.add(rgPhaseCreatePools, func(ctx context.Context) error { return spec.create(ctx, name, o) })
					case e.removing:
						plan.add(rgPhaseDropPools, func(ctx context.Context) error { return spec.drop(ctx, name) })
					case !slices.Equal(e.cur, e.orig):
						o := spec.options(e.cur, e.orig)
						plan.add(rgPhaseAlterPools, func(ctx context.Context) error { return spec.alter(ctx, name, o) })
					}
				}
				return nil
			}
			return propsheet.NewForm(rows...), apply, nil
		},
	}
}

// rgEditName is a pool's or group's name as the grids show it: built-in ones
// marked as the tree marks them, and one not created yet as new.
func rgEditName(name string, system, isNew bool) string {
	if isNew {
		return name + " (new)"
	}
	return systemSuffix(name, system)
}
