package cmd

import (
	"context"
	"slices"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/db"
)

const (
	restoreData = "/var/opt/mssql/data"
	restoreLog  = "/var/opt/mssql/log"
)

func appFiles(dir string) []*gosmo.BackupFile {
	return []*gosmo.BackupFile{
		{LogicalName: "app", PhysicalName: dir + "/app.mdf", Type: gosmo.BackupFileData, FileGroupName: "PRIMARY", Size: 8 << 20},
		{LogicalName: "app_log", PhysicalName: dir + "/app_log.ldf", Type: gosmo.BackupFileLog, Size: 2 << 20},
	}
}

// newRestoreServer is newBackupServer's (app FULL id 5, hr SIMPLE id 8,
// archive OFFLINE, the snapshot app_snap) with default data and log
// directories, the media of a few backup files, each set's file list, and
// the instance's files: app's and hr's in the data directory.
func newRestoreServer(t *testing.T) *maintServer {
	c := newBackupServer(t)
	c.Info.DataPath, c.Info.LogPath = restoreData, restoreLog
	// app.bak: one full backup of app. multi.bak: app's full, then its log.
	// hr.bak: a full backup of hr.
	full := func(db string) *gosmo.BackupHeader {
		return &gosmo.BackupHeader{DatabaseName: db, SetType: gosmo.BackupSetDatabase, Position: 1, ServerName: "srv",
			BackupStart: agentDate("2026-11-03 10:00:00"), BackupFinish: agentDate("2026-11-03 10:00:05"),
			BackupSize: 10 << 20, HasChecksums: true, SoftwareVersionMajor: 16}
	}
	c.media["/b/app.bak"] = []*gosmo.BackupHeader{full("app")}
	c.media["/b/hr.bak"] = []*gosmo.BackupHeader{full("hr")}
	c.media["/b/multi.bak"] = []*gosmo.BackupHeader{full("app"), {DatabaseName: "app", SetType: gosmo.BackupSetLog, Position: 2,
		BackupStart: agentDate("2026-11-03 11:00:00"), BackupFinish: agentDate("2026-11-03 11:00:01"), BackupSize: 1 << 20}}
	c.fileLists["/b/app.bak"] = map[int][]*gosmo.BackupFile{0: appFiles(restoreData)}
	c.fileLists["/b/multi.bak"] = map[int][]*gosmo.BackupFile{0: appFiles(restoreData), 2: appFiles(restoreData)}
	c.fileLists["/b/hr.bak"] = map[int][]*gosmo.BackupFile{0: {
		{LogicalName: "hr", PhysicalName: `D:\Data\hr.mdf`, Type: gosmo.BackupFileData, FileGroupName: "PRIMARY", Size: 4 << 20},
		{LogicalName: "hr_log", PhysicalName: `D:\Data\hr_log.ldf`, Type: gosmo.BackupFileLog, Size: 1 << 20},
	}}
	c.files = []*gosmo.InstanceFile{
		{DatabaseID: 5, DatabaseName: "app", FileID: 1, Name: "app", PhysicalName: restoreData + "/app.mdf", Type: "ROWS"},
		{DatabaseID: 5, DatabaseName: "app", FileID: 2, Name: "app_log", PhysicalName: restoreData + "/app_log.ldf", Type: "LOG"},
		{DatabaseID: 8, DatabaseName: "hr", FileID: 1, Name: "hr", PhysicalName: restoreData + "/hr_live.mdf", Type: "ROWS"},
		{DatabaseID: 8, DatabaseName: "hr", FileID: 2, Name: "hr_log", PhysicalName: restoreData + "/hr_live.ldf", Type: "LOG"},
	}
	return c
}

func restoreFakes(t *testing.T) (Catalog, Writer) {
	return ServerCatalog(&db.ServerConn{Server: newRestoreServer(t).Server()}), nil
}

// runRestore runs maint restore <argv> over s; dials counts the connects.
func runRestore(t *testing.T, s *maintServer, argv string) (res *output.Result, dials int, err error) {
	t.Helper()
	d := maintDeps(s, &dials)
	argv0 := append([]string{"maint", "restore", "--server", "srv", "--auth", "windows"}, strings.Fields(argv)...)
	inv, perr := args.Parse(&spec.Tree{Commands: MaintCommands(d)}, argv0, args.Options{})
	if perr != nil {
		t.Fatalf("%q: %v", argv, perr)
	}
	res, err = inv.Command.Run(context.Background(), inv)
	return res, dials, err
}

func resultSet(t *testing.T, res *output.Result, name string) [][]string {
	t.Helper()
	for _, s := range res.Sets {
		if s.Name == name {
			return setRows(s)
		}
	}
	t.Fatalf("no set %s in %+v", name, res.Sets)
	return nil
}

// TestMaintRestoreCopy: a copy under a new name with --move-to-default —
// every file renamed into the default directories, the record, the sets,
// the database read back.
func TestMaintRestoreCopy(t *testing.T) {
	cat := newRestoreServer(t)
	res, _, err := runRestore(t, cat, "app_copy --from /b/app.bak --move-to-default --checksum --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"USE [master];\nRESTORE DATABASE [app_copy]\nFROM DISK = N'/b/app.bak'\n" +
		"WITH MOVE N'app' TO N'" + restoreData + "/app_copy_app.mdf',\n     MOVE N'app_log' TO N'" + restoreLog + "/app_copy_app_log.ldf',\n" +
		"     CHECKSUM,\n     STATS = 10"}; !slices.Equal(cat.writes, want) {
		t.Errorf("sent\n%q\nwant\n%q", cat.writes, want)
	}
	got := fieldValues(res, "database", "from", "setNumber", "backupType", "sourceDatabase", "targetExists",
		"recovery", "replace", "closeConnections", "checksum", "dryRun", "state")
	want := []string{"app_copy", "/b/app.bak", "1", "full", "app", "false", "recovery", "false", "false", "true", "false", "ONLINE"}
	if !slices.Equal(got, want) {
		t.Errorf("record\n%q\nwant\n%q", got, want)
	}
	if files := resultSet(t, res, "files"); !slices.EqualFunc(files, [][]string{
		{"app", "data", "PRIMARY", "8.00", restoreData + "/app.mdf", restoreData + "/app_copy_app.mdf"},
		{"app_log", "log", "NULL", "2.00", restoreData + "/app_log.ldf", restoreLog + "/app_copy_app_log.ldf"},
	}, slices.Equal) {
		t.Errorf("files %q", files)
	}
	if b := resultSet(t, res, "backup"); len(b) != 1 || b[0][0] != "1" || b[0][1] != "1" || b[0][2] != "app" || b[0][11] != "16" {
		t.Errorf("backup %q", b)
	}
	if res.Database != "app_copy" || len(res.Warnings) != 0 || hasField(res, "tsql") {
		t.Errorf("database %q, warnings %q", res.Database, res.Warnings)
	}
}

// TestMaintRestoreDryRun: the reads are real, the RESTORE is scripted —
// gosmo's statement, USE [master] and the MOVE clauses included.
func TestMaintRestoreDryRun(t *testing.T) {
	cat := newRestoreServer(t)
	res, _, err := runRestore(t, cat, "app_copy --from /b/app.bak --with-move app=/x/a.mdf --with-move app_log=/x/a.ldf --norecovery --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.writes) != 0 {
		t.Errorf("a dry run restored: %q", cat.writes)
	}
	if len(cat.headerRead) != 1 || len(cat.fileListReads) != 1 || cat.instanceReads != 1 {
		t.Errorf("reads: headers %d, file lists %d, instance files %d; want one each", len(cat.headerRead), len(cat.fileListReads), cat.instanceReads)
	}
	want := "USE [master];\nRESTORE DATABASE [app_copy]\nFROM DISK = N'/b/app.bak'\n" +
		"WITH MOVE N'app' TO N'/x/a.mdf',\n     MOVE N'app_log' TO N'/x/a.ldf',\n     NORECOVERY,\n     STATS = 10\nGO\n"
	if got := fieldValue(res, "tsql"); got != want {
		t.Errorf("tsql\n%q\nwant\n%q", got, want)
	}
	if files := resultSet(t, res, "files"); files[0][5] != "/x/a.mdf" || files[1][5] != "/x/a.ldf" {
		t.Errorf("files %q", files)
	}
}

// TestMaintRestoreCollision: a planned path another database holds is
// BLOCKED before anything is sent; restoring over a database keeps its own
// file paths, which are no collision.
func TestMaintRestoreCollision(t *testing.T) {
	cat := newRestoreServer(t)
	_, _, err := runRestore(t, cat, "app_copy --from /b/app.bak --allow admin --dry-run")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeBlocked ||
		!strings.Contains(e.Message, "which is [app]'s file app") || !strings.Contains(e.Hint, "--move-to-default") {
		t.Fatalf("restore onto app's files: %v", err)
	}
	// Case differs, still the same file to Windows.
	cat = newRestoreServer(t)
	_, _, err = runRestore(t, cat, "app_copy --from /b/app.bak --with-move app=/VAR/opt/mssql/data/HR_live.mdf "+
		"--with-move app_log=/x/l.ldf --allow admin --dry-run")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeBlocked || !strings.Contains(e.Message, "[hr]'s file hr") {
		t.Errorf("moved onto hr's file: %v", err)
	}

	// Over hr, from hr's backup of D:\: the files stay at hr's own paths.
	cat = newRestoreServer(t)
	res, _, err := runRestore(t, cat, "hr --from /b/hr.bak --replace --confirm hr --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if files := resultSet(t, res, "files"); files[0][5] != restoreData+"/hr_live.mdf" || files[1][5] != restoreData+"/hr_live.ldf" {
		t.Errorf("over an existing database, files %q", files)
	}
	if want := []string{"USE [master];\nRESTORE DATABASE [hr]\nFROM DISK = N'/b/hr.bak'\nWITH REPLACE,\n     STATS = 10"}; !slices.Equal(cat.writes, want) ||
		fieldValue(res, "targetExists") != "true" {
		t.Errorf("sent\n%q\nwant\n%q", cat.writes, want)
	}
}

// TestMaintRestoreChain: a full set with --norecovery leaves the database
// RESTORING; its log set from the same media then needs --file-number,
// no --confirm (it overwrites nothing), and places no files.
func TestMaintRestoreChain(t *testing.T) {
	cat := newRestoreServer(t)
	res, _, err := runRestore(t, cat, "app2 --from /b/multi.bak --file-number 1 --move-to-default --norecovery --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldValues(res, "setNumber", "recovery", "state"); !slices.Equal(got, []string{"1", "norecovery", "RESTORING"}) {
		t.Errorf("full: %q", got)
	}
	if b := resultSet(t, res, "backup"); b[0][1] != "1" || b[1][1] != "0" {
		t.Errorf("selected marks %q", b)
	}

	res, _, err = runRestore(t, cat, "app2 --from /b/multi.bak --file-number 2 --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if want := "USE [master];\nRESTORE LOG [app2]\nFROM DISK = N'/b/multi.bak'\nWITH FILE = 2,\n     STATS = 10"; cat.writes[1] != want {
		t.Errorf("log restore\n%q\nwant\n%q", cat.writes[1], want)
	}
	if got := fieldValues(res, "setNumber", "backupType", "targetExists", "state"); !slices.Equal(got, []string{"2", "log", "true", "ONLINE"}) {
		t.Errorf("log: %q", got)
	}
	if files := resultSet(t, res, "files"); len(files) != 0 {
		t.Errorf("a log set listed files %q", files)
	}
	if len(cat.fileListReads) != 1 {
		t.Errorf("file lists read %v: a log set places no files", cat.fileListReads)
	}

	// Onto the database now ONLINE, the log needs --confirm again.
	_, _, err = runRestore(t, cat, "app2 --from /b/multi.bak --file-number 2 --allow admin")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeBlocked || !strings.Contains(e.Message, "exists") {
		t.Errorf("log onto an online database: %v", err)
	}
	// --standby names the undo file.
	cat.dbs[len(cat.dbs)-1].State = gosmo.DatabaseRestoring
	if _, _, err := runRestore(t, cat, "app2 --from /b/multi.bak --file-number 2 --standby /x/undo.dat --allow admin"); err != nil {
		t.Fatal(err)
	}
	if last := cat.writes[len(cat.writes)-1]; !strings.Contains(last, "STANDBY = N'/x/undo.dat'") {
		t.Errorf("standby %q", last)
	}
}

// TestMaintRestoreVerifyOnly: RESTORE VERIFYONLY of the chosen set,
// nothing else; no database needed, and a dry run scripts it.
func TestMaintRestoreVerifyOnly(t *testing.T) {
	cat := newRestoreServer(t)
	res, _, err := runRestore(t, cat, "--from /b/multi.bak --file-number 2 --verify-only --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"RESTORE VERIFYONLY FROM DISK = N'/b/multi.bak' WITH FILE = 2"}; !slices.Equal(cat.writes, want) {
		t.Errorf("sent %q, want %q", cat.writes, want)
	}
	if got := fieldValues(res, "setNumber", "backupType", "sourceDatabase", "valid"); !slices.Equal(got, []string{"2", "log", "app", "true"}) {
		t.Errorf("record %q", got)
	}
	if len(cat.fileListReads) != 0 || cat.instanceReads != 0 {
		t.Errorf("verify read file lists %v, instance files %d", cat.fileListReads, cat.instanceReads)
	}

	res, _, err = runRestore(t, cat, "hr --from /b/app.bak --verify-only --checksum --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldValue(res, "tsql"); got != "RESTORE VERIFYONLY FROM DISK = N'/b/app.bak' WITH CHECKSUM\nGO\n" {
		t.Errorf("tsql %q", got)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "backup of [app], not of [hr]") || hasField(res, "valid") {
		t.Errorf("warnings %q", res.Warnings)
	}
	// --checksum on a set backed up without: refused before sending.
	_, _, err = runRestore(t, cat, "--from /b/multi.bak --file-number 2 --verify-only --checksum --allow admin")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeUsageError || !strings.Contains(e.Message, "without checksums") {
		t.Errorf("checksum on a set without: %v", err)
	}
}

// TestMaintRestoreRefusals: each refusal and when it is made, by dial count.
func TestMaintRestoreRefusals(t *testing.T) {
	for _, c := range []struct {
		argv  string
		setup func(*maintServer)
		code  output.Code
		dials int
		msg   string
	}{
		{"app --allow admin", nil, output.CodeUsageError, 0, "--from is required"},
		{"--from /b/app.bak --allow admin", nil, output.CodeUsageError, 0, "the database to restore to is missing"},
		{"MSDB --from /b/app.bak --allow admin", nil, output.CodeBlocked, 0, "is a system database"},
		{"app --from /b/app.bak --confirm APP --allow admin", nil, output.CodeBlocked, 0, `--confirm "APP" is not`},
		{"x --from /b/app.bak --file-number 0 --allow admin", nil, output.CodeUsageError, 0, "not a backup set number"},
		{"x --from /b/app.bak --with-move app --allow admin", nil, output.CodeUsageError, 0, "is not <logical name>=<path>"},
		{"x --from /b/app.bak --with-move app=/a --with-move APP=/b --allow admin", nil, output.CodeUsageError, 0, "twice"},
		{"x --from /b/app.bak --with-move app=/a --move-to-default --allow admin", nil, output.CodeUsageError, 0, "both place"},
		{"x --from /b/app.bak --norecovery --standby /u --allow admin", nil, output.CodeUsageError, 0, "two end states"},
		{"x --from /b/app.bak --verify-only --move-to-default --allow admin", nil, output.CodeUsageError, 0, "--move-to-default is not for --verify-only"},
		{"x --from /b/app.bak --verify-only --confirm x --allow admin", nil, output.CodeUsageError, 0, "--confirm is not for --verify-only"},
		{"x --from /b/app.bak", nil, output.CodeBlocked, 0, ""},
		{"x --from /b/app.bak --allow admin", func(c *maintServer) {
			c.Info.EngineEdition = int(gosmo.EngineAzureSQLDatabase)
		}, output.CodeUnsupported, 1, "Azure SQL Database"},
		{"x --from /b/none.bak --allow admin", nil, output.CodeNotFound, 1, "no backup set on /b/none.bak"},
		{"x --from /b/multi.bak --move-to-default --allow admin", nil, output.CodeUsageError, 1, "holds 2 backup sets"},
		{"x --from /b/multi.bak --file-number 3 --allow admin", nil, output.CodeNotFound, 1, "it holds set 1, 2"},
		{"x --from /b/multi.bak --file-number 2 --move-to-default --allow admin", nil, output.CodeUsageError, 1, "places no files"},
		{"app --from /b/app.bak --allow admin --dry-run", nil, output.CodeBlocked, 1, "[app] exists"},
		{"APP --from /b/app.bak --confirm APP --allow admin", func(c *maintServer) { c.current = "app" },
			output.CodeUsageError, 1, "the connection's own database"},
		{"app_snap --from /b/app.bak --confirm app_snap --allow admin", nil, output.CodeUsageError, 1, "is a database snapshot"},
		{"app --from /b/app.bak --confirm app --allow admin", func(c *maintServer) {
			c.Info.HADREnabled = true
			c.ags = map[string][]string{"ag1": {"app"}}
		}, output.CodeBlocked, 1, "is in availability group [ag1]"},
		{"x --from /b/app.bak --with-move nope=/a --allow admin", nil, output.CodeNotFound, 1, `has no file "nope"`},
		{"x --from /b/app.bak --with-move app=/a --with-move app_log=/A --allow admin", nil, output.CodeUsageError, 1, "would both go to"},
	} {
		cat := newRestoreServer(t)
		if c.setup != nil {
			c.setup(cat)
		}
		_, dials, err := runRestore(t, cat, c.argv)
		e, ok := err.(*output.Error)
		if !ok || e.Code != c.code || dials != c.dials || !strings.Contains(e.Message, c.msg) {
			t.Errorf("%s: %v (dials %d), want %s with %q, dials %d", c.argv, err, dials, c.code, c.msg, c.dials)
			continue
		}
		if len(cat.writes) != 0 {
			t.Errorf("%s: sent a restore", c.argv)
		}
	}
}

// TestMaintRestoreServerRefusals: a refused RESTORE's (or media read's)
// cause, not its closing Msg 3013, is reported, with its hint and code.
func TestMaintRestoreServerRefusals(t *testing.T) {
	terminated := mssql.Error{Number: 3013, Class: 16, Message: "RESTORE DATABASE is terminating abnormally."}
	for _, c := range []struct {
		argv  string
		cause mssql.Error
		code  output.Code
		hint  string
	}{
		{"app --from /b/hr.bak --confirm app --allow admin", mssql.Error{Number: 3154, Class: 16,
			Message: "The backup set holds a backup of a database other than the existing 'app' database."},
			output.CodeSQLError, "--replace"},
		{"app --from /b/app.bak --confirm app --allow admin", mssql.Error{Number: 3159, Class: 16,
			Message: `The tail of the log for the database "app" has not been backed up.`},
			output.CodeSQLError, "maint backup app --type log"},
		{"app --from /b/app.bak --confirm app --allow admin", mssql.Error{Number: 3101, Class: 16,
			Message: "Exclusive access could not be obtained because the database is in use."},
			output.CodeBlocked, "--close-connections"},
		{"x --from /b/hr.bak --allow admin", mssql.Error{Number: 5133, Class: 16,
			Message: `Directory lookup for the file "D:\Data\hr.mdf" failed with the operating system error 3.`},
			output.CodeSQLError, "--move-to-default"},
		{"x --from /b/app.bak --move-to-default --allow admin", mssql.Error{Number: 3169, Class: 16,
			Message: "The database was backed up on a server running version 17.00.1000."},
			output.CodeUnsupported, "same SQL Server version or a newer"},
		{"x --from /b/app.bak --move-to-default --allow admin", mssql.Error{Number: 262, Class: 14,
			Message: "CREATE DATABASE permission denied in database 'master'."},
			output.CodePermissionDenied, "dbcreator"},
	} {
		end := terminated
		end.All = []mssql.Error{c.cause, terminated}
		cat := newRestoreServer(t)
		cat.writeErr = end
		res, _, err := runRestore(t, cat, c.argv)
		e, ok := err.(*output.Error)
		if !ok || e.Code != c.code || e.SQL == nil || e.SQL.Number != c.cause.Number || e.Message != c.cause.Message ||
			!strings.Contains(e.Hint, c.hint) {
			t.Errorf("%s: %+v, want %s Msg %d, hint with %q", c.argv, err, c.code, c.cause.Number, c.hint)
			continue
		}
		if hasField(res, "state") {
			t.Errorf("%s: a failed restore was read back", c.argv)
		}
	}

	// The media read fails the same way: its cause is reported.
	cat := newRestoreServer(t)
	end := mssql.Error{Number: 3013, Class: 16, Message: "RESTORE HEADERONLY is terminating abnormally."}
	cause := mssql.Error{Number: 3201, Class: 16, Message: "Cannot open backup device 'Z:\\nope.bak'."}
	end.All = []mssql.Error{cause, end}
	cat.headersErr = end
	_, _, err := runRestore(t, cat, "x --from Z:\\nope.bak --allow admin")
	if e, ok := err.(*output.Error); !ok || e.SQL == nil || e.SQL.Number != 3201 || !strings.Contains(e.Hint, "service account") {
		t.Errorf("media read: %+v", err)
	}
}
