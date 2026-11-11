package cmd

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/clitest"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// dbServer is real gosmo over canned rows for the db create, drop, set and
// scoped-config commands: a catalogServer with the server's databases (and
// whether the login can open each), their options, Query Store state and
// scoped configurations, sizes, snapshots, availability groups and msdb's
// full backups. The writes gosmo sends — CREATE and DROP DATABASE, ALTER
// DATABASE SET and ALTER DATABASE SCOPED CONFIGURATION — change this state
// as the server would (applyWrite), so the read after one sees it.
//
// Every rule answers what the state holds when the query arrives. The server
// metadata (Info) is read on each connect, so a test sets it before the
// first command.
type dbServer struct {
	*catalogServer
	// dbs is sys.databases; the rules answer them in name order. access is
	// HAS_DBACCESS by database id.
	dbs    []*gosmo.Database
	access map[int]bool
	sizes  map[int]gosmo.DatabaseSize
	ags    map[string][]string // availability group → its databases
	// fullBackups is msdb's newest full backup by database name.
	fullBackups map[string]time.Time

	// opts, qs and cfgs are the databases' options, Query Store state and
	// scoped configurations, by name; a database with no entry reads
	// defaults (cfgs: none). optionReads and scopedReads count the reads;
	// optionsErr and scopedErr fail every one after the first.
	opts        map[string]*gosmo.DatabaseOptions
	qs          map[string]*gosmo.QueryStoreInfo
	cfgs        map[string][]*gosmo.DatabaseScopedConfig
	optionReads int
	optionsErr  error
	scopedReads int
	scopedErr   error

	// writes is each write statement sent, as sent. failAt (1-based) fails
	// that write with writeErr; ignore accepts the ALTERs and applies none
	// of them; block holds every write past a 1 s --timeout.
	writes   []string
	failAt   int
	writeErr error
	ignore   bool
	block    bool
}

// The write statements applyWrite knows.
var (
	createDatabase = regexp.MustCompile(`^CREATE DATABASE (\[(?:[^\]]|\]\])*\])(?: COLLATE (\S+))?`)
	dropDatabase   = regexp.MustCompile(`DROP DATABASE (\[(?:[^\]]|\]\])*\])`)
	alterDatabase  = regexp.MustCompile(`^ALTER DATABASE (\[(?:[^\]]|\]\])*\]) SET (\w+)(?: = |\s)?(.*?)(?: WITH ROLLBACK IMMEDIATE)?$`)
	alterScoped    = regexp.MustCompile(`^ALTER DATABASE SCOPED CONFIGURATION (FOR SECONDARY )?SET (\w+) = (.*)$`)
)

// dbServerDatabases is the server's databases: app FULL with a snapshot,
// archive OFFLINE, hr SIMPLE, model FULL at 2022's level.
func dbServerDatabases() []*gosmo.Database {
	full, simple := gosmo.RecoveryModelFull, gosmo.RecoveryModelSimple
	online := gosmo.DatabaseOnline
	return []*gosmo.Database{
		{Name: "master", ID: 1, State: online, RecoveryModel: simple},
		{Name: "app", ID: 5, State: online, RecoveryModel: full, CompatibilityLevel: gosmo.CompatLevel2022},
		{Name: "archive", ID: 6, State: gosmo.DatabaseOffline, RecoveryModel: full},
		{Name: "app_snap", ID: 7, State: online, RecoveryModel: full, SourceDatabaseID: 5},
		{Name: "hr", ID: 8, State: online, RecoveryModel: simple},
		{Name: "model", ID: 3, State: online, RecoveryModel: full, CompatibilityLevel: gosmo.CompatLevel2022},
	}
}

func newDBServer(t *testing.T) *dbServer {
	s := &dbServer{
		dbs: dbServerDatabases(),
		// HAS_DBACCESS: false on the OFFLINE database for every login; hr
		// has no user for the login.
		access: map[int]bool{1: true, 3: true, 5: true, 6: false, 7: true, 8: false},
		sizes:  map[int]gosmo.DatabaseSize{5: {DataKB: 8192, LogKB: 2048}, 8: {DataKB: 1024, LogKB: 1024}},
		fullBackups: map[string]time.Time{
			"app": time.Date(2026, 10, 29, 22, 0, 0, 0, time.UTC),
		},
		opts: map[string]*gosmo.DatabaseOptions{}, qs: map[string]*gosmo.QueryStoreInfo{},
		cfgs: map[string][]*gosmo.DatabaseScopedConfig{},
	}
	newCatalogServerWith(t, "master", func(c *catalogServer) { s.catalogServer = c; s.rules() })
	return s
}

// setMajor makes the server report version major (SQL Server 2016 is 13).
func (s *dbServer) setMajor(major int) {
	s.Info.ProductVersion = fmt.Sprintf("%d.0.1000.7", major)
}

// db is s's database name, any case; nil when there is none.
func (s *dbServer) db(name string) *gosmo.Database {
	for _, d := range s.dbs {
		if strings.EqualFold(d.Name, name) {
			return d
		}
	}
	return nil
}

func (s *dbServer) byName() []*gosmo.Database {
	dbs := slices.Clone(s.dbs)
	slices.SortFunc(dbs, func(a, b *gosmo.Database) int { return strings.Compare(a.Name, b.Name) })
	return dbs
}

// optionsOf is name's options as the server holds them.
func (s *dbServer) optionsOf(name string) *gosmo.DatabaseOptions {
	if o, ok := s.opts[name]; ok {
		return o
	}
	o := &gosmo.DatabaseOptions{PageVerify: "CHECKSUM", DefaultCursor: "GLOBAL", Containment: "NONE",
		SnapshotIsolation: "OFF", UserAccess: gosmo.UserAccessMulti, AutoCreateStats: true, AutoUpdateStats: true}
	s.opts[name] = o
	return o
}

// qsOf is name's Query Store as the server holds it.
func (s *dbServer) qsOf(name string) *gosmo.QueryStoreInfo {
	if q, ok := s.qs[name]; ok {
		return q
	}
	q := &gosmo.QueryStoreInfo{DesiredState: gosmo.QueryStoreOff, ActualState: gosmo.QueryStoreOff, MaxStorageMB: 123,
		CaptureMode: gosmo.QueryStoreCaptureAll, SizeCleanupMode: gosmo.QueryStoreCleanupOff, StaleThresholdDays: 11,
		FlushIntervalSec: 300, IntervalMinutes: 15, MaxPlansPerQuery: 77, WaitStatsCaptureMode: gosmo.QueryStoreWaitStatsOff}
	s.qs[name] = q
	return q
}

// scopedMajor is the major version the connect read reported.
func (s *dbServer) scopedMajor() int {
	n, _, _ := strings.Cut(s.Info.ProductVersion, ".")
	major, _ := strconv.Atoi(n)
	return major
}

// scopedConfigs is app's scoped configurations, in the view's own (not
// name) order to show the command sorts.
func scopedConfigs17() []*gosmo.DatabaseScopedConfig {
	return []*gosmo.DatabaseScopedConfig{
		{ID: 1, Name: "MAXDOP", Value: "0", ValueForSecondary: "2", ValueType: "int", ValueForSecondaryType: "int"},
		{ID: 2, Name: "ELEVATE_ONLINE", Value: "OFF", IsValueDefault: true, ValueType: "nvarchar"},
		{ID: 3, Name: "LEGACY_CARDINALITY_ESTIMATION", Value: "0", IsValueDefault: true, ValueType: "bit"},
		{ID: 4, Name: "PARAMETER_SNIFFING", Value: "1", IsValueDefault: true, ValueType: "bit"},
		{ID: 5, Name: "IDENTITY_CACHE", Value: "1", IsValueDefault: true, ValueType: "bit"},
	}
}

func (s *dbServer) rules() {
	s.OnFunc(func(q gosmotest.Query) bool { return q.Exec }, s.applyWrite)
	// The access mode a forced drop saves.
	s.on(func(q gosmotest.Query) [][]any {
		return [][]any{{string(s.optionsOf(argString(q, 0)).UserAccess)}}
	}, "SELECT user_access_desc FROM sys.databases WHERE name = @p1")

	// The databases, by name and listed, and what the login can open.
	s.on(func(q gosmotest.Query) [][]any {
		if d := s.db(argString(q, 0)); d != nil {
			return [][]any{clitest.DatabaseOf(d)}
		}
		return nil
	}, "FROM sys.databases", "WHERE name = @p1")
	s.on(func(gosmotest.Query) [][]any {
		var rows [][]any
		for _, d := range s.byName() {
			rows = append(rows, clitest.DatabaseOf(d))
		}
		return rows
	}, "FROM sys.databases", "ORDER BY name")
	s.on(func(gosmotest.Query) [][]any {
		var rows [][]any
		for _, d := range s.dbs {
			if ok, known := s.access[d.ID]; known {
				rows = append(rows, []any{d.ID, ok})
			}
		}
		return rows
	}, "CAST(HAS_DBACCESS(name) AS bit)")
	s.on(func(gosmotest.Query) [][]any {
		var rows [][]any
		for _, id := range slices.Sorted(maps.Keys(s.sizes)) {
			rows = append(rows, []any{id, s.sizes[id].DataKB, s.sizes[id].LogKB})
		}
		return rows
	}, "FROM   sys.master_files mf", "GROUP  BY mf.database_id")

	// Snapshots of a database, by source.
	s.on(func(q gosmotest.Query) [][]any {
		src := s.db(argString(q, 0))
		var rows [][]any
		for _, d := range s.byName() {
			if src != nil && d.SourceDatabaseID == src.ID {
				rows = append(rows, clitest.SnapshotRow(&gosmo.DatabaseSnapshot{Name: d.Name, DatabaseID: d.ID,
					SourceDatabase: src.Name, SourceDatabaseID: src.ID}, catalogAt))
			}
		}
		return rows
	}, "d.source_database_id = DB_ID(@p1)")

	// Availability groups and their databases.
	s.on(func(gosmotest.Query) [][]any {
		var rows [][]any
		for _, name := range slices.Sorted(maps.Keys(s.ags)) {
			rows = append(rows, clitest.AvailabilityGroupRow("ag-"+name, name))
		}
		return rows
	}, "FROM sys.availability_groups ag", "ORDER BY ag.name")
	s.on(func(q gosmotest.Query) [][]any {
		id := argString(q, 0)
		var rows [][]any
		for _, d := range s.ags[strings.TrimPrefix(id, "ag-")] {
			rows = append(rows, clitest.AvailabilityDatabaseRow(id, d))
		}
		return rows
	}, "FROM sys.availability_databases_cluster adc")

	// msdb's newest full backups: LastBackups' two reads.
	s.on(func(gosmotest.Query) [][]any {
		var rows [][]any
		for _, d := range s.byName() {
			if d.ID != 2 && !d.IsSnapshot() {
				rows = append(rows, []any{d.Name, clitest.Nullable(string(d.RecoveryModel))})
			}
		}
		return rows
	}, "SELECT name, recovery_model_desc", "source_database_id IS NULL")
	s.on(func(gosmotest.Query) [][]any {
		var rows [][]any
		for i, name := range slices.Sorted(maps.Keys(s.fullBackups)) {
			b := &gosmo.BackupInfo{DatabaseName: name, SetType: gosmo.BackupSetDatabase, BackupFinish: s.fullBackups[name],
				Devices: []string{"/backup/" + name + ".bak"}}
			rows = append(rows, clitest.BackupInfoRows(b, int64(i+1))...)
		}
		return rows
	}, "FROM   msdb.dbo.backupset bs", "ROW_NUMBER() OVER (PARTITION BY database_name, type")

	// A database's options, Query Store and scoped configurations.
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "FROM   sys.databases d", "WHERE  d.name = @p1") },
		func(q gosmotest.Query) gosmotest.Result {
			s.optionReads++
			if s.optionsErr != nil && s.optionReads > 1 {
				return gosmotest.Result{Err: s.optionsErr}
			}
			var rows [][]any
			if d := s.db(argString(q, 0)); d != nil {
				rows = append(rows, clitest.DatabaseOptionsRow(s.optionsOf(d.Name)))
			}
			return gosmotest.Result{Sets: []gosmotest.Set{{Rows: rows}}}
		})
	s.on(func(q gosmotest.Query) [][]any {
		d := s.db(q.Database)
		if d == nil || d.ID <= 4 && d.ID != 3 {
			return nil // master and tempdb have no Query Store
		}
		return [][]any{clitest.QueryStoreOptionsRow(s.qsOf(d.Name))}
	}, "FROM   sys.database_query_store_options")
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "FROM   sys.database_scoped_configurations") },
		func(q gosmotest.Query) gosmotest.Result {
			s.scopedReads++
			if s.scopedErr != nil && s.scopedReads > 1 {
				return gosmotest.Result{Err: s.scopedErr}
			}
			var rows [][]any
			for _, c := range s.cfgs[q.Database] {
				row := *c // two reads are two copies, as from the server
				if m := s.scopedMajor(); m < 14 && s.Info.EngineEdition != int(gosmo.EngineAzureManagedInst) {
					row.IsValueDefault = false // 2016 has no is_value_default
				}
				rows = append(rows, clitest.ScopedConfigRow(&row))
			}
			return gosmotest.Result{Sets: []gosmotest.Set{{Rows: rows}}}
		})
}

// applyWrite is the server taking q, a write: recorded, failed as failAt
// says, else applied to s. Under block it holds the call until --timeout has
// passed and fails it as a cancelled one.
func (s *dbServer) applyWrite(q gosmotest.Query) gosmotest.Result {
	s.writes = append(s.writes, q.Text)
	if s.block {
		time.Sleep(1200 * time.Millisecond)
		return gosmotest.Result{Err: fmt.Errorf("%w", context.DeadlineExceeded)}
	}
	if s.failAt == len(s.writes) {
		return gosmotest.Result{Err: s.writeErr}
	}
	switch text := q.Text; {
	case createDatabase.MatchString(text):
		m := createDatabase.FindStringSubmatch(text)
		d := &gosmo.Database{Name: bracketNames(m[1])[0], ID: 42, State: gosmo.DatabaseOnline,
			RecoveryModel: gosmo.RecoveryModelFull, CompatibilityLevel: gosmo.CompatLevel2022,
			Collation: cmp.Or(m[2], s.Info.Collation)}
		s.dbs = append(s.dbs, d)
	case alterScoped.MatchString(text):
		m := alterScoped.FindStringSubmatch(text)
		if s.ignore {
			break
		}
		for _, c := range s.cfgs[q.Database] {
			if c.Name != m[2] {
				continue
			}
			raw := m[3]
			switch raw {
			case "ON":
				raw = "1"
			case "OFF":
				if c.ValueType == "bit" {
					raw = "0"
				}
			}
			switch {
			case m[1] == "":
				c.Value = raw
			case m[3] == scopedPrimary:
				c.ValueForSecondary, c.ValueForSecondaryType = "", ""
			default:
				c.ValueForSecondary, c.ValueForSecondaryType = raw, c.ValueType
			}
		}
	case alterDatabase.MatchString(text):
		if s.ignore {
			break
		}
		m := alterDatabase.FindStringSubmatch(text)
		s.applyAlter(bracketNames(m[1])[0], m[2], m[3])
	case dropDatabase.MatchString(text):
		name := bracketNames(dropDatabase.FindStringSubmatch(text)[1])[0]
		s.dbs = slices.DeleteFunc(s.dbs, func(d *gosmo.Database) bool { return d.Name == name })
	default:
		s.t.Errorf("dbServer: a write it does not know: %s", q)
	}
	return gosmotest.Result{}
}

var storeOption = regexp.MustCompile(`OPERATION_MODE = (\w+)`)

// applyAlter is ALTER DATABASE name SET key value taking effect.
func (s *dbServer) applyAlter(name, key, value string) {
	d := s.db(name)
	if d == nil {
		return
	}
	o := s.optionsOf(d.Name)
	switch key {
	case "RECOVERY":
		d.RecoveryModel = gosmo.RecoveryModel(value)
	case "COMPATIBILITY_LEVEL":
		n, _ := strconv.Atoi(value)
		d.CompatibilityLevel = gosmo.CompatibilityLevel(n)
	case "READ_ONLY", "READ_WRITE":
		d.IsReadOnly = key == "READ_ONLY"
	case "QUERY_STORE":
		qs := s.qsOf(d.Name)
		qs.DesiredState = gosmo.QueryStoreOff
		if m := storeOption.FindStringSubmatch(value); m != nil {
			qs.DesiredState = gosmo.QueryStoreState(m[1])
		}
		qs.ActualState = qs.DesiredState
	case "PAGE_VERIFY":
		o.PageVerify = value
	case "AUTO_UPDATE_STATISTICS_ASYNC":
		o.AutoUpdateStatsAsync = value == "ON"
	case "READ_COMMITTED_SNAPSHOT":
		o.ReadCommittedSnapshot = value == "ON"
	case "ALLOW_SNAPSHOT_ISOLATION":
		o.SnapshotIsolation = value
	case "CONTAINMENT":
		o.Containment = value
	}
}

// run runs db <argv> (the subcommand first) against s as the first command
// on an ad-hoc connection; dials counts the connects.
func (s *dbServer) run(t *testing.T, argv string) (res *output.Result, dials int, err error) {
	t.Helper()
	res, dials, parse, err := s.runRaw(argv)
	if parse != nil {
		t.Fatalf("%q: %v", argv, parse)
	}
	return res, dials, err
}

// runRaw is run with a parse failure returned apart from the command's.
func (s *dbServer) runRaw(argv string) (res *output.Result, dials int, parse, err error) {
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(ctx context.Context, _ config.Connection, _ db.Role) (*db.ServerConn, error) {
			dials++
			return s.conn(ctx)
		},
	}
	fields := strings.Fields(argv)
	n := 1
	if fields[0] == "scoped-config" {
		n = 2
	}
	argv0 := append(slices.Clone(fields[:n]), "--server", "srv", "--auth", "windows")
	argv0 = append(append([]string{"db"}, argv0...), fields[n:]...)
	inv, perr := args.Parse(&spec.Tree{Commands: DBCommands(d)}, argv0, args.Options{})
	if perr != nil {
		return nil, 0, perr, nil
	}
	res, err = inv.Command.Run(context.Background(), inv)
	return res, dials, nil, err
}
