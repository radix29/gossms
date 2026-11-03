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

// backupCatalog is newDropCatalog's databases (app FULL, hr SIMPLE) on a
// Developer edition with a backup directory, msdb's history per database,
// and the headers each backup file holds — the media a backupWriter writes.
type backupCatalog struct {
	*dropCatalog
	history    map[string][]*gosmo.BackupInfo
	media      map[string][]*gosmo.BackupHeader
	headersErr error
	headerRead [][]string
	historyFor []string
	clock      time.Time
}

func newBackupCatalog() *backupCatalog {
	c := &backupCatalog{dropCatalog: newDropCatalog(), media: map[string][]*gosmo.BackupHeader{},
		clock: time.Date(2026, 11, 3, 12, 0, 0, 0, jobServerZone)}
	c.info = &gosmo.ServerInfo{VersionMajor: 16, EngineEdition: int(gosmo.EngineEnterprise), Edition: "Developer Edition (64-bit)",
		DefaultBackupPath: "/var/opt/mssql/backup"}
	for _, d := range c.dbs {
		d.RecoveryModel = gosmo.RecoveryModelFull
		if d.Name == "hr" {
			d.RecoveryModel = gosmo.RecoveryModelSimple
		}
	}
	c.history = map[string][]*gosmo.BackupInfo{
		"app": {
			{DatabaseName: "app", SetType: gosmo.BackupSetLog, BackupStart: agentDate("2026-11-02 23:00:00"),
				BackupFinish: agentDate("2026-11-02 23:00:02"), BackupSize: 3 << 20, Devices: []string{"/b/app.trn"},
				Position: 2, FirstLSN: "41000000012300001", LastLSN: "41000000045600001", UserName: "sa", ServerName: "srv"},
			{DatabaseName: "app", SetType: gosmo.BackupSetDatabase, BackupStart: agentDate("2026-11-02 22:00:00"),
				BackupFinish: agentDate("2026-11-02 22:01:30"), BackupSize: 512 << 20, Devices: []string{"/b/app_1.bak", "/b/app_2.bak"},
				Position: 1, FirstLSN: "41000000010000001", LastLSN: "41000000012300001", UserName: "sa", ServerName: "srv"},
		},
		"hr": {
			{DatabaseName: "hr", SetType: gosmo.BackupSetDatabase, IsCopyOnly: true, BackupStart: agentDate("2026-11-01 08:00:00"),
				BackupFinish: agentDate("2026-11-01 08:00:10"), BackupSize: 10 << 20, Devices: []string{"/b/hr.bak"}},
		},
		// gone was dropped; msdb keeps its history.
		"gone": {
			{DatabaseName: "gone", SetType: gosmo.BackupSetDatabase, BackupStart: agentDate("2026-10-01 08:00:00"),
				BackupFinish: agentDate("2026-10-01 08:00:01"), BackupSize: 1 << 20, Devices: []string{"/b/gone.bak"}},
		},
	}
	c.backups = []*gosmo.DatabaseLastBackups{
		{DatabaseName: "app", RecoveryModel: gosmo.RecoveryModelFull, Full: c.history["app"][1], Log: c.history["app"][0]},
		{DatabaseName: "archive", RecoveryModel: gosmo.RecoveryModelFull},
		{DatabaseName: "hr", RecoveryModel: gosmo.RecoveryModelSimple, Full: c.history["hr"][0]},
		{DatabaseName: "master", RecoveryModel: gosmo.RecoveryModelSimple, Full: &gosmo.BackupInfo{DatabaseName: "master",
			SetType: gosmo.BackupSetDatabase, BackupStart: agentDate("2026-11-03 01:00:00"), BackupFinish: agentDate("2026-11-03 01:00:05")}},
		{DatabaseName: "model", RecoveryModel: gosmo.RecoveryModelFull, Full: &gosmo.BackupInfo{DatabaseName: "model",
			SetType: gosmo.BackupSetDatabase, BackupStart: agentDate("2026-11-03 01:00:06"), BackupFinish: agentDate("2026-11-03 01:00:07")}},
	}
	return c
}

func (c *backupCatalog) CurrentTime(context.Context) (time.Time, error) { return c.clock, nil }

func (c *backupCatalog) BackupHistory(_ context.Context, name string) ([]*gosmo.BackupInfo, error) {
	c.historyFor = append(c.historyFor, name)
	return c.history[name], nil
}

func (c *backupCatalog) BackupHeaders(_ context.Context, targets ...gosmo.BackupTarget) ([]*gosmo.BackupHeader, error) {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.String()
	}
	c.headerRead = append(c.headerRead, names)
	if c.headersErr != nil {
		return nil, c.headersErr
	}
	return c.media[names[0]], nil
}

// backupWriter appends a set to the first device's media, as BACKUP does
// (INIT replaces them), failing with err. Under WithScript it is the real
// serverWriter, so a dry run captures gosmo's statement.
type backupWriter struct {
	Writer
	cat     *backupCatalog
	sent    []gosmo.BackupOptions
	err     error
	noMedia bool // succeed and write no set
}

func (w *backupWriter) Backup(ctx context.Context, opts gosmo.BackupOptions) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{&gosmo.Server{}}.Backup(ctx, opts)
	}
	w.sent = append(w.sent, opts)
	if w.err != nil || w.noMedia {
		return w.err
	}
	dev := opts.Devices[0].String()
	if opts.Init {
		w.cat.media[dev] = nil
	}
	set := gosmo.BackupSetDatabase
	switch opts.Action {
	case gosmo.BackupActionLog:
		set = gosmo.BackupSetLog
	case gosmo.BackupActionDifferential:
		set = gosmo.BackupSetDifferential
	}
	comp := opts.Compression != nil && *opts.Compression
	size := int64(8 << 20)
	w.cat.media[dev] = append(w.cat.media[dev], &gosmo.BackupHeader{DatabaseName: opts.Database, SetType: set,
		Position: len(w.cat.media[dev]) + 1, BackupStart: agentDate("2026-11-03 12:00:00"),
		BackupFinish: agentDate("2026-11-03 12:00:04"), BackupSize: size, CompressedSize: map[bool]int64{true: size / 4, false: size}[comp],
		Compressed: comp, HasChecksums: opts.Checksum})
	return nil
}

// fixBackupClock fixes the clock default backup files are named by.
func fixBackupClock(t *testing.T) {
	old := backupNow
	t.Cleanup(func() { backupNow = old })
	backupNow = func() time.Time { return time.Date(2026, 11, 3, 13, 4, 5, 0, jobServerZone) }
}

func backupFakes(t *testing.T) (Catalog, Writer) {
	fixBackupClock(t)
	cat := newBackupCatalog()
	return cat, &backupWriter{cat: cat}
}

// runMaint runs maint <argv> over cat and w, an ad-hoc connection; dials
// counts the connects.
func runMaint(t *testing.T, cat *backupCatalog, w *backupWriter, argv string) (res *output.Result, dials int, err error) {
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

func fieldValues(res *output.Result, keys ...string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = fieldValue(res, k)
	}
	return out
}

// TestMaintBackup: the default file in the instance's backup directory,
// named for the database, type and UTC time; the record, then the new set
// read back from it.
func TestMaintBackup(t *testing.T) {
	fixBackupClock(t)
	cat, w := newBackupCatalog(), &backupWriter{}
	res, _, err := runMaint(t, cat, w, "backup APP --compression --checksum --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.sent) != 1 {
		t.Fatalf("sent %d backups", len(w.sent))
	}
	o := w.sent[0]
	if o.Database != "app" || o.Action != gosmo.BackupActionDatabase || o.Stats != backupStats || o.Progress == nil ||
		o.Compression == nil || !*o.Compression || !o.Checksum || o.Init || o.CopyOnly {
		t.Errorf("options %+v", o)
	}
	const file = "/var/opt/mssql/backup/app_full_20261103_110405.bak"
	got := fieldValues(res, "database", "type", "to", "compression", "copyOnly", "checksum", "init", "dryRun",
		"setNumber", "backupStart", "backupFinish", "sizeBytes", "compressedBytes", "compressed", "hasChecksums")
	want := []string{"app", "full", file, "on", "false", "true", "false", "false",
		"1", "2026-11-03 12:00:00.000", "2026-11-03 12:00:04.000", "8388608", "2097152", "true", "true"}
	if !slices.Equal(got, want) {
		t.Errorf("record\n%q\nwant\n%q", got, want)
	}
	if res.Database != "app" || len(res.Warnings) != 0 || hasField(res, "tsql") {
		t.Errorf("header %q, warnings %q", res.Database, res.Warnings)
	}
	if !slices.EqualFunc(cat.headerRead, [][]string{{file}}, slices.Equal) {
		t.Errorf("headers read from %q", cat.headerRead)
	}

	// Appended to a file holding another database's set and an older one
	// of app's: the newest of app's of the type is the one reported.
	cat.media["/b/x.bak"] = []*gosmo.BackupHeader{{DatabaseName: "app", SetType: gosmo.BackupSetDatabase, Position: 1},
		{DatabaseName: "hr", SetType: gosmo.BackupSetDatabase, Position: 2}}
	res, _, err = runMaint(t, cat, &backupWriter{}, "backup app --to /b/x.bak --no-compression --copy-only --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldValues(res, "to", "compression", "copyOnly", "setNumber", "compressed"); !slices.Equal(got,
		[]string{"/b/x.bak", "off", "true", "3", "false"}) {
		t.Errorf("appended record %q", got)
	}
}

// TestMaintBackupTypesAndStripes: each type's action and default extension;
// --to repeated stripes, every file named and read back; --init with
// --confirm replaces the media's sets.
func TestMaintBackupTypesAndStripes(t *testing.T) {
	fixBackupClock(t)
	for _, c := range []struct{ argv, file string }{
		{"backup app --type diff --allow admin", "/var/opt/mssql/backup/app_diff_20261103_110405.bak"},
		{"backup app --type log --allow admin", "/var/opt/mssql/backup/app_log_20261103_110405.trn"},
	} {
		res, _, err := runMaint(t, newBackupCatalog(), &backupWriter{}, c.argv)
		if err != nil {
			t.Fatalf("%s: %v", c.argv, err)
		}
		if got := fieldValues(res, "to", "setNumber"); !slices.Equal(got, []string{c.file, "1"}) {
			t.Errorf("%s: %q", c.argv, got)
		}
	}

	cat, w := newBackupCatalog(), &backupWriter{}
	cat.media["/b/s1.bak"] = []*gosmo.BackupHeader{{DatabaseName: "app", SetType: gosmo.BackupSetDatabase, Position: 1}}
	res, _, err := runMaint(t, cat, w, "backup app --to /b/s1.bak --to /b/s2.bak --init --confirm app --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldValues(res, "to", "init", "setNumber"); !slices.Equal(got, []string{"/b/s1.bak, /b/s2.bak", "true", "1"}) {
		t.Errorf("striped record %q", got)
	}
	if !slices.EqualFunc(cat.headerRead, [][]string{{"/b/s1.bak", "/b/s2.bak"}}, slices.Equal) {
		t.Errorf("headers read from %q: a striped set is read over all its files", cat.headerRead)
	}

	// A Windows backup directory joins with its own separator; a database
	// name a file system refuses is made safe; no directory leaves the name
	// bare, for the server to place.
	cat = newBackupCatalog()
	cat.info.DefaultBackupPath = `D:\Backup`
	cat.dbs = append(cat.dbs, &gosmo.Database{Name: `a/b:c`, ID: 30, State: gosmo.DatabaseOnline, RecoveryModel: gosmo.RecoveryModelFull})
	res, _, err = runMaint(t, cat, &backupWriter{}, "backup a/b:c --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldValue(res, "to"); got != `D:\Backup\a_b_c_full_20261103_110405.bak` {
		t.Errorf("Windows default file %q", got)
	}
	cat.info.DefaultBackupPath = ""
	res, _, _ = runMaint(t, cat, &backupWriter{}, "backup app --allow admin --dry-run")
	if got := fieldValue(res, "to"); got != "app_full_20261103_110405.bak" {
		t.Errorf("no backup directory: %q", got)
	}
}

func TestMaintBackupDryRun(t *testing.T) {
	fixBackupClock(t)
	cat, w := newBackupCatalog(), &backupWriter{}
	res, _, err := runMaint(t, cat, w, "backup app --to https://acct.blob.core.windows.net/c/app.bak --credential cred "+
		"--type full --copy-only --checksum --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.sent) != 0 || len(cat.headerRead) != 0 {
		t.Errorf("a dry run sent %d backups and read %d headers", len(w.sent), len(cat.headerRead))
	}
	want := "BACKUP DATABASE [app] TO URL = N'https://acct.blob.core.windows.net/c/app.bak' " +
		"WITH CREDENTIAL = N'cred', COPY_ONLY, CHECKSUM, STATS = 10\nGO\n"
	if got := fieldValue(res, "tsql"); got != want {
		t.Errorf("tsql\n%q\nwant\n%q", got, want)
	}
	if fieldValue(res, "dryRun") != "true" || hasField(res, "setNumber") {
		t.Errorf("record %+v", res.Fields)
	}
}

// TestMaintBackupRefusals: each refusal and when it is made, by dial count.
func TestMaintBackupRefusals(t *testing.T) {
	fixBackupClock(t)
	for _, c := range []struct {
		argv  string
		setup func(*backupCatalog)
		code  output.Code
		dials int
		msg   string
	}{
		{"backup app --init --allow admin", nil, output.CodeBlocked, 0, "--init overwrites"},
		{"backup app --init --confirm APP --allow admin", nil, output.CodeBlocked, 0, `--confirm "APP" is not`},
		{"backup app --confirm app --allow admin", nil, output.CodeUsageError, 0, "--confirm is for --init"},
		{"backup app --to /b/a.bak --credential c --allow admin", nil, output.CodeUsageError, 0, "--credential is for a backup to URL"},
		{"backup app --to= --allow admin", nil, output.CodeUsageError, 0, "--to: empty path"},
		{"backup app", nil, output.CodeBlocked, 0, ""},
		{"backup nope --allow admin", nil, output.CodeNotFound, 1, "no database [nope]"},
		{"backup tempdb --allow admin", func(c *backupCatalog) {
			c.dbs = append(c.dbs, &gosmo.Database{Name: "tempdb", ID: 2, State: gosmo.DatabaseOnline})
		}, output.CodeUsageError, 1, "tempdb cannot be backed up"},
		{"backup app_snap --allow admin", nil, output.CodeUsageError, 1, "is a database snapshot"},
		{"backup hr --type log --allow admin", nil, output.CodeUsageError, 1, "SIMPLE recovery"},
		{"backup hr --type log --allow admin --dry-run", nil, output.CodeUsageError, 1, "SIMPLE recovery"},
		{"backup app --compression --allow admin", func(c *backupCatalog) {
			c.info.EngineEdition, c.info.Edition = int(gosmo.EngineExpress), "Express Edition (64-bit)"
		}, output.CodeUnsupported, 1, "Express Edition"},
		{"backup app --allow admin", func(c *backupCatalog) {
			c.info.EngineEdition = int(gosmo.EngineAzureSQLDatabase)
		}, output.CodeUnsupported, 1, "Azure SQL Database"},
	} {
		cat, w := newBackupCatalog(), &backupWriter{}
		if c.setup != nil {
			c.setup(cat)
		}
		res, dials, err := runMaint(t, cat, w, c.argv)
		e, ok := err.(*output.Error)
		if !ok || e.Code != c.code || dials != c.dials || !strings.Contains(e.Message, c.msg) {
			t.Errorf("%s: %v (dials %d), want %s with %q, dials %d", c.argv, err, dials, c.code, c.msg, c.dials)
			continue
		}
		if len(w.sent) != 0 {
			t.Errorf("%s: sent a backup", c.argv)
		}
		_ = res
	}
	// --no-compression on Express is fine: NO_COMPRESSION is accepted everywhere.
	cat := newBackupCatalog()
	cat.info.EngineEdition = int(gosmo.EngineExpress)
	if _, _, err := runMaint(t, cat, &backupWriter{}, "backup app --no-compression --allow admin"); err != nil {
		t.Errorf("--no-compression on Express: %v", err)
	}
}

// TestMaintBackupServerRefusals: a refused BACKUP's cause, not its closing
// Msg 3013, is reported, with the hint and code that cause gets.
func TestMaintBackupServerRefusals(t *testing.T) {
	fixBackupClock(t)
	terminated := mssql.Error{Number: 3013, Class: 16, Message: "BACKUP DATABASE is terminating abnormally."}
	for _, c := range []struct {
		argv  string
		cause mssql.Error
		code  output.Code
		hint  string
	}{
		{"backup app --type diff --allow admin", mssql.Error{Number: 3035, Class: 16,
			Message: `Cannot perform a differential backup for database "app", because a current database backup does not exist.`},
			output.CodeSQLError, "maint backup app --type full"},
		{"backup app --type log --allow admin", mssql.Error{Number: 4214, Class: 16,
			Message: "BACKUP LOG cannot be performed because there is no current database backup."},
			output.CodeSQLError, "maint backup app --type full"},
		{"backup app --to /nope/a.bak --allow admin", mssql.Error{Number: 3201, Class: 16,
			Message: "Cannot open backup device '/nope/a.bak'. Operating system error 2(The system cannot find the file specified.)."},
			output.CodeSQLError, "service account"},
		{"backup app --type log --allow admin", mssql.Error{Number: 4208, Class: 16,
			Message: "The statement BACKUP LOG is not allowed while the recovery model is SIMPLE."},
			output.CodeSQLError, "RECOVERY=FULL"},
		{"backup app --compression --allow admin", mssql.Error{Number: 1844, Class: 16,
			Message: "BACKUP DATABASE WITH COMPRESSION is not supported on Web Edition (64-bit)."},
			output.CodeUnsupported, "no backup compression"},
		{"backup app --to /b/s1.bak --allow admin", mssql.Error{Number: 3231, Class: 16,
			Message: `The media loaded on "/b/s1.bak" is formatted to support 2 media families, but 1 media families are expected according to the backup device specification.`},
			output.CodeSQLError, "every file of"},
		{"backup app --allow admin", mssql.Error{Number: 262, Class: 14,
			Message: "BACKUP DATABASE permission denied in database 'app'."},
			output.CodePermissionDenied, "db_backupoperator"},
	} {
		cause := c.cause
		end := terminated
		end.All = []mssql.Error{cause, terminated}
		w := &backupWriter{err: fmt.Errorf("gosmo: backup %q: %w", "app", end)}
		res, _, err := runMaint(t, newBackupCatalog(), w, c.argv)
		e, ok := err.(*output.Error)
		if !ok || e.Code != c.code || e.SQL == nil || e.SQL.Number != cause.Number || e.Message != cause.Message ||
			!strings.Contains(e.Hint, c.hint) {
			t.Errorf("%s: %+v, want %s Msg %d, hint with %q", c.argv, err, c.code, cause.Number, c.hint)
			continue
		}
		if hasField(res, "setNumber") {
			t.Errorf("%s: a failed backup was read back", c.argv)
		}
	}
}

// TestMaintBackupReadBackFails: the backup succeeded, so a header read that
// fails or finds nothing is a warning and NULL set fields.
func TestMaintBackupReadBackFails(t *testing.T) {
	fixBackupClock(t)
	cat := newBackupCatalog()
	cat.headersErr = mssql.Error{Number: 3201, Class: 16, Message: "Cannot open backup device."}
	res, _, err := runMaint(t, cat, &backupWriter{}, "backup app --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "not read back") {
		t.Errorf("warnings %q", res.Warnings)
	}
	for _, f := range res.Fields {
		if f.Key == "setNumber" && !f.Null {
			t.Errorf("setNumber %q, want NULL", f.Value)
		}
	}

	// A backup that leaves no set of it on the media.
	res, _, err = runMaint(t, newBackupCatalog(), &backupWriter{noMedia: true}, "backup app --to /b/x.bak --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "holds no set") {
		t.Errorf("warnings %q", res.Warnings)
	}
}

// historyCells is the set's rows, each its cells joined by |, NULL as -.
func historyCells(res *output.Result) []string {
	var out []string
	for _, r := range res.Sets[0].Rows {
		c := make([]string, len(r))
		for i, v := range r {
			c[i] = "-"
			if v != nil {
				c[i] = *v
			}
		}
		out = append(out, strings.Join(c, "|"))
	}
	return out
}

func TestMaintBackupHistory(t *testing.T) {
	cat := newBackupCatalog()
	res, dials, err := runMaint(t, cat, &backupWriter{}, "backup-history app")
	if err != nil || dials != 1 {
		t.Fatal(err, dials)
	}
	want := []string{
		"app|log|0|2026-11-02 23:00:00.000|2026-11-02 23:00:02.000|2|3.00|2|/b/app.trn|41000000012300001|41000000045600001|sa|srv",
		"app|full|0|2026-11-02 22:00:00.000|2026-11-02 22:01:30.000|90|512.00|1|/b/app_1.bak, /b/app_2.bak|41000000010000001|41000000012300001|sa|srv",
	}
	if got := historyCells(res); !slices.Equal(got, want) {
		t.Errorf("rows\n%q\nwant\n%q", got, want)
	}
	if res.Sets[0].Name != "backups" || len(res.Sets[0].Columns) != 13 {
		t.Errorf("set %s, %d columns", res.Sets[0].Name, len(res.Sets[0].Columns))
	}

	// --type and --since (zoned: 23:00+02:00 is 23:00 server local).
	res, _, err = runMaint(t, newBackupCatalog(), &backupWriter{}, "backup-history app --type full")
	if err != nil || len(res.Sets[0].Rows) != 1 || *res.Sets[0].Rows[0][1] != "full" {
		t.Errorf("--type full: %v %q", err, historyCells(res))
	}
	res, _, err = runMaint(t, newBackupCatalog(), &backupWriter{}, "backup-history app --since 2026-11-02T22:30:00+02:00")
	if err != nil || len(res.Sets[0].Rows) != 1 || *res.Sets[0].Rows[0][1] != "log" {
		t.Errorf("--since: %v %q", err, historyCells(res))
	}

	// A dropped database's history is msdb's; a database with neither
	// history nor a row is NOT_FOUND; one with no history is an empty set.
	res, _, err = runMaint(t, newBackupCatalog(), &backupWriter{}, "backup-history gone")
	if err != nil || len(res.Sets[0].Rows) != 1 {
		t.Errorf("dropped database: %v %q", err, historyCells(res))
	}
	if _, _, err = runMaint(t, newBackupCatalog(), &backupWriter{}, "backup-history nope"); err == nil ||
		err.(*output.Error).Code != output.CodeNotFound {
		t.Errorf("missing database: %v", err)
	}
	res, _, err = runMaint(t, newBackupCatalog(), &backupWriter{}, "backup-history archive")
	if err != nil || len(res.Sets[0].Rows) != 0 {
		t.Errorf("no history: %v %q", err, historyCells(res))
	}
}

// TestMaintBackupHistoryAll: every database's sets, newest first, read per
// database (not tempdb, not snapshots), capped by --top.
func TestMaintBackupHistoryAll(t *testing.T) {
	cat := newBackupCatalog()
	res, _, err := runMaint(t, cat, &backupWriter{}, "backup-history --all --top 2")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cat.historyFor, []string{"master", "app", "archive", "hr", "model"}) {
		t.Errorf("history read for %q", cat.historyFor)
	}
	set := res.Sets[0]
	if len(set.Rows) != 2 || !set.Truncated || set.TotalRowCount != 3 ||
		*set.Rows[0][0] != "app" || *set.Rows[0][1] != "log" || *set.Rows[1][1] != "full" {
		t.Errorf("rows %q, truncated %v of %d", historyCells(res), set.Truncated, set.TotalRowCount)
	}
}

// TestMaintBackupHistoryLast: one row per database and type, NULL past the
// type for none; the warnings name the databases never backed up, and the
// FULL-recovery ones with no log backup.
func TestMaintBackupHistoryLast(t *testing.T) {
	res, _, err := runMaint(t, newBackupCatalog(), &backupWriter{}, "backup-history --all --last")
	if err != nil {
		t.Fatal(err)
	}
	got := historyCells(res)
	if len(got) != 15 {
		t.Fatalf("%d rows, want 5 databases × 3 types: %q", len(got), got)
	}
	for i, want := range []string{
		"app|FULL|full|0|2026-11-02 22:00:00.000|",
		"app|FULL|diff|-|-|-|-|-|-|-|-|-|-|-",
		"app|FULL|log|0|2026-11-02 23:00:00.000|",
		"archive|FULL|full|-|-|",
	} {
		if !strings.HasPrefix(got[i], want) {
			t.Errorf("row %d %q, want prefix %q", i, got[i], want)
		}
	}
	if len(res.Sets[0].Columns) != 14 || res.Sets[0].Columns[1].Name != "recovery_model" {
		t.Errorf("columns %+v", res.Sets[0].Columns)
	}
	if len(res.Warnings) != 2 || !strings.Contains(res.Warnings[0], "no full backup of [archive]") ||
		!strings.Contains(res.Warnings[1], "no log backup of [model]") {
		t.Errorf("warnings %q", res.Warnings)
	}

	// One database, one type; --since drops an older backup to NULL.
	res, _, err = runMaint(t, newBackupCatalog(), &backupWriter{}, "backup-history APP --last --type full --since 2026-11-03")
	if err != nil {
		t.Fatal(err)
	}
	if got := historyCells(res); !slices.Equal(got, []string{"app|FULL|full|-|-|-|-|-|-|-|-|-|-|-"}) || res.Database != "app" {
		t.Errorf("one database: %q, header %q", got, res.Database)
	}
	if _, _, err = runMaint(t, newBackupCatalog(), &backupWriter{}, "backup-history app_snap --last"); err == nil ||
		err.(*output.Error).Code != output.CodeUsageError {
		t.Errorf("snapshot --last: %v", err)
	}
}

func TestMaintBackupHistoryUsage(t *testing.T) {
	for _, argv := range []string{
		"backup-history",
		"backup-history app --all",
		"backup-history --all --last --top 5",
		"backup-history app --top 0",
		"backup-history app --since yesterday",
	} {
		_, dials, err := runMaint(t, newBackupCatalog(), &backupWriter{}, argv)
		if e, ok := err.(*output.Error); !ok || e.Code != output.CodeUsageError || dials != 0 {
			t.Errorf("%s: %v (dials %d), want a usage error before connecting", argv, err, dials)
		}
	}
}
