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

// dropCatalog is newStorageCatalog's databases plus model, with what db
// create and db drop read around them: snapshots come from the databases'
// SourceDatabaseID, and app has a full backup in msdb.
type dropCatalog struct {
	*storageCatalog
	info    *gosmo.ServerInfo
	current string
	ags     map[string][]string // group → its databases
	backups []*gosmo.DatabaseLastBackups
}

func newDropCatalog() *dropCatalog {
	c := &dropCatalog{
		storageCatalog: newStorageCatalog(),
		info:           &gosmo.ServerInfo{VersionMajor: 16, Collation: "SQL_Latin1_General_CP1_CI_AS"},
		current:        "master",
		backups: []*gosmo.DatabaseLastBackups{
			{DatabaseName: "app", Full: &gosmo.BackupInfo{BackupFinish: time.Date(2026, 10, 29, 22, 0, 0, 0, time.UTC)}},
			{DatabaseName: "hr"},
		},
	}
	c.dbs = append(c.dbs, &gosmo.Database{Name: "model", ID: 3, State: gosmo.DatabaseOnline,
		RecoveryModel: gosmo.RecoveryModelFull, CompatibilityLevel: gosmo.CompatLevel2022})
	return c
}

func (c *dropCatalog) ServerInfo() *gosmo.ServerInfo                   { return c.info }
func (c *dropCatalog) CurrentDatabase(context.Context) (string, error) { return c.current, nil }

func (c *dropCatalog) DatabaseSizes(context.Context) (map[int]gosmo.DatabaseSize, error) {
	return map[int]gosmo.DatabaseSize{5: {DataKB: 8192, LogKB: 2048}, 8: {DataKB: 1024, LogKB: 1024}}, nil
}

func (c *dropCatalog) SnapshotsOf(_ context.Context, name string) ([]*gosmo.DatabaseSnapshot, error) {
	src, err := c.DatabaseByName(context.Background(), name)
	if err != nil {
		return nil, err
	}
	var out []*gosmo.DatabaseSnapshot
	for _, d := range c.dbs {
		if d.SourceDatabaseID == src.ID {
			out = append(out, &gosmo.DatabaseSnapshot{Name: d.Name, SourceDatabase: src.Name})
		}
	}
	return out, nil
}

func (c *dropCatalog) LastBackups(context.Context) ([]*gosmo.DatabaseLastBackups, error) {
	return c.backups, nil
}

func (c *dropCatalog) AvailabilityGroups(context.Context) ([]*gosmo.AvailabilityGroup, error) {
	var out []*gosmo.AvailabilityGroup
	for name := range c.ags {
		out = append(out, &gosmo.AvailabilityGroup{Name: name})
	}
	return out, nil
}

func (c *dropCatalog) AvailabilityGroupDatabases(_ context.Context, ag *gosmo.AvailabilityGroup) ([]*gosmo.AvailabilityDatabase, error) {
	var out []*gosmo.AvailabilityDatabase
	for _, d := range c.ags[ag.Name] {
		out = append(out, &gosmo.AvailabilityDatabase{DatabaseName: d})
	}
	return out, nil
}

// dropWriter creates and drops in its catalog as the server would; err
// fails the call, and createdThenErr creates the database and then fails (a
// SET after the CREATE refused); ignoreRecovery leaves model's recovery
// model. Under WithScript it is the real serverWriter over a never-connected
// server, so a dry run captures gosmo's statements.
type dropWriter struct {
	Writer
	cat            *dropCatalog
	calls          []string
	err            error
	createdThenErr bool
	ignoreRecovery bool
}

func (w *dropWriter) CreateDatabase(ctx context.Context, req gosmo.CreateDatabaseRequest) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{&gosmo.Server{}}.CreateDatabase(ctx, req)
	}
	w.calls = append(w.calls, "create "+req.Name)
	if w.err != nil && !w.createdThenErr {
		return w.err
	}
	d := &gosmo.Database{Name: req.Name, ID: 42, State: gosmo.DatabaseOnline, RecoveryModel: gosmo.RecoveryModelFull,
		CompatibilityLevel: gosmo.CompatLevel2022, Collation: w.cat.info.Collation}
	if req.RecoveryModel != "" && !w.ignoreRecovery {
		d.RecoveryModel = req.RecoveryModel
	}
	if req.CompatLevel != 0 {
		d.CompatibilityLevel = req.CompatLevel
	}
	if req.Collation != "" {
		d.Collation = req.Collation
	}
	w.cat.dbs = append(w.cat.dbs, d)
	return w.err
}

func (w *dropWriter) DropDatabase(ctx context.Context, d *gosmo.Database, term gosmo.Termination) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.DropDatabase(ctx, scriptDB(d), term)
	}
	w.calls = append(w.calls, fmt.Sprintf("drop %s %d", d.Name, term))
	if w.err != nil {
		return w.err
	}
	w.cat.dbs = slices.DeleteFunc(w.cat.dbs, func(x *gosmo.Database) bool { return x.Name == d.Name })
	return nil
}

// runDropCmd runs db <argv> (create or drop first) over cat and w, an
// ad-hoc connection; dials counts the connects.
func runDropCmd(t *testing.T, cat *dropCatalog, w *dropWriter, argv string) (res *output.Result, dials int, err error) {
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
	argv0 := append([]string{"db", fields[0], "--server", "srv", "--auth", "windows"}, fields[1:]...)
	inv, perr := args.Parse(&spec.Tree{Commands: DBCommands(d)}, argv0, args.Options{})
	if perr != nil {
		t.Fatalf("%q: %v", argv, perr)
	}
	res, err = inv.Command.Run(context.Background(), inv)
	return res, dials, err
}

func TestDBCreate(t *testing.T) {
	cat, w := newDropCatalog(), &dropWriter{}
	res, _, err := runDropCmd(t, cat, w, "create Scratch --recovery simple --compat 140 --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.calls, []string{"create Scratch"}) {
		t.Errorf("calls %q", w.calls)
	}
	got := []string{fieldValue(res, "name"), fieldValue(res, "state"), fieldValue(res, "recoveryModel"),
		fieldValue(res, "compatibilityLevel"), fieldValue(res, "collation"), fieldValue(res, "dryRun")}
	if want := []string{"Scratch", "ONLINE", "SIMPLE", "140", "SQL_Latin1_General_CP1_CI_AS", "false"}; !slices.Equal(got, want) {
		t.Errorf("record %q, want %q", got, want)
	}
	if res.Database != "Scratch" || len(res.Warnings) != 0 || hasField(res, "tsql") {
		t.Errorf("header %q, warnings %q", res.Database, res.Warnings)
	}
}

// TestDBCreateDryRun: the record is what the database would be — model's
// recovery and level, the server's collation — and nothing is created.
func TestDBCreateDryRun(t *testing.T) {
	cat, w := newDropCatalog(), &dropWriter{}
	res, _, err := runDropCmd(t, cat, w, "create Scratch --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 0 || slices.ContainsFunc(cat.dbs, func(d *gosmo.Database) bool { return d.Name == "Scratch" }) {
		t.Errorf("a dry run created: %q", w.calls)
	}
	got := []string{fieldValue(res, "recoveryModel"), fieldValue(res, "compatibilityLevel"), fieldValue(res, "collation")}
	if want := []string{"FULL", "160", "SQL_Latin1_General_CP1_CI_AS"}; !slices.Equal(got, want) || !res.Fields[1].Null {
		t.Errorf("record %q, want %q", got, want)
	}
	if tsql := fieldValue(res, "tsql"); tsql != "CREATE DATABASE [Scratch]\nGO\n" {
		t.Errorf("tsql %q", tsql)
	}

	res, _, err = runDropCmd(t, newDropCatalog(), &dropWriter{},
		"create Scratch --collation Latin1_General_100_CI_AS --recovery bulk-logged --compat 130 --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	tsql := fieldValue(res, "tsql")
	for _, s := range []string{"COLLATE Latin1_General_100_CI_AS", "SET RECOVERY BULK_LOGGED", "SET COMPATIBILITY_LEVEL = 130"} {
		if !strings.Contains(tsql, s) {
			t.Errorf("tsql lacks %q:\n%s", s, tsql)
		}
	}
	if got := fieldValue(res, "recoveryModel") + " " + fieldValue(res, "compatibilityLevel") + " " + fieldValue(res, "collation"); got != "BULK_LOGGED 130 Latin1_General_100_CI_AS" {
		t.Errorf("record %s", got)
	}
}

func TestDBCreateRefused(t *testing.T) {
	for argv, code := range map[string]output.Code{
		"create Scratch --compat 95 --allow admin":   output.CodeUsageError, // not a step of 10
		"create Scratch --compat 90 --allow admin":   output.CodeUsageError, // under 100
		"create Scratch --compat 170 --allow admin":  output.CodeUsageError, // above 16's 160
		"create app --allow admin":                   output.CodeUsageError, // taken
		"create APP --allow admin --dry-run":         output.CodeUsageError, // taken, any case
		"create app_snap --allow admin":              output.CodeUsageError, // a snapshot's name
		"create Scratch":                             output.CodeBlocked,    // --allow read
		"create Scratch --collation= --allow admin":  output.CodeUsageError,
		"create Scratch --recovery none --allow ddl": output.CodeUsageError, // parse
	} {
		t.Run(argv, func(t *testing.T) {
			w := &dropWriter{}
			cat := newDropCatalog()
			d := Deps{
				LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
				Connect: func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
					return &db.ServerConn{}, nil
				},
				Catalog: func(*db.ServerConn) Catalog { return cat },
				Writer:  func(*db.ServerConn) Writer { w.cat = cat; return w },
			}
			fields := strings.Fields(argv)
			inv, err := args.Parse(&spec.Tree{Commands: DBCommands(d)},
				append([]string{"db", fields[0], "--server", "srv", "--auth", "windows"}, fields[1:]...), args.Options{})
			if err != nil {
				// A value the parser refuses: a usage error before anything.
				if code != output.CodeUsageError {
					t.Errorf("parse: %v", err)
				}
				return
			}
			_, err = inv.Command.Run(context.Background(), inv)
			if c := errCode(err); c != code {
				t.Errorf("%v (%s), want %s", err, c, code)
			}
			if len(w.calls) != 0 {
				t.Errorf("calls %q", w.calls)
			}
		})
	}
}

// TestDBCreateFailures: the server's refusal for permission is exit 9; a SET
// after the CREATE failing leaves the database, which the record shows and a
// warning names; a recovery model the server did not take is a warning.
func TestDBCreateFailures(t *testing.T) {
	denied := fmt.Errorf("gosmo: create database: %w", mssql.Error{Number: 262, Class: 14,
		Message: "CREATE DATABASE permission denied in database 'master'."})
	_, _, err := runDropCmd(t, newDropCatalog(), &dropWriter{err: denied}, "create Scratch --allow admin")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodePermissionDenied || !strings.Contains(e.Hint, "CREATE ANY DATABASE") {
		t.Errorf("%v", err)
	}

	cat := newDropCatalog()
	setFailed := fmt.Errorf("gosmo: set compat level: %w", mssql.Error{Number: 15048, Class: 16,
		Message: "Valid values of the database compatibility level are 100, 110, 120, 130, 140, 150 or 160."})
	res, _, err := runDropCmd(t, cat, &dropWriter{err: setFailed, createdThenErr: true}, "create Scratch --compat 150 --allow admin")
	if errCode(err) != output.CodeSQLError {
		t.Errorf("%v", err)
	}
	if fieldValue(res, "name") != "Scratch" || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "was created and stays") {
		t.Errorf("record %v, warnings %q", res.Fields, res.Warnings)
	}

	res, _, err = runDropCmd(t, newDropCatalog(), &dropWriter{ignoreRecovery: true}, "create Scratch --recovery simple --allow admin")
	if err != nil || len(res.Warnings) != 1 || res.Warnings[0] != "The recovery model is FULL, not the SIMPLE requested." {
		t.Errorf("%v, warnings %q", err, res.Warnings)
	}
}

func TestDBDrop(t *testing.T) {
	cat, w := newDropCatalog(), &dropWriter{}
	res, _, err := runDropCmd(t, cat, w, "drop hr --confirm hr --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.calls, []string{fmt.Sprintf("drop hr %d", gosmo.TerminationNone)}) {
		t.Errorf("calls %q", w.calls)
	}
	got := []string{fieldValue(res, "name"), fieldValue(res, "state"), fieldValue(res, "sizeMb"),
		fieldValue(res, "closeConnections"), fieldValue(res, "dryRun"), fieldValue(res, "dropped")}
	if want := []string{"hr", "ONLINE", "2.00", "false", "false", "true"}; !slices.Equal(got, want) {
		t.Errorf("record %q, want %q", got, want)
	}
	// hr has a LastBackups row with no full backup.
	if f := res.Fields[slices.IndexFunc(res.Fields, func(f output.Field) bool { return f.Key == "lastFullBackup" })]; !f.Null {
		t.Errorf("lastFullBackup %+v", f)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "no full backup of [hr]") {
		t.Errorf("warnings %q", res.Warnings)
	}

	// app has a backup, but a snapshot: drop the snapshot, then app.
	cat, w = newDropCatalog(), &dropWriter{}
	if _, _, err := runDropCmd(t, cat, w, "drop app --confirm app --allow admin"); errCode(err) != output.CodeBlocked ||
		!strings.Contains(err.Error(), "[app_snap]") || len(w.calls) != 0 {
		t.Errorf("%v, calls %q", err, w.calls)
	}
	res, _, err = runDropCmd(t, cat, w, "drop app_snap --confirm app_snap --allow admin")
	if err != nil || len(res.Warnings) != 0 || fieldValue(res, "dropped") != "true" {
		t.Errorf("%v, warnings %q", err, res.Warnings)
	}
	res, _, err = runDropCmd(t, cat, w, "drop app --confirm app --close-connections --allow admin")
	if err != nil || len(res.Warnings) != 0 || fieldValue(res, "lastFullBackup") != "2026-10-29 22:00:00.000" ||
		fieldValue(res, "sizeMb") != "10.00" {
		t.Errorf("%v, record %v, warnings %q", err, res.Fields, res.Warnings)
	}
	if w.calls[len(w.calls)-1] != fmt.Sprintf("drop app %d", gosmo.TerminationRollbackImmediate) {
		t.Errorf("calls %q", w.calls)
	}
}

func TestDBDropDryRun(t *testing.T) {
	cat, w := newDropCatalog(), &dropWriter{}
	res, _, err := runDropCmd(t, cat, w, "drop hr --confirm hr --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 0 || hasField(res, "dropped") {
		t.Errorf("calls %q, record %v", w.calls, res.Fields)
	}
	if tsql := fieldValue(res, "tsql"); tsql != "USE [master];\nDROP DATABASE [hr]\nGO\n" {
		t.Errorf("tsql %q", tsql)
	}
	res, _, _ = runDropCmd(t, cat, w, "drop hr --confirm hr --close-connections --allow admin --dry-run")
	if tsql := fieldValue(res, "tsql"); !strings.Contains(tsql, "SET SINGLE_USER WITH ROLLBACK IMMEDIATE") ||
		!strings.Contains(tsql, "DROP DATABASE [hr]") {
		t.Errorf("tsql %q", tsql)
	}
}

// TestDBDropRefused: each refusal is made before anything is sent; the
// --confirm ones before connecting.
func TestDBDropRefused(t *testing.T) {
	for _, c := range []struct {
		argv  string
		setup func(*dropCatalog)
		code  output.Code
		dials int
		text  string
	}{
		{"drop hr --allow admin", nil, output.CodeBlocked, 0, "Add --confirm hr"},
		{"drop hr --confirm HR --allow admin", nil, output.CodeBlocked, 0, `--confirm "HR" is not`},
		{"drop hr --confirm hr", nil, output.CodeBlocked, 0, ""}, // --allow read
		{"drop nosuch --confirm nosuch --allow admin", nil, output.CodeNotFound, 1, ""},
		{"drop master --confirm master --allow admin", nil, output.CodeBlocked, 1, "system database"},
		{"drop model --confirm model --allow admin", nil, output.CodeBlocked, 1, "system database"},
		{"drop hr --confirm hr --allow admin", func(c *dropCatalog) { c.dbs[4].IsDistributor = true }, output.CodeBlocked, 1, "sp_dropdistributiondb"},
		{"drop hr --confirm hr --allow admin", func(c *dropCatalog) { c.current = "HR" }, output.CodeUsageError, 1, "-d master"},
		{"drop hr --confirm hr --allow admin --dry-run", func(c *dropCatalog) {
			c.info.IsHADREnabled = true
			c.ags = map[string][]string{"ag1": {"app"}, "ag2": {"HR"}}
		}, output.CodeBlocked, 1, "REMOVE DATABASE [hr]"},
	} {
		t.Run(c.argv, func(t *testing.T) {
			cat, w := newDropCatalog(), &dropWriter{}
			if c.setup != nil {
				c.setup(cat)
			}
			_, dials, err := runDropCmd(t, cat, w, c.argv)
			if errCode(err) != c.code || dials != c.dials || len(w.calls) != 0 {
				t.Errorf("%v (%s), dials %d, calls %q; want %s, %d dials", err, errCode(err), dials, w.calls, c.code, c.dials)
			}
			if e, ok := err.(*output.Error); ok && !strings.Contains(e.Message+" "+e.Hint, c.text) {
				t.Errorf("%q / %q lacks %q", e.Message, e.Hint, c.text)
			}
		})
	}
	// An AG on a server without HADR is not read.
	cat := newDropCatalog()
	cat.ags = map[string][]string{"ag2": {"hr"}}
	if _, _, err := runDropCmd(t, cat, &dropWriter{}, "drop hr --confirm hr --allow admin"); err != nil {
		t.Errorf("%v", err)
	}
}

// TestDBDropServerRefusals: the server's refusals db drop explains.
func TestDBDropServerRefusals(t *testing.T) {
	for _, c := range []struct {
		err  mssql.Error
		code output.Code
		hint string
	}{
		{mssql.Error{Number: 3702, Class: 16, Message: `Cannot drop database "hr" because it is currently in use.`},
			output.CodeBlocked, "--confirm hr --close-connections"},
		{mssql.Error{Number: 3709, Class: 16, Message: "Cannot drop the database while the database snapshot \"s\" refers to it."},
			output.CodeBlocked, "drop the snapshots first"},
		{mssql.Error{Number: 3752, Class: 16, Message: "The database 'hr' is currently joined to an availability group."},
			output.CodeBlocked, "REMOVE DATABASE"},
		{mssql.Error{Number: 3701, Class: 14, Message: "Cannot drop the database 'hr', because it does not exist or you do not have permission."},
			output.CodePermissionDenied, "ALTER ANY DATABASE"},
		{mssql.Error{Number: 5120, Class: 16, Message: "Unable to open the physical file."},
			output.CodeSQLError, ""},
	} {
		w := &dropWriter{err: fmt.Errorf("gosmo: drop database %q: %w", "hr", c.err)}
		res, _, err := runDropCmd(t, newDropCatalog(), w, "drop hr --confirm hr --allow admin")
		e, ok := err.(*output.Error)
		if !ok || e.Code != c.code || !strings.Contains(e.Hint, c.hint) {
			t.Errorf("Msg %d: %v", c.err.Number, err)
		}
		if fieldValue(res, "name") != "hr" || hasField(res, "dropped") {
			t.Errorf("Msg %d: record %v", c.err.Number, res.Fields)
		}
	}
}

// TestDBDropOffline: the drop leaves an OFFLINE database's files, which a
// warning says.
func TestDBDropOffline(t *testing.T) {
	res, _, err := runDropCmd(t, newDropCatalog(), &dropWriter{}, "drop archive --confirm archive --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, "OFFLINE: SQL Server deletes") }) {
		t.Errorf("warnings %q", res.Warnings)
	}
}
