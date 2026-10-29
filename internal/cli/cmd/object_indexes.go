package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// object indexes' --fragmentation, off unless given (D5): the read scans.
const optFragmentation = "fragmentation"

// tableOrViewArg is the object argument of the commands that read a table's
// or an indexed view's storage.
var tableOrViewArg = spec.Arg{Name: "object", Help: "The table or view: schema.name, or a name alone when one schema has it",
	Format: "schema.name", Required: true}

func (d Deps) objectIndexesCommand() *spec.Command {
	return &spec.Command{
		Path:    []string{"object", "indexes"},
		Summary: "List a table's or indexed view's indexes with their columns and usage",
		Description: "One row per index in index_id order, a heap first as a HEAP row with no name: type, key and " +
			"included columns, filter, and the user seeks, scans, lookups and updates the server has counted since " +
			"it started (sys.dm_db_index_usage_stats) with the last time of each. Reading the usage needs VIEW " +
			"SERVER STATE (VIEW SERVER PERFORMANCE STATE on SQL Server 2022 and later); without it the usage " +
			"columns are NULL with a warning. --fragmentation adds each index's fragmentation, page and fragment " +
			"counts from sys.dm_db_index_physical_stats in that mode — it reads the index, so it is off by default: " +
			"limited reads the pages above the leaf and has no page fullness; sampled and detailed read the leaf.",
		Args: []spec.Arg{tableOrViewArg},
		Options: append(connectOptions(),
			spec.Option{Name: optFragmentation, Type: spec.TypeEnum, Enum: []string{"limited", "sampled", "detailed"},
				Help: "Also read fragmentation, scanning in this mode"},
		),
		Class: spec.ClassRead,
		Run:   d.objectIndexes,
		Examples: []string{
			"gossms object indexes -p dev Sales.SalesOrderDetail",
			"gossms object indexes -p dev Sales.SalesOrderDetail --fragmentation limited -o json",
		},
	}
}

func (d Deps) objectStatsCommand() *spec.Command {
	return &spec.Command{
		Path:    []string{"object", "stats"},
		Summary: "List a table's or indexed view's statistics and when each was last updated",
		Description: "One row per statistics object, by name: its columns, origin (index — the statistic an " +
			"index keeps; auto — created by the optimizer; user — CREATE STATISTICS), filter, last update, the " +
			"rows in the table then and how many were sampled, the histogram steps, and the modifications counted " +
			"since. A statistic never computed — or one the login may see but not read — has those columns NULL.",
		Args:    []spec.Arg{tableOrViewArg},
		Options: connectOptions(),
		Class:   spec.ClassRead,
		Run:     d.objectStats,
		Examples: []string{
			"gossms object stats -p dev Sales.SalesOrderDetail",
			"gossms object stats -p dev Sales.SalesOrderDetail -o json",
		},
	}
}

// tableOrView is what object indexes and stats read from: exactly one is set.
type tableOrView struct {
	table *gosmo.Table
	view  *gosmo.View
}

// lookupTableOrView is lookupObject for the commands that take a table or a
// view: anything else is a usage error naming its kind. The caller closes the
// connection through the returned func.
func (d Deps) lookupTableOrView(ctx context.Context, inv *spec.Invocation, res *output.Result, what string) (Catalog, *gosmo.ObjectInfo, tableOrView, func(), error) {
	cat, dbase, o, done, err := d.lookupObject(ctx, inv, res)
	if err != nil {
		return nil, nil, tableOrView{}, nil, err
	}
	var tv tableOrView
	switch kind := kindOfDesc(o.TypeDesc); kind {
	case kindTable:
		tv.table, err = cat.TableByName(ctx, dbase, o.Schema, o.Name)
	case kindView:
		tv.view, err = cat.ViewByName(ctx, dbase, o.Schema, o.Name)
	default:
		done()
		return nil, nil, tableOrView{}, nil, usageError(
			fmt.Sprintf("%s is a %s, which has no %s", gosmo.QualifiedName(o.Schema, o.Name), kind, what),
			"Give a table or a view; `gossms object describe` shows any object.")
	}
	if err != nil {
		done()
		return nil, nil, tableOrView{}, nil, readError(err)
	}
	return cat, o, tv, done, nil
}

// cellTime is a time cell, NULL for the zero time (never happened, or not
// read).
func cellTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	return ptr(gridTime(t))
}

func int64Cell(n int64) *string { return ptr(strconv.FormatInt(n, 10)) }

func (d Deps) objectIndexes(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	cat, o, tv, done, err := d.lookupTableOrView(ctx, inv, res, "indexes")
	if err != nil {
		return res, err
	}
	defer done()

	mode := gosmo.FragmentationMode(strings.ToUpper(inv.Values[optFragmentation].(string)))
	var (
		idx   []*gosmo.Index
		usage []*gosmo.IndexUsage
		frag  []*gosmo.IndexFragmentation
	)
	if tv.table != nil {
		if idx, err = cat.TableIndexes(ctx, tv.table); err == nil {
			if usage, err = cat.TableIndexUsage(ctx, tv.table); err == nil && mode != "" {
				frag, err = cat.TableFragmentation(ctx, tv.table, mode)
			}
		}
	} else {
		if idx, err = cat.ViewIndexes(ctx, tv.view); err == nil {
			if usage, err = cat.ViewIndexUsage(ctx, tv.view); err == nil && mode != "" {
				frag, err = cat.ViewFragmentation(ctx, tv.view, mode)
			}
		}
	}
	if err != nil {
		return res, readError(err)
	}

	usageByID := map[int]*gosmo.IndexUsage{}
	visible := true
	for _, u := range usage {
		usageByID[u.IndexID] = u
		visible = visible && u.UsageVisible
	}
	if !visible {
		res.Warnings = append(res.Warnings, "The login may not read index usage (it needs VIEW SERVER STATE, or VIEW "+
			"SERVER PERFORMANCE STATE on SQL Server 2022 and later): the usage columns are NULL.")
	}
	fragByID := map[int]*gosmo.IndexFragmentation{}
	for _, f := range frag {
		fragByID[f.IndexID] = f
	}

	cols := []output.Column{
		{Name: "name", Type: "sysname", Nullable: true}, {Name: "index_id", Type: "int"}, {Name: "type", Type: "nvarchar(60)"},
		{Name: "unique", Type: "bit"}, {Name: "primary_key", Type: "bit"},
		{Name: "key_columns", Type: "nvarchar(max)", Nullable: true}, {Name: "included_columns", Type: "nvarchar(max)", Nullable: true},
		{Name: "filter", Type: "nvarchar(max)", Nullable: true}, {Name: "disabled", Type: "bit"}, {Name: "hypothetical", Type: "bit"},
		{Name: "user_seeks", Type: "bigint", Nullable: true}, {Name: "user_scans", Type: "bigint", Nullable: true},
		{Name: "user_lookups", Type: "bigint", Nullable: true}, {Name: "user_updates", Type: "bigint", Nullable: true},
		{Name: "last_user_seek", Type: "datetime", Nullable: true}, {Name: "last_user_scan", Type: "datetime", Nullable: true},
		{Name: "last_user_lookup", Type: "datetime", Nullable: true}, {Name: "last_user_update", Type: "datetime", Nullable: true},
	}
	if mode != "" {
		cols = append(cols,
			output.Column{Name: "avg_fragmentation_pct", Type: "decimal(5,2)", Nullable: true},
			output.Column{Name: "page_count", Type: "bigint", Nullable: true},
			output.Column{Name: "fragment_count", Type: "bigint", Nullable: true},
			output.Column{Name: "avg_page_space_used_pct", Type: "decimal(5,2)", Nullable: true})
	}
	set := output.Set{Name: "indexes", Columns: cols}
	row := func(base []*string, id int) []*string {
		u := usageByID[id]
		if u != nil && u.UsageVisible {
			base = append(base, int64Cell(u.UserSeeks), int64Cell(u.UserScans), int64Cell(u.UserLookups), int64Cell(u.UserUpdates),
				cellTime(u.LastUserSeek), cellTime(u.LastUserScan), cellTime(u.LastUserLookup), cellTime(u.LastUserUpdate))
		} else {
			// Not read, or no usage row at all: a hypothetical index has none.
			base = append(base, make([]*string, 8)...)
		}
		if mode != "" {
			if f := fragByID[id]; f != nil {
				// LIMITED does not read the leaf, so it has no page fullness.
				var used *string
				if mode != gosmo.FragmentationLimited {
					used = ptr(mbf(f.AvgPageSpaceUsedPct))
				}
				base = append(base, ptr(mbf(f.AvgFragmentationPct)), int64Cell(f.PageCount), int64Cell(f.FragmentCount), used)
			} else {
				base = append(base, make([]*string, 4)...)
			}
		}
		return base
	}
	// A heap is index 0: sys.indexes has it, the index list (type > 0) not.
	if u := usageByID[0]; u != nil {
		set.Rows = append(set.Rows, row([]*string{nil, ptr("0"), ptr("HEAP"), bitCell(false), bitCell(false),
			nil, nil, nil, bitCell(false), bitCell(false)}, 0))
	}
	for _, i := range idx {
		set.Rows = append(set.Rows, row([]*string{ptr(i.Name), ptr(strconv.Itoa(i.IndexID)), ptr(string(i.Type)),
			bitCell(i.IsUnique), bitCell(i.IsPrimaryKey), nonEmpty(indexColumns(i.KeyColumns)),
			nonEmpty(indexColumns(i.IncludedColumns)), nonEmpty(i.FilterDefinition), bitCell(i.IsDisabled),
			bitCell(i.IsHypothetical)}, i.IndexID))
	}
	set.TotalRowCount = len(set.Rows)
	res.Fields = objectFields(o)
	if mode != "" {
		res.Fields = append(res.Fields, output.Field{Key: "fragmentationMode", Value: string(mode)})
	}
	res.Sets = []output.Set{set}
	return res, nil
}

// statOrigin is where a statistic came from: an index's, the optimizer's
// (auto-created) or CREATE STATISTICS's.
func statOrigin(st *gosmo.Statistic) string {
	switch {
	case st.IsAutoCreated:
		return "auto"
	case st.IsUserCreated:
		return "user"
	}
	return "index"
}

func (d Deps) objectStats(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	cat, o, tv, done, err := d.lookupTableOrView(ctx, inv, res, "statistics")
	if err != nil {
		return res, err
	}
	defer done()

	var stats []*gosmo.Statistic
	if tv.table != nil {
		stats, err = cat.TableStatistics(ctx, tv.table)
	} else {
		stats, err = cat.ViewStatistics(ctx, tv.view)
	}
	if err != nil {
		return res, readError(err)
	}

	set := output.Set{Name: "statistics", Columns: []output.Column{
		{Name: "name", Type: "sysname"}, {Name: "stats_id", Type: "int"}, {Name: "columns", Type: "nvarchar(max)"},
		{Name: "origin", Type: "nvarchar(10)"}, {Name: "filter", Type: "nvarchar(max)", Nullable: true},
		{Name: "last_updated", Type: "datetime", Nullable: true}, {Name: "rows", Type: "bigint", Nullable: true},
		{Name: "rows_sampled", Type: "bigint", Nullable: true}, {Name: "sample_pct", Type: "decimal(5,2)", Nullable: true},
		{Name: "steps", Type: "int", Nullable: true}, {Name: "modification_counter", Type: "bigint", Nullable: true},
		{Name: "no_recompute", Type: "bit"}, {Name: "incremental", Type: "bit"},
	}}
	unread := 0
	for _, st := range stats {
		cols, err := cat.StatisticColumns(ctx, st)
		if err != nil {
			return res, readError(err)
		}
		// sys.dm_db_stats_properties has no row for a statistic never
		// computed or not readable: gosmo reads zeros, which are not counts.
		props := make([]*string, 6)
		if st.LastUpdated.IsZero() {
			unread++
		} else {
			var pct *string
			if st.TotalRows > 0 {
				pct = ptr(mbf(100 * float64(st.RowsSampled) / float64(st.TotalRows)))
			}
			props = []*string{ptr(gridTime(st.LastUpdated)), int64Cell(st.TotalRows), int64Cell(st.RowsSampled), pct,
				ptr(strconv.Itoa(st.Steps)), int64Cell(st.ModificationCounter)}
		}
		var filter *string
		if st.HasFilter {
			filter = ptr(st.FilterDef)
		}
		r := []*string{ptr(st.Name), ptr(strconv.Itoa(st.StatID)), ptr(strings.Join(cols, ", ")), ptr(statOrigin(st)), filter}
		r = append(r, props...)
		set.Rows = append(set.Rows, append(r, bitCell(st.NoRecompute), bitCell(st.IsIncremental)))
	}
	set.TotalRowCount = len(set.Rows)
	if unread > 0 {
		// The catalog cannot tell the two apart: a login the DMF refuses
		// gets no row for any statistic.
		res.Warnings = append(res.Warnings, fmt.Sprintf("%d of %d statistics show no properties: never computed, "+
			"or the login may not read them (sys.dm_db_stats_properties needs SELECT on the columns, or "+
			"ownership of the table).", unread, len(stats)))
	}
	res.Fields = objectFields(o)
	res.Sets = []output.Set{set}
	return res, nil
}
