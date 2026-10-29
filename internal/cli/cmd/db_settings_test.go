package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// scopedCatalog is newStorageCatalog's databases with app's scoped
// configurations, in the view's own (not name) order to show the command
// sorts; readErr fails every scoped read after the first afterReads.
type scopedCatalog struct {
	*storageCatalog
	info       *gosmo.ServerInfo
	cfgs       []*gosmo.DatabaseScopedConfig
	reads      int
	readErr    error
	afterReads int
}

func (c *scopedCatalog) ServerInfo() *gosmo.ServerInfo { return c.info }

func (c *scopedCatalog) DatabaseScopedConfigs(_ context.Context, d *gosmo.Database) ([]*gosmo.DatabaseScopedConfig, error) {
	c.reads++
	if c.readErr != nil && c.reads > c.afterReads {
		return nil, c.readErr
	}
	if d.Name != "app" {
		return nil, fmt.Errorf("read of %s", d.Name)
	}
	out := make([]*gosmo.DatabaseScopedConfig, len(c.cfgs))
	for i, cfg := range c.cfgs {
		out[i] = new(*cfg) // two reads are two copies, as from the server
	}
	return out, nil
}

func newScopedCatalog() *scopedCatalog {
	return &scopedCatalog{
		storageCatalog: newStorageCatalog(),
		info:           &gosmo.ServerInfo{VersionMajor: 17},
		cfgs: []*gosmo.DatabaseScopedConfig{
			{Name: "MAXDOP", Value: "0", ValueForSecondary: "2", ValueType: "int", ValueForSecondaryType: "int"},
			{Name: "ELEVATE_ONLINE", Value: "OFF", IsValueDefault: true, ValueType: "nvarchar"},
			{Name: "LEGACY_CARDINALITY_ESTIMATION", Value: "0", IsValueDefault: true, ValueType: "bit"},
			{Name: "PARAMETER_SNIFFING", Value: "1", IsValueDefault: true, ValueType: "bit"},
			{Name: "IDENTITY_CACHE", Value: "1", IsValueDefault: true, ValueType: "bit"},
		},
	}
}

// scopedWriter applies a change to its catalog as the server does; with err
// set it fails instead, and with ignore it does nothing. Under WithScript it
// is the real serverWriter, so a dry run captures gosmo's statement.
type scopedWriter struct {
	Writer
	cat    *scopedCatalog
	calls  []string
	err    error
	ignore bool
}

func (w *scopedWriter) SetDatabaseScopedConfig(ctx context.Context, d *gosmo.Database, name, value string, replica gosmo.ScopedConfigReplica) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.SetDatabaseScopedConfig(ctx, d, name, value, replica)
	}
	w.calls = append(w.calls, fmt.Sprintf("%s %s=%s %d", d.Name, name, value, replica))
	if w.err != nil || w.ignore {
		return w.err
	}
	for _, c := range w.cat.cfgs {
		if c.Name != name {
			continue
		}
		raw := value
		switch value {
		case "ON":
			raw = "1"
		case "OFF":
			if c.ValueType == "bit" {
				raw = "0"
			}
		}
		if replica == gosmo.ScopedConfigPrimary {
			c.Value = raw
		} else if value == scopedPrimary {
			c.ValueForSecondary, c.ValueForSecondaryType = "", ""
		} else {
			c.ValueForSecondary, c.ValueForSecondaryType = raw, c.ValueType
		}
	}
	return nil
}

// runScoped runs db scoped-config <argv> (list, get or set first) over cat
// and w, an ad-hoc connection; dials counts the connects.
func runScoped(t *testing.T, cat *scopedCatalog, w *scopedWriter, argv string) (res *output.Result, dials int, err error) {
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
	args0 := append([]string{"db", "scoped-config", fields[0], "--server", "srv", "--auth", "windows"}, fields[1:]...)
	inv, perr := args.Parse(&spec.Tree{Commands: DBCommands(d)}, args0, args.Options{})
	if perr != nil {
		t.Fatalf("%q: %v", argv, perr)
	}
	res, err = inv.Command.Run(context.Background(), inv)
	return res, dials, err
}

func TestDBScopedConfigList(t *testing.T) {
	res, _, err := runScoped(t, newScopedCatalog(), &scopedWriter{}, "list app")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"ELEVATE_ONLINE", "OFF", "NULL", "1", "nvarchar"},
		{"IDENTITY_CACHE", "1", "NULL", "1", "bit"},
		{"LEGACY_CARDINALITY_ESTIMATION", "0", "NULL", "1", "bit"},
		{"MAXDOP", "0", "2", "0", "int"},
		{"PARAMETER_SNIFFING", "1", "NULL", "1", "bit"},
	}
	if got := setRows(res.Sets[0]); !slices.EqualFunc(got, want, slices.Equal) || res.Database != "app" {
		t.Errorf("rows %q, want %q (database %q)", got, want, res.Database)
	}
	// 2016 has no is_value_default: NULL, not the false gosmo reads.
	cat := newScopedCatalog()
	cat.info.VersionMajor = 13
	res, _, _ = runScoped(t, cat, &scopedWriter{}, "list app")
	for _, r := range setRows(res.Sets[0]) {
		if r[3] != "NULL" {
			t.Errorf("13: %q", r)
		}
	}
	// Azure reports 12 and has the column.
	cat.info = &gosmo.ServerInfo{VersionMajor: 12, EngineEdition: int(gosmo.EngineAzureManagedInst)}
	res, _, _ = runScoped(t, cat, &scopedWriter{}, "list app")
	if r := setRows(res.Sets[0])[0]; r[3] != "1" {
		t.Errorf("Azure: %q", r)
	}
}

func TestDBScopedConfigGet(t *testing.T) {
	res, _, err := runScoped(t, newScopedCatalog(), &scopedWriter{}, "get app maxdop PARAMETER_SNIFFING MAXDOP")
	if err != nil {
		t.Fatal(err)
	}
	if got := setRows(res.Sets[0]); len(got) != 2 || got[0][0] != "MAXDOP" || got[1][0] != "PARAMETER_SNIFFING" {
		t.Errorf("rows %q", got)
	}
	_, _, err = runScoped(t, newScopedCatalog(), &scopedWriter{}, "get app cardinality")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeNotFound || e.Hint != "Did you mean LEGACY_CARDINALITY_ESTIMATION?" {
		t.Errorf("%v", err)
	}
	_, _, err = runScoped(t, newScopedCatalog(), &scopedWriter{}, "get app nothing_like_it")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeNotFound || !strings.Contains(e.Hint, "db scoped-config list app") {
		t.Errorf("%v", err)
	}
}

// The database must be there, ONLINE and accessible: 8, 8 and 9, and its
// configurations are never read otherwise.
func TestDBScopedConfigUnreadableDatabase(t *testing.T) {
	for argv, code := range map[string]output.Code{
		"list nosuch": output.CodeNotFound, "list archive": output.CodeNotFound, "get hr MAXDOP": output.CodePermissionDenied,
		"set archive MAXDOP 4 --allow admin": output.CodeNotFound,
	} {
		cat, w := newScopedCatalog(), &scopedWriter{}
		_, _, err := runScoped(t, cat, w, argv)
		if errCode(err) != code || cat.reads != 0 || len(w.calls) != 0 {
			t.Errorf("%q: %v (want %s), %d reads, calls %q", argv, err, code, cat.reads, w.calls)
		}
	}
}

func TestDBScopedConfigSet(t *testing.T) {
	for _, tc := range []struct {
		argv, call string
		row        []string
	}{
		{"set app maxdop 4", "app MAXDOP=4 0", []string{"MAXDOP", "primary", "0", "4", "4"}},
		{"set app legacy_cardinality_estimation on", "app LEGACY_CARDINALITY_ESTIMATION=ON 0",
			[]string{"LEGACY_CARDINALITY_ESTIMATION", "primary", "OFF", "ON", "ON"}},
		// 1 and 0 are sent as ON and OFF: the statement refuses a number.
		{"set app PARAMETER_SNIFFING 0", "app PARAMETER_SNIFFING=OFF 0", []string{"PARAMETER_SNIFFING", "primary", "ON", "OFF", "OFF"}},
		{"set app ELEVATE_ONLINE when_supported", "app ELEVATE_ONLINE=WHEN_SUPPORTED 0",
			[]string{"ELEVATE_ONLINE", "primary", "OFF", "WHEN_SUPPORTED", "WHEN_SUPPORTED"}},
		// A secondary value of its own, and PRIMARY (NULL in the view) back.
		{"set app MAXDOP 1 --for-secondary", "app MAXDOP=1 1", []string{"MAXDOP", "secondary", "2", "1", "1"}},
		{"set app MAXDOP primary --for-secondary", "app MAXDOP=PRIMARY 1", []string{"MAXDOP", "secondary", "2", "PRIMARY", "PRIMARY"}},
		{"set app PARAMETER_SNIFFING 1 --for-secondary", "app PARAMETER_SNIFFING=ON 1",
			[]string{"PARAMETER_SNIFFING", "secondary", "PRIMARY", "ON", "ON"}},
	} {
		cat, w := newScopedCatalog(), &scopedWriter{}
		res, dials, err := runScoped(t, cat, w, tc.argv+" --allow admin")
		if err != nil {
			t.Fatalf("%q: %v", tc.argv, err)
		}
		set := res.Sets[0]
		if dials != 1 || !slices.Equal(w.calls, []string{tc.call}) || cat.reads != 2 || set.Name != "change" {
			t.Errorf("%q: %d dials, calls %q, %d reads, set %q", tc.argv, dials, w.calls, cat.reads, set.Name)
		}
		if got := setRows(set); !slices.EqualFunc(got, [][]string{tc.row}, slices.Equal) {
			t.Errorf("%q: rows %q, want %q", tc.argv, got, tc.row)
		}
		if fieldValue(res, "dryRun") != "false" || hasField(res, "tsql") || len(res.Warnings) != 0 {
			t.Errorf("%q: fields %+v, warnings %q", tc.argv, res.Fields, res.Warnings)
		}
	}
}

// Without --allow admin nothing dials (the 5.b gate); list and get need none.
func TestDBScopedConfigGate(t *testing.T) {
	for _, argv := range []string{"set app MAXDOP 4", "set app MAXDOP 4 --dry-run", "set app MAXDOP 4 --allow write"} {
		w := &scopedWriter{}
		_, dials, err := runScoped(t, newScopedCatalog(), w, argv)
		if errCode(err) != output.CodeBlocked || dials != 0 || len(w.calls) != 0 {
			t.Errorf("%q: %v, %d dials, calls %q", argv, err, dials, w.calls)
		}
	}
}

// A value the configuration's type does not take is exit 2, before anything
// runs; so is PRIMARY for the primary (Msg 12109 on the server).
func TestDBScopedConfigSetValues(t *testing.T) {
	for _, argv := range []string{
		"set app MAXDOP PRIMARY", "set app PARAMETER_SNIFFING 2", "set app PARAMETER_SNIFFING yes",
		"set app MAXDOP ON", "set app MAXDOP 1.5", "set app ELEVATE_ONLINE 'x'",
		"set app ELEVATE_ONLINE 1ON", "set app ELEVATE_ONLINE WHEN-SUPPORTED",
	} {
		cat, w := newScopedCatalog(), &scopedWriter{}
		_, _, err := runScoped(t, cat, w, argv+" --allow admin")
		if errCode(err) != output.CodeUsageError || len(w.calls) != 0 || cat.reads != 1 {
			t.Errorf("%q: %v, calls %q, %d reads", argv, err, w.calls, cat.reads)
		}
	}
	// A negative number follows --, after the options.
	cat, w := newScopedCatalog(), &scopedWriter{}
	if _, _, err := runScoped(t, cat, w, "set app MAXDOP --allow admin -- -1"); errCode(err) != output.CodeUsageError || len(w.calls) != 0 {
		t.Errorf("-1: %v, calls %q", err, w.calls)
	}
	_, _, err := runScoped(t, newScopedCatalog(), &scopedWriter{}, "set app MAXDOOP 4 --allow admin")
	if errCode(err) != output.CodeNotFound {
		t.Errorf("unknown name: %v", err)
	}
}

// --dry-run: gosmo's statement in the database, the planned change, nothing
// applied and no second read.
func TestDBScopedConfigDryRun(t *testing.T) {
	cat, w := newScopedCatalog(), &scopedWriter{}
	res, _, err := runScoped(t, cat, w, "set app MAXDOP 2 --for-secondary --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 0 || cat.reads != 1 {
		t.Errorf("a dry run applied %q, %d reads", w.calls, cat.reads)
	}
	if got, want := fieldValue(res, "tsql"), "USE [app];\nGO\nALTER DATABASE SCOPED CONFIGURATION FOR SECONDARY SET MAXDOP = 2\nGO\n"; got != want {
		t.Errorf("tsql %q, want %q", got, want)
	}
	set := res.Sets[0]
	if got := setRows(set); set.Name != "planned_change" || fieldValue(res, "dryRun") != "true" ||
		!slices.EqualFunc(got, [][]string{{"MAXDOP", "secondary", "2", "2"}}, slices.Equal) {
		t.Errorf("set %q rows %q, fields %+v", set.Name, got, res.Fields)
	}
}

// A change the second read does not show is a warning.
func TestDBScopedConfigNotTaken(t *testing.T) {
	res, _, err := runScoped(t, newScopedCatalog(), &scopedWriter{ignore: true}, "set app MAXDOP 4 --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if got := setRows(res.Sets[0]); !slices.EqualFunc(got, [][]string{{"MAXDOP", "primary", "0", "4", "0"}}, slices.Equal) {
		t.Errorf("rows %q", got)
	}
	if !slices.Equal(res.Warnings, []string{"MAXDOP is 0 for the primary, not the 4 requested."}) {
		t.Errorf("warnings %q", res.Warnings)
	}
}

// The server's refusals: no permission (Msg 15247, live-captured on 17 and
// 13) is exit 9 with the permission named;
// a configuration with no secondary value (Msg 12110, live-captured on 17)
// gets a hint; neither reads back.
func TestDBScopedConfigRefused(t *testing.T) {
	cat := newScopedCatalog()
	w := &scopedWriter{err: sqlErr(15247, 16, "User does not have permission to perform this action.")}
	_, _, err := runScoped(t, cat, w, "set app MAXDOP 4 --allow admin")
	if errCode(err) != output.CodePermissionDenied || !strings.Contains(err.(*output.Error).Hint, "ALTER ANY DATABASE SCOPED CONFIGURATION") || cat.reads != 1 {
		t.Errorf("%v, %d reads", err, cat.reads)
	}
	w = &scopedWriter{err: sqlErr(12110, 16, "Statement 'ALTER DATABASE SCOPED CONFIGURATION' failed, because it attempted "+
		"to set the 'IDENTITY_CACHE' option for the secondaries replica while this option is only allowed to be set for the primary.")}
	_, _, err = runScoped(t, newScopedCatalog(), w, "set app IDENTITY_CACHE OFF --for-secondary --allow admin")
	if errCode(err) != output.CodeSQLError || err.(*output.Error).Hint != "IDENTITY_CACHE has no secondary value: set it without --for-secondary." {
		t.Errorf("%v", err)
	}
}

// On a readable secondary the database is read-only (Msg 3906, live on
// ubusql2): the hint sends the change to the primary.
func TestDBScopedConfigOnSecondary(t *testing.T) {
	w := &scopedWriter{err: sqlErr(3906, 16, `Failed to update database "app" because the database is read-only.`)}
	_, _, err := runScoped(t, newScopedCatalog(), w, "set app MAXDOP 1 --for-secondary --allow admin")
	if errCode(err) != output.CodeSQLError || !strings.Contains(err.(*output.Error).Hint, "run the change on the primary") {
		t.Errorf("%v", err)
	}
}

// A failed second read after a change that ran says so.
func TestDBScopedConfigReadBackFails(t *testing.T) {
	cat := newScopedCatalog()
	cat.readErr, cat.afterReads = fmt.Errorf("gosmo: connection reset"), 1
	_, _, err := runScoped(t, cat, &scopedWriter{}, "set app MAXDOP 4 --allow admin")
	if e, ok := err.(*output.Error); !ok || !strings.HasPrefix(e.Message, "the change ran, but sys.database_scoped_configurations could not be read back") {
		t.Errorf("%v", err)
	}
}

// --- db set -----------------------------------------------------------------

// dbSetCatalog is newStorageCatalog's databases with app's options and Query
// Store state; every read is a copy, as from the server. info is the
// server's.
type dbSetCatalog struct {
	*storageCatalog
	info    *gosmo.ServerInfo
	db      gosmo.Database
	opts    gosmo.DatabaseOptions
	qs      gosmo.QueryStoreInfo
	reads   int // Options reads
	readErr error
}

func (c *dbSetCatalog) ServerInfo() *gosmo.ServerInfo { return c.info }

func (c *dbSetCatalog) DatabaseByName(ctx context.Context, name string) (*gosmo.Database, error) {
	if strings.EqualFold(name, c.db.Name) {
		return new(c.db), nil
	}
	return c.storageCatalog.DatabaseByName(ctx, name)
}

func (c *dbSetCatalog) DatabaseOptions(context.Context, *gosmo.Database) (*gosmo.DatabaseOptions, error) {
	c.reads++
	if c.readErr != nil && c.reads > 1 {
		return nil, c.readErr
	}
	return new(c.opts), nil
}

func (c *dbSetCatalog) QueryStoreOptions(context.Context, *gosmo.Database) (*gosmo.QueryStoreInfo, error) {
	return new(c.qs), nil
}

func newDBSetCatalog() *dbSetCatalog {
	return &dbSetCatalog{
		storageCatalog: newStorageCatalog(),
		info:           &gosmo.ServerInfo{VersionMajor: 17},
		db: gosmo.Database{Name: "app", ID: 5, State: gosmo.DatabaseOnline, RecoveryModel: gosmo.RecoveryModelSimple,
			CompatibilityLevel: gosmo.CompatLevel2022},
		opts: gosmo.DatabaseOptions{PageVerify: "TORN_PAGE_DETECTION", DefaultCursor: "GLOBAL", Containment: "NONE",
			SnapshotIsolation: "OFF", AutoCreateStats: true, AutoUpdateStats: true},
		qs: gosmo.QueryStoreInfo{DesiredState: gosmo.QueryStoreOff, ActualState: gosmo.QueryStoreOff, MaxStorageMB: 123,
			CaptureMode: gosmo.QueryStoreCaptureAll, SizeCleanupMode: gosmo.QueryStoreCleanupOff, StaleThresholdDays: 11,
			FlushIntervalSec: 300, IntervalMinutes: 15, MaxPlansPerQuery: 77, WaitStatsCaptureMode: gosmo.QueryStoreWaitStatsOff},
	}
}

// dbSetWriter applies each change to its catalog as the server does; failAt
// (1-based) makes that call fail with err, and block makes every call wait
// for its context. Under WithScript it is the real serverWriter over a
// never-connected server, so a dry run captures gosmo's statements.
type dbSetWriter struct {
	Writer
	cat    *dbSetCatalog
	calls  []string
	qsOpts gosmo.QueryStoreOptions
	failAt int
	err    error
	block  bool
	ignore bool
}

func (w *dbSetWriter) call(ctx context.Context, s string) error {
	w.calls = append(w.calls, s)
	if w.block {
		<-ctx.Done()
		return fmt.Errorf("gosmo: %s: %w", s, ctx.Err())
	}
	if w.failAt == len(w.calls) {
		return w.err
	}
	if w.ignore {
		return errIgnored
	}
	return nil
}

var errIgnored = errors.New("ignored")

func scriptDB(d *gosmo.Database) *gosmo.Database { return (&gosmo.Server{}).DatabaseRef(d.Name) }

func (w *dbSetWriter) SetDatabaseOption(ctx context.Context, d *gosmo.Database, opt gosmo.DatabaseOption, value string, term gosmo.Termination) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.SetDatabaseOption(ctx, scriptDB(d), opt, value, term)
	}
	if err := w.call(ctx, fmt.Sprintf("%s=%s %d", opt, value, term)); err != nil {
		return ignoredNil(err)
	}
	o := &w.cat.opts
	switch opt {
	case gosmo.DBOptPageVerify:
		o.PageVerify = value
	case gosmo.DBOptAutoUpdateStatisticsAsync:
		o.AutoUpdateStatsAsync = value == "ON"
	case gosmo.DBOptReadCommittedSnapshot:
		o.ReadCommittedSnapshot = value == "ON"
	case gosmo.DBOptSnapshotIsolation:
		o.SnapshotIsolation = value
	}
	return nil
}

func (w *dbSetWriter) SetRecoveryModel(ctx context.Context, d *gosmo.Database, model gosmo.RecoveryModel) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.SetRecoveryModel(ctx, scriptDB(d), model)
	}
	if err := w.call(ctx, "RECOVERY="+string(model)); err != nil {
		return ignoredNil(err)
	}
	w.cat.db.RecoveryModel = model
	return nil
}

func (w *dbSetWriter) SetCompatibilityLevel(ctx context.Context, d *gosmo.Database, level gosmo.CompatibilityLevel) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.SetCompatibilityLevel(ctx, scriptDB(d), level)
	}
	if err := w.call(ctx, fmt.Sprintf("COMPATIBILITY_LEVEL=%d", level)); err != nil {
		return ignoredNil(err)
	}
	w.cat.db.CompatibilityLevel = level
	return nil
}

func (w *dbSetWriter) SetReadOnly(ctx context.Context, d *gosmo.Database, readOnly bool, term gosmo.Termination) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.SetReadOnly(ctx, scriptDB(d), readOnly, term)
	}
	if err := w.call(ctx, fmt.Sprintf("READ_ONLY=%v %d", readOnly, term)); err != nil {
		return ignoredNil(err)
	}
	w.cat.db.IsReadOnly = readOnly
	return nil
}

func (w *dbSetWriter) SetQueryStoreOptions(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreOptions) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.SetQueryStoreOptions(ctx, scriptDB(d), opts)
	}
	if err := w.call(ctx, "QUERY_STORE="+string(opts.DesiredState)); err != nil {
		return ignoredNil(err)
	}
	w.qsOpts = opts
	w.cat.qs.DesiredState, w.cat.qs.ActualState = opts.DesiredState, opts.DesiredState
	return nil
}

// ignoredNil is a call's error, nil for one the writer ignores.
func ignoredNil(err error) error {
	if errors.Is(err, errIgnored) {
		return nil
	}
	return err
}

// runDBSet runs db set <argv> over cat and w, an ad-hoc connection; dials
// counts the connects.
func runDBSet(t *testing.T, cat *dbSetCatalog, w *dbSetWriter, argv string) (res *output.Result, dials int, err error) {
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
	args0 := append([]string{"db", "set", "--server", "srv", "--auth", "windows"}, strings.Fields(argv)...)
	inv, perr := args.Parse(&spec.Tree{Commands: DBCommands(d)}, args0, args.Options{})
	if perr != nil {
		t.Fatalf("%q: %v", argv, perr)
	}
	res, err = inv.Command.Run(context.Background(), inv)
	return res, dials, err
}

// The changes run in order, one call each, keys and values ignoring case;
// QUERY_STORE=ON is READ_WRITE and re-sends every other option as read; the
// change set comes from a second read.
func TestDBSet(t *testing.T) {
	cat, w := newDBSetCatalog(), &dbSetWriter{}
	res, dials, err := runDBSet(t, cat, w, "app --option recovery=full --option PAGE_VERIFY=checksum "+
		"--option QUERY_STORE=on --option COMPATIBILITY_LEVEL=170 --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{"RECOVERY=FULL", "PAGE_VERIFY=CHECKSUM 0", "QUERY_STORE=READ_WRITE", "COMPATIBILITY_LEVEL=170"}
	if dials != 1 || !slices.Equal(w.calls, wantCalls) || cat.reads != 2 {
		t.Errorf("%d dials, calls %q, %d reads", dials, w.calls, cat.reads)
	}
	want := [][]string{
		{"RECOVERY", "SIMPLE", "FULL", "FULL"},
		{"PAGE_VERIFY", "TORN_PAGE_DETECTION", "CHECKSUM", "CHECKSUM"},
		{"QUERY_STORE", "OFF", "READ_WRITE", "READ_WRITE"},
		{"COMPATIBILITY_LEVEL", "160", "170", "170"},
	}
	set := res.Sets[0]
	if got := setRows(set); set.Name != "change" || !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("set %s %q", set.Name, got)
	}
	keep := gosmo.QueryStoreOptions{DesiredState: gosmo.QueryStoreReadWrite, MaxStorageMB: 123, CaptureMode: gosmo.QueryStoreCaptureAll,
		SizeCleanupMode: gosmo.QueryStoreCleanupOff, StaleThresholdDays: 11, FlushIntervalSec: 300, IntervalMinutes: 15,
		MaxPlansPerQuery: 77, WaitStatsCaptureMode: gosmo.QueryStoreWaitStatsOff}
	if w.qsOpts != keep {
		t.Errorf("Query Store options sent %+v", w.qsOpts)
	}
	if fieldValue(res, "dryRun") != "false" || hasField(res, "tsql") || len(res.Warnings) != 0 || res.Database != "app" {
		t.Errorf("fields %v, warnings %q, database %q", res.Fields, res.Warnings, res.Database)
	}
}

// Every argument error is exit 2 before connecting; a --rollback-immediate
// without its --confirm is BLOCKED, also before connecting.
func TestDBSetArgumentErrors(t *testing.T) {
	for argv, code := range map[string]output.Code{
		"app":                                                           output.CodeUsageError,
		"app --option RECOVERY":                                         output.CodeUsageError,
		"app --option =FULL":                                            output.CodeUsageError,
		"app --option RECOVERY=":                                        output.CodeUsageError,
		"app --option USER_ACCESS=SINGLE_USER":                          output.CodeUsageError,
		"app --option RECOVERY=PARTIAL":                                 output.CodeUsageError,
		"app --option AUTO_CLOSE=1":                                     output.CodeUsageError,
		"app --option QUERY_STORE=ERROR":                                output.CodeUsageError,
		"app --option PAGE_VERIFY=ON":                                   output.CodeUsageError,
		"app --option COMPATIBILITY_LEVEL=90":                           output.CodeUsageError,
		"app --option COMPATIBILITY_LEVEL=155":                          output.CodeUsageError,
		"app --option COMPATIBILITY_LEVEL=abc":                          output.CodeUsageError,
		"app --option RECOVERY=FULL --option recovery=SIMPLE":           output.CodeUsageError,
		"app --option RECOVERY=FULL --timeout -1":                       output.CodeUsageError,
		"app --option RECOVERY=FULL --confirm app":                      output.CodeUsageError,
		"app --option RECOVERY=FULL --rollback-immediate --confirm app": output.CodeUsageError,
		"app --option ALLOW_SNAPSHOT_ISOLATION=ON --rollback-immediate --confirm app": output.CodeUsageError,
		"app --option READ_COMMITTED_SNAPSHOT=ON --rollback-immediate":                output.CodeBlocked,
		"app --option READ_COMMITTED_SNAPSHOT=ON --rollback-immediate --confirm APP":  output.CodeBlocked,
		"app --option READ_ONLY=ON --rollback-immediate --confirm other":              output.CodeBlocked,
	} {
		w := &dbSetWriter{}
		_, dials, err := runDBSet(t, newDBSetCatalog(), w, argv+" --allow admin")
		if errCode(err) != code || dials != 0 || len(w.calls) != 0 {
			t.Errorf("%q: %v, %d dials, calls %q", argv, err, dials, w.calls)
		}
	}
	_, _, err := runDBSet(t, newDBSetCatalog(), &dbSetWriter{}, "app --option NOSUCH=ON --allow admin")
	if e, ok := err.(*output.Error); !ok || !strings.Contains(e.Hint, "AUTO_CLOSE, AUTO_CREATE_STATISTICS,") ||
		!strings.Contains(e.Hint, "QUERY_STORE, QUOTED_IDENTIFIER, READ_COMMITTED_SNAPSHOT, READ_ONLY, RECOVERY,") {
		t.Errorf("%v", err)
	}
	_, _, err = runDBSet(t, newDBSetCatalog(), &dbSetWriter{}, "app --option READ_ONLY=ON --rollback-immediate --allow admin")
	if e, ok := err.(*output.Error); !ok || e.Hint != "Add --confirm app to accept it." || !strings.Contains(e.Message, "disconnects every other session in [app]") {
		t.Errorf("%v", err)
	}
}

// Without --allow admin nothing dials (the 5.b gate), --dry-run included.
func TestDBSetGate(t *testing.T) {
	for _, argv := range []string{"app --option RECOVERY=FULL", "app --option RECOVERY=FULL --dry-run",
		"app --option RECOVERY=FULL --allow ddl"} {
		w := &dbSetWriter{}
		_, dials, err := runDBSet(t, newDBSetCatalog(), w, argv)
		if errCode(err) != output.CodeBlocked || dials != 0 || len(w.calls) != 0 {
			t.Errorf("%q: %v, %d dials, calls %q", argv, err, dials, w.calls)
		}
	}
}

// Once connected: an unknown database is exit 8, a level above the server's
// exit 2; nothing is written. Azure's levels are the server's to check.
func TestDBSetServerChecks(t *testing.T) {
	w := &dbSetWriter{}
	if _, _, err := runDBSet(t, newDBSetCatalog(), w, "nosuch --option RECOVERY=FULL --allow admin"); errCode(err) != output.CodeNotFound || len(w.calls) != 0 {
		t.Errorf("%v, calls %q", err, w.calls)
	}
	cat := newDBSetCatalog()
	cat.info.VersionMajor = 13
	_, _, err := runDBSet(t, cat, w, "app --option RECOVERY=FULL --option COMPATIBILITY_LEVEL=140 --allow admin")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeUsageError || e.Hint != "Give a level from 100 to 130." || len(w.calls) != 0 {
		t.Errorf("%v, calls %q", err, w.calls)
	}
	cat = newDBSetCatalog()
	cat.info = &gosmo.ServerInfo{VersionMajor: 12, EngineEdition: int(gosmo.EngineAzureManagedInst)}
	if _, _, err := runDBSet(t, cat, w, "app --option COMPATIBILITY_LEVEL=170 --allow admin"); err != nil || !slices.Equal(w.calls, []string{"COMPATIBILITY_LEVEL=170"}) {
		t.Errorf("Azure: %v, calls %q", err, w.calls)
	}
}

// --rollback-immediate reaches only the options that need the database to
// themselves; the others are sent without a termination clause.
func TestDBSetRollbackImmediate(t *testing.T) {
	w := &dbSetWriter{}
	_, _, err := runDBSet(t, newDBSetCatalog(), w, "app --option ALLOW_SNAPSHOT_ISOLATION=ON --option READ_COMMITTED_SNAPSHOT=ON "+
		"--option READ_ONLY=ON --rollback-immediate --confirm app --allow admin")
	want := []string{"ALLOW_SNAPSHOT_ISOLATION=ON 0", "READ_COMMITTED_SNAPSHOT=ON 1", "READ_ONLY=true 1"}
	if err != nil || !slices.Equal(w.calls, want) {
		t.Errorf("%v, calls %q", err, w.calls)
	}
}

// A dry run reads the state, writes nothing, and prints gosmo's statements
// in order with the planned_change set; a key that would wait is warned of.
func TestDBSetDryRun(t *testing.T) {
	cat, w := newDBSetCatalog(), &dbSetWriter{}
	res, _, err := runDBSet(t, cat, w, "app --option RECOVERY=FULL --option READ_COMMITTED_SNAPSHOT=ON --option QUERY_STORE=OFF --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 0 || cat.reads != 1 {
		t.Errorf("calls %q, %d reads", w.calls, cat.reads)
	}
	tsql := fieldValue(res, "tsql")
	r, rcsi, qs := strings.Index(tsql, "SET RECOVERY FULL"), strings.Index(tsql, "SET READ_COMMITTED_SNAPSHOT ON"), strings.Index(tsql, "SET QUERY_STORE = OFF")
	if r < 0 || rcsi < r || qs < rcsi || strings.Contains(tsql, "ROLLBACK") || fieldValue(res, "dryRun") != "true" {
		t.Errorf("tsql %q", tsql)
	}
	want := [][]string{{"RECOVERY", "SIMPLE", "FULL"}, {"READ_COMMITTED_SNAPSHOT", "OFF", "ON"}, {"QUERY_STORE", "OFF", "OFF"}}
	if got := setRows(res.Sets[0]); res.Sets[0].Name != "planned_change" || !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("set %q", got)
	}
	if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "READ_COMMITTED_SNAPSHOT needs the database to itself") {
		t.Errorf("warnings %q", res.Warnings)
	}

	res, _, _ = runDBSet(t, newDBSetCatalog(), &dbSetWriter{}, "app --option READ_ONLY=ON --rollback-immediate --confirm app --allow admin --dry-run")
	if tsql := fieldValue(res, "tsql"); !strings.Contains(tsql, "SET READ_ONLY WITH ROLLBACK IMMEDIATE") || len(res.Warnings) != 0 {
		t.Errorf("tsql %q, warnings %q", tsql, res.Warnings)
	}
}

// The first failure stops the run: exit 5, the change set shows every key
// after a second read, and the keys not attempted are named.
func TestDBSetPartialFailure(t *testing.T) {
	cat := newDBSetCatalog()
	// The server's last message is the contentless Msg 5069; the error is
	// the first, which says why (live on 17).
	cause := mssql.Error{Number: 12824, Class: 16, Message: "The sp_configure value 'contained database authentication' must be set to 1."}
	failed := mssql.Error{Number: 5069, Class: 16, Message: "ALTER DATABASE statement failed."}
	failed.All = []mssql.Error{cause, failed}
	w := &dbSetWriter{failAt: 2, err: fmt.Errorf("gosmo: set CONTAINMENT: %w", failed)}
	res, _, err := runDBSet(t, cat, w, "app --option RECOVERY=FULL --option CONTAINMENT=PARTIAL --option PAGE_VERIFY=CHECKSUM --allow admin")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeSQLError || e.SQL.Number != 12824 || e.Message != cause.Message ||
		len(w.calls) != 2 || cat.reads != 2 {
		t.Fatalf("%v, calls %q, %d reads", err, w.calls, cat.reads)
	}
	want := [][]string{
		{"RECOVERY", "SIMPLE", "FULL", "FULL"},
		{"CONTAINMENT", "NONE", "PARTIAL", "NONE"},
		{"PAGE_VERIFY", "TORN_PAGE_DETECTION", "CHECKSUM", "TORN_PAGE_DETECTION"},
	}
	if got := setRows(res.Sets[0]); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("set %q", got)
	}
	if !slices.Equal(res.Warnings, []string{"Not attempted after CONTAINMENT failed: PAGE_VERIFY."}) {
		t.Errorf("warnings %q", res.Warnings)
	}

	// A failed read-back keeps the write's error.
	cat = newDBSetCatalog()
	cat.readErr = errors.New("gosmo: connection reset")
	if _, _, err := runDBSet(t, cat, &dbSetWriter{failAt: 1, err: w.err}, "app --option CONTAINMENT=PARTIAL --allow admin"); errCode(err) != output.CodeSQLError {
		t.Errorf("%v", err)
	}
	// ... and after a change that ran, says so.
	cat = newDBSetCatalog()
	cat.readErr = errors.New("gosmo: connection reset")
	_, _, err = runDBSet(t, cat, &dbSetWriter{}, "app --option RECOVERY=FULL --allow admin")
	if e, ok := err.(*output.Error); !ok || !strings.HasPrefix(e.Message, "the change ran, but the database's options could not be read back") {
		t.Errorf("%v", err)
	}
}

// The server's refusals: no ALTER on the database (Msg 5011, which gosmo
// reads as missing or denied) is exit 9; a lock refused (Msg 5061) points at
// --rollback-immediate; a database made read-only earlier in the run (Msg
// 3906) at the order.
func TestDBSetRefused(t *testing.T) {
	w := &dbSetWriter{failAt: 1, err: fmt.Errorf("gosmo: set recovery model: %w", mssql.Error{Number: 5011, Class: 14,
		Message: "User does not have permission to alter database 'app', the database does not exist, or the database is not in a state that allows access checks."})}
	_, _, err := runDBSet(t, newDBSetCatalog(), w, "app --option RECOVERY=FULL --allow admin")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodePermissionDenied || !strings.Contains(e.Hint, "ALTER on the database") {
		t.Errorf("%v", err)
	}
	w = &dbSetWriter{failAt: 1, err: fmt.Errorf("gosmo: set CONTAINMENT: %w", mssql.Error{Number: 5061, Class: 16,
		Message: "ALTER DATABASE failed because a lock could not be placed on database 'app'. Try again later."})}
	_, _, err = runDBSet(t, newDBSetCatalog(), w, "app --option CONTAINMENT=PARTIAL --allow admin")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeSQLError || !strings.Contains(e.Hint, "--rollback-immediate --confirm app") {
		t.Errorf("%v", err)
	}
	w = &dbSetWriter{failAt: 2, err: fmt.Errorf("gosmo: set compatibility level: %w", mssql.Error{Number: 3906, Class: 16,
		Message: `Failed to update database "app" because the database is read-only.`})}
	_, _, err = runDBSet(t, newDBSetCatalog(), w, "app --option READ_ONLY=ON --option COMPATIBILITY_LEVEL=150 --allow admin")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeSQLError || !strings.Contains(e.Hint, "give READ_ONLY=ON last") {
		t.Errorf("%v", err)
	}
}

// A change waiting past --timeout is TIMEOUT; for an option needing the
// database to itself the hint names --rollback-immediate. The read-back
// still runs.
func TestDBSetTimeout(t *testing.T) {
	cat := newDBSetCatalog()
	res, _, err := runDBSet(t, cat, &dbSetWriter{block: true}, "app --option READ_COMMITTED_SNAPSHOT=ON --timeout 1 --allow admin")
	e, ok := err.(*output.Error)
	if !ok || e.Code != output.CodeTimeout || !strings.Contains(e.Hint, "--rollback-immediate --confirm app") ||
		e.Message != "READ_COMMITTED_SNAPSHOT=ON ran longer than --timeout 1 s and was cancelled" {
		t.Fatalf("%v", err)
	}
	if cat.reads != 2 || len(res.Sets) != 1 {
		t.Errorf("%d reads, sets %v", cat.reads, res.Sets)
	}
}

// A change the server took without error but did not make is a warning.
func TestDBSetNotTaken(t *testing.T) {
	res, _, err := runDBSet(t, newDBSetCatalog(), &dbSetWriter{ignore: true}, "app --option RECOVERY=FULL --allow admin")
	if err != nil || !slices.Equal(res.Warnings, []string{"RECOVERY is SIMPLE, not the FULL requested."}) {
		t.Errorf("%v, warnings %q", err, res.Warnings)
	}
}
