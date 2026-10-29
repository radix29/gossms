package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/cmd"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// fakeCatalog answers the catalog reads from its fields. A nil sizes map
// with sizesErr set is a failed size read; spaceUsed counts the reads that
// needed the database ONLINE.
type fakeCatalog struct {
	current   string
	dbs       []*gosmo.Database
	sizes     map[int]gosmo.DatabaseSize
	sizesErr  error
	options   *gosmo.DatabaseOptions
	spaceUsed int
}

func (c *fakeCatalog) CurrentDatabase(context.Context) (string, error) { return c.current, nil }

func (c *fakeCatalog) Databases(context.Context) ([]*gosmo.Database, error) { return c.dbs, nil }

func (c *fakeCatalog) DatabaseByName(_ context.Context, name string) (*gosmo.Database, error) {
	for _, d := range c.dbs {
		if strings.EqualFold(d.Name, name) {
			return d, nil
		}
	}
	return nil, fmt.Errorf("gosmo: database %q not found: %w", name, gosmo.ErrNotFound)
}

func (c *fakeCatalog) DatabaseSizes(context.Context) (map[int]gosmo.DatabaseSize, error) {
	return c.sizes, c.sizesErr
}

func (c *fakeCatalog) DatabaseOptions(context.Context, *gosmo.Database) (*gosmo.DatabaseOptions, error) {
	if c.options == nil {
		return nil, errors.New("gosmo: options: boom")
	}
	return c.options, nil
}

func (c *fakeCatalog) SpaceUsed(context.Context, *gosmo.Database) (gosmo.SpaceInfo, error) {
	c.spaceUsed++
	return gosmo.SpaceInfo{UnallocatedMB: 1.5, AvailLogMB: 0.25}, nil
}

func (c *fakeCatalog) ObjectByName(_ context.Context, d *gosmo.Database, schema, name string) (*gosmo.ObjectInfo, error) {
	return nil, gosmo.ErrNotFound
}

func (c *fakeCatalog) ObjectsNamed(context.Context, *gosmo.Database, string) ([]*gosmo.ObjectInfo, error) {
	return nil, nil
}

func (c *fakeCatalog) Search(context.Context, *gosmo.Database, string) ([]*gosmo.SearchResult, error) {
	return nil, nil
}

func newFakeCatalog() *fakeCatalog {
	created := time.Date(2026, 3, 14, 9, 12, 44, 0, time.UTC)
	return &fakeCatalog{
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

// catalogDeps is profileDeps (dev) with a connection that always succeeds and
// cat as its catalog.
func catalogDeps(t *testing.T, cat cmd.Catalog) cmd.Deps {
	d := profileDeps(t, func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
		return &db.ServerConn{}, nil
	})
	d.Catalog = func(*db.ServerConn) cmd.Catalog { return cat }
	return d
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
	d := catalogDeps(t, newFakeCatalog())

	r := runTree(t, context.Background(), newCommandTree(d), "db", "list", "-p", "dev", "-o", "json")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	env := envelope(t, r.stdout)
	// master and the distribution database are system; the snapshot is not.
	if got := strings.Join(dbListNames(t, env), ","); got != "app,archive,app_snap" {
		t.Errorf("names %s", got)
	}
	set := env["resultSets"].([]any)[0].(map[string]any)
	app := set["rows"].([]any)[0].([]any)
	// database_id is a JSON number, sizes are strings (decimal), read_only a bool.
	if app[1] != 5.0 || app[7] != "3.00" || app[8] != "2.00" || app[9] != "1.00" || app[6] != false {
		t.Errorf("app row %v", app)
	}
	snap := set["rows"].([]any)[2].([]any)
	if snap[12] != "app" || snap[6] != true {
		t.Errorf("snapshot row %v, want snapshot_of app and read_only", snap)
	}
	// An OFFLINE database has no collation in sys.databases: NULL, not "".
	if archive := set["rows"].([]any)[1].([]any); archive[5] != nil {
		t.Errorf("archive collation %v, want NULL", archive[5])
	}

	r = runTree(t, context.Background(), newCommandTree(d), "db", "list", "-p", "dev", "--include-system", "-o", "json")
	if got := strings.Join(dbListNames(t, envelope(t, r.stdout)), ","); got != "app,archive,app_snap,distribution,master" {
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
	cat := newFakeCatalog()
	cat.sizes, cat.sizesErr = nil, errors.New("gosmo: list database sizes: boom")
	r := runTree(t, context.Background(), newCommandTree(catalogDeps(t, cat)), "db", "list", "-p", "dev", "-o", "json")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	env := envelope(t, r.stdout)
	row := env["resultSets"].([]any)[0].(map[string]any)["rows"].([]any)[0].([]any)
	if row[7] != nil || len(env["warnings"].([]any)) != 1 {
		t.Errorf("size %v, warnings %v; want NULL and one warning", row[7], env["warnings"])
	}
}

func dbInfo(t *testing.T, cat *fakeCatalog, name string) (int, map[string]any) {
	t.Helper()
	r := runTree(t, context.Background(), newCommandTree(catalogDeps(t, cat)), "db", "info", "-p", "dev", name, "-o", "json")
	return r.exit, envelope(t, r.stdout)
}

func TestDBInfo(t *testing.T) {
	cat := newFakeCatalog()
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
	cat := newFakeCatalog()
	exit, env := dbInfo(t, cat, "archive")
	if exit != 0 {
		t.Fatalf("exit %d: %v", exit, env)
	}
	data := env["data"].(map[string]any)
	if data["state"] != "OFFLINE" || data["sizeMb"] != "16.00" || data["dataUnallocatedMb"] != "" {
		t.Errorf("data %v", data)
	}
	if cat.spaceUsed != 0 {
		t.Errorf("space used read %d times on an OFFLINE database", cat.spaceUsed)
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
	exit, env := dbInfo(t, newFakeCatalog(), "nope")
	e := firstError(t, env)
	if exit != 8 || e["code"] != "NOT_FOUND" || !strings.Contains(e["hint"].(string), "gossms db list") {
		t.Errorf("exit %d, error %v", exit, e)
	}
}
