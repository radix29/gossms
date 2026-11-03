package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// indexCatalog is newDropCatalog's databases, connected to app on a
// Developer edition, with the table dbo.Orders (three indexes, one
// disabled), the heap dbo.Log and the indexed view dbo.vTotals. Their
// indexes and statistics are handles on a never-connected server, so a
// write scripts through the real gosmo.
type indexCatalog struct {
	*dropCatalog
	objs     objectCatalog
	ref      *gosmo.Database
	indexes  map[string][]*gosmo.Index
	frag     map[string]map[int]float64 // object → index id → percent
	stats    map[string][]*gosmo.Statistic
	dbFrag   []*gosmo.DatabaseIndexFragmentation
	dbFragAt []string // each DatabaseFragmentation call: mode/minPct/minPages
	fragErr  error
}

var statsBefore = time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)

func newIndexCatalog() *indexCatalog {
	c := &indexCatalog{dropCatalog: newDropCatalog(), ref: (&gosmo.Server{}).DatabaseRef("app"),
		objs: objectCatalog{objects: []*gosmo.ObjectInfo{
			{ObjectID: 1, Schema: "dbo", Name: "Orders", Type: "U", TypeDesc: "USER_TABLE"},
			{ObjectID: 2, Schema: "dbo", Name: "Log", Type: "U", TypeDesc: "USER_TABLE"},
			{ObjectID: 3, Schema: "dbo", Name: "vTotals", Type: "V", TypeDesc: "VIEW"},
			{ObjectID: 4, Schema: "dbo", Name: "usp_Load", Type: "P", TypeDesc: "SQL_STORED_PROCEDURE"},
		}},
	}
	c.current = "app"
	c.info = &gosmo.ServerInfo{VersionMajor: 16, EngineEdition: int(gosmo.EngineEnterprise), Edition: "Developer Edition (64-bit)"}
	orders := c.ref.TableRef("dbo", "Orders")
	view := c.ref.ViewRef("dbo", "vTotals")
	idx := func(i *gosmo.Index, id int, typ gosmo.IndexType, disabled bool) *gosmo.Index {
		i.IndexID, i.Type, i.IsDisabled = id, typ, disabled
		return i
	}
	c.indexes = map[string][]*gosmo.Index{
		"dbo.Orders": {
			idx(orders.IndexRef("PK_Orders"), 1, gosmo.IndexTypeClustered, false),
			idx(orders.IndexRef("IX_Orders_Date"), 2, gosmo.IndexTypeNonClustered, false),
			idx(orders.IndexRef("IX_Orders_Old"), 3, gosmo.IndexTypeNonClustered, true),
		},
		"dbo.vTotals": {idx(view.IndexRef("CIX_vTotals"), 1, gosmo.IndexTypeClustered, false)},
	}
	c.frag = map[string]map[int]float64{
		"dbo.Orders":  {1: 42.5, 2: 12.25},
		"dbo.vTotals": {1: 7},
	}
	st := func(s *gosmo.Statistic, auto bool) *gosmo.Statistic {
		s.IsAutoCreated, s.LastUpdated, s.TotalRows, s.RowsSampled, s.ModificationCounter = auto, statsBefore, 1000, 300, 250
		return s
	}
	c.stats = map[string][]*gosmo.Statistic{
		"dbo.Orders": {st(orders.StatisticRef("_WA_Sys_00000002_Orders"), true), st(orders.StatisticRef("IX_Orders_Date"), false),
			st(orders.StatisticRef("PK_Orders"), false)},
		"dbo.vTotals": {st(view.StatisticRef("CIX_vTotals"), false)},
	}
	c.dbFrag = []*gosmo.DatabaseIndexFragmentation{
		{IndexFragmentation: gosmo.IndexFragmentation{IndexName: "PK_Orders", IndexID: 1, AvgFragmentationPct: 42.5,
			PageCount: 5000, FragmentCount: 900, AvgPageSpaceUsedPct: 71.25}, Schema: "dbo", ObjectName: "Orders",
			IndexType: gosmo.IndexTypeClustered, PartitionCount: 4},
		{IndexFragmentation: gosmo.IndexFragmentation{IndexName: "IX_Orders_Date", IndexID: 2, AvgFragmentationPct: 12.25,
			PageCount: 1500, FragmentCount: 80}, Schema: "dbo", ObjectName: "Orders", IndexType: gosmo.IndexTypeNonClustered},
		{IndexFragmentation: gosmo.IndexFragmentation{IndexName: "CIX_Totals", IndexID: 1, AvgFragmentationPct: 4.5,
			PageCount: 1200, FragmentCount: 10}, Schema: "Sales", ObjectName: "vTotals", IsView: true, IndexType: gosmo.IndexTypeClustered},
	}
	return c
}

func objKey(schema, name string) string { return schema + "." + name }

func (c *indexCatalog) ObjectByName(ctx context.Context, d *gosmo.Database, schema, name string) (*gosmo.ObjectInfo, error) {
	return c.objs.ObjectByName(ctx, d, schema, name)
}

func (c *indexCatalog) ObjectsNamed(ctx context.Context, d *gosmo.Database, name string) ([]*gosmo.ObjectInfo, error) {
	return c.objs.ObjectsNamed(ctx, d, name)
}

func (c *indexCatalog) Search(ctx context.Context, d *gosmo.Database, pattern string) ([]*gosmo.SearchResult, error) {
	return c.objs.Search(ctx, d, pattern)
}

func (c *indexCatalog) TableByName(_ context.Context, _ *gosmo.Database, schema, name string) (*gosmo.Table, error) {
	return c.ref.TableRef(schema, name), nil
}

func (c *indexCatalog) ViewByName(_ context.Context, _ *gosmo.Database, schema, name string) (*gosmo.View, error) {
	return c.ref.ViewRef(schema, name), nil
}

func (c *indexCatalog) TableIndexes(_ context.Context, t *gosmo.Table) ([]*gosmo.Index, error) {
	return c.indexes[objKey(t.Schema, t.Name)], nil
}

func (c *indexCatalog) ViewIndexes(_ context.Context, v *gosmo.View) ([]*gosmo.Index, error) {
	return c.indexes[objKey(v.Schema, v.Name)], nil
}

func (c *indexCatalog) fragOf(key string) ([]*gosmo.IndexFragmentation, error) {
	if c.fragErr != nil {
		return nil, c.fragErr
	}
	var out []*gosmo.IndexFragmentation
	for id, pct := range c.frag[key] {
		out = append(out, &gosmo.IndexFragmentation{IndexID: id, AvgFragmentationPct: pct, PageCount: int64(1000 * id)})
	}
	return out, nil
}

func (c *indexCatalog) TableFragmentation(_ context.Context, t *gosmo.Table, mode gosmo.FragmentationMode) ([]*gosmo.IndexFragmentation, error) {
	if mode != gosmo.FragmentationLimited {
		return nil, fmt.Errorf("mode %s, want LIMITED", mode)
	}
	return c.fragOf(objKey(t.Schema, t.Name))
}

func (c *indexCatalog) ViewFragmentation(_ context.Context, v *gosmo.View, mode gosmo.FragmentationMode) ([]*gosmo.IndexFragmentation, error) {
	if mode != gosmo.FragmentationLimited {
		return nil, fmt.Errorf("mode %s, want LIMITED", mode)
	}
	return c.fragOf(objKey(v.Schema, v.Name))
}

func (c *indexCatalog) DatabaseFragmentation(_ context.Context, d *gosmo.Database, mode gosmo.FragmentationMode, minPct float64, minPages int64) ([]*gosmo.DatabaseIndexFragmentation, error) {
	c.dbFragAt = append(c.dbFragAt, fmt.Sprintf("%s/%s/%g/%d", d.Name, mode, minPct, minPages))
	if c.fragErr != nil {
		return nil, c.fragErr
	}
	return c.dbFrag, nil
}

// statsCopy hands out copies, so the update's change to the catalog does
// not reach the statistics read before it.
func (c *indexCatalog) statsCopy(key string) ([]*gosmo.Statistic, error) {
	var out []*gosmo.Statistic
	for _, s := range c.stats[key] {
		cp := *s
		out = append(out, &cp)
	}
	return out, nil
}

func (c *indexCatalog) TableStatistics(_ context.Context, t *gosmo.Table) ([]*gosmo.Statistic, error) {
	return c.statsCopy(objKey(t.Schema, t.Name))
}

func (c *indexCatalog) ViewStatistics(_ context.Context, v *gosmo.View) ([]*gosmo.Statistic, error) {
	return c.statsCopy(objKey(v.Schema, v.Name))
}

// indexWriter applies the maintenance writes to its catalog as the server
// would — a rebuild clears its indexes' fragmentation, an update stamps its
// statistics — recording each call; under WithScript it is the real
// serverWriter. err fails every call; rows is CHECKDB's answer.
type indexWriter struct {
	Writer
	cat   *indexCatalog
	calls []string
	err   error
	rows  []*gosmo.CheckDBError
}

var statsAfter = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func (w *indexWriter) record(call string) error {
	w.calls = append(w.calls, call)
	return w.err
}

func (w *indexWriter) defrag(key string, ids ...int) {
	for id := range w.cat.frag[key] {
		if len(ids) == 0 || slices.Contains(ids, id) {
			w.cat.frag[key][id] = 0.5
		}
	}
}

func idxKey(i *gosmo.Index) string {
	if i.View() != nil {
		return objKey(i.View().Schema, i.View().Name)
	}
	return objKey(i.Table().Schema, i.Table().Name)
}

func tvKey(o tableOrView) string {
	if o.view != nil {
		return objKey(o.view.Schema, o.view.Name)
	}
	return objKey(o.table.Schema, o.table.Name)
}

func (w *indexWriter) RebuildIndex(ctx context.Context, idx *gosmo.Index, opts gosmo.IndexRebuildOptions) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.RebuildIndex(ctx, idx, opts)
	}
	if err := w.record(fmt.Sprintf("rebuild %s %+v", idx.Name, opts)); err != nil {
		return err
	}
	w.defrag(idxKey(idx), idx.IndexID)
	return nil
}

func (w *indexWriter) ReorganizeIndex(ctx context.Context, idx *gosmo.Index) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.ReorganizeIndex(ctx, idx)
	}
	if err := w.record("reorganize " + idx.Name); err != nil {
		return err
	}
	w.defrag(idxKey(idx), idx.IndexID)
	return nil
}

func (w *indexWriter) RebuildAllIndexes(ctx context.Context, o tableOrView, opts gosmo.IndexRebuildOptions) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.RebuildAllIndexes(ctx, o, opts)
	}
	if err := w.record(fmt.Sprintf("rebuild all %s %+v", tvKey(o), opts)); err != nil {
		return err
	}
	w.defrag(tvKey(o))
	return nil
}

func (w *indexWriter) ReorganizeAllIndexes(ctx context.Context, o tableOrView) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.ReorganizeAllIndexes(ctx, o)
	}
	if err := w.record("reorganize all " + tvKey(o)); err != nil {
		return err
	}
	w.defrag(tvKey(o))
	return nil
}

func (w *indexWriter) stamp(key, name string) {
	for _, s := range w.cat.stats[key] {
		if name == "" || s.Name == name {
			s.LastUpdated, s.RowsSampled, s.ModificationCounter = statsAfter, s.TotalRows, 0
		}
	}
}

func (w *indexWriter) UpdateStatistics(ctx context.Context, o tableOrView, opts gosmo.StatisticsUpdateOptions) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.UpdateStatistics(ctx, o, opts)
	}
	if err := w.record(fmt.Sprintf("update stats %s %+v", tvKey(o), opts)); err != nil {
		return err
	}
	w.stamp(tvKey(o), "")
	return nil
}

func (w *indexWriter) UpdateStatistic(ctx context.Context, st *gosmo.Statistic, opts gosmo.StatisticsUpdateOptions) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.UpdateStatistic(ctx, st, opts)
	}
	if err := w.record(fmt.Sprintf("update stat %s %+v", st.Name, opts)); err != nil {
		return err
	}
	key := ""
	if st.View() != nil {
		key = objKey(st.View().Schema, st.View().Name)
	} else {
		key = objKey(st.Table().Schema, st.Table().Name)
	}
	w.stamp(key, st.Name)
	return nil
}

func (w *indexWriter) UpdateDatabaseStatistics(ctx context.Context, d *gosmo.Database, resample bool) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.UpdateDatabaseStatistics(ctx, (&gosmo.Server{}).DatabaseRef(d.Name), resample)
	}
	return w.record(fmt.Sprintf("sp_updatestats %s %v", d.Name, resample))
}

func (w *indexWriter) CheckDB(ctx context.Context, d *gosmo.Database, opts gosmo.CheckDBOptions) ([]*gosmo.CheckDBError, error) {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.CheckDB(ctx, (&gosmo.Server{}).DatabaseRef(d.Name), opts)
	}
	if err := w.record(fmt.Sprintf("checkdb %s %+v", d.Name, opts)); err != nil {
		return nil, err
	}
	return w.rows, nil
}

func indexFakes(*testing.T) (Catalog, Writer) {
	cat := newIndexCatalog()
	return cat, &indexWriter{cat: cat}
}

// runIndex runs maint <argv> over cat and w, an ad-hoc connection; dials
// counts the connects.
func runIndex(t *testing.T, cat *indexCatalog, w *indexWriter, argv string) (res *output.Result, dials int, err error) {
	t.Helper()
	w.cat = cat
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
			dials++
			return &db.ServerConn{}, nil
		},
		Catalog: func(*db.ServerConn) Catalog { return cat },
		Writer:  func(*db.ServerConn) Writer { return w },
	}
	fields := strings.Fields(argv)
	argv0 := append([]string{"maint", fields[0], "--server", "srv", "--auth", "windows"}, fields[1:]...)
	inv, perr := args.Parse(&spec.Tree{Commands: MaintCommands(d)}, argv0, args.Options{})
	if perr != nil {
		t.Fatalf("%q: %v", argv, perr)
	}
	res, err = inv.Command.Run(context.Background(), inv)
	return res, dials, err
}

// setCells is res's first set's rows, each its cells joined by |, NULL as -.
func setCells(res *output.Result) []string { return historyCells(res) }

func TestMaintIndexFragmentation(t *testing.T) {
	cat := newIndexCatalog()
	res, _, err := runIndex(t, cat, &indexWriter{}, "index-fragmentation")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cat.dbFragAt, []string{"app/LIMITED/10/1000"}) {
		t.Errorf("read %q", cat.dbFragAt)
	}
	// LIMITED reads no leaf: page fullness is NULL.
	want := []string{
		"dbo|Orders|table|PK_Orders|1|CLUSTERED|4|42.50|5000|900|-|rebuild",
		"dbo|Orders|table|IX_Orders_Date|2|NONCLUSTERED|1|12.25|1500|80|-|reorganize",
		"Sales|vTotals|view|CIX_Totals|1|CLUSTERED|1|4.50|1200|10|-|none",
	}
	if got := setCells(res); !slices.Equal(got, want) {
		t.Errorf("rows\n%q\nwant\n%q", got, want)
	}
	if got := fieldValues(res, "mode", "minPercent", "minPages"); !slices.Equal(got, []string{"LIMITED", "10", "1000"}) {
		t.Errorf("record %q", got)
	}
	if res.Database != "app" || res.Sets[0].Truncated {
		t.Errorf("database %q, truncated %v", res.Database, res.Sets[0].Truncated)
	}

	cat = newIndexCatalog()
	res, _, err = runIndex(t, cat, &indexWriter{}, "index-fragmentation --mode sampled --min-percent 0 --min-pages 0 --schema DBO --top 1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cat.dbFragAt, []string{"app/SAMPLED/0/0"}) {
		t.Errorf("read %q", cat.dbFragAt)
	}
	if got := setCells(res); !slices.Equal(got, []string{"dbo|Orders|table|PK_Orders|1|CLUSTERED|4|42.50|5000|900|71.25|rebuild"}) ||
		!res.Sets[0].Truncated {
		t.Errorf("sampled, schema, top 1: %q truncated %v", got, res.Sets[0].Truncated)
	}

	for _, argv := range []string{"index-fragmentation --min-percent 101", "index-fragmentation --min-pages -1",
		"index-fragmentation --top -1"} {
		_, dials, err := runIndex(t, newIndexCatalog(), &indexWriter{}, argv)
		if errCode(err) != output.CodeUsageError || dials != 0 {
			t.Errorf("%s: %v, %d dials; want a usage error before connecting", argv, err, dials)
		}
	}

	cat = newIndexCatalog()
	cat.fragErr = mssql.Error{Number: 297, Class: 16, Message: "The user does not have permission to perform this action."}
	_, _, err = runIndex(t, cat, &indexWriter{}, "index-fragmentation")
	if e := wantCode(t, err, output.CodePermissionDenied); e != nil && !strings.Contains(e.Hint, "VIEW DATABASE STATE") {
		t.Errorf("hint %q", e.Hint)
	}
}

func TestMaintIndexRebuild(t *testing.T) {
	cat, w := newIndexCatalog(), &indexWriter{}
	res, _, err := runIndex(t, cat, w, "index-rebuild Orders --online --maxdop 4 --fill-factor 90 --allow ddl")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.calls, []string{"rebuild all dbo.Orders {FillFactor:90 PadIndex:<nil> DataCompression: Online:true MaxDOP:4}"}) {
		t.Errorf("calls %q", w.calls)
	}
	if got := fieldValues(res, "schema", "name", "type", "index", "operation", "online", "maxdop", "fillFactor", "dryRun"); !slices.Equal(got,
		[]string{"dbo", "Orders", "table", "", "rebuild", "true", "4", "90", "false"}) {
		t.Errorf("record %q", got)
	}
	want := []string{
		"PK_Orders|1|CLUSTERED|0|42.50|0.50|1000",
		"IX_Orders_Date|2|NONCLUSTERED|0|12.25|0.50|2000",
		"IX_Orders_Old|3|NONCLUSTERED|1|-|-|-",
	}
	if got := setCells(res); !slices.Equal(got, want) {
		t.Errorf("rows\n%q\nwant\n%q", got, want)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "IX_Orders_Old] is disabled: a rebuild enables it") {
		t.Errorf("warnings %q", res.Warnings)
	}

	// One index, named in another case; reorganize; a view's index.
	cat, w = newIndexCatalog(), &indexWriter{}
	res, _, err = runIndex(t, cat, w, "index-rebuild dbo.Orders --index ix_orders_date --reorganize --allow ddl")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.calls, []string{"reorganize IX_Orders_Date"}) || fieldValue(res, "index") != "IX_Orders_Date" ||
		fieldValue(res, "online") != "true" || len(res.Warnings) != 0 {
		t.Errorf("reorganize one: calls %q, record %q, warnings %q", w.calls, fieldValues(res, "index", "online"), res.Warnings)
	}
	if got := setCells(res); !slices.Equal(got, []string{"IX_Orders_Date|2|NONCLUSTERED|0|12.25|0.50|2000"}) {
		t.Errorf("reorganize rows %q", got)
	}
	cat, w = newIndexCatalog(), &indexWriter{}
	res, _, err = runIndex(t, cat, w, "index-rebuild vTotals --allow ddl --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 0 || fieldValue(res, "type") != "view" ||
		fieldValue(res, "tsql") != "USE [app];\nGO\nALTER INDEX ALL ON [dbo].[vTotals] REBUILD\nGO\n" {
		t.Errorf("view dry run: calls %q, tsql %q", w.calls, fieldValue(res, "tsql"))
	}
	if got := setCells(res); !slices.Equal(got, []string{"CIX_vTotals|1|CLUSTERED|0|7.00|-|1000"}) {
		t.Errorf("dry run rows %q: before only", got)
	}
}

func TestMaintIndexRebuildRefusals(t *testing.T) {
	for _, c := range []struct {
		argv   string
		code   output.Code
		dialed bool
		msg    string
	}{
		{"index-rebuild dbo.Orders --reorganize --online --maxdop 2 --allow ddl", output.CodeUsageError, false, "--online, --maxdop"},
		{"index-rebuild dbo.Orders --maxdop 0 --allow ddl", output.CodeUsageError, false, "--maxdop"},
		{"index-rebuild dbo.Orders --maxdop 65 --allow ddl", output.CodeUsageError, false, "--maxdop"},
		{"index-rebuild dbo.Orders --fill-factor 0 --allow ddl", output.CodeUsageError, false, "--fill-factor"},
		{"index-rebuild dbo.Orders --index= --allow ddl", output.CodeUsageError, false, "--index"},
		{"index-rebuild dbo.Orders", output.CodeBlocked, false, ""},
		{"index-rebuild dbo.Orders --allow write", output.CodeBlocked, false, ""},
		{"index-rebuild dbo.Log --allow ddl", output.CodeUsageError, true, "heap"},
		{"index-rebuild usp_Load --allow ddl", output.CodeUsageError, true, "procedure"},
		{"index-rebuild dbo.Nope --allow ddl", output.CodeNotFound, true, "no object"},
		{"index-rebuild dbo.Orders --index IX_Nope --allow ddl", output.CodeNotFound, true, "IX_Nope"},
		{"index-rebuild dbo.Orders --index IX_Orders_Old --reorganize --allow ddl", output.CodeUsageError, true, "disabled"},
	} {
		w := &indexWriter{}
		res, dials, err := runIndex(t, newIndexCatalog(), w, c.argv)
		e, _ := err.(*output.Error)
		if e == nil || e.Code != c.code || (dials > 0) != c.dialed || !strings.Contains(e.Message+e.Hint, c.msg) {
			t.Errorf("%s: %v (%d dials), want %s with %q, dialed %v", c.argv, err, dials, c.code, c.msg, c.dialed)
		}
		if len(w.calls) != 0 || res != nil && hasField(res, "tsql") {
			t.Errorf("%s: wrote %q", c.argv, w.calls)
		}
	}
	// The missing index's hint lists the object's indexes.
	_, _, err := runIndex(t, newIndexCatalog(), &indexWriter{}, "index-rebuild dbo.Orders --index IX_Nope --allow ddl")
	if e, _ := err.(*output.Error); e == nil || e.Hint != "Its indexes: PK_Orders, IX_Orders_Date, IX_Orders_Old." {
		t.Errorf("hint: %v", err)
	}

	// --online on Standard: exit 11 before anything is sent, dry run too.
	for _, argv := range []string{"index-rebuild dbo.Orders --online --allow ddl", "index-rebuild dbo.Orders --online --allow ddl --dry-run"} {
		cat, w := newIndexCatalog(), &indexWriter{}
		cat.info.EngineEdition, cat.info.Edition = int(gosmo.EngineStandard), "Standard Edition (64-bit)"
		res, _, err := runIndex(t, cat, w, argv)
		if errCode(err) != output.CodeUnsupported || len(w.calls) != 0 || hasField(res, "tsql") {
			t.Errorf("%s on Standard: %v, calls %q", argv, err, w.calls)
		}
	}
	// --reorganize is always online: fine on Standard.
	cat := newIndexCatalog()
	cat.info.EngineEdition = int(gosmo.EngineStandard)
	if _, _, err := runIndex(t, cat, &indexWriter{}, "index-rebuild dbo.Orders --reorganize --allow ddl"); err != nil {
		t.Errorf("reorganize on Standard: %v", err)
	}
}

func TestMaintIndexRebuildServerErrors(t *testing.T) {
	for _, c := range []struct {
		err  mssql.Error
		code output.Code
		hint string
	}{
		{mssql.Error{Number: 1712, Class: 16, Message: "Online index operations can only be performed in Enterprise edition of SQL Server."},
			output.CodeUnsupported, "leave --online out"},
		{mssql.Error{Number: 1088, Class: 16, Message: "Cannot find the object \"dbo.Orders\" because it does not exist or you do not have permissions."},
			output.CodePermissionDenied, "ALTER on [dbo].[Orders]"},
		{mssql.Error{Number: 1222, Class: 16, Message: "Lock request time out period exceeded."}, output.CodeSQLError, "monitor blocking"},
		{mssql.Error{Number: 2552, Class: 16, Message: "The index \"IX\" (partition 1) on table \"Orders\" cannot be reorganized because page level locking is disabled."},
			output.CodeSQLError, "ALLOW_PAGE_LOCKS"},
	} {
		w := &indexWriter{err: fmt.Errorf("gosmo: rebuild all indexes on [dbo].[Orders]: %w", c.err)}
		_, _, err := runIndex(t, newIndexCatalog(), w, "index-rebuild dbo.Orders --allow ddl")
		e, _ := err.(*output.Error)
		if e == nil || e.Code != c.code || !strings.Contains(e.Hint, c.hint) {
			t.Errorf("Msg %d: %+v, want %s with hint %q", c.err.Number, err, c.code, c.hint)
		}
	}

	// A fragmentation read that fails is a warning: the rebuild still runs.
	cat, w := newIndexCatalog(), &indexWriter{}
	cat.fragErr = mssql.Error{Number: 297, Class: 16, Message: "The user does not have permission to perform this action."}
	res, _, err := runIndex(t, cat, w, "index-rebuild dbo.Orders --index PK_Orders --allow ddl")
	if err != nil || len(w.calls) != 1 {
		t.Fatal(err, w.calls)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "fragmentation before was not read") {
		t.Errorf("warnings %q", res.Warnings)
	}
	if got := setCells(res); !slices.Equal(got, []string{"PK_Orders|1|CLUSTERED|0|-|-|-"}) {
		t.Errorf("rows %q", got)
	}
}

func TestMaintUpdateStats(t *testing.T) {
	cat, w := newIndexCatalog(), &indexWriter{}
	res, _, err := runIndex(t, cat, w, "update-stats dbo.Orders --allow ddl")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.calls, []string{"update stats dbo.Orders {FullScan:false SamplePercent:0 Resample:false}"}) {
		t.Errorf("calls %q", w.calls)
	}
	if got := fieldValues(res, "schema", "name", "type", "statistic", "sampling", "dryRun"); !slices.Equal(got,
		[]string{"dbo", "Orders", "table", "", "default", "false"}) {
		t.Errorf("record %q", got)
	}
	want := []string{
		"_WA_Sys_00000002_Orders|auto|2026-11-03 12:00:00.000|2026-10-01 03:00:00.000|1000|1000|0",
		"IX_Orders_Date|index|2026-11-03 12:00:00.000|2026-10-01 03:00:00.000|1000|1000|0",
		"PK_Orders|index|2026-11-03 12:00:00.000|2026-10-01 03:00:00.000|1000|1000|0",
	}
	if got := setCells(res); !slices.Equal(got, want) {
		t.Errorf("rows\n%q\nwant\n%q", got, want)
	}

	// One statistic of a view, sampled, as a dry run: the set is as it stands.
	cat, w = newIndexCatalog(), &indexWriter{}
	res, _, err = runIndex(t, cat, w, "update-stats vTotals --statistic cix_vtotals --sample-percent 20 --allow ddl --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 0 || fieldValue(res, "statistic") != "CIX_vTotals" || fieldValue(res, "sampling") != "sample 20 percent" ||
		fieldValue(res, "tsql") != "USE [app];\nGO\nUPDATE STATISTICS [dbo].[vTotals] [CIX_vTotals] WITH SAMPLE 20 PERCENT\nGO\n" {
		t.Errorf("dry run: calls %q, record %q", w.calls, fieldValues(res, "statistic", "sampling", "tsql"))
	}
	if got := setCells(res); !slices.Equal(got, []string{"CIX_vTotals|index|2026-10-01 03:00:00.000|1000|300|250"}) {
		t.Errorf("dry run rows %q", got)
	}

	// --all: sp_updatestats in the connection's database.
	cat, w = newIndexCatalog(), &indexWriter{}
	res, _, err = runIndex(t, cat, w, "update-stats --all --resample --allow ddl")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.calls, []string{"sp_updatestats app true"}) || len(res.Sets) != 0 ||
		!slices.Equal(fieldValues(res, "database", "sampling", "dryRun"), []string{"app", "resample", "false"}) {
		t.Errorf("--all: calls %q, record %q", w.calls, fieldValues(res, "database", "sampling", "dryRun"))
	}
	res, _, err = runIndex(t, newIndexCatalog(), &indexWriter{}, "update-stats --all --allow ddl --dry-run")
	if err != nil || fieldValue(res, "tsql") != "USE [app];\nGO\nEXEC sys.sp_updatestats\nGO\n" {
		t.Errorf("--all dry run: %v, tsql %q", err, fieldValue(res, "tsql"))
	}
}

func TestMaintUpdateStatsRefusals(t *testing.T) {
	for _, c := range []struct {
		argv   string
		code   output.Code
		dialed bool
		msg    string
	}{
		{"update-stats --allow ddl", output.CodeUsageError, false, "<object> or --all"},
		{"update-stats dbo.Orders --all --allow ddl", output.CodeUsageError, false, "not both"},
		{"update-stats dbo.Orders --fullscan --sample-percent 10 --allow ddl", output.CodeUsageError, false, "alternatives"},
		{"update-stats dbo.Orders --fullscan --resample --allow ddl", output.CodeUsageError, false, "alternatives"},
		{"update-stats dbo.Orders --sample-percent 0 --allow ddl", output.CodeUsageError, false, "--sample-percent"},
		{"update-stats --all --fullscan --allow ddl", output.CodeUsageError, false, "per table"},
		{"update-stats --all --statistic PK_Orders --allow ddl", output.CodeUsageError, false, "--statistic"},
		{"update-stats dbo.Orders --allow write", output.CodeBlocked, false, ""},
		{"update-stats --all --allow write", output.CodeBlocked, false, ""},
		{"update-stats dbo.Orders --statistic nope --allow ddl", output.CodeNotFound, true, "_WA_Sys_00000002_Orders, IX_Orders_Date, PK_Orders"},
		{"update-stats usp_Load --allow ddl", output.CodeUsageError, true, "procedure"},
	} {
		w := &indexWriter{}
		res, dials, err := runIndex(t, newIndexCatalog(), w, c.argv)
		e, _ := err.(*output.Error)
		if e == nil || e.Code != c.code || (dials > 0) != c.dialed || !strings.Contains(e.Message+e.Hint, c.msg) {
			t.Errorf("%s: %v (%d dials), want %s with %q, dialed %v", c.argv, err, dials, c.code, c.msg, c.dialed)
		}
		if len(w.calls) != 0 || res != nil && hasField(res, "tsql") {
			t.Errorf("%s: wrote %q", c.argv, w.calls)
		}
	}

	// A read-only database takes no sp_updatestats.
	cat := newIndexCatalog()
	cat.dbs[1].IsReadOnly = true
	if _, _, err := runIndex(t, cat, &indexWriter{}, "update-stats --all --allow ddl"); errCode(err) != output.CodeUsageError {
		t.Errorf("read-only: %v", err)
	}

	// sp_updatestats refuses a login that is not db_owner with its own
	// message: exit 9, naming db_owner.
	w := &indexWriter{err: fmt.Errorf("gosmo: update statistics: %w",
		mssql.Error{Number: 15247, Class: 16, Message: "User does not have permission to perform this action."})}
	_, _, err := runIndex(t, newIndexCatalog(), w, "update-stats --all --allow ddl")
	if e := wantCode(t, err, output.CodePermissionDenied); e != nil && !strings.Contains(e.Hint, "db_owner") {
		t.Errorf("hint %q", e.Hint)
	}
	w = &indexWriter{err: fmt.Errorf("gosmo: update all statistics: %w",
		mssql.Error{Number: 1088, Class: 16, Message: "Cannot find the object \"dbo.Orders\" because it does not exist or you do not have permissions."})}
	_, _, err = runIndex(t, newIndexCatalog(), w, "update-stats dbo.Orders --allow ddl")
	if e := wantCode(t, err, output.CodePermissionDenied); e != nil && !strings.Contains(e.Hint, "ALTER on [dbo].[Orders]") {
		t.Errorf("hint %q", e.Hint)
	}
}

// checkDBCorrupt is a corrupted page's rows as CHECKDB returns them
// (trimmed from gosmo's live fixture): two errors, their table's 8990
// summary, the database's 8989.
var checkDBCorrupt = []*gosmo.CheckDBError{
	{Error: 8939, Level: 16, State: 98, Message: "Table error: Object ID 901578250, index ID 2 … Test (IS_OFF (BUF_IOERR, pBUF->bstat)) failed.",
		RepairLevel: "repair_rebuild", ObjectID: 901578250, IndexID: 2, PartitionID: 72057594045595648, AllocUnitID: 72057594051231744, File: 1, Page: 312},
	{Error: 8928, Level: 16, State: 1, Message: "Object ID 901578250, index ID 2 … Page (1:312) could not be processed.",
		RepairLevel: "repair_allow_data_loss", ObjectID: 901578250, IndexID: 2, File: 1, Page: 312},
	{Error: 8990, Level: 10, State: 1, Message: "CHECKDB found 0 allocation errors and 2 consistency errors in table 't' (object ID 901578250).", ObjectID: 901578250},
	{Error: 8989, Level: 10, State: 1, Message: "CHECKDB found 0 allocation errors and 2 consistency errors in database 'app'."},
}

func TestMaintCheckDB(t *testing.T) {
	// The connection's database, clean.
	w := &indexWriter{}
	res, _, err := runIndex(t, newIndexCatalog(), w, "checkdb --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.calls, []string{"checkdb app {PhysicalOnly:false NoIndex:false ExtendedLogicalChecks:false DataPurity:false MaxDOP:0}"}) {
		t.Errorf("calls %q", w.calls)
	}
	got := fieldValues(res, "database", "physicalOnly", "maxdop", "dryRun", "allocationErrors", "consistencyErrors", "minimumRepair")
	if !slices.Equal(got, []string{"app", "false", "", "false", "0", "0", ""}) || len(res.Warnings) != 0 ||
		res.Sets[0].Name != "errors" || len(res.Sets[0].Rows) != 0 {
		t.Errorf("clean: record %q, warnings %q, set %+v", got, res.Warnings, res.Sets)
	}

	// Corruption: the status stays ok; the counts come from 8989, the
	// summaries are not rows, the least repair is the heaviest one named.
	w = &indexWriter{rows: checkDBCorrupt}
	res, _, err = runIndex(t, newIndexCatalog(), w, "checkdb hr --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldValues(res, "database", "allocationErrors", "consistencyErrors", "minimumRepair"); !slices.Equal(got,
		[]string{"hr", "0", "2", "repair_allow_data_loss"}) {
		t.Errorf("corrupt record %q", got)
	}
	if got := setCells(res); len(got) != 2 || !strings.HasPrefix(got[0], "8939|16|98|") ||
		!strings.HasSuffix(got[0], "|repair_rebuild|901578250|2|72057594045595648|72057594051231744|1|312|-") {
		t.Errorf("rows %q", got)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "0 allocation and 2 consistency errors; repair_allow_data_loss") {
		t.Errorf("warnings %q", res.Warnings)
	}

	// Dry run: the statement, nothing run, no counts.
	w = &indexWriter{}
	res, _, err = runIndex(t, newIndexCatalog(), w, "checkdb app --physical-only --no-index --maxdop 2 --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 0 || hasField(res, "consistencyErrors") || len(res.Sets) != 0 || fieldValue(res, "tsql") !=
		"USE [app];\nGO\nDBCC CHECKDB (N'app', NOINDEX) WITH NO_INFOMSGS, ALL_ERRORMSGS, TABLERESULTS, PHYSICAL_ONLY, MAXDOP = 2\nGO\n" {
		t.Errorf("dry run: calls %q, tsql %q", w.calls, fieldValue(res, "tsql"))
	}
}

func TestMaintCheckDBRefusals(t *testing.T) {
	for _, c := range []struct {
		argv   string
		code   output.Code
		dialed bool
		msg    string
	}{
		{"checkdb app --physical-only --data-purity --allow admin", output.CodeUsageError, false, "--data-purity"},
		{"checkdb app --physical-only --extended-logical-checks --allow admin", output.CodeUsageError, false, "--extended-logical-checks"},
		{"checkdb app --maxdop 0 --allow admin", output.CodeUsageError, false, "--maxdop"},
		{"checkdb app --allow ddl", output.CodeBlocked, false, ""},
		{"checkdb nope --allow admin", output.CodeNotFound, true, "no database"},
		{"checkdb archive --allow admin", output.CodeUsageError, true, "OFFLINE"},
	} {
		w := &indexWriter{}
		res, dials, err := runIndex(t, newIndexCatalog(), w, c.argv)
		e, _ := err.(*output.Error)
		if e == nil || e.Code != c.code || (dials > 0) != c.dialed || !strings.Contains(e.Message+e.Hint, c.msg) {
			t.Errorf("%s: %v (%d dials), want %s with %q, dialed %v", c.argv, err, dials, c.code, c.msg, c.dialed)
		}
		if len(w.calls) != 0 || res != nil && hasField(res, "tsql") {
			t.Errorf("%s: ran %q", c.argv, w.calls)
		}
	}
	w := &indexWriter{err: fmt.Errorf("gosmo: check database %q: %w", "app",
		mssql.Error{Number: 7983, Class: 14, Message: "User 'low' does not have permission to run DBCC checkdb for database 'app'."})}
	_, _, err := runIndex(t, newIndexCatalog(), w, "checkdb app --allow admin")
	if e := wantCode(t, err, output.CodePermissionDenied); e != nil && !strings.Contains(e.Hint, "sysadmin, or db_owner") {
		t.Errorf("hint %q", e.Hint)
	}
}
