package cmd

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/db"
)

// newIndexServer is a maintServer connected to app on a Developer edition,
// with the table dbo.Orders (three indexes, one disabled), the heap dbo.Log
// and the indexed view dbo.vTotals, and the procedure dbo.usp_Load.
func newIndexServer(t *testing.T) *maintServer {
	s := newMaintServer(t, "app",
		&gosmo.ObjectInfo{ObjectID: 1, Schema: "dbo", Name: "Orders", Type: "U", TypeDesc: "USER_TABLE"},
		&gosmo.ObjectInfo{ObjectID: 2, Schema: "dbo", Name: "Log", Type: "U", TypeDesc: "USER_TABLE"},
		&gosmo.ObjectInfo{ObjectID: 3, Schema: "dbo", Name: "vTotals", Type: "V", TypeDesc: "VIEW"},
		&gosmo.ObjectInfo{ObjectID: 4, Schema: "dbo", Name: "usp_Load", Type: "P", TypeDesc: "SQL_STORED_PROCEDURE"})
	s.indexes = map[string][]*gosmo.Index{
		"dbo.Orders": {
			{Name: "PK_Orders", IndexID: 1, Type: gosmo.IndexTypeClustered, IsUnique: true, IsPrimaryKey: true},
			{Name: "IX_Orders_Date", IndexID: 2, Type: gosmo.IndexTypeNonClustered},
			{Name: "IX_Orders_Old", IndexID: 3, Type: gosmo.IndexTypeNonClustered, IsDisabled: true},
		},
		"dbo.vTotals": {{Name: "CIX_vTotals", IndexID: 1, Type: gosmo.IndexTypeClustered, IsUnique: true}},
	}
	s.frag = map[string]map[int]float64{
		"dbo.Orders":  {1: 42.5, 2: 12.25},
		"dbo.vTotals": {1: 7},
	}
	st := func(name string, auto bool) *gosmo.Statistic {
		return &gosmo.Statistic{Name: name, IsAutoCreated: auto, LastUpdated: statsBefore, TotalRows: 1000, RowsSampled: 300,
			ModificationCounter: 250}
	}
	s.stats = map[string][]*gosmo.Statistic{
		"dbo.Orders":  {st("_WA_Sys_00000002_Orders", true), st("IX_Orders_Date", false), st("PK_Orders", false)},
		"dbo.vTotals": {st("CIX_vTotals", false)},
	}
	s.dbFrag = []*gosmo.DatabaseIndexFragmentation{
		{IndexFragmentation: gosmo.IndexFragmentation{IndexName: "PK_Orders", IndexID: 1, AvgFragmentationPct: 42.5,
			PageCount: 5000, FragmentCount: 900, AvgPageSpaceUsedPct: 71.25}, Schema: "dbo", ObjectName: "Orders", ObjectID: 1,
			IndexType: gosmo.IndexTypeClustered, PartitionCount: 4},
		{IndexFragmentation: gosmo.IndexFragmentation{IndexName: "IX_Orders_Date", IndexID: 2, AvgFragmentationPct: 12.25,
			PageCount: 1500, FragmentCount: 80}, Schema: "dbo", ObjectName: "Orders", ObjectID: 1, IndexType: gosmo.IndexTypeNonClustered},
		{IndexFragmentation: gosmo.IndexFragmentation{IndexName: "CIX_Totals", IndexID: 1, AvgFragmentationPct: 4.5,
			PageCount: 1200, FragmentCount: 10}, Schema: "Sales", ObjectName: "vTotals", ObjectID: 3, IsView: true,
			IndexType: gosmo.IndexTypeClustered},
	}
	return s
}

var statsBefore = time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)

func objKey(schema, name string) string { return schema + "." + name }

func indexFakes(t *testing.T) Catalog {
	return ServerCatalog(&db.ServerConn{Server: newIndexServer(t).Server()})
}

// runIndex runs maint <argv> over s, an ad-hoc connection; dials counts
// the connects.
func runIndex(t *testing.T, s *maintServer, argv string) (res *output.Result, dials int, err error) {
	t.Helper()
	d := maintDeps(s, &dials)
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
	cat := newIndexServer(t)
	res, _, err := runIndex(t, cat, "index-fragmentation")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cat.dbFragAt, []string{"app/LIMITED/10/1000"}) {
		t.Errorf("read %q", cat.dbFragAt)
	}
	// LIMITED reads no leaf: page fullness is NULL. The server keeps the
	// indexes at least 10% fragmented.
	want := []string{
		"dbo|Orders|table|PK_Orders|1|CLUSTERED|4|42.50|5000|900|-|rebuild",
		"dbo|Orders|table|IX_Orders_Date|2|NONCLUSTERED|1|12.25|1500|80|-|reorganize",
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
	res, _, err = runIndex(t, newIndexServer(t), "index-fragmentation --min-percent 0")
	if got := setCells(res); err != nil || len(got) != 3 || got[2] != "Sales|vTotals|view|CIX_Totals|1|CLUSTERED|1|4.50|1200|10|-|none" {
		t.Errorf("--min-percent 0: %v %q", err, got)
	}

	cat = newIndexServer(t)
	res, _, err = runIndex(t, cat, "index-fragmentation --mode sampled --min-percent 0 --min-pages 0 --schema DBO --top 1")
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
		_, dials, err := runIndex(t, newIndexServer(t), argv)
		if errCode(err) != output.CodeUsageError || dials != 0 {
			t.Errorf("%s: %v, %d dials; want a usage error before connecting", argv, err, dials)
		}
	}

	cat = newIndexServer(t)
	cat.fragErr = mssql.Error{Number: 297, Class: 16, Message: "The user does not have permission to perform this action."}
	_, _, err = runIndex(t, cat, "index-fragmentation")
	if e := wantCode(t, err, output.CodePermissionDenied); e != nil && !strings.Contains(e.Hint, "VIEW DATABASE STATE") {
		t.Errorf("hint %q", e.Hint)
	}
}

func TestMaintIndexRebuild(t *testing.T) {
	cat := newIndexServer(t)
	res, _, err := runIndex(t, cat, "index-rebuild Orders --online --maxdop 4 --fill-factor 90 --allow ddl")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cat.writes, []string{"ALTER INDEX ALL ON [dbo].[Orders] REBUILD WITH (FILLFACTOR = 90, ONLINE = ON, MAXDOP = 4)"}) {
		t.Errorf("sent %q", cat.writes)
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
	cat = newIndexServer(t)
	res, _, err = runIndex(t, cat, "index-rebuild dbo.Orders --index ix_orders_date --reorganize --allow ddl")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cat.writes, []string{"ALTER INDEX [IX_Orders_Date] ON [dbo].[Orders] REORGANIZE"}) || fieldValue(res, "index") != "IX_Orders_Date" ||
		fieldValue(res, "online") != "true" || len(res.Warnings) != 0 {
		t.Errorf("reorganize one: sent %q, record %q, warnings %q", cat.writes, fieldValues(res, "index", "online"), res.Warnings)
	}
	if got := setCells(res); !slices.Equal(got, []string{"IX_Orders_Date|2|NONCLUSTERED|0|12.25|0.50|2000"}) {
		t.Errorf("reorganize rows %q", got)
	}
	cat = newIndexServer(t)
	res, _, err = runIndex(t, cat, "index-rebuild vTotals --allow ddl --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.writes) != 0 || fieldValue(res, "type") != "view" ||
		fieldValue(res, "tsql") != "USE [app];\nGO\nALTER INDEX ALL ON [dbo].[vTotals] REBUILD\nGO\n" {
		t.Errorf("view dry run: sent %q, tsql %q", cat.writes, fieldValue(res, "tsql"))
	}
	if got := setCells(res); !slices.Equal(got, []string{"CIX_vTotals|1|CLUSTERED|0|7.00|-|1000"}) {
		t.Errorf("dry run rows %q: before only", got)
	}
	// Every index of a view reorganized, for real: the view's statement,
	// its fragmentation read back cleared.
	cat = newIndexServer(t)
	res, _, err = runIndex(t, cat, "index-rebuild vTotals --reorganize --allow ddl")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cat.writes, []string{"ALTER INDEX ALL ON [dbo].[vTotals] REORGANIZE"}) || cat.frag["dbo.vTotals"][1] != 0.5 {
		t.Errorf("view reorganize: sent %q, fragmentation %v", cat.writes, cat.frag["dbo.vTotals"])
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
		cat := newIndexServer(t)
		res, dials, err := runIndex(t, cat, c.argv)
		e, _ := err.(*output.Error)
		if e == nil || e.Code != c.code || (dials > 0) != c.dialed || !strings.Contains(e.Message+e.Hint, c.msg) {
			t.Errorf("%s: %v (%d dials), want %s with %q, dialed %v", c.argv, err, dials, c.code, c.msg, c.dialed)
		}
		if len(cat.writes) != 0 || res != nil && hasField(res, "tsql") {
			t.Errorf("%s: sent %q", c.argv, cat.writes)
		}
	}
	// The missing index's hint lists the object's indexes.
	_, _, err := runIndex(t, newIndexServer(t), "index-rebuild dbo.Orders --index IX_Nope --allow ddl")
	if e, _ := err.(*output.Error); e == nil || e.Hint != "Its indexes: PK_Orders, IX_Orders_Date, IX_Orders_Old." {
		t.Errorf("hint: %v", err)
	}

	// --online on Standard: exit 11 before anything is sent, dry run too.
	for _, argv := range []string{"index-rebuild dbo.Orders --online --allow ddl", "index-rebuild dbo.Orders --online --allow ddl --dry-run"} {
		cat := newIndexServer(t)
		cat.Info.EngineEdition, cat.Info.Edition = int(gosmo.EngineStandard), "Standard Edition (64-bit)"
		res, _, err := runIndex(t, cat, argv)
		if errCode(err) != output.CodeUnsupported || len(cat.writes) != 0 || hasField(res, "tsql") {
			t.Errorf("%s on Standard: %v, sent %q", argv, err, cat.writes)
		}
	}
	// --reorganize is always online: fine on Standard.
	cat := newIndexServer(t)
	cat.Info.EngineEdition = int(gosmo.EngineStandard)
	if _, _, err := runIndex(t, cat, "index-rebuild dbo.Orders --reorganize --allow ddl"); err != nil {
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
		cat := newIndexServer(t)
		cat.writeErr = c.err
		_, _, err := runIndex(t, cat, "index-rebuild dbo.Orders --allow ddl")
		e, _ := err.(*output.Error)
		if e == nil || e.Code != c.code || !strings.Contains(e.Hint, c.hint) {
			t.Errorf("Msg %d: %+v, want %s with hint %q", c.err.Number, err, c.code, c.hint)
		}
	}

	// A fragmentation read that fails is a warning: the rebuild still runs.
	cat := newIndexServer(t)
	cat.fragErr = mssql.Error{Number: 297, Class: 16, Message: "The user does not have permission to perform this action."}
	res, _, err := runIndex(t, cat, "index-rebuild dbo.Orders --index PK_Orders --allow ddl")
	if err != nil || len(cat.writes) != 1 {
		t.Fatal(err, cat.writes)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "fragmentation before was not read") {
		t.Errorf("warnings %q", res.Warnings)
	}
	if got := setCells(res); !slices.Equal(got, []string{"PK_Orders|1|CLUSTERED|0|-|-|-"}) {
		t.Errorf("rows %q", got)
	}
}

func TestMaintUpdateStats(t *testing.T) {
	cat := newIndexServer(t)
	res, _, err := runIndex(t, cat, "update-stats dbo.Orders --allow ddl")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cat.writes, []string{"UPDATE STATISTICS [dbo].[Orders]"}) {
		t.Errorf("sent %q", cat.writes)
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
	cat = newIndexServer(t)
	res, _, err = runIndex(t, cat, "update-stats vTotals --statistic cix_vtotals --sample-percent 20 --allow ddl --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.writes) != 0 || fieldValue(res, "statistic") != "CIX_vTotals" || fieldValue(res, "sampling") != "sample 20 percent" ||
		fieldValue(res, "tsql") != "USE [app];\nGO\nUPDATE STATISTICS [dbo].[vTotals] [CIX_vTotals] WITH SAMPLE 20 PERCENT\nGO\n" {
		t.Errorf("dry run: sent %q, record %q", cat.writes, fieldValues(res, "statistic", "sampling", "tsql"))
	}
	if got := setCells(res); !slices.Equal(got, []string{"CIX_vTotals|index|2026-10-01 03:00:00.000|1000|300|250"}) {
		t.Errorf("dry run rows %q", got)
	}

	// --all: sp_updatestats in the connection's database.
	cat = newIndexServer(t)
	res, _, err = runIndex(t, cat, "update-stats --all --resample --allow ddl")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cat.writes, []string{"EXEC sys.sp_updatestats @resample = 'resample'"}) || len(res.Sets) != 0 ||
		!slices.Equal(fieldValues(res, "database", "sampling", "dryRun"), []string{"app", "resample", "false"}) {
		t.Errorf("--all: sent %q, record %q", cat.writes, fieldValues(res, "database", "sampling", "dryRun"))
	}
	if q := cat.Queries(); q[len(q)-1].Database != "app" {
		t.Errorf("sp_updatestats ran in %q", q[len(q)-1].Database)
	}
	res, _, err = runIndex(t, newIndexServer(t), "update-stats --all --allow ddl --dry-run")
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
		cat := newIndexServer(t)
		res, dials, err := runIndex(t, cat, c.argv)
		e, _ := err.(*output.Error)
		if e == nil || e.Code != c.code || (dials > 0) != c.dialed || !strings.Contains(e.Message+e.Hint, c.msg) {
			t.Errorf("%s: %v (%d dials), want %s with %q, dialed %v", c.argv, err, dials, c.code, c.msg, c.dialed)
		}
		if len(cat.writes) != 0 || res != nil && hasField(res, "tsql") {
			t.Errorf("%s: sent %q", c.argv, cat.writes)
		}
	}

	// A read-only database takes no sp_updatestats.
	cat := newIndexServer(t)
	cat.dbs[1].IsReadOnly = true
	if _, _, err := runIndex(t, cat, "update-stats --all --allow ddl"); errCode(err) != output.CodeUsageError {
		t.Errorf("read-only: %v", err)
	}

	// sp_updatestats refuses a login that is not db_owner with its own
	// message: exit 9, naming db_owner.
	cat = newIndexServer(t)
	cat.writeErr = mssql.Error{Number: 15247, Class: 16, Message: "User does not have permission to perform this action."}
	_, _, err := runIndex(t, cat, "update-stats --all --allow ddl")
	if e := wantCode(t, err, output.CodePermissionDenied); e != nil && !strings.Contains(e.Hint, "db_owner") {
		t.Errorf("hint %q", e.Hint)
	}
	cat = newIndexServer(t)
	cat.writeErr = mssql.Error{Number: 1088, Class: 16, Message: "Cannot find the object \"dbo.Orders\" because it does not exist or you do not have permissions."}
	_, _, err = runIndex(t, cat, "update-stats dbo.Orders --allow ddl")
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
	cat := newIndexServer(t)
	res, _, err := runIndex(t, cat, "checkdb --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cat.writes, []string{"DBCC CHECKDB (N'app') WITH NO_INFOMSGS, ALL_ERRORMSGS, TABLERESULTS"}) {
		t.Errorf("sent %q", cat.writes)
	}
	got := fieldValues(res, "database", "physicalOnly", "maxdop", "dryRun", "allocationErrors", "consistencyErrors", "minimumRepair")
	if !slices.Equal(got, []string{"app", "false", "", "false", "0", "0", ""}) || len(res.Warnings) != 0 ||
		res.Sets[0].Name != "errors" || len(res.Sets[0].Rows) != 0 {
		t.Errorf("clean: record %q, warnings %q, set %+v", got, res.Warnings, res.Sets)
	}

	// Corruption: the status stays ok; the counts come from 8989, the
	// summaries are not rows, the least repair is the heaviest one named.
	cat = newIndexServer(t)
	cat.checkRows = checkDBCorrupt
	res, _, err = runIndex(t, cat, "checkdb hr --allow admin")
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
	cat = newIndexServer(t)
	res, _, err = runIndex(t, cat, "checkdb app --physical-only --no-index --maxdop 2 --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.writes) != 0 || hasField(res, "consistencyErrors") || len(res.Sets) != 0 || fieldValue(res, "tsql") !=
		"USE [app];\nGO\nDBCC CHECKDB (N'app', NOINDEX) WITH NO_INFOMSGS, ALL_ERRORMSGS, TABLERESULTS, PHYSICAL_ONLY, MAXDOP = 2\nGO\n" {
		t.Errorf("dry run: sent %q, tsql %q", cat.writes, fieldValue(res, "tsql"))
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
		cat := newIndexServer(t)
		res, dials, err := runIndex(t, cat, c.argv)
		e, _ := err.(*output.Error)
		if e == nil || e.Code != c.code || (dials > 0) != c.dialed || !strings.Contains(e.Message+e.Hint, c.msg) {
			t.Errorf("%s: %v (%d dials), want %s with %q, dialed %v", c.argv, err, dials, c.code, c.msg, c.dialed)
		}
		if len(cat.writes) != 0 || res != nil && hasField(res, "tsql") {
			t.Errorf("%s: ran %q", c.argv, cat.writes)
		}
	}
	cat := newIndexServer(t)
	cat.writeErr = mssql.Error{Number: 7983, Class: 14, Message: "User 'low' does not have permission to run DBCC checkdb for database 'app'."}
	_, _, err := runIndex(t, cat, "checkdb app --allow admin")
	if e := wantCode(t, err, output.CodePermissionDenied); e != nil && !strings.Contains(e.Hint, "sysadmin, or db_owner") {
		t.Errorf("hint %q", e.Hint)
	}
}
