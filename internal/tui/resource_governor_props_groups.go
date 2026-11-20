package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/propsheet"
)

// resource_governor_props_groups.go is Resource Governor Properties > Workload
// Groups: a pool dropdown, that pool's groups in a grid, and the selected group's
// settings (docs/decisions.md: the dropdown, rather than a grid following the
// Resource Pools page's selection, keeps the pages independent). The page holds
// every group on the server; the dropdown only chooses which pool's are listed, so
// a group moved to another pool leaves the grid and turns up under that pool,
// unsaved, until Apply.
//
// The pools offered are the pool pages' unsaved ones (rgModel): a pool added there
// can take a group here in the same Apply, and one removed there is no longer a
// group's destination, though it stays listed in the dropdown while a group is
// still in it, so the group can be moved out.

// rgImportanceItems are IMPORTANCE's values, as the catalog spells them.
var rgImportanceItems = []string{
	string(gosmo.ImportanceLow), string(gosmo.ImportanceMedium), string(gosmo.ImportanceHigh),
}

// rgGroupValues is a workload group's settings as the page edits them. The
// tempdb limits are text, "" for none, so the struct compares with ==.
type rgGroupValues struct {
	importance    string
	grant         float64
	cpuSec        int
	timeoutSec    int
	maxDOP        int
	maxRequests   int
	pool          string
	external      string
	tempdbPercent string
	tempdbMB      string
}

// rgGroupDefaults is a new group's settings (the server's defaults) in pool.
func rgGroupDefaults(pool string) rgGroupValues {
	return rgGroupValues{importance: string(gosmo.ImportanceMedium), grant: 25, pool: pool, external: "default"}
}

// rgGroupEdit is one workload group's row.
type rgGroupEdit struct {
	name   string
	system bool
	locked bool // the internal group: no ALTER
	propsheet.PendingState
	orig rgGroupValues
	cur  rgGroupValues
}

// options is the WorkloadGroupOptions for every setting cur differs from base
// in. A new group always names its pool, so its script says where it goes.
func (e *rgGroupEdit) options(base rgGroupValues) (gosmo.WorkloadGroupOptions, error) {
	cur := e.cur
	var o gosmo.WorkloadGroupOptions
	if cur.importance != base.importance {
		o.Importance = new(gosmo.WorkloadImportance(cur.importance))
	}
	if cur.grant != base.grant {
		o.RequestMaxMemoryGrantPercent = new(cur.grant)
	}
	if cur.cpuSec != base.cpuSec {
		o.RequestMaxCPUTimeSec = new(cur.cpuSec)
	}
	if cur.timeoutSec != base.timeoutSec {
		o.RequestMemoryGrantTimeoutSec = new(cur.timeoutSec)
	}
	if cur.maxDOP != base.maxDOP {
		o.MaxDOP = new(cur.maxDOP)
	}
	if cur.maxRequests != base.maxRequests {
		o.GroupMaxRequests = new(cur.maxRequests)
	}
	if e.IsNew || cur.pool != base.pool {
		o.Pool = new(cur.pool)
	}
	if cur.external != base.external {
		o.ExternalPool = new(cur.external)
	}
	var err error
	if cur.tempdbPercent != base.tempdbPercent {
		o.GroupMaxTempdbDataPercent, o.ClearGroupMaxTempdbDataPercent, err = rgOptionalLimit(cur.tempdbPercent)
		if err != nil {
			return o, err
		}
	}
	if cur.tempdbMB != base.tempdbMB {
		o.GroupMaxTempdbDataMB, o.ClearGroupMaxTempdbDataMB, err = rgOptionalLimit(cur.tempdbMB)
	}
	return o, err
}

// rgOptionalLimit turns a tempdb limit's text into the option: a value, or a
// clear for "".
func rgOptionalLimit(text string) (*float64, bool, error) {
	if text == "" {
		return nil, true, nil
	}
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil, false, fmt.Errorf("%q is not a number", text)
	}
	return &v, false, nil
}

// rgLimitText renders a nullable limit for the page, "" for none.
func rgLimitText(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

// rgFloatValidator accepts a number in [lo, hi] — whole only, when whole —
// or, when blank is allowed, nothing.
func rgFloatValidator(lo, hi float64, whole, blank bool) func(string) error {
	return func(s string) error {
		if s == "" && blank {
			return nil
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return fmt.Errorf("%q is not a number", s)
		}
		if v < lo || v > hi {
			return fmt.Errorf("%s is outside %s to %s", s, grantPercentText(lo), grantPercentText(hi))
		}
		if whole && v != float64(int64(v)) {
			return fmt.Errorf("%s is not a whole number — a fractional percentage needs SQL Server 2019 or later", s)
		}
		return nil
	}
}

// rgListedPools is the Workload Groups dropdown: the pools the pool page will
// leave, then any other pool a group is still in.
func rgListedPools(pools []string, edits []*rgGroupEdit) []string {
	out := slices.Clone(pools)
	for _, e := range edits {
		if !e.Removing && !slices.Contains(out, e.cur.pool) {
			out = append(out, e.cur.pool)
		}
	}
	return out
}

func pageRGGroups(sc *db.ServerConn, model *rgModel, focus rgFocus) propPage {
	return propPage{
		title: "Workload Groups",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			pools, err := sc.Server.ResourcePools(ctx)
			if err != nil {
				return nil, nil, err
			}
			groups, err := sc.Server.WorkloadGroups(ctx)
			if err != nil {
				return nil, nil, err
			}
			externals, err := sc.Server.ExternalResourcePools(ctx)
			if err != nil {
				return nil, nil, err
			}
			if len(pools) == 0 || len(groups) == 0 {
				return nil, nil, errResourceGovernorNotVisible
			}
			major := serverMajor(sc)
			// Unknown major (0) is treated as newest, as gosmo's gates do.
			fractional := major == 0 || major >= int(gosmo.SQLServer2019)
			tempdb := major == 0 || major >= int(gosmo.SQLServer2025)

			loadedPools := make([]string, len(pools))
			for i, p := range pools {
				loadedPools[i] = p.Name
			}
			loadedExternals := make([]string, len(externals))
			for i, p := range externals {
				loadedExternals[i] = p.Name
			}
			// These are read and replaced on the UI goroutine only.
			var poolNames, movable, externalNames []string
			readModel := func() {
				poolNames = model.pools.get(loadedPools)
				// A group can be put in any pool but internal.
				movable = slices.DeleteFunc(slices.Clone(poolNames), func(n string) bool { return n == "internal" })
				externalNames = model.externals.get(loadedExternals)
			}
			readModel()

			loaded := make([]*rgGroupEdit, len(groups))
			for i, g := range groups {
				v := rgGroupValues{
					importance: g.Importance, grant: g.RequestMaxMemoryGrantPercent,
					cpuSec: g.RequestMaxCPUTimeSec, timeoutSec: g.RequestMemoryGrantTimeoutSec,
					maxDOP: g.MaxDOP, maxRequests: g.GroupMaxRequests,
					pool: g.PoolName, external: g.ExternalPoolName,
					tempdbPercent: rgLimitText(g.GroupMaxTempdbDataPercent), tempdbMB: rgLimitText(g.GroupMaxTempdbDataMB),
				}
				loaded[i] = &rgGroupEdit{name: g.Name, system: g.IsSystem(), locked: g.Name == "internal", orig: v, cur: v}
			}
			edits := newPendingEdits(serverCollation(sc), loaded,
				func(e *rgGroupEdit) string { return e.name },
				func(e *rgGroupEdit) bool { return e.cur != e.orig },
				func(e *rgGroupEdit) { e.cur = e.orig })

			// The pool shown first: the focused group's, else the focused pool, else default.
			startPool := "default"
			if i := slices.IndexFunc(loaded, func(e *rgGroupEdit) bool { return e.name == focus.group }); i >= 0 {
				startPool = loaded[i].cur.pool
			} else if slices.Contains(poolNames, focus.pool) {
				startPool = focus.pool
			}
			listed := rgListedPools(poolNames, edits.All())
			poolRow := propsheet.Select("Resource pool", listed, max(slices.Index(listed, startPool), 0))
			// Which pool is listed is a view, not an edit.
			poolRow.SetDirtyTracked(false)

			visible := func() []*rgGroupEdit {
				pool := poolRow.Value()
				return slices.DeleteFunc(edits.Visible(), func(e *rgGroupEdit) bool { return e.cur.pool != pool })
			}
			headers := []string{"Name", "Importance", "Grant %", "CPU sec", "Timeout sec", "MAXDOP", "Max requests", "External pool"}
			if tempdb {
				headers = append(headers, "Tempdb %", "Tempdb MB")
			}
			cells := func(e *rgGroupEdit) []string {
				v := e.cur
				row := []string{
					rgEditName(e.name, e.system, e.IsNew), v.importance, grantPercentText(v.grant),
					zeroAs(v.cpuSec, "Unlimited"), zeroAs(v.timeoutSec, "Default"),
					zeroAs(v.maxDOP, "Default"), zeroAs(v.maxRequests, "Unlimited"), v.external,
				}
				if tempdb {
					row = append(row, orDefault(v.tempdbPercent, "None"), orDefault(v.tempdbMB, "None"))
				}
				return row
			}

			selectedRow := propsheet.Static("Name", "")
			importanceRow := propsheet.Select("Importance", rgImportanceItems, 1)
			grantRow := propsheet.Text("Max memory grant %", "25", 10)
			grantRow.SetValidate(rgFloatValidator(0, 100, !fractional, false))
			cpuRow := propsheet.Int("Max CPU time per request", 0, 0, 2147483647, "sec")
			timeoutRow := propsheet.Int("Memory grant timeout", 0, 0, 2147483647, "sec")
			dopRow := propsheet.Int("Maximum DOP", 0, 0, 64, "")
			requestsRow := propsheet.Int("Maximum requests", 0, 0, 2147483647, "")
			moveRow := propsheet.Select("Uses resource pool", movable, 0)
			externalRow := propsheet.Select("Uses external pool", externalNames, 0)
			tempdbPctRow := propsheet.Text("Tempdb data limit %", "", 10)
			tempdbPctRow.SetValidate(rgFloatValidator(0, 100, false, true))
			tempdbMBRow := propsheet.Text("Tempdb data limit (MB)", "", 14)
			tempdbMBRow.SetValidate(rgFloatValidator(0, 1e15, false, true))
			textRows := []*propsheet.TextRow{grantRow, cpuRow, timeoutRow, dopRow, requestsRow}
			if tempdb {
				textRows = append(textRows, tempdbPctRow, tempdbMBRow)
			}
			selectRows := []*propsheet.SelectRow{importanceRow, moveRow, externalRow}

			// valid reads a detail row's value only when it validates; a value it would
			// refuse leaves the group as it was.
			valid := func(row *propsheet.TextRow) (string, bool) {
				return row.Value(), row.Validate() == nil
			}
			intOf := func(row *propsheet.TextRow, dst *int) {
				if n, err := row.IntValue(); err == nil && row.Validate() == nil {
					*dst = int(n)
				}
			}
			commit := func(e *rgGroupEdit) {
				if e.locked {
					return
				}
				v := &e.cur
				v.importance = importanceRow.Value()
				if s, ok := valid(grantRow); ok {
					v.grant, _ = strconv.ParseFloat(s, 64)
				}
				intOf(cpuRow, &v.cpuSec)
				intOf(timeoutRow, &v.timeoutSec)
				intOf(dopRow, &v.maxDOP)
				intOf(requestsRow, &v.maxRequests)
				v.pool = moveRow.Value()
				v.external = externalRow.Value()
				if !tempdb {
					return
				}
				if s, ok := valid(tempdbPctRow); ok {
					v.tempdbPercent = s
				}
				if s, ok := valid(tempdbMBRow); ok {
					v.tempdbMB = s
				}
			}
			show := func(e *rgGroupEdit) {
				locked := e == nil || e.locked
				for _, row := range textRows {
					row.SetReadOnly(locked)
				}
				for _, row := range selectRows {
					row.SetReadOnly(locked)
				}
				if e == nil {
					// Nothing selected (a pool with no groups): blank, not the last group's settings
					// under no name.
					selectedRow.SetValue("")
					for _, row := range textRows {
						row.SetValue("")
					}
					return
				}
				v := e.cur
				selectedRow.SetValue(rgEditName(e.name, e.system, e.IsNew))
				items, i := propsheet.PreservingItems(rgImportanceItems, v.importance)
				importanceRow.SetItems(items)
				importanceRow.SetSelected(i)
				grantRow.SetValue(grantPercentText(v.grant))
				cpuRow.SetValue(strconv.Itoa(v.cpuSec))
				timeoutRow.SetValue(strconv.Itoa(v.timeoutSec))
				dopRow.SetValue(strconv.Itoa(v.maxDOP))
				requestsRow.SetValue(strconv.Itoa(v.maxRequests))
				items, i = propsheet.PreservingItems(movable, v.pool)
				moveRow.SetItems(items)
				moveRow.SetSelected(i)
				items, i = propsheet.PreservingItems(externalNames, v.external)
				externalRow.SetItems(items)
				externalRow.SetSelected(i)
				tempdbPctRow.SetValue(v.tempdbPercent)
				tempdbMBRow.SetValue(v.tempdbMB)
			}

			var list *propsheet.ListEditor[*rgGroupEdit]
			// warnRemoved says so when the pool listed is one the Resource Pools page
			// removes: its groups must move out first.
			warnRemoved := func() {
				if shown := poolRow.Value(); !slices.Contains(poolNames, shown) {
					list.Hint().Set(shown + " is removed on the Resource Pools page; move its groups to another pool.")
				}
			}
			refreshPools := func() {
				list.Commit()
				readModel()
				shown, row := poolRow.Value(), list.Grid().SelectedRow()
				listed := rgListedPools(poolNames, edits.All())
				poolRow.SetItems(listed)
				if i := slices.Index(listed, shown); i >= 0 {
					poolRow.SetSelected(i)
				} else {
					poolRow.SetSelected(max(slices.Index(listed, "default"), 0))
					row = 0
				}
				list.Reselect(max(min(row, len(visible())-1), 0))
				warnRemoved()
			}
			list = propsheet.NewListEditor(propsheet.ListEditorSpec[*rgGroupEdit]{
				Noun:       "workload group",
				NameLabel:  "New workload group",
				NameWidth:  24,
				Headers:    headers,
				GridHeight: 9,
				Items:      visible,
				Cells:      cells,
				Initial:    func(e *rgGroupEdit) bool { return e.name == focus.group },
				Show:       show,
				Commit:     commit,
				Add: func(name string) (string, bool) {
					pool := poolRow.Value()
					switch {
					case pool == "internal":
						return "The internal pool takes no workload groups; pick another pool above.", false
					case edits.Taken(name):
						// Names are unique server-wide, not per pool.
						return "A workload group named " + name + " already exists.", false
					}
					def := rgGroupDefaults(pool)
					edits.Add(&rgGroupEdit{name: name, orig: def, cur: def})
					return "", true
				},
				Remove: func(e *rgGroupEdit) (string, bool) {
					if e.system {
						return e.name + " is built in and cannot be removed.", false
					}
					edits.Remove(e)
					return "", true
				},
				Dirty: edits.Dirty,
				Revert: func() {
					edits.Revert()
					// A reverted group may be back in a pool no longer listed.
					refreshPools()
				},
			})

			// Listing another pool, and moving the selected group to one, both change which
			// groups the grid holds.
			poolRow.SetOnChange(func(string) {
				list.Commit()
				list.Hint().Clear()
				list.Reselect(0)
				warnRemoved()
			})
			moveRow.SetOnChange(func(to string) {
				cur := list.Current()
				if cur == nil {
					return
				}
				i := list.Grid().SelectedRow()
				list.Commit()
				list.Hint().Set(cur.name + " moves to " + to + " on Apply; it is listed under that pool now.")
				list.Reselect(min(i, len(visible())-1))
			})

			rows := []propsheet.Row{
				propsheet.Section("Workload groups"),
				poolRow,
				list.GridRow(),
				propsheet.Section("Selected workload group"),
				selectedRow,
				importanceRow, grantRow, cpuRow, timeoutRow, dopRow, requestsRow,
				moveRow, externalRow,
			}
			if tempdb {
				rows = append(rows, tempdbPctRow, tempdbMBRow,
					propsheet.Note("Tempdb data limits: blank is no limit."))
			}
			rows = append(rows, list.AddRemoveRows()...)
			rows = append(rows,
				propsheet.Note("A new group is added to the pool listed above. internal accepts no changes; default and internal cannot be removed. 0 means unlimited, or the server's own value, for CPU time, timeout, DOP and requests."),
				propsheet.Note("Pools added on the Resource Pools and External Pools pages are offered here at once."),
			)

			// The pool pages' edits change what is listed and offered. The pool listed stays
			// listed while it is still offered, or holds a group.
			model.pools.onChange(func() { refreshPools() })
			model.externals.onChange(func() { refreshPools() })

			apply := func(ctx context.Context) error {
				plan, err := rgPlanFrom(ctx)
				if err != nil {
					return err
				}
				for _, e := range edits.All() {
					name := e.name
					switch {
					case e.IsNew:
						o, err := e.options(rgGroupDefaults(e.cur.pool))
						if err != nil {
							return fmt.Errorf("workload group %s: %w", name, err)
						}
						plan.add(rgPhaseCreateGroups, func(ctx context.Context) error {
							_, err := sc.Server.CreateWorkloadGroup(ctx, gosmo.CreateWorkloadGroupRequest{Name: name, Options: o})
							return err
						})
					case e.Removing:
						plan.add(rgPhaseDropGroups, func(ctx context.Context) error {
							return sc.Server.WorkloadGroupRef(name).Drop(ctx)
						})
					case e.cur != e.orig:
						o, err := e.options(e.orig)
						if err != nil {
							return fmt.Errorf("workload group %s: %w", name, err)
						}
						plan.add(rgPhaseAlterGroups, func(ctx context.Context) error {
							return sc.Server.WorkloadGroupRef(name).Alter(ctx, o)
						})
					}
				}
				return nil
			}
			return list.NewForm(rows...), apply, nil
		},
	}
}
