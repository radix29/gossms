package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// The maint group (phase 8): maint backup (admin, as query run classes
// BACKUP, D1) and maint backup-history (read), D4; maint restore is in
// maint_restore.go, the index and statistics commands in maint_index.go,
// maint checkdb in maint_checkdb.go.

// The maint backup commands' own options; --to and --type are shared names
// (security grant's --to, object list's --type).
const (
	optCompression = "compression"
	optCopyOnly    = "copy-only"
	optChecksum    = "checksum"
	optInit        = "init"
	optCredential  = "credential"
	optLast        = "last"
)

// defaultBackupHistoryTop is maint backup-history --top's default.
const defaultBackupHistoryTop = 50

// backupStats is the STATS = n every maint backup sends: the percent
// messages reach stderr under --verbose. Set on every run, so a dry run's
// statement is the real one whatever --verbose is.
const backupStats = 10

// backupTypes is maint backup's --type enum, each with the action it sends,
// the extension of a file named for it, and the set type it writes.
var backupTypes = map[string]struct {
	action gosmo.BackupAction
	ext    string
	set    gosmo.BackupSetType
}{
	"full": {gosmo.BackupActionDatabase, ".bak", gosmo.BackupSetDatabase},
	"diff": {gosmo.BackupActionDifferential, ".bak", gosmo.BackupSetDifferential},
	"log":  {gosmo.BackupActionLog, ".trn", gosmo.BackupSetLog},
}

// backupTypeNames is --type's enum, in the order a backup chain is taken.
var backupTypeNames = []string{"full", "diff", "log"}

// backupNow is the clock a default backup file is named by; tests fix it.
var backupNow = time.Now

// MaintCommands is the maint group: its node and its commands. Its summary
// names only what exists.
func MaintCommands(d Deps) []*spec.Command {
	return append([]*spec.Command{
		{
			Path:    []string{"maint"},
			Summary: "Backups, restores, backup history, integrity checks, index and statistics upkeep",
			Class:   spec.ClassRead,
		},
		{
			Path:    []string{"maint", "backup"},
			Summary: "Back up a database: full, differential or log, to a file or a URL",
			Description: "BACKUP DATABASE or BACKUP LOG through gosmo, WITH STATS = 10 (the progress lines go to stderr " +
				"under --verbose). Needs --allow admin (query run classes BACKUP so too) and BACKUP DATABASE / BACKUP " +
				"LOG on the database (db_backupoperator, db_owner, sysadmin; without: exit 9). --to names the file " +
				"(a path on the server, written by the SQL Server service account) or an https URL; repeat it to " +
				"stripe the backup over several. Without --to the backup goes to the instance's default backup " +
				"directory as <db>_<type>_<UTC yyyymmdd_hhmmss>.bak (.trn for a log). --compression and " +
				"--no-compression force it on or off; neither is the server's default (backup compression default). " +
				"--init overwrites the backup sets the file holds instead of appending, so it needs --confirm with " +
				"the database's name (exit 7 without, before connecting). Refused before anything is sent: tempdb or " +
				"a snapshot (exit 2), a log backup of a database in SIMPLE recovery (exit 2), --compression on an " +
				"edition without it (Express, Web: exit 11), Azure SQL Database (exit 11). --dry-run prints the " +
				"statement; otherwise the record adds the new backup set read back from the file (RESTORE HEADERONLY): " +
				"its number on the media, start, finish, size and compressed size. A differential or log backup with " +
				"no full backup before it, or a file the service account cannot write, is the server's error, exit 5, " +
				"with a hint.",
			Args: []spec.Arg{{Name: "database", Help: "Database to back up", Required: true}},
			Options: slices.Concat(ConnectOptions(), writeOptions(), []spec.Option{
				{Name: optTo, Type: spec.TypeRepeat,
					Help: "Backup file on the server, or an https URL; repeat to stripe. Default: the instance's backup directory"},
				{Name: optType, Type: spec.TypeEnum, Enum: backupTypeNames, Default: "full", Help: "Backup type"},
				{Name: optCompression, Type: spec.TypeBool,
					Help: "Compress the backup (--no-compression: never); default the server's backup compression default"},
				{Name: optCopyOnly, Type: spec.TypeBool,
					Help: "COPY_ONLY: outside the backup sequence (no differential base, the log not truncated)"},
				{Name: optChecksum, Type: spec.TypeBool, Help: "WITH CHECKSUM: verify page checksums while backing up"},
				{Name: optInit, Type: spec.TypeBool, Help: "Overwrite the backup sets the file holds (needs --confirm)"},
				{Name: optConfirm, Type: spec.TypeString, Help: "The database's name again, exactly as given: accepts --init"},
				{Name: optCredential, Type: spec.TypeString,
					Help: "SQL Server credential for a backup to URL (storage-account-key form); not for a file"},
			}),
			Class: spec.ClassAdmin,
			Run:   d.maintBackup,
			Examples: []string{
				"gossms maint backup AdventureWorks -p dev --allow admin --dry-run",
				"gossms maint backup AdventureWorks --to /var/opt/mssql/backup/AW_full.bak --compression --checksum -p dev --allow admin",
				"gossms maint backup AdventureWorks --type log -p dev --allow admin",
			},
		},
		{
			Path:    []string{"maint", "backup-history"},
			Summary: "Read the backup history msdb records: one database's, or every database's last backups",
			Description: "With <database>: its backup sets from msdb, newest first — type, copy-only, start, finish, " +
				"size, the set's number on its media and the files it was written to (a striped set lists them all, " +
				"a RESTORE needs them all), and the LSN range a log chain is followed by. A database dropped since " +
				"still has its history. With --all: every database's, one msdb read per database. --last keeps the " +
				"newest full, differential and log backup of each database instead (one row per database and type, " +
				"NULL when msdb has none): a database never backed up is a warning, and so is one in FULL or " +
				"BULK_LOGGED recovery with no log backup (its log cannot be truncated). --type keeps one type, " +
				"--since the sets that started at or after the time, --top caps the rows (not with --last). Times are " +
				"the server's local time, as msdb records them. msdb holds what was recorded: a backup whose file " +
				"was since deleted or overwritten is still listed, and history purged (sp_delete_backuphistory) or " +
				"taken on another server is not.",
			Args: []spec.Arg{{Name: "database", Help: "Database whose history to read; or --all"}},
			Options: append(ConnectOptions(),
				spec.Option{Name: optAll, Type: spec.TypeBool, Help: "Every database instead of <database>"},
				spec.Option{Name: optLast, Type: spec.TypeBool, Help: "Only the newest backup of each type, per database"},
				spec.Option{Name: optType, Type: spec.TypeEnum, Enum: backupTypeNames, Help: "Only this backup type"},
				spec.Option{Name: optSince, Type: spec.TypeString, Help: "Only sets started since this time: " + agentTimeHelp},
				spec.Option{Name: optTop, Type: spec.TypeInt, Default: strconv.Itoa(defaultBackupHistoryTop),
					Help: "Keep this many sets, the newest first (not with --last)"},
			),
			Class: spec.ClassRead,
			Run:   d.maintBackupHistory,
			Examples: []string{
				"gossms maint backup-history --all --last -p prod-ro",
				"gossms maint backup-history AdventureWorks --type log --since 2026-10-27 -p prod-ro -o json",
			},
		},
		restoreCommand(d),
		checkDBCommand(d),
	}, maintIndexCommands(d)...)
}

// --- maint backup -------------------------------------------------------------

// backupRequest is maint backup's options, checked before connecting; its
// Devices are empty when --to was not given (the default path needs the
// server's).
func backupRequest(inv *spec.Invocation) (gosmo.BackupOptions, error) {
	db := inv.Args[0]
	if strings.TrimSpace(db) == "" {
		return gosmo.BackupOptions{}, usageError("the database name is empty", "")
	}
	typ := inv.Values[optType].(string)
	opts := gosmo.BackupOptions{
		Database: db,
		Action:   backupTypes[typ].action,
		CopyOnly: inv.Values[optCopyOnly].(bool),
		Checksum: inv.Values[optChecksum].(bool),
		Init:     inv.Values[optInit].(bool),
		Stats:    backupStats,
		Progress: func(pct int, msg string) { log.Printf("maint backup: %s", msg) },
	}
	if inv.Given[optCompression] {
		opts.Compression = new(inv.Values[optCompression].(bool))
	}
	to, _ := inv.Values[optTo].([]string)
	for _, path := range to {
		if strings.TrimSpace(path) == "" {
			return opts, usageError("--to: empty path", "Name the backup file, or leave --to out for the default directory.")
		}
		opts.Devices = append(opts.Devices, gosmo.DiskTarget(path))
	}
	if inv.Given[optCredential] {
		opts.Credential = inv.Values[optCredential].(string)
		if opts.Credential == "" {
			return opts, usageError("--credential: empty", "")
		}
		if len(opts.Devices) > 0 && !slices.ContainsFunc(opts.Devices, gosmo.BackupTarget.IsURL) {
			return opts, usageError("--credential is for a backup to URL, and every --to is a file",
				"A file is written as the SQL Server service account: leave --credential out.")
		}
	}
	if opts.Init {
		if confirm, _ := inv.Values[optConfirm].(string); confirm != db {
			msg := "--init overwrites every backup set the file holds"
			if inv.Given[optConfirm] {
				msg += fmt.Sprintf("; --confirm %q is not the database's name as given", confirm)
			}
			return opts, output.NewError(output.CodeBlocked, msg, "Add --confirm "+db+" to accept it, or leave --init out to append.")
		}
	} else if inv.Given[optConfirm] {
		return opts, usageError("--confirm is for --init", "Leave --confirm out: an appended backup overwrites nothing.")
	}
	return opts, nil
}

// backupFileName is the default file a backup of db of type typ is written
// to at t: <db>_<type>_<UTC yyyymmdd_hhmmss><ext>, the characters no file
// system takes made _.
func backupFileName(db, typ string, t time.Time) string {
	safe := strings.Map(func(r rune) rune {
		if r < ' ' || strings.ContainsRune(`\/:*?"<>|`, r) {
			return '_'
		}
		return r
	}, db)
	return fmt.Sprintf("%s_%s_%s%s", safe, typ, t.UTC().Format("20060102_150405"), backupTypes[typ].ext)
}

// SQL Server's refusals of a BACKUP that maint backup explains. A refused
// BACKUP ends with Msg 3013, after the message that says why: writeError
// reports that one.
const (
	msgBackupDeviceFailed = 3201 // Cannot open backup device.
	msgNoDiffBase         = 3035 // Cannot perform a differential backup … no current database backup.
	msgNoLogBase          = 4214 // BACKUP LOG cannot be performed because there is no current database backup.
	msgLogBackupSimple    = 4208 // BACKUP LOG is not allowed while the recovery model is SIMPLE.
	msgNoCompression      = 1844 // BACKUP DATABASE WITH COMPRESSION is not supported on <edition>.
	msgBackupInitExpired  = 4030 // The medium … contains sets that have not expired (with INIT).
	msgMediaFamilies      = 3231 // The media … is formatted to support n media families, but m are expected.
)

func (d Deps) maintBackup(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	opts, err := backupRequest(inv)
	if err != nil {
		return nil, err
	}
	typ := inv.Values[optType].(string)
	res := &output.Result{}
	sc, cat, err := d.connectGated(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	info := sc.Server.Info()
	if info != nil && info.EngineEdition == int(gosmo.EngineAzureSQLDatabase) {
		return res, output.NewError(output.CodeUnsupported, "Azure SQL Database takes no BACKUP: its backups are automatic",
			"Restore one through the Azure portal or CLI (point-in-time restore), or copy the database with CREATE DATABASE … AS COPY OF.")
	}
	db, err := namedDatabase(ctx, cat, opts.Database, res)
	if err != nil {
		return res, err
	}
	opts.Database = db.Name
	name := gosmo.QuoteName(db.Name)
	switch {
	case strings.EqualFold(db.Name, "tempdb"):
		return res, usageError("tempdb cannot be backed up", "It is rebuilt at every start: there is nothing in it to keep.")
	case db.IsSnapshot():
		return res, usageError(name+" is a database snapshot, which cannot be backed up",
			"Back up the database it is a snapshot of.")
	case typ == "log" && db.RecoveryModel == gosmo.RecoveryModelSimple:
		return res, usageError(name+" is in SIMPLE recovery, which keeps no log to back up",
			fmt.Sprintf("Take a full backup (--type full), or switch it first: db set %s --option RECOVERY=FULL, then a full backup starts the log chain.", db.Name))
	}
	if opts.Compression != nil && *opts.Compression && !info.SupportsBackupCompression() {
		return res, output.NewError(output.CodeUnsupported, fmt.Sprintf("this edition (%s) has no backup compression", info.Edition),
			"Leave --compression out.")
	}
	if len(opts.Devices) == 0 {
		dir := ""
		if info != nil {
			dir = info.DefaultBackupPath
		}
		opts.Devices = []gosmo.BackupTarget{gosmo.DiskTarget(gosmo.JoinServerPath(dir, backupFileName(db.Name, typ, backupNow())))}
	}

	res.Fields = backupFields(db.Name, typ, opts)
	backup := func(ctx context.Context) error { return sc.Server.Backup(ctx, opts) }
	if err := applyWrite(ctx, inv, res, backup); err != nil {
		return res, backupError(err, db.Name)
	}
	if dryRun(inv) {
		return res, nil
	}
	res.Fields = append(res.Fields, backupSetFields(ctx, sc.Server, opts, typ, res)...)
	return res, nil
}

// backupFields is maint backup's record of the backup it makes, or would.
func backupFields(db, typ string, opts gosmo.BackupOptions) []output.Field {
	to := make([]string, len(opts.Devices))
	for i, t := range opts.Devices {
		to[i] = t.String()
	}
	compression := "default"
	if opts.Compression != nil {
		compression = map[bool]string{true: "on", false: "off"}[*opts.Compression]
	}
	return []output.Field{
		{Key: "database", Value: db},
		{Key: "type", Value: typ},
		{Key: "to", Value: strings.Join(to, ", ")},
		{Key: "compression", Value: compression},
		{Key: "copyOnly", Value: boolText(opts.CopyOnly)},
		{Key: "checksum", Value: boolText(opts.Checksum)},
		{Key: "init", Value: boolText(opts.Init)},
	}
}

// backupSetFields is the set the backup just wrote, read back from its
// media: the newest of the database's sets of the type there. A read that
// fails, or finds none, is a warning — the backup itself succeeded.
func backupSetFields(ctx context.Context, srv *gosmo.Server, opts gosmo.BackupOptions, typ string, res *output.Result) []output.Field {
	keys := []string{"setNumber", "backupStart", "backupFinish", "sizeBytes", "compressedBytes", "compressed", "hasChecksums"}
	nulls := func() []output.Field {
		f := make([]output.Field, len(keys))
		for i, k := range keys {
			f[i] = output.Field{Key: k, Null: true}
		}
		return f
	}
	headers, err := srv.BackupHeaders(ctx, opts.Devices...)
	if err != nil {
		res.Warnings = append(res.Warnings, "The backup succeeded, but its set was not read back from the file: "+mediaReadError(err).Message)
		return nulls()
	}
	var h *gosmo.BackupHeader
	for _, x := range headers {
		if strings.EqualFold(x.DatabaseName, opts.Database) && x.SetType == backupTypes[typ].set &&
			(h == nil || x.Position > h.Position) {
			h = x
		}
	}
	if h == nil {
		res.Warnings = append(res.Warnings, "The backup succeeded, but the file holds no set of it to read back.")
		return nulls()
	}
	return []output.Field{
		{Key: "setNumber", Value: strconv.Itoa(cmp.Or(h.Position, 1))},
		{Key: "backupStart", Value: gridTime(h.BackupStart)},
		{Key: "backupFinish", Value: gridTime(h.BackupFinish)},
		{Key: "sizeBytes", Value: strconv.FormatInt(h.BackupSize, 10)},
		{Key: "compressedBytes", Value: strconv.FormatInt(h.CompressedSize, 10)},
		{Key: "compressed", Value: boolText(h.Compressed)},
		{Key: "hasChecksums", Value: boolText(h.HasChecksums)},
	}
}

// backupError is maint backup's failure, the server's refusals it can
// explain given a hint (writeError has already put the cause before Msg
// 3013).
func backupError(err error, db string) error {
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.SQL == nil {
		return err
	}
	if e.Code == output.CodePermissionDenied {
		e.Hint = "BACKUP DATABASE and BACKUP LOG need db_backupoperator or db_owner in the database, or sysadmin. " +
			"Use a login that has one, or have it granted."
		return e
	}
	switch e.SQL.Number {
	case msgLogBackupSimple:
		e.Hint = fmt.Sprintf("The database keeps no log in SIMPLE recovery. Switch it first (db set %s --option "+
			"RECOVERY=FULL), then a full backup starts the log chain.", db)
	case msgNoDiffBase, msgNoLogBase:
		e.Hint = fmt.Sprintf("The database has no full backup since its log chain began (or since it was created, "+
			"or switched to FULL recovery): take one first, maint backup %s --type full.", db)
	case msgNoCompression:
		e.Code = output.CodeUnsupported
		e.Hint = "This edition has no backup compression: leave --compression out."
	case msgBackupDeviceFailed:
		e.Hint = "The file is opened by the SQL Server service account, on the server: its directory must exist " +
			"there and the account must be able to write it. Leave --to out for the instance's default backup directory."
	case msgMediaFamilies:
		e.Hint = "The file belongs to a media set striped over another number of files: name every file of " +
			"that set with --to (maint backup-history lists them under devices), or write to a new file."
	case msgBackupInitExpired:
		e.Hint = "The file holds backup sets whose expiry date has not passed: --init does not overwrite them. Write to another file."
	}
	return e
}

// --- maint backup-history -----------------------------------------------------

// backupTypeName is a set type as maint backup-history prints it: --type's
// words for the three it takes.
func backupTypeName(t gosmo.BackupSetType) string {
	switch t {
	case gosmo.BackupSetDatabase:
		return "full"
	case gosmo.BackupSetDifferential:
		return "diff"
	case gosmo.BackupSetLog:
		return "log"
	case "":
		return "unknown"
	}
	return strings.ToLower(strings.ReplaceAll(string(t), " ", "_"))
}

// backupRow is one maint backup-history row: b in database db, nil for a
// type --last found none of.
type backupRow struct {
	db, recovery, typ string
	b                 *gosmo.BackupInfo
}

func (d Deps) maintBackupHistory(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	name, _, err := nameOrAll(inv) // name "" is --all
	if err != nil {
		return nil, err
	}
	last := inv.Values[optLast].(bool)
	top := inv.Values[optTop].(int)
	switch {
	case last && inv.Given[optTop]:
		return nil, usageError("--top is not for --last", "--last answers one row per database and type: leave --top out.")
	case top < 1:
		return nil, usageError(fmt.Sprintf("--top: %d is not a number of rows", top), "Give the number of sets to keep, 1 or more.")
	}
	since, err := readAgentTime(inv, optSince)
	if err != nil {
		return nil, err
	}
	typ, _ := inv.Values[optType].(string)
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	var from time.Time
	if inv.Given[optSince] {
		if from, err = agentTime(ctx, cat, since); err != nil {
			return res, err
		}
	}
	keep := func(b *gosmo.BackupInfo) bool {
		return (typ == "" || backupTypeName(b.SetType) == typ) && (from.IsZero() || !b.BackupStart.Before(from))
	}

	var rows []backupRow
	if last {
		rows, err = lastBackupRows(ctx, sc.Server, cat, name, typ, keep, res)
	} else {
		rows, err = backupHistoryRows(ctx, sc.Server, cat, name, keep, res)
	}
	if err != nil {
		return res, err
	}
	set := backupHistorySet(rows, last)
	if !last && len(set.Rows) > top {
		set.Rows, set.Truncated = set.Rows[:top], true
	}
	res.Sets = []output.Set{set}
	return res, nil
}

// backupHistoryRows is every set of name's history (every database's, name
// "") that keep keeps, newest first.
func backupHistoryRows(ctx context.Context, srv *gosmo.Server, cat Catalog, name string, keep func(*gosmo.BackupInfo) bool, res *output.Result) ([]backupRow, error) {
	names := []string{name}
	if name == "" {
		dbs, err := srv.Databases(ctx)
		if err != nil {
			return nil, readError(err)
		}
		names = names[:0]
		for _, d := range dbs {
			if !d.IsSnapshot() && !strings.EqualFold(d.Name, "tempdb") {
				names = append(names, d.Name)
			}
		}
	}
	var rows []backupRow
	for _, n := range names {
		hist, err := srv.BackupHistory(ctx, n)
		if err != nil {
			return nil, readError(err)
		}
		for _, b := range hist {
			if keep(b) {
				rows = append(rows, backupRow{db: cmp.Or(b.DatabaseName, n), typ: backupTypeName(b.SetType), b: b})
			}
		}
		if name != "" && len(hist) == 0 {
			// No history: say whether the database exists at all.
			if _, err := namedDatabase(ctx, cat, name, res); err != nil {
				return nil, err
			}
		}
	}
	slices.SortStableFunc(rows, func(a, b backupRow) int { return b.b.BackupStart.Compare(a.b.BackupStart) })
	return rows, nil
}

// lastBackupRows is each database's newest backup of each type (only typ's
// when given), from LastBackups: one row per database and type, b nil when
// msdb has none or keep drops it. The warnings name the databases never
// backed up, and those whose log no backup truncates.
func lastBackupRows(ctx context.Context, srv *gosmo.Server, cat Catalog, name, typ string, keep func(*gosmo.BackupInfo) bool, res *output.Result) ([]backupRow, error) {
	all, err := srv.LastBackups(ctx)
	if err != nil {
		return nil, readError(err)
	}
	if name != "" {
		i := slices.IndexFunc(all, func(b *gosmo.DatabaseLastBackups) bool { return strings.EqualFold(b.DatabaseName, name) })
		if i < 0 {
			if _, err := namedDatabase(ctx, cat, name, res); err != nil {
				return nil, err
			}
			return nil, usageError(gosmo.QuoteName(name)+" cannot be backed up (tempdb, or a database snapshot)",
				"Without --last, its history is what msdb recorded.")
		}
		all = all[i : i+1]
		res.Database = all[0].DatabaseName
	}
	var rows []backupRow
	var never, noLog []string
	for _, l := range all {
		if l.Full == nil {
			never = append(never, gosmo.QuoteName(l.DatabaseName))
		} else if l.Log == nil && l.RecoveryModel != gosmo.RecoveryModelSimple && l.RecoveryModel != "" {
			noLog = append(noLog, gosmo.QuoteName(l.DatabaseName))
		}
		for _, t := range backupTypeNames {
			if typ != "" && t != typ {
				continue
			}
			b := map[string]*gosmo.BackupInfo{"full": l.Full, "diff": l.Differential, "log": l.Log}[t]
			if b != nil && !keep(b) {
				b = nil
			}
			rows = append(rows, backupRow{db: l.DatabaseName, recovery: string(l.RecoveryModel), typ: t, b: b})
		}
	}
	if len(never) > 0 {
		res.Warnings = append(res.Warnings, "msdb holds no full backup of "+strings.Join(never, ", ")+
			": nothing to restore it from.")
	}
	if len(noLog) > 0 {
		res.Warnings = append(res.Warnings, "msdb holds no log backup of "+strings.Join(noLog, ", ")+
			", in FULL or BULK_LOGGED recovery: its log is not truncated until one is taken.")
	}
	return rows, nil
}

// backupHistorySet is maint backup-history's set; --last adds
// recovery_model, and a row with no backup is NULL past its type.
func backupHistorySet(rows []backupRow, last bool) output.Set {
	cols := []output.Column{{Name: "database", Type: "nvarchar(128)"}}
	if last {
		cols = append(cols, output.Column{Name: "recovery_model", Type: "nvarchar(60)"})
	}
	cols = append(cols,
		output.Column{Name: "type", Type: "nvarchar(30)"},
		output.Column{Name: "copy_only", Type: "bit", Nullable: true},
		output.Column{Name: "start", Type: "datetime", Nullable: true},
		output.Column{Name: "finish", Type: "datetime", Nullable: true},
		output.Column{Name: "duration_s", Type: "bigint", Nullable: true},
		output.Column{Name: "size_mb", Type: "decimal(19,2)", Nullable: true},
		output.Column{Name: "set_number", Type: "int", Nullable: true},
		output.Column{Name: "devices", Type: "nvarchar(max)", Nullable: true},
		output.Column{Name: "first_lsn", Type: "numeric(25,0)", Nullable: true},
		output.Column{Name: "last_lsn", Type: "numeric(25,0)", Nullable: true},
		output.Column{Name: "user", Type: "nvarchar(128)", Nullable: true},
		output.Column{Name: "server", Type: "nvarchar(128)", Nullable: true},
	)
	set := output.Set{Name: "backups", Columns: cols}
	for _, r := range rows {
		row := []*string{ptr(r.db)}
		if last {
			row = append(row, textOrNull(r.recovery))
		}
		row = append(row, ptr(r.typ))
		if b := r.b; b == nil {
			row = append(row, make([]*string, len(cols)-len(row))...)
		} else {
			var dur *string
			if !b.BackupStart.IsZero() && !b.BackupFinish.IsZero() {
				dur = secondsCell(b.BackupFinish.Sub(b.BackupStart))
			}
			row = append(row, bitCell(b.IsCopyOnly), cellTime(b.BackupStart), cellTime(b.BackupFinish), dur,
				ptr(mbf(float64(b.BackupSize)/(1<<20))), ptr(strconv.Itoa(cmp.Or(b.Position, 1))),
				textOrNull(strings.Join(b.Devices, ", ")), textOrNull(b.FirstLSN), textOrNull(b.LastLSN),
				textOrNull(b.UserName), textOrNull(b.ServerName))
		}
		set.Rows = append(set.Rows, row)
	}
	set.TotalRowCount = len(set.Rows)
	return set
}
