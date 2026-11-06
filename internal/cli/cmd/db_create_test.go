package cmd

import (
	"slices"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/radix29/gossms/internal/cli/output"
)

func TestDBCreate(t *testing.T) {
	s := newDBServer(t)
	res, _, err := s.run(t, "create Scratch --recovery simple --compat 140 --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	// The CREATE, then the two SETs for the options given.
	if len(s.writes) != 3 || !strings.HasPrefix(s.writes[0], "CREATE DATABASE [Scratch]") ||
		s.writes[1] != "ALTER DATABASE [Scratch] SET RECOVERY SIMPLE" ||
		s.writes[2] != "ALTER DATABASE [Scratch] SET COMPATIBILITY_LEVEL = 140" {
		t.Errorf("writes %q", s.writes)
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
	s := newDBServer(t)
	res, _, err := s.run(t, "create Scratch --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.writes) != 0 || s.db("Scratch") != nil {
		t.Errorf("a dry run created: %q", s.writes)
	}
	got := []string{fieldValue(res, "recoveryModel"), fieldValue(res, "compatibilityLevel"), fieldValue(res, "collation")}
	if want := []string{"FULL", "160", "SQL_Latin1_General_CP1_CI_AS"}; !slices.Equal(got, want) || !res.Fields[1].Null {
		t.Errorf("record %q, want %q", got, want)
	}
	if tsql := fieldValue(res, "tsql"); tsql != "CREATE DATABASE [Scratch]\nGO\n" {
		t.Errorf("tsql %q", tsql)
	}

	res, _, err = newDBServer(t).run(t,
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
			s := newDBServer(t)
			_, _, parse, err := s.runRaw(argv)
			if parse != nil {
				// A value the parser refuses: a usage error before anything.
				if code != output.CodeUsageError {
					t.Errorf("parse: %v", parse)
				}
				return
			}
			if c := errCode(err); c != code {
				t.Errorf("%v (%s), want %s", err, c, code)
			}
			if len(s.writes) != 0 {
				t.Errorf("writes %q", s.writes)
			}
		})
	}
}

// TestDBCreateFailures: the server's refusal for permission is exit 9; a SET
// after the CREATE failing leaves the database, which the record shows and a
// warning names; a recovery model the server did not take is a warning.
func TestDBCreateFailures(t *testing.T) {
	s := newDBServer(t)
	s.failAt, s.writeErr = 1, sqlErr(262, 14, "CREATE DATABASE permission denied in database 'master'.")
	_, _, err := s.run(t, "create Scratch --allow admin")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodePermissionDenied || !strings.Contains(e.Hint, "CREATE ANY DATABASE") {
		t.Errorf("%v", err)
	}

	s = newDBServer(t)
	s.failAt, s.writeErr = 2, mssql.Error{Number: 15048, Class: 16,
		Message: "Valid values of the database compatibility level are 100, 110, 120, 130, 140, 150 or 160."}
	res, _, err := s.run(t, "create Scratch --compat 150 --allow admin")
	if errCode(err) != output.CodeSQLError {
		t.Errorf("%v", err)
	}
	if fieldValue(res, "name") != "Scratch" || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "was created and stays") {
		t.Errorf("record %v, warnings %q", res.Fields, res.Warnings)
	}

	s = newDBServer(t)
	s.ignore = true
	res, _, err = s.run(t, "create Scratch --recovery simple --allow admin")
	if err != nil || len(res.Warnings) != 1 || res.Warnings[0] != "The recovery model is FULL, not the SIMPLE requested." {
		t.Errorf("%v, warnings %q", err, res.Warnings)
	}
}

func TestDBDrop(t *testing.T) {
	s := newDBServer(t)
	res, _, err := s.run(t, "drop hr --confirm hr --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.writes) != 1 || !strings.Contains(s.writes[0], "DROP DATABASE [hr]") || strings.Contains(s.writes[0], "ROLLBACK") {
		t.Errorf("writes %q", s.writes)
	}
	got := []string{fieldValue(res, "name"), fieldValue(res, "state"), fieldValue(res, "sizeMb"),
		fieldValue(res, "closeConnections"), fieldValue(res, "dryRun"), fieldValue(res, "dropped")}
	if want := []string{"hr", "ONLINE", "2.00", "false", "false", "true"}; !slices.Equal(got, want) {
		t.Errorf("record %q, want %q", got, want)
	}
	// msdb holds no full backup of hr.
	if f := res.Fields[slices.IndexFunc(res.Fields, func(f output.Field) bool { return f.Key == "lastFullBackup" })]; !f.Null {
		t.Errorf("lastFullBackup %+v", f)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "no full backup of [hr]") {
		t.Errorf("warnings %q", res.Warnings)
	}

	// app has a backup, but a snapshot: drop the snapshot, then app.
	s = newDBServer(t)
	if _, _, err := s.run(t, "drop app --confirm app --allow admin"); errCode(err) != output.CodeBlocked ||
		!strings.Contains(err.Error(), "[app_snap]") || len(s.writes) != 0 {
		t.Errorf("%v, writes %q", err, s.writes)
	}
	res, _, err = s.run(t, "drop app_snap --confirm app_snap --allow admin")
	if err != nil || len(res.Warnings) != 0 || fieldValue(res, "dropped") != "true" {
		t.Errorf("%v, warnings %q", err, res.Warnings)
	}
	res, _, err = s.run(t, "drop app --confirm app --close-connections --allow admin")
	if err != nil || len(res.Warnings) != 0 || fieldValue(res, "lastFullBackup") != "2026-10-29 22:00:00.000" ||
		fieldValue(res, "sizeMb") != "10.00" {
		t.Errorf("%v, record %v, warnings %q", err, res.Fields, res.Warnings)
	}
	if last := s.writes[len(s.writes)-1]; !strings.Contains(last, "SET SINGLE_USER WITH ROLLBACK IMMEDIATE") ||
		!strings.Contains(last, "DROP DATABASE [app]") {
		t.Errorf("writes %q", s.writes)
	}
}

func TestDBDropDryRun(t *testing.T) {
	s := newDBServer(t)
	res, _, err := s.run(t, "drop hr --confirm hr --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.writes) != 0 || hasField(res, "dropped") {
		t.Errorf("writes %q, record %v", s.writes, res.Fields)
	}
	if tsql := fieldValue(res, "tsql"); tsql != "USE [master];\nDROP DATABASE [hr]\nGO\n" {
		t.Errorf("tsql %q", tsql)
	}
	res, _, _ = s.run(t, "drop hr --confirm hr --close-connections --allow admin --dry-run")
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
		setup func(*dbServer)
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
		{"drop hr --confirm hr --allow admin", func(s *dbServer) { s.db("hr").IsDistributor = true }, output.CodeBlocked, 1, "sp_dropdistributiondb"},
		{"drop hr --confirm hr --allow admin", func(s *dbServer) { s.current = "HR" }, output.CodeUsageError, 1, "-d master"},
		{"drop hr --confirm hr --allow admin --dry-run", func(s *dbServer) {
			s.Info.HADREnabled = true
			s.ags = map[string][]string{"ag1": {"app"}, "ag2": {"HR"}}
		}, output.CodeBlocked, 1, "REMOVE DATABASE [hr]"},
	} {
		t.Run(c.argv, func(t *testing.T) {
			s := newDBServer(t)
			if c.setup != nil {
				c.setup(s)
			}
			_, dials, err := s.run(t, c.argv)
			if errCode(err) != c.code || dials != c.dials || len(s.writes) != 0 {
				t.Errorf("%v (%s), dials %d, writes %q; want %s, %d dials", err, errCode(err), dials, s.writes, c.code, c.dials)
			}
			if e, ok := err.(*output.Error); ok && !strings.Contains(e.Message+" "+e.Hint, c.text) {
				t.Errorf("%q / %q lacks %q", e.Message, e.Hint, c.text)
			}
		})
	}
	// An AG on a server without HADR is not read.
	s := newDBServer(t)
	s.ags = map[string][]string{"ag2": {"hr"}}
	if _, _, err := s.run(t, "drop hr --confirm hr --allow admin"); err != nil {
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
		s := newDBServer(t)
		s.failAt, s.writeErr = 1, c.err
		res, _, err := s.run(t, "drop hr --confirm hr --allow admin")
		e, ok := err.(*output.Error)
		if !ok || e.Code != c.code || !strings.Contains(e.Hint, c.hint) {
			t.Errorf("Msg %d: %v", c.err.Number, err)
		}
		if fieldValue(res, "name") != "hr" || hasField(res, "dropped") || s.db("hr") == nil {
			t.Errorf("Msg %d: record %v", c.err.Number, res.Fields)
		}
	}
}

// TestDBDropOffline: the drop leaves an OFFLINE database's files, which a
// warning says.
func TestDBDropOffline(t *testing.T) {
	res, _, err := newDBServer(t).run(t, "drop archive --confirm archive --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, "OFFLINE: SQL Server deletes") }) {
		t.Errorf("warnings %q", res.Warnings)
	}
}
