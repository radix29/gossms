package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/clitest"
	"slices"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/cmd"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// dbFake is gosmo over canned rows (gosmotest) for the db and agent
// commands: the connection's database, the databases, their sizes, options
// and space used, and — for agent context — the server info, the permission
// probes and HAS_DBACCESS. Its fields are read as each query arrives. A nil
// sizes map with sizesErr set is a failed size read, a nil options a failed
// options read; a nil srv or dbc with its error set is a failed probe.
type dbFake struct {
	current  string
	dbs      []*gosmo.Database
	sizes    map[int]gosmo.DatabaseSize
	sizesErr error
	options  *gosmo.DatabaseOptions

	info   *gosmo.ServerInfo // the connect reads' answer; nil keeps gosmotest's
	srv    *gosmo.Capabilities
	srvErr error
	dbc    *gosmo.DatabaseCapabilities
	dbcErr error
	// dbcBy answers for the databases it names, ahead of dbc.
	dbcBy  map[string]*gosmo.DatabaseCapabilities
	access map[int]bool
	// msdbErr fails the msdb lookup agent context makes for the job row.
	msdbErr error

	fake *gosmotest.Fake // the last fakeDeps' server
}

func newDBFake() *dbFake {
	created := time.Date(2026, 3, 14, 9, 12, 44, 0, time.UTC)
	return &dbFake{
		current: "app",
		dbs: []*gosmo.Database{
			{Name: "app", ID: 5, State: gosmo.DatabaseOnline, RecoveryModel: gosmo.RecoveryModelFull,
				CompatibilityLevel: 160, Collation: "Latin1_General_CI_AS", CatalogCollation: "Latin1_General_CI_AS", CreateDate: created},
			{Name: "archive", ID: 6, State: gosmo.DatabaseOffline, RecoveryModel: gosmo.RecoveryModelSimple,
				CompatibilityLevel: 150, CreateDate: created},
			{Name: "app_snap", ID: 7, State: gosmo.DatabaseOnline, SourceDatabaseID: 5, IsReadOnly: true,
				CompatibilityLevel: 160, Collation: "Latin1_General_CI_AS", CreateDate: created},
			{Name: "distribution", ID: 8, State: gosmo.DatabaseOnline, IsDistributor: true, CreateDate: created},
			{Name: "master", ID: 1, State: gosmo.DatabaseOnline, CreateDate: created},
		},
		sizes: map[int]gosmo.DatabaseSize{
			5: {DataKB: 2048, LogKB: 1024}, 6: {DataKB: 8192, LogKB: 8192}, 7: {DataKB: 2048},
			8: {DataKB: 1024, LogKB: 512}, 1: {DataKB: 5120, LogKB: 2048},
		},
		options: &gosmo.DatabaseOptions{Owner: "sa", UserAccess: gosmo.UserAccessMulti, PageVerify: "CHECKSUM",
			Containment: "NONE", TargetRecoveryTimeSeconds: 60, AutoCreateStats: true},
	}
}

// db is c's database name (msdb, for agent context's role probe, beside
// them), compared as the server's collation does; nil when there is none.
func (c *dbFake) db(name string) *gosmo.Database {
	if strings.EqualFold(name, "msdb") {
		return &gosmo.Database{Name: "msdb", ID: 4, State: gosmo.DatabaseOnline}
	}
	for _, d := range c.dbs {
		if strings.EqualFold(d.Name, name) {
			return d
		}
	}
	return nil
}

// capsOf is the probe of database name: dbcBy's, else dbc (dbcErr).
func (c *dbFake) capsOf(name string) (*gosmo.DatabaseCapabilities, error) {
	if dbc, ok := c.dbcBy[name]; ok {
		return dbc, nil
	}
	if c.dbc == nil {
		return nil, cmp.Or(c.dbcErr, errors.New("no capabilities"))
	}
	return c.dbc, c.dbcErr
}

// readIn is, for each query of the last run's containing sub, its first
// argument, or the database it ran in when it has none.
func (c *dbFake) readIn(sub string) []string {
	var out []string
	for _, q := range c.fake.Queries() {
		if strings.Contains(q.Text, sub) {
			if a, ok := q.Arg(0).(string); ok {
				out = append(out, a)
			} else {
				out = append(out, q.Database)
			}
		}
	}
	return out
}

// spaceUsed counts the reads that needed the database ONLINE.
func (c *dbFake) spaceUsed() int { return len(c.readIn("AS avail_log_mb")) }

// probed is the databases a permission probe was asked of.
func (c *dbFake) probed() []string { return c.readIn("SELECT HAS_DBACCESS(@p1)") }

// on answers the queries containing every one of subs with fn's rows, or
// its error as the query's.
func on(f *gosmotest.Fake, fn func(gosmotest.Query) ([][]any, error), subs ...string) {
	f.OnFunc(func(q gosmotest.Query) bool {
		for _, sub := range subs {
			if !strings.Contains(q.Text, sub) {
				return false
			}
		}
		return true
	}, func(q gosmotest.Query) gosmotest.Result {
		rows, err := fn(q)
		return gosmotest.Result{Sets: []gosmotest.Set{{Rows: rows}}, Err: err}
	})
}

// server is a gosmotest server answering c's reads.
func (c *dbFake) server(t *testing.T) *gosmotest.Fake {
	f := gosmotest.New(t)
	if i := c.info; i != nil {
		f.Info.Name, f.Info.ProductVersion, f.Info.Edition, f.Info.Login = i.Name, i.ProductVersion, i.Edition, i.Login
	}
	arg := func(q gosmotest.Query) string { s, _ := q.Arg(0).(string); return s }
	on(f, func(gosmotest.Query) ([][]any, error) { return [][]any{{c.current}}, nil }, "SELECT DB_NAME()")
	// In the ORDER BY name, under a case-insensitive collation.
	on(f, func(gosmotest.Query) ([][]any, error) {
		var rows [][]any
		for _, d := range slices.SortedFunc(slices.Values(c.dbs), func(a, b *gosmo.Database) int {
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		}) {
			rows = append(rows, clitest.DatabaseOf(d))
		}
		return rows, nil
	}, "FROM sys.databases", "ORDER BY name")
	on(f, func(q gosmotest.Query) ([][]any, error) {
		if strings.EqualFold(arg(q), "msdb") && c.msdbErr != nil {
			return nil, c.msdbErr
		}
		if d := c.db(arg(q)); d != nil {
			return [][]any{clitest.DatabaseOf(d)}, nil
		}
		return nil, nil
	}, "FROM sys.databases", "WHERE name = @p1")
	on(f, func(gosmotest.Query) ([][]any, error) {
		var rows [][]any
		for id, s := range c.sizes {
			rows = append(rows, clitest.DatabaseSizeRow(id, s))
		}
		return rows, c.sizesErr
	}, "GROUP  BY mf.database_id")
	on(f, func(gosmotest.Query) ([][]any, error) {
		if c.options == nil {
			return nil, errors.New("boom")
		}
		return [][]any{clitest.DatabaseOptionsRow(c.options)}, nil
	}, "FROM   sys.databases d", "WHERE  d.name = @p1")
	on(f, func(gosmotest.Query) ([][]any, error) {
		return [][]any{clitest.SpaceUsedRow(gosmo.SpaceInfo{UnallocatedMB: 1.5, AvailLogMB: 0.25})}, nil
	}, "AS avail_log_mb")
	on(f, func(gosmotest.Query) ([][]any, error) {
		if c.srv == nil {
			return nil, cmp.Or(c.srvErr, errors.New("no capabilities"))
		}
		return clitest.ServerCapabilityRows(c.srv), nil
	}, "IS_SRVROLEMEMBER(n.v)")
	on(f, func(q gosmotest.Query) ([][]any, error) {
		caps, err := c.capsOf(arg(q))
		if err != nil {
			return nil, err
		}
		return [][]any{{caps.Accessible}}, nil
	}, "SELECT HAS_DBACCESS(@p1)")
	on(f, func(q gosmotest.Query) ([][]any, error) {
		caps, err := c.capsOf(q.Database)
		if err != nil {
			return nil, err
		}
		return clitest.DatabaseCapabilityRows(caps), nil
	}, "IS_ROLEMEMBER(n.v)")
	on(f, func(gosmotest.Query) ([][]any, error) {
		var rows [][]any
		for id, ok := range c.access {
			rows = append(rows, clitest.DatabaseAccessRow(id, ok))
		}
		return rows, nil
	}, "HAS_DBACCESS(name)")
	c.fake = f
	return f
}

// fakeDeps is profileDeps (dev) with every connection to c's server.
func fakeDeps(t *testing.T, c *dbFake) cmd.Deps {
	f := c.server(t)
	return profileDeps(t, func(ctx context.Context, _ config.Connection, _ db.Role) (*db.ServerConn, error) {
		srv, err := f.Connect(ctx)
		return &db.ServerConn{Server: srv}, err
	})
}

// dbListNames is the name column of a db list answer.
func dbListNames(t *testing.T, env map[string]any) []string {
	t.Helper()
	var names []string
	for _, r := range env["resultSets"].([]any)[0].(map[string]any)["rows"].([]any) {
		names = append(names, r.([]any)[0].(string))
	}
	return names
}

func TestDBList(t *testing.T) {
	d := fakeDeps(t, newDBFake())

	r := runTree(t, context.Background(), newCommandTree(d), "db", "list", "-p", "dev", "-o", "json")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	env := envelope(t, r.stdout)
	// master and the distribution database are system; the snapshot is not.
	// By name, the read's ORDER BY.
	if got := strings.Join(dbListNames(t, env), ","); got != "app,app_snap,archive" {
		t.Errorf("names %s", got)
	}
	set := env["resultSets"].([]any)[0].(map[string]any)
	app := set["rows"].([]any)[0].([]any)
	// database_id is a JSON number, sizes are strings (decimal), read_only a bool.
	if app[1] != 5.0 || app[7] != "3.00" || app[8] != "2.00" || app[9] != "1.00" || app[6] != false {
		t.Errorf("app row %v", app)
	}
	snap := set["rows"].([]any)[1].([]any)
	if snap[12] != "app" || snap[6] != true {
		t.Errorf("snapshot row %v, want snapshot_of app and read_only", snap)
	}
	// An OFFLINE database has no collation in sys.databases: NULL, not "".
	if archive := set["rows"].([]any)[2].([]any); archive[5] != nil {
		t.Errorf("archive collation %v, want NULL", archive[5])
	}

	r = runTree(t, context.Background(), newCommandTree(d), "db", "list", "-p", "dev", "--include-system", "-o", "json")
	if got := strings.Join(dbListNames(t, envelope(t, r.stdout)), ","); got != "app,app_snap,archive,distribution,master" {
		t.Errorf("--include-system names %s", got)
	}
	r = runTree(t, context.Background(), newCommandTree(d), "db", "list", "-p", "dev", "--state", "offline", "-o", "json")
	if got := strings.Join(dbListNames(t, envelope(t, r.stdout)), ","); got != "archive" {
		t.Errorf("--state offline names %s", got)
	}
	r = runTree(t, context.Background(), newCommandTree(d), "db", "list", "-p", "dev", "--state", "gone")
	if r.exit != 2 {
		t.Errorf("--state gone: exit %d, want 2", r.exit)
	}
}

// A failed size read costs the sizes, not the list.
func TestDBListSizesUnread(t *testing.T) {
	cat := newDBFake()
	cat.sizes, cat.sizesErr = nil, errors.New("boom")
	r := runTree(t, context.Background(), newCommandTree(fakeDeps(t, cat)), "db", "list", "-p", "dev", "-o", "json")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	env := envelope(t, r.stdout)
	row := env["resultSets"].([]any)[0].(map[string]any)["rows"].([]any)[0].([]any)
	if row[7] != nil || len(env["warnings"].([]any)) != 1 {
		t.Errorf("size %v, warnings %v; want NULL and one warning", row[7], env["warnings"])
	}
}

func dbInfo(t *testing.T, cat *dbFake, name string) (int, map[string]any) {
	t.Helper()
	r := runTree(t, context.Background(), newCommandTree(fakeDeps(t, cat)), "db", "info", "-p", "dev", name, "-o", "json")
	return r.exit, envelope(t, r.stdout)
}

func TestDBInfo(t *testing.T) {
	cat := newDBFake()
	exit, env := dbInfo(t, cat, "app")
	if exit != 0 {
		t.Fatalf("exit %d: %v", exit, env)
	}
	data := env["data"].(map[string]any)
	for k, want := range map[string]string{
		"name": "app", "state": "ONLINE", "compatibilityLevel": "160", "sizeMb": "3.00", "logMb": "1.00",
		"dataUnallocatedMb": "1.50", "owner": "sa", "autoCreateStats": "true", "targetRecoveryTimeSeconds": "60",
		"snapshotOf": "", "created": "2026-03-14 09:12:44.000",
	} {
		if data[k] != want {
			t.Errorf("%s = %v, want %q", k, data[k], want)
		}
	}
	if env["database"] != "app" || len(env["warnings"].([]any)) != 0 {
		t.Errorf("database %v, warnings %v", env["database"], env["warnings"])
	}

	if _, env := dbInfo(t, cat, "app_snap"); env["data"].(map[string]any)["snapshotOf"] != "app" {
		t.Errorf("snapshotOf %v, want app", env["data"].(map[string]any)["snapshotOf"])
	}
}

// P3-7: a database that is not ONLINE answers what the server catalog holds;
// what needs the database itself is a warning, never a failed command.
func TestDBInfoOffline(t *testing.T) {
	cat := newDBFake()
	exit, env := dbInfo(t, cat, "archive")
	if exit != 0 {
		t.Fatalf("exit %d: %v", exit, env)
	}
	data := env["data"].(map[string]any)
	if data["state"] != "OFFLINE" || data["sizeMb"] != "16.00" || data["dataUnallocatedMb"] != "" {
		t.Errorf("data %v", data)
	}
	if cat.spaceUsed() != 0 {
		t.Errorf("space used read %d times on an OFFLINE database", cat.spaceUsed())
	}
	warnings := fmt.Sprint(env["warnings"])
	if !strings.Contains(warnings, "the database is OFFLINE") {
		t.Errorf("warnings %s", warnings)
	}

	cat.options = nil
	if exit, env := dbInfo(t, cat, "app"); exit != 0 || !strings.Contains(fmt.Sprint(env["warnings"]), "Options were not read") {
		t.Errorf("failed options read: exit %d, warnings %v", exit, env["warnings"])
	}
}

func TestDBInfoNotFound(t *testing.T) {
	exit, env := dbInfo(t, newDBFake(), "nope")
	e := firstError(t, env)
	if exit != 8 || e["code"] != "NOT_FOUND" || !strings.Contains(e["hint"].(string), "gossms db list") {
		t.Errorf("exit %d, error %v", exit, e)
	}
}
