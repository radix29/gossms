package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/db"
)

// maint index-fragmentation (read), index-rebuild and update-stats (ddl, as
// query run classes ALTER INDEX, UPDATE STATISTICS and sp_updatestats: D1),
// D7. Each works in the connection's database (-d): a --database option
// would clash with it, as phase 7 found.

const (
	optMinPercent    = "min-percent"
	optMinPages      = "min-pages"
	optMode          = "mode"
	optIndex         = "index"
	optReorganize    = "reorganize"
	optOnline        = "online"
	optMaxDOP        = "maxdop"
	optFillFactor    = "fill-factor"
	optFullScan      = "fullscan"
	optSamplePercent = "sample-percent"
	optResample      = "resample"
	optStatistic     = "statistic"
)

// index-fragmentation's defaults: Microsoft's guidance ignores an index under
// about 1,000 pages, and fragmentation under 10 % rarely matters.
const (
	defaultMinFragmentation = 10
	defaultMinPages         = 1000
	defaultFragmentationTop = 50
)

// fragmentationModes is --mode's enum (sys.dm_db_index_physical_stats's
// scan modes), the cheapest first.
var fragmentationModes = []string{"limited", "sampled", "detailed"}

// The fragmentation suggestion thresholds: Microsoft's long-standing
// guideline (reorganize from 5 to 30 %, rebuild above), advice only.
const (
	reorganizeFromPct = 5
	rebuildAbovePct   = 30
)

// maxDOPLimit is the largest MAXDOP the index and DBCC options take.
const maxDOPLimit = 64

func maintIndexCommands(d Deps) []*spec.Command {
	return []*spec.Command{
		{
			Path:    []string{"maint", "index-fragmentation"},
			Summary: "List the fragmented indexes of the connection's database, the most fragmented first",
			Description: "One read of sys.dm_db_index_physical_stats for the whole database (-d, the connection's), " +
				"filtered on the server: the indexes of user tables and indexed views at least --min-percent " +
				"fragmented (default 10) whose leaf has at least --min-pages pages (default 1000: a small index's " +
				"percentage means nothing). Heaps are left out, and a partitioned index's partitions are folded into " +
				"one row (partitions counts them). --mode limited (default) reads only the pages above the leaf and " +
				"has no page fullness (avg_page_space_used_pct NULL); sampled and detailed read the leaf too, detailed " +
				"every page — slow on a large database. suggestion is Microsoft's guideline, advice only: reorganize " +
				"from 5 to 30 %, rebuild above (maint index-rebuild does either). --schema keeps one schema " +
				"(case-insensitive), --top the most fragmented rows (default 50, 0 for all). Reading every object " +
				"needs VIEW DATABASE STATE (exit 9 without).",
			Options: append(ConnectOptions(),
				spec.Option{Name: optMinPercent, Type: spec.TypeInt, Default: strconv.Itoa(defaultMinFragmentation),
					Help: "Only indexes at least this fragmented, in percent (0-100)"},
				spec.Option{Name: optMinPages, Type: spec.TypeInt, Default: strconv.Itoa(defaultMinPages),
					Help: "Only indexes whose leaf has at least this many pages"},
				spec.Option{Name: optMode, Type: spec.TypeEnum, Enum: fragmentationModes, Default: fragmentationModes[0],
					Help: "Scan mode: limited (fast, no page fullness), sampled, detailed (reads every page)"},
				spec.Option{Name: optSchema, Type: spec.TypeString, Help: "Only indexes in this schema (case-insensitive)"},
				spec.Option{Name: optTop, Type: spec.TypeInt, Default: strconv.Itoa(defaultFragmentationTop),
					Help: "Keep this many indexes, the most fragmented first; 0 for all"},
			),
			Class: spec.ClassRead,
			Run:   d.maintIndexFragmentation,
			Examples: []string{
				"gossms maint index-fragmentation -p prod-ro -d AdventureWorks --min-percent 30",
				"gossms maint index-fragmentation -p dev -d AdventureWorks --mode sampled --schema Sales -o json",
			},
		},
		{
			Path:    []string{"maint", "index-rebuild"},
			Summary: "Rebuild or reorganize a table's or indexed view's indexes: one, or all of them",
			Description: "ALTER INDEX … REBUILD (or REORGANIZE with --reorganize) on the object in the connection's " +
				"database (-d): the index --index names, or every index on it (ALTER INDEX ALL). Needs --allow ddl " +
				"(query run classes ALTER INDEX so too) and ALTER on the object. A rebuild takes --online (the " +
				"object stays readable and writable; Enterprise, Developer and Azure only — another edition is exit " +
				"11 before anything is sent), --maxdop n (1-64) and --fill-factor n (1-100, stored as the index's " +
				"new fill factor); a reorganize takes none of them (exit 2): it is always online, and lighter. " +
				"There is no automatic choice between the two — maint index-fragmentation suggests one. The record " +
				"lists the indexes touched with their fragmentation before (a LIMITED scan, dry run too) and, after " +
				"a real run, after. An offline rebuild locks the object for its duration; --dry-run prints the " +
				"statement. A heap is not an index: an object with none is exit 2.",
			Args: []spec.Arg{tableOrViewArg},
			Options: slices.Concat(ConnectOptions(), writeOptions(), []spec.Option{
				{Name: optIndex, Type: spec.TypeString, Help: "The index to rebuild; default every index on the object"},
				{Name: optReorganize, Type: spec.TypeBool, Help: "REORGANIZE instead of REBUILD (always online; no other option)"},
				{Name: optOnline, Type: spec.TypeBool, Help: "ONLINE = ON: keep the object usable (Enterprise, Developer, Azure)"},
				{Name: optMaxDOP, Type: spec.TypeInt, Help: "MAXDOP = n (1-64): cap the rebuild's parallelism"},
				{Name: optFillFactor, Type: spec.TypeInt, Help: "FILLFACTOR = n (1-100): the leaf pages' fill, stored with the index"},
			}),
			Class: spec.ClassDDL,
			Run:   d.maintIndexRebuild,
			Examples: []string{
				"gossms maint index-rebuild Sales.SalesOrderDetail --online -p dev --allow ddl --dry-run",
				"gossms maint index-rebuild Sales.SalesOrderDetail --index IX_SalesOrderDetail_ProductID --reorganize -p dev --allow ddl",
			},
		},
		{
			Path:    []string{"maint", "update-stats"},
			Summary: "Update a table's or indexed view's statistics, or the database's stale ones (sp_updatestats)",
			Description: "With <object>: UPDATE STATISTICS on it in the connection's database (-d) — every statistic, " +
				"or the one --statistic names (an index's statistic has the index's name; object stats lists them). " +
				"Sampling: --fullscan reads every row, --sample-percent n that share, --resample each statistic's last " +
				"rate; none of them lets the server choose (a bare UPDATE STATISTICS). Needs ALTER on the object. " +
				"The set lists the statistics with their last update — after the update in a real run, with the " +
				"previous one beside it. With --all: sys.sp_updatestats over the whole database, which updates only " +
				"the statistics with rows modified since their last update, at the server's sample (--resample: each " +
				"one's last rate); it needs db_owner or sysadmin. --all with --fullscan or --sample-percent is exit " +
				"2: a full scan of every table is a choice to make per table. Needs --allow ddl (query run classes " +
				"UPDATE STATISTICS and sp_updatestats so too); --dry-run prints the statement.",
			Args: []spec.Arg{{Name: "object", Help: "The table or view: schema.name, or a name alone when one schema has it; or --all",
				Format: "schema.name"}},
			Options: slices.Concat(ConnectOptions(), writeOptions(), []spec.Option{
				{Name: optAll, Type: spec.TypeBool, Help: "The database's stale statistics (sp_updatestats) instead of <object>'s"},
				{Name: optStatistic, Type: spec.TypeString, Help: "Only this statistic of <object> (an index's is named as the index)"},
				{Name: optFullScan, Type: spec.TypeBool, Help: "WITH FULLSCAN: read every row"},
				{Name: optSamplePercent, Type: spec.TypeInt, Help: "WITH SAMPLE n PERCENT (1-100)"},
				{Name: optResample, Type: spec.TypeBool, Help: "WITH RESAMPLE: each statistic's last sample rate"},
			}),
			Class: spec.ClassDDL,
			Run:   d.maintUpdateStats,
			Examples: []string{
				"gossms maint update-stats Sales.SalesOrderDetail --fullscan -p dev --allow ddl",
				"gossms maint update-stats --all -p dev --allow ddl --dry-run",
			},
		},
	}
}

// --- maint index-fragmentation ----------------------------------------------

// fragmentationSuggestion is Microsoft's guideline for pct: none below 5 %,
// reorganize to 30 %, rebuild above.
func fragmentationSuggestion(pct float64) string {
	switch {
	case pct > rebuildAbovePct:
		return "rebuild"
	case pct >= reorganizeFromPct:
		return "reorganize"
	}
	return "none"
}

func (d Deps) maintIndexFragmentation(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	minPct, minPages, top := inv.Values[optMinPercent].(int), inv.Values[optMinPages].(int), inv.Values[optTop].(int)
	switch {
	case minPct < 0 || minPct > 100:
		return nil, usageError(fmt.Sprintf("--min-percent: %d is not a percentage", minPct), "Give 0 to 100.")
	case minPages < 0:
		return nil, usageError(fmt.Sprintf("--min-pages: %d is negative", minPages), "Give 0 for every index, whatever its size.")
	case top < 0:
		return nil, usageError(fmt.Sprintf("--top: %d is negative", top), "Give the number of indexes to keep, or --top 0 for all.")
	}
	mode := gosmo.FragmentationMode(strings.ToUpper(inv.Values[optMode].(string)))
	res := &output.Result{}
	sc, cat, dbase, err := d.connectDatabase(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	rows, err := cat.DatabaseFragmentation(ctx, dbase, mode, float64(minPct), int64(minPages))
	if err != nil {
		e := readError(err)
		if e.Code == output.CodePermissionDenied {
			e.Hint = "Reading every index's fragmentation needs VIEW DATABASE STATE in the database: have it granted, " +
				"or read one object's with object indexes --fragmentation (CONTROL on it is enough)."
		}
		return res, e
	}
	if schema, _ := inv.Values[optSchema].(string); schema != "" {
		rows = slices.DeleteFunc(rows, func(f *gosmo.DatabaseIndexFragmentation) bool { return !strings.EqualFold(f.Schema, schema) })
	}
	set := fragmentationSet(rows, mode)
	if top > 0 && len(set.Rows) > top {
		set.Rows, set.Truncated = set.Rows[:top], true
	}
	res.Fields = []output.Field{
		{Key: "mode", Value: string(mode)},
		{Key: "minPercent", Value: strconv.Itoa(minPct)},
		{Key: "minPages", Value: strconv.Itoa(minPages)},
	}
	res.Sets = []output.Set{set}
	return res, nil
}

// fragmentationSet is index-fragmentation's set, in gosmo's order (most
// fragmented first). LIMITED reads no leaf, so it has no page fullness.
func fragmentationSet(rows []*gosmo.DatabaseIndexFragmentation, mode gosmo.FragmentationMode) output.Set {
	set := output.Set{Name: "indexes", Columns: []output.Column{
		{Name: "schema", Type: "sysname"}, {Name: "object", Type: "sysname"}, {Name: "object_type", Type: "nvarchar(10)"},
		{Name: "index", Type: "sysname"}, {Name: "index_id", Type: "int"}, {Name: "type", Type: "nvarchar(60)"},
		{Name: "partitions", Type: "int"}, {Name: "avg_fragmentation_pct", Type: "decimal(5,2)"},
		{Name: "page_count", Type: "bigint"}, {Name: "fragment_count", Type: "bigint"},
		{Name: "avg_page_space_used_pct", Type: "decimal(5,2)", Nullable: true}, {Name: "suggestion", Type: "nvarchar(10)"},
	}}
	for _, f := range rows {
		kind := kindTable
		if f.IsView {
			kind = kindView
		}
		var used *string
		if mode != gosmo.FragmentationLimited {
			used = ptr(mbf(f.AvgPageSpaceUsedPct))
		}
		set.Rows = append(set.Rows, []*string{ptr(f.Schema), ptr(f.ObjectName), ptr(kind), ptr(f.IndexName),
			ptr(strconv.Itoa(f.IndexID)), ptr(string(f.IndexType)), ptr(strconv.Itoa(cmp.Or(f.PartitionCount, 1))),
			ptr(mbf(f.AvgFragmentationPct)), int64Cell(f.PageCount), int64Cell(f.FragmentCount), used,
			ptr(fragmentationSuggestion(f.AvgFragmentationPct))})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// --- The writes' object lookup ------------------------------------------------

// connectTableOrView is lookupTableOrView for a writing command: the gate,
// the connection with its writes, then inv's object argument resolved in the
// connection's database as a table or a view. The caller closes sc.
func (d Deps) connectTableOrView(ctx context.Context, inv *spec.Invocation, res *output.Result, what string) (*db.ServerConn, Catalog, Writer, *gosmo.ObjectInfo, tableOrView, error) {
	sc, cat, w, err := d.connectWriter(ctx, inv, res)
	if err != nil {
		return nil, nil, nil, nil, tableOrView{}, err
	}
	o, tv, err := resolveTableOrView(ctx, cat, inv.Args[0], res, what)
	if err != nil {
		sc.Close()
		return nil, nil, nil, nil, tableOrView{}, err
	}
	return sc, cat, w, o, tv, nil
}

// resolveTableOrView is arg resolved in the connection's database as a table
// or a view, read by name; another kind is a usage error naming it.
func resolveTableOrView(ctx context.Context, cat Catalog, arg string, res *output.Result, what string) (*gosmo.ObjectInfo, tableOrView, error) {
	dbase, err := currentDatabase(ctx, cat, res)
	if err != nil {
		return nil, tableOrView{}, err
	}
	n, err := parseObjectName(arg, dbase.Name)
	if err != nil {
		return nil, tableOrView{}, err
	}
	o, err := resolveObject(ctx, cat, dbase, n)
	if err != nil {
		return nil, tableOrView{}, err
	}
	tv, err := tableOrViewOf(ctx, cat, dbase, o, what)
	return o, tv, err
}

// tableOrViewOf reads o, found in dbase, as the table or view it is; another
// kind is a usage error saying it has no what.
func tableOrViewOf(ctx context.Context, cat Catalog, dbase *gosmo.Database, o *gosmo.ObjectInfo, what string) (tableOrView, error) {
	var tv tableOrView
	var err error
	switch kind := kindOfDesc(o.TypeDesc); kind {
	case kindTable:
		tv.table, err = cat.TableByName(ctx, dbase, o.Schema, o.Name)
	case kindView:
		tv.view, err = cat.ViewByName(ctx, dbase, o.Schema, o.Name)
	default:
		return tv, usageError(fmt.Sprintf("%s is a %s, which has no %s", gosmo.QualifiedName(o.Schema, o.Name), kind, what),
			"Give a table or a view; `gossms object describe` shows any object.")
	}
	if err != nil {
		return tv, readError(err)
	}
	return tv, nil
}

// indexes is tv's indexes, read through cat.
func (tv tableOrView) indexes(ctx context.Context, cat Catalog) ([]*gosmo.Index, error) {
	if tv.table != nil {
		return cat.TableIndexes(ctx, tv.table)
	}
	return cat.ViewIndexes(ctx, tv.view)
}

// fragmentation is tv's indexes' fragmentation in a LIMITED scan, by index
// id.
func (tv tableOrView) fragmentation(ctx context.Context, cat Catalog) (map[int]*gosmo.IndexFragmentation, error) {
	var frag []*gosmo.IndexFragmentation
	var err error
	if tv.table != nil {
		frag, err = cat.TableFragmentation(ctx, tv.table, gosmo.FragmentationLimited)
	} else {
		frag, err = cat.ViewFragmentation(ctx, tv.view, gosmo.FragmentationLimited)
	}
	if err != nil {
		return nil, err
	}
	out := make(map[int]*gosmo.IndexFragmentation, len(frag))
	for _, f := range frag {
		out[f.IndexID] = f
	}
	return out, nil
}

// statistics is tv's statistics, read through cat.
func (tv tableOrView) statistics(ctx context.Context, cat Catalog) ([]*gosmo.Statistic, error) {
	if tv.table != nil {
		return cat.TableStatistics(ctx, tv.table)
	}
	return cat.ViewStatistics(ctx, tv.view)
}

// --- maint index-rebuild ------------------------------------------------------

// rebuildRequest is index-rebuild's options, checked before connecting.
type rebuildRequest struct {
	index      string // "" for every index
	reorganize bool
	opts       gosmo.IndexRebuildOptions
}

func parseRebuild(inv *spec.Invocation) (rebuildRequest, error) {
	r := rebuildRequest{reorganize: inv.Values[optReorganize].(bool)}
	if inv.Given[optIndex] {
		if r.index = inv.Values[optIndex].(string); strings.TrimSpace(r.index) == "" {
			return r, usageError("--index: empty name", "Name the index (object indexes lists them), or leave --index out for every index.")
		}
	}
	r.opts.Online = inv.Values[optOnline].(bool)
	if inv.Given[optMaxDOP] {
		if r.opts.MaxDOP = inv.Values[optMaxDOP].(int); r.opts.MaxDOP < 1 || r.opts.MaxDOP > maxDOPLimit {
			return r, usageError(fmt.Sprintf("--maxdop: %d is not 1 to %d", r.opts.MaxDOP, maxDOPLimit),
				"Leave --maxdop out for the server's own setting.")
		}
	}
	if inv.Given[optFillFactor] {
		if r.opts.FillFactor = inv.Values[optFillFactor].(int); r.opts.FillFactor < 1 || r.opts.FillFactor > 100 {
			return r, usageError(fmt.Sprintf("--fill-factor: %d is not 1 to 100", r.opts.FillFactor),
				"Leave --fill-factor out to keep each index's own.")
		}
	}
	if r.reorganize {
		var with []string
		for _, o := range []string{optOnline, optMaxDOP, optFillFactor} {
			if inv.Given[o] {
				with = append(with, "--"+o)
			}
		}
		if len(with) > 0 {
			return r, usageError("--reorganize takes no "+strings.Join(with, ", "),
				"A reorganize is always online and takes no options: leave them out, or rebuild instead.")
		}
	}
	return r, nil
}

// SQL Server's refusals of an index rebuild that maint index-rebuild explains.
const (
	msgOnlineNotSupported = 1712 // Online index operations can only be performed in Enterprise edition.
	msgLockTimeout        = 1222 // Lock request time out period exceeded.
	msgReorganizeDisabled = 1973 // Cannot perform the specified operation on disabled index.
	msgPageLockingOff     = 2552 // The index … cannot be reorganized because page level locking is disabled.
)

func (d Deps) maintIndexRebuild(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	r, err := parseRebuild(inv)
	if err != nil {
		return nil, err
	}
	res := &output.Result{}
	sc, cat, w, o, tv, err := d.connectTableOrView(ctx, inv, res, "indexes")
	if err != nil {
		return res, err
	}
	defer sc.Close()

	if info := cat.ServerInfo(); r.opts.Online && !info.SupportsOnlineIndexOperations() {
		return res, output.NewError(output.CodeUnsupported,
			fmt.Sprintf("this edition (%s) has no online index operations", info.Edition),
			"Leave --online out (the object is locked while the index rebuilds), or --reorganize, which is always online.")
	}
	all, err := tv.indexes(ctx, cat)
	if err != nil {
		return res, readError(err)
	}
	all = slices.DeleteFunc(all, func(i *gosmo.Index) bool { return i.IsHypothetical })
	name := gosmo.QualifiedName(o.Schema, o.Name)
	if len(all) == 0 {
		return res, usageError(name+" has no index to rebuild (a heap)",
			"A heap is rebuilt with ALTER TABLE … REBUILD, through query run.")
	}
	targets := all
	if r.index != "" {
		i := slices.IndexFunc(all, func(i *gosmo.Index) bool { return i.Name == r.index })
		if i < 0 {
			i = slices.IndexFunc(all, func(i *gosmo.Index) bool { return strings.EqualFold(i.Name, r.index) })
		}
		if i < 0 {
			names := make([]string, len(all))
			for j, x := range all {
				names[j] = x.Name
			}
			return res, output.NewError(output.CodeNotFound, fmt.Sprintf("no index %s on %s", gosmo.QuoteName(r.index), name),
				"Its indexes: "+strings.Join(names, ", ")+".")
		}
		targets = all[i : i+1]
		if r.reorganize && targets[0].IsDisabled {
			return res, usageError(fmt.Sprintf("%s is disabled: it has no data to reorganize", gosmo.QuoteName(targets[0].Name)),
				"Rebuild it (leave --reorganize out), which also enables it.")
		}
	}

	before, fragErr := tv.fragmentation(ctx, cat)
	if fragErr != nil {
		res.Warnings = append(res.Warnings, "The fragmentation before was not read: "+readError(fragErr).Message)
	}
	res.Fields = append(objectFields(o), rebuildFields(r, targets)...)
	write := func(ctx context.Context) error {
		switch {
		case r.index != "" && r.reorganize:
			return deniedOnResolved(w.ReorganizeIndex(ctx, targets[0]))
		case r.index != "":
			return deniedOnResolved(w.RebuildIndex(ctx, targets[0], r.opts))
		case r.reorganize:
			return deniedOnResolved(w.ReorganizeAllIndexes(ctx, tv))
		}
		return deniedOnResolved(w.RebuildAllIndexes(ctx, tv, r.opts))
	}
	if err := applyWrite(ctx, inv, res, write); err != nil {
		return res, indexError(err, name)
	}
	var after map[int]*gosmo.IndexFragmentation
	if !dryRun(inv) && fragErr == nil {
		if after, err = tv.fragmentation(ctx, cat); err != nil {
			res.Warnings = append(res.Warnings, "The fragmentation after was not read: "+readError(err).Message)
		}
	}
	res.Sets = []output.Set{rebuildSet(targets, before, after)}
	if !r.reorganize {
		for _, i := range targets {
			if i.IsDisabled {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s is disabled: a rebuild enables it.", gosmo.QuoteName(i.Name)))
			}
		}
	}
	return res, nil
}

// rebuildFields is index-rebuild's record after the object's: what it
// rebuilds and how. index is NULL for every index.
func rebuildFields(r rebuildRequest, targets []*gosmo.Index) []output.Field {
	op := "rebuild"
	if r.reorganize {
		op = "reorganize"
	}
	f := []output.Field{{Key: "index", Null: r.index == ""}}
	if r.index != "" {
		f[0].Value = targets[0].Name
	}
	f = append(f,
		output.Field{Key: "operation", Value: op},
		output.Field{Key: "online", Value: boolText(r.reorganize || r.opts.Online)},
		output.Field{Key: "maxdop", Value: nonZero(r.opts.MaxDOP), Null: r.opts.MaxDOP == 0},
		output.Field{Key: "fillFactor", Value: nonZero(r.opts.FillFactor), Null: r.opts.FillFactor == 0},
	)
	return f
}

// nonZero is n as a field's text, "" for the zero a NULL field carries.
func nonZero(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// rebuildSet is the indexes index-rebuild touches, with their fragmentation
// before and (a real run) after; NULL where unread.
func rebuildSet(targets []*gosmo.Index, before, after map[int]*gosmo.IndexFragmentation) output.Set {
	set := output.Set{Name: "indexes", Columns: []output.Column{
		{Name: "index", Type: "sysname"}, {Name: "index_id", Type: "int"}, {Name: "type", Type: "nvarchar(60)"},
		{Name: "disabled", Type: "bit"},
		{Name: "fragmentation_before_pct", Type: "decimal(5,2)", Nullable: true},
		{Name: "fragmentation_after_pct", Type: "decimal(5,2)", Nullable: true},
		{Name: "page_count", Type: "bigint", Nullable: true},
	}}
	pct := func(m map[int]*gosmo.IndexFragmentation, id int) *string {
		if f := m[id]; f != nil {
			return ptr(mbf(f.AvgFragmentationPct))
		}
		return nil
	}
	for _, i := range targets {
		var pages *string
		if f := cmp.Or(after[i.IndexID], before[i.IndexID]); f != nil {
			pages = int64Cell(f.PageCount)
		}
		set.Rows = append(set.Rows, []*string{ptr(i.Name), ptr(strconv.Itoa(i.IndexID)), ptr(string(i.Type)),
			bitCell(i.IsDisabled), pct(before, i.IndexID), pct(after, i.IndexID), pages})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// deniedOnResolved is a write's err with SQL Server's "does not exist or you
// do not have permission" (1088 for ALTER INDEX and UPDATE STATISTICS, 15247
// for sp_updatestats) read as the refusal it is: the command has just read
// the object or database, so it exists. Any other err is returned as is.
func deniedOnResolved(err error) error {
	se, ok := gosmo.AsSQLError(err)
	if !ok || !gosmo.IsMissingOrDenied(err) {
		return err
	}
	e := sqlError(se)
	e.Code = output.CodePermissionDenied
	return e
}

// indexError is index-rebuild's failure, with a hint for the server's
// refusals it can explain.
func indexError(err error, object string) error {
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.SQL == nil {
		return err
	}
	if e.Code == output.CodePermissionDenied {
		e.Hint = "ALTER INDEX needs ALTER on " + object + " (or db_ddladmin, db_owner). Use a login that has it, or have it granted."
		return e
	}
	switch e.SQL.Number {
	case msgOnlineNotSupported:
		e.Code = output.CodeUnsupported
		e.Hint = "This edition has no online index operations: leave --online out, or --reorganize."
	case msgLockTimeout:
		e.Hint = "Another session holds a lock on the object: retry when it is idle (monitor blocking shows who)."
	case msgReorganizeDisabled:
		e.Hint = "A disabled index has no data to reorganize: rebuild it (without --reorganize), which enables it."
	case msgPageLockingOff:
		e.Hint = "The index has ALLOW_PAGE_LOCKS = OFF, which a reorganize needs: rebuild it instead."
	}
	return e
}

// --- maint update-stats -------------------------------------------------------

// statsRequest is update-stats' options, checked before connecting.
type statsRequest struct {
	object    string // "" with --all
	statistic string
	opts      gosmo.StatisticsUpdateOptions
}

func parseUpdateStats(inv *spec.Invocation) (statsRequest, error) {
	var r statsRequest
	all := inv.Values[optAll].(bool)
	if len(inv.Args) > 0 {
		r.object = inv.Args[0]
	}
	switch {
	case r.object == "" && !all:
		return r, usageError("update-stats: give an <object> or --all", "Name a table or view, or pass --all for the database's stale statistics.")
	case r.object != "" && all:
		return r, usageError("update-stats: give an <object> or --all, not both", "")
	}
	r.opts.FullScan = inv.Values[optFullScan].(bool)
	r.opts.Resample = inv.Values[optResample].(bool)
	if inv.Given[optSamplePercent] {
		if r.opts.SamplePercent = inv.Values[optSamplePercent].(int); r.opts.SamplePercent < 1 || r.opts.SamplePercent > 100 {
			return r, usageError(fmt.Sprintf("--sample-percent: %d is not 1 to 100", r.opts.SamplePercent), "Or --fullscan for every row.")
		}
	}
	var sampling []string
	for _, o := range []string{optFullScan, optSamplePercent, optResample} {
		if inv.Given[o] && (o == optSamplePercent || inv.Values[o].(bool)) {
			sampling = append(sampling, "--"+o)
		}
	}
	if len(sampling) > 1 {
		return r, usageError(strings.Join(sampling, " and ")+" are alternatives", "Choose one way to sample, or none for the server's.")
	}
	if inv.Given[optStatistic] {
		if r.statistic = inv.Values[optStatistic].(string); strings.TrimSpace(r.statistic) == "" {
			return r, usageError("--statistic: empty name", "Name the statistic (object stats lists them), or leave it out for all.")
		}
	}
	if all {
		switch {
		case r.opts.FullScan || r.opts.SamplePercent > 0:
			return r, usageError("--all updates at the server's sample (or --resample): it takes no --fullscan or --sample-percent",
				"A full scan of every table is a choice to make per table: name the object.")
		case r.statistic != "":
			return r, usageError("--statistic is for an <object>", "Name the object it is on.")
		}
	}
	return r, nil
}

// samplingText is how opts samples, as the record says it.
func samplingText(opts gosmo.StatisticsUpdateOptions) string {
	switch {
	case opts.FullScan:
		return "fullscan"
	case opts.SamplePercent > 0:
		return fmt.Sprintf("sample %d percent", opts.SamplePercent)
	case opts.Resample:
		return "resample"
	}
	return "default"
}

func (d Deps) maintUpdateStats(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	r, err := parseUpdateStats(inv)
	if err != nil {
		return nil, err
	}
	if r.object == "" {
		return d.updateDatabaseStats(ctx, inv, r)
	}
	res := &output.Result{}
	sc, cat, w, o, tv, err := d.connectTableOrView(ctx, inv, res, "statistics")
	if err != nil {
		return res, err
	}
	defer sc.Close()

	name := gosmo.QualifiedName(o.Schema, o.Name)
	before, err := tv.statistics(ctx, cat)
	if err != nil {
		return res, readError(err)
	}
	targets := before
	if r.statistic != "" {
		i := slices.IndexFunc(before, func(st *gosmo.Statistic) bool { return st.Name == r.statistic })
		if i < 0 {
			i = slices.IndexFunc(before, func(st *gosmo.Statistic) bool { return strings.EqualFold(st.Name, r.statistic) })
		}
		if i < 0 {
			names := make([]string, len(before))
			for j, st := range before {
				names[j] = st.Name
			}
			hint := "It has no statistics yet."
			if len(names) > 0 {
				hint = "Its statistics: " + strings.Join(names, ", ") + "."
			}
			return res, output.NewError(output.CodeNotFound, fmt.Sprintf("no statistic %s on %s", gosmo.QuoteName(r.statistic), name), hint)
		}
		targets = before[i : i+1]
	} else if len(before) == 0 {
		res.Warnings = append(res.Warnings, name+" has no statistics yet: the statement updates nothing.")
	}

	res.Fields = append(objectFields(o),
		output.Field{Key: "statistic", Value: r.statistic, Null: r.statistic == ""},
		output.Field{Key: "sampling", Value: samplingText(r.opts)})
	if r.statistic != "" {
		res.Fields[len(res.Fields)-2].Value = targets[0].Name
	}
	write := func(ctx context.Context) error {
		if r.statistic != "" {
			return deniedOnResolved(w.UpdateStatistic(ctx, targets[0], r.opts))
		}
		return deniedOnResolved(w.UpdateStatistics(ctx, tv, r.opts))
	}
	if err := applyWrite(ctx, inv, res, write); err != nil {
		return res, statsError(err, name, false)
	}
	var after []*gosmo.Statistic
	if !dryRun(inv) {
		if after, err = tv.statistics(ctx, cat); err != nil {
			res.Warnings = append(res.Warnings, "The statistics were not read back: "+readError(err).Message)
			after = nil
		}
	}
	res.Sets = []output.Set{statsSet(targets, after, !dryRun(inv) && after != nil)}
	return res, nil
}

// statsSet is update-stats' set: each statistic as it now stands (after a
// real run, its previous update beside it), or as it stands before a dry
// run.
func statsSet(targets, after []*gosmo.Statistic, readBack bool) output.Set {
	cols := []output.Column{{Name: "statistic", Type: "sysname"}, {Name: "origin", Type: "nvarchar(10)"},
		{Name: "last_updated", Type: "datetime2", Nullable: true}}
	if readBack {
		cols = append(cols, output.Column{Name: "previous_update", Type: "datetime2", Nullable: true})
	}
	cols = append(cols, output.Column{Name: "rows", Type: "bigint", Nullable: true},
		output.Column{Name: "rows_sampled", Type: "bigint", Nullable: true},
		output.Column{Name: "modifications", Type: "bigint", Nullable: true})
	set := output.Set{Name: "statistics", Columns: cols}
	byName := map[string]*gosmo.Statistic{}
	for _, st := range after {
		byName[st.Name] = st
	}
	for _, b := range targets {
		cur := b
		if readBack {
			cur = cmp.Or(byName[b.Name], b)
		}
		row := []*string{ptr(b.Name), ptr(statOrigin(b)), cellTime(cur.LastUpdated)}
		if readBack {
			row = append(row, cellTime(b.LastUpdated))
		}
		if cur.LastUpdated.IsZero() {
			// Never computed, or not readable: dm_db_stats_properties gave nothing.
			row = append(row, nil, nil, nil)
		} else {
			row = append(row, int64Cell(cur.TotalRows), int64Cell(cur.RowsSampled), int64Cell(cur.ModificationCounter))
		}
		set.Rows = append(set.Rows, row)
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// updateDatabaseStats is update-stats --all: sp_updatestats in the
// connection's database.
func (d Deps) updateDatabaseStats(ctx context.Context, inv *spec.Invocation, r statsRequest) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, w, err := d.connectWriter(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()
	dbase, err := currentDatabase(ctx, cat, res)
	if err != nil {
		return res, err
	}
	if dbase.IsSnapshot() || dbase.IsReadOnly {
		return res, usageError(gosmo.QuoteName(dbase.Name)+" is read-only: its statistics cannot be updated",
			"A read-only database (or a snapshot) keeps the statistics it had; the server creates temporary ones in tempdb as queries need them.")
	}
	res.Fields = []output.Field{
		{Key: "database", Value: dbase.Name},
		{Key: "sampling", Value: samplingText(r.opts)},
	}
	write := func(ctx context.Context) error {
		return deniedOnResolved(w.UpdateDatabaseStatistics(ctx, dbase, r.opts.Resample))
	}
	if err := applyWrite(ctx, inv, res, write); err != nil {
		return res, statsError(err, gosmo.QuoteName(dbase.Name), true)
	}
	return res, nil
}

// statsError is update-stats' failure, with the permission each form needs.
func statsError(err error, object string, all bool) error {
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.SQL == nil {
		return err
	}
	switch {
	case all && e.Code == output.CodePermissionDenied:
		e.Hint = "sp_updatestats needs db_owner in the database, or sysadmin. Name each object instead (ALTER on it is enough), or use a login that has it."
	case e.Code == output.CodePermissionDenied:
		e.Hint = "UPDATE STATISTICS needs ALTER on " + object + " (or db_ddladmin, db_owner). Use a login that has it, or have it granted."
	case e.SQL.Number == msgLockTimeout:
		e.Hint = "Another session holds a lock on the object: retry when it is idle (monitor blocking shows who)."
	}
	return e
}
