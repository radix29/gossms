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

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// maint restore (phase 8 D5): one backup set per call, restored as a
// database or verified (RESTORE VERIFYONLY), admin as query run classes
// RESTORE (D1). Log and differential chains are the caller's to sequence,
// call by call with --norecovery: no PlanRestore here.

// maint restore's own options; --from, --confirm, --checksum and
// --close-connections are shared names.
const (
	optFileNumber    = "file-number"
	optWithMove      = "with-move"
	optMoveToDefault = "move-to-default"
	optReplace       = "replace"
	optNoRecovery    = "norecovery"
	optStandby       = "standby"
	optVerifyOnly    = "verify-only"
)

// restoreStats is the STATS = n every maint restore sends, as backupStats.
const restoreStats = 10

// systemDatabaseNames are the databases maint restore refuses as a target:
// restoring one is single-user-mode work (master), or replaces what the
// instance runs on.
var systemDatabaseNames = []string{"master", "msdb", "model", "tempdb"}

func restoreCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"maint", "restore"},
		Summary: "Restore a database from one backup set, or verify a backup (RESTORE VERIFYONLY)",
		Description: "RESTORE DATABASE (RESTORE LOG for a log backup) of one backup set, through gosmo, WITH STATS = 10 " +
			"(the progress lines go to stderr under --verbose). Needs --allow admin (query run classes RESTORE so " +
			"too, VERIFYONLY included). --from names the backup file on the server (repeat it for every file of a " +
			"striped set) or an https URL (a credential named for the container's URL, the shared access " +
			"signature form). The media is read first, in a dry run too: the set backup lists every set it holds, " +
			"and files the restored set's files with where each would go. A file holding several sets needs " +
			"--file-number (backup's set_number). The files go to the paths recorded in the backup unless " +
			"--move-to-default (every file into the instance's default data and log directories, renamed " +
			"<database>_<logical name> when restoring under another name: the way to restore a copy beside the " +
			"original) or --with-move <logical>=<path>, repeated. A planned path another database's file already " +
			"holds is refused before sending (exit 7). Restoring over a database that exists needs --confirm with " +
			"its name (exit 7 without, dry run included; not a differential or log restore onto a database left " +
			"RESTORING, which overwrites nothing), and --replace when the backup is of another database " +
			"or the tail of its log was not backed up; --close-connections disconnects its sessions first (rolling " +
			"back their transactions). --norecovery leaves the database RESTORING for the next differential or " +
			"log restore, --standby <undo file> read-only between them; the last restore of a chain leaves both " +
			"out. master, msdb, model and tempdb are refused (exit 7). --verify-only checks the set is complete " +
			"and readable (with --checksum, its checksums too) and restores nothing: <database> may be left out, " +
			"and nothing needs confirming.",
		Args: []spec.Arg{{Name: "database", Help: "Database to restore to (created when missing); not needed with --verify-only"}},
		Options: slices.Concat(ConnectOptions(), writeOptions(), []spec.Option{
			{Name: optFrom, Type: spec.TypeRepeat, Help: "Backup file on the server, or an https URL; repeat for every file of a striped set"},
			{Name: optFileNumber, Type: spec.TypeInt, Help: "The backup set's number on the media (backup's set_number); needed when it holds several"},
			{Name: optWithMove, Type: spec.TypeRepeat, Help: "<logical name>=<path>: put this file there; repeat per file"},
			{Name: optMoveToDefault, Type: spec.TypeBool, Help: "Put every file in the instance's default data and log directories"},
			{Name: optReplace, Type: spec.TypeBool,
				Help: "WITH REPLACE: overwrite a database of another name, or one whose log tail was not backed up"},
			{Name: optNoRecovery, Type: spec.TypeBool, Help: "Leave the database RESTORING, for a differential or log restore after it"},
			{Name: optStandby, Type: spec.TypeString, Help: "Undo file on the server: leave the database read-only between log restores"},
			{Name: optCloseConnections, Type: spec.TypeBool,
				Help: "Disconnect the database's other sessions first, rolling back their transactions"},
			{Name: optChecksum, Type: spec.TypeBool, Help: "WITH CHECKSUM: verify the backup's checksums (refused for a backup taken without)"},
			{Name: optVerifyOnly, Type: spec.TypeBool, Help: "RESTORE VERIFYONLY: check the set is complete and readable, restore nothing"},
			{Name: optConfirm, Type: spec.TypeString, Help: "The database's name again, exactly as given: accepts restoring over it"},
		}),
		Class: spec.ClassAdmin,
		Run:   d.maintRestore,
		Examples: []string{
			"gossms maint restore --from /var/opt/mssql/backup/AW_full.bak --verify-only --checksum -p dev --allow admin",
			"gossms maint restore AW_copy --from /var/opt/mssql/backup/AW_full.bak --move-to-default -p dev --allow admin --dry-run",
			"gossms maint restore AdventureWorks --from D:\\Backup\\AW.bak --file-number 2 --replace --close-connections --confirm AdventureWorks -p dev --allow admin",
		},
	}
}

// restoreRequest is maint restore's options, checked before connecting.
type restoreRequest struct {
	target     string // "" only with --verify-only
	devices    []gosmo.BackupTarget
	fileNumber int // 0: not given
	moves      []gosmo.RelocateFile
	toDefault  bool
	verifyOnly bool
	opts       gosmo.RestoreOptions // the RESTORE's own settings; FromHeader fills the set's
}

func parseRestore(inv *spec.Invocation) (*restoreRequest, error) {
	r := &restoreRequest{
		verifyOnly: inv.Values[optVerifyOnly].(bool),
		toDefault:  inv.Values[optMoveToDefault].(bool),
	}
	if len(inv.Args) > 0 {
		r.target = inv.Args[0]
	}
	from, _ := inv.Values[optFrom].([]string)
	if len(from) == 0 {
		return nil, usageError("--from is required", "Name the backup file on the server (maint backup-history lists them under devices).")
	}
	for _, f := range from {
		if strings.TrimSpace(f) == "" {
			return nil, usageError("--from: empty path", "Name the backup file on the server.")
		}
		r.devices = append(r.devices, gosmo.DiskTarget(f))
	}
	if inv.Given[optFileNumber] {
		if r.fileNumber = inv.Values[optFileNumber].(int); r.fileNumber < 1 {
			return nil, usageError(fmt.Sprintf("--file-number: %d is not a backup set number", r.fileNumber),
				"Sets are numbered from 1: maint restore --verify-only --from … lists them.")
		}
	}
	withMove, _ := inv.Values[optWithMove].([]string)
	for _, m := range withMove {
		logical, path, ok := strings.Cut(m, "=")
		if !ok || strings.TrimSpace(logical) == "" || strings.TrimSpace(path) == "" {
			return nil, usageError(fmt.Sprintf("--with-move %q is not <logical name>=<path>", m),
				"Give the file's logical name (the files set of a dry run lists them), =, and its new path on the server.")
		}
		if slices.ContainsFunc(r.moves, func(x gosmo.RelocateFile) bool { return strings.EqualFold(x.LogicalName, logical) }) {
			return nil, usageError(fmt.Sprintf("--with-move names the file %q twice", logical), "")
		}
		r.moves = append(r.moves, gosmo.RelocateFile{LogicalName: logical, PhysicalName: path})
	}
	noRecovery := inv.Values[optNoRecovery].(bool)
	standby, _ := inv.Values[optStandby].(string)
	switch {
	case len(r.moves) > 0 && r.toDefault:
		return nil, usageError("--with-move and --move-to-default both place the files", "Give one of them.")
	case noRecovery && inv.Given[optStandby]:
		return nil, usageError("--norecovery and --standby are two end states", "Give one of them, or neither to recover the database.")
	case inv.Given[optStandby] && strings.TrimSpace(standby) == "":
		return nil, usageError("--standby: empty path", "Name the undo file, on the server.")
	}
	if r.verifyOnly {
		for _, o := range []string{optWithMove, optMoveToDefault, optReplace, optNoRecovery, optStandby, optCloseConnections, optConfirm} {
			if inv.Given[o] {
				return nil, usageError("--"+o+" is not for --verify-only", "--verify-only restores nothing: leave --"+o+" out.")
			}
		}
		return r, nil
	}

	switch {
	case r.target == "":
		return nil, usageError("the database to restore to is missing", "Name it: maint restore <database> --from …, or add --verify-only.")
	case strings.TrimSpace(r.target) == "":
		return nil, usageError("the database name is empty", "")
	case slices.ContainsFunc(systemDatabaseNames, func(s string) bool { return strings.EqualFold(s, r.target) }):
		return nil, output.NewError(output.CodeBlocked, gosmo.QuoteName(r.target)+" is a system database: maint restore does not restore one",
			"Restoring master needs the instance in single-user mode, and msdb or model the services that use them stopped: a human's work, by hand.")
	}
	if confirm, _ := inv.Values[optConfirm].(string); inv.Given[optConfirm] && confirm != r.target {
		return nil, output.NewError(output.CodeBlocked, fmt.Sprintf("--confirm %q is not the database's name as given", confirm),
			"Add --confirm "+r.target+" to accept restoring over it.")
	}
	r.opts = gosmo.RestoreOptions{
		Database:                 r.target,
		Devices:                  r.devices,
		Replace:                  inv.Values[optReplace].(bool),
		Checksum:                 inv.Values[optChecksum].(bool),
		CloseExistingConnections: inv.Values[optCloseConnections].(bool),
		Stats:                    restoreStats,
		Progress:                 func(pct int, msg string) { log.Printf("maint restore: %s", msg) },
	}
	switch {
	case noRecovery:
		r.opts.Recovery = gosmo.RestoreWithNoRecovery
	case inv.Given[optStandby]:
		r.opts.Recovery, r.opts.StandByFile = gosmo.RestoreWithStandBy, standby
	}
	return r, nil
}

// SQL Server's refusals of a RESTORE that maint restore explains. A refused
// RESTORE ends with Msg 3013 after the message that says why, as BACKUP
// does: writeError reports that one.
const (
	msgRestoreOtherDatabase = 3154 // The backup set holds a backup of a database other than the existing database.
	msgRestoreTailLog       = 3159 // The tail of the log for the database has not been backed up.
	msgRestoreExclusive     = 3101 // Exclusive access could not be obtained because the database is in use.
	msgRestoreFilePath      = 5133 // Directory lookup for the file failed.
	msgRestoreFileCannot    = 3156 // File cannot be restored to <path>.
	msgRestoreFileInUse     = 1834 // The file cannot be overwritten. It is being used by database.
	msgRestoreFileClaimed   = 3176 // File is claimed by … (two files on one path).
	msgRestoreNewerVersion  = 3169 // The database was backed up on a server running a newer version.
	msgRestoreNotReady      = 3117 // The log or differential backup cannot be restored because no files are ready to rollforward.
	msgRestoreLogTooEarly   = 4326 // The log in this backup set terminates at LSN …, which is too early.
	msgRestoreLogTooLate    = 4305 // The log in this backup set begins at LSN …, which is too recent.
	msgRestoreFamilies      = 3132 // The media set has n media families but only m are provided.
	msgRestoreNotABackup    = 3241 // The media family on device is incorrectly formed.
	msgRestoreEmptyVolume   = 3254 // The volume on device is empty (a file too short to be a backup: live on 17).
	msgRestoreChecksum      = 3183 // RESTORE detected an error on page …
	msgRestoreNoChecksum    = 3187 // RESTORE WITH CHECKSUM cannot be specified because the backup set does not contain checksum information.
	msgRestoreBadChecksum   = 3189 // Damage to the backup set was detected.
)

func (d Deps) maintRestore(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	r, err := parseRestore(inv)
	if err != nil {
		return nil, err
	}
	res := &output.Result{}
	sc, cat, err := d.connectGated(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	info := sc.Server.Info()
	if info != nil && info.EngineEdition == int(gosmo.EngineAzureSQLDatabase) {
		return res, output.NewError(output.CodeUnsupported, "Azure SQL Database takes no RESTORE from a backup file",
			"Restore through the Azure portal or CLI (point-in-time restore), or import a .bacpac.")
	}
	headers, err := sc.Server.BackupHeaders(ctx, r.devices...)
	if err != nil {
		return res, restoreError(mediaReadError(err), r.target)
	}
	h, err := restoreSetOf(headers, r.fileNumber, r.devices)
	res.Sets = []output.Set{backupMediaSet(headers, h)}
	if err != nil {
		return res, err
	}
	if r.verifyOnly {
		return verifyBackup(ctx, inv, res, sc.Server, r, h)
	}
	if (len(r.moves) > 0 || r.toDefault) && !h.SetType.PlacesFiles() {
		return res, usageError(fmt.Sprintf("set %d is a %s backup, which places no files", cmp.Or(h.Position, 1), backupTypeName(h.SetType)),
			"Its files are the database's, placed by the full backup restored before it: leave --with-move and --move-to-default out.")
	}

	target, err := sc.Server.DatabaseByName(ctx, r.target)
	switch {
	case errors.Is(err, gosmo.ErrNotFound):
		target, err = nil, nil
	case err != nil:
		return res, readError(err)
	}
	res.Database = r.target
	if target != nil {
		if err := restoreOverChecks(ctx, inv, sc.Server, cat, target, h.SetType.PlacesFiles()); err != nil {
			return res, err
		}
		r.opts.Database = target.Name
	}

	var files []*gosmo.BackupFile
	if h.SetType.PlacesFiles() {
		if files, err = sc.Server.BackupFileList(ctx, h.SetNumber(), r.devices...); err != nil {
			return res, restoreError(mediaReadError(err), r.target)
		}
	}
	reloc := gosmo.RestoreRelocation{Mode: gosmo.RelocateNone}
	if r.toDefault {
		reloc = gosmo.RestoreRelocation{Mode: gosmo.RelocateToFolders}
		if info != nil {
			reloc.DefaultDataDir, reloc.DefaultLogDir, reloc.Collation = info.DefaultDataPath, info.DefaultLogPath, info.Collation
		}
	}
	r.opts.FromHeader(h, files, reloc)
	if len(r.moves) > 0 {
		if r.opts.RelocateFiles, err = explicitMoves(r.moves, files); err != nil {
			return res, err
		}
	}
	var held []*gosmo.InstanceFile
	if len(files) > 0 {
		if held, err = sc.Server.InstanceFiles(ctx); err != nil {
			return res, readError(err)
		}
	}
	planned := plannedPaths(files, r.opts.RelocateFiles, held, target)
	res.Sets = append(res.Sets, restoreFilesSet(files, planned, h.SetType.PlacesFiles()))
	if err := pathCollisions(r.opts.Database, files, planned, held); err != nil {
		return res, err
	}

	res.Fields = restoreFields(r, h, target != nil)
	restore := func(ctx context.Context) error { return sc.Server.Restore(ctx, r.opts) }
	if err := applyWrite(ctx, inv, res, restore); err != nil {
		return res, restoreError(err, r.target)
	}
	if dryRun(inv) {
		return res, nil
	}
	state := output.Field{Key: "state", Null: true}
	if after, err := sc.Server.DatabaseByName(ctx, r.opts.Database); err != nil {
		res.Warnings = append(res.Warnings, "The restore succeeded, but the database was not read back: "+readError(err).Message)
	} else {
		state = output.Field{Key: "state", Value: string(after.State)}
		res.Database = after.Name
	}
	res.Fields = append(res.Fields, state)
	return res, nil
}

// restoreSetOf is the set maint restore restores or verifies: --file-number's,
// or the media's only one. A media holding several needs --file-number:
// without it RESTORE reads set 1, the oldest, which is rarely the one meant.
func restoreSetOf(headers []*gosmo.BackupHeader, fileNumber int, devices []gosmo.BackupTarget) (*gosmo.BackupHeader, error) {
	where := targetsText(devices)
	switch {
	case len(headers) == 0:
		return nil, output.NewError(output.CodeNotFound, "no backup set on "+where, "")
	case fileNumber > 0:
		if h := gosmo.BackupSetAt(headers, setNumberOf(fileNumber)); h != nil {
			return h, nil
		}
		return nil, output.NewError(output.CodeNotFound, fmt.Sprintf("no backup set %d on %s: it holds %s", fileNumber, where, setNumbers(headers)),
			"Name one of them with --file-number (the set backup lists them).")
	case len(headers) > 1:
		return nil, usageError(fmt.Sprintf("%s holds %d backup sets", where, len(headers)),
			"Name the one to restore with --file-number (the set backup lists them; without it RESTORE reads set 1, the oldest).")
	}
	return headers[0], nil
}

// setNumberOf is BackupHeader.SetNumber's key for set n: 0 for the first.
func setNumberOf(n int) int {
	if n > 1 {
		return n
	}
	return 0
}

func setNumbers(headers []*gosmo.BackupHeader) string {
	n := make([]string, len(headers))
	for i, h := range headers {
		n[i] = strconv.Itoa(cmp.Or(h.Position, 1))
	}
	return "set " + strings.Join(n, ", ")
}

func targetsText(devices []gosmo.BackupTarget) string {
	n := make([]string, len(devices))
	for i, t := range devices {
		n[i] = t.String()
	}
	return strings.Join(n, ", ")
}

// restoreOverChecks are the refusals for restoring over target, a database
// that exists: --confirm (D3), the connection's own database, and one in
// an availability group. A differential or log set (places false) restored
// onto a database left RESTORING continues its chain and overwrites
// nothing, so it needs no --confirm.
func restoreOverChecks(ctx context.Context, inv *spec.Invocation, srv *gosmo.Server, cat Catalog, target *gosmo.Database, places bool) error {
	name := gosmo.QuoteName(target.Name)
	continues := !places && target.State == gosmo.DatabaseRestoring
	if confirm, _ := inv.Values[optConfirm].(string); confirm != inv.Args[0] && !continues {
		return output.NewError(output.CodeBlocked, name+" exists: the restore overwrites it",
			"Add --confirm "+inv.Args[0]+" to accept it, or restore under another name (with --move-to-default).")
	}
	if target.IsSnapshot() {
		return usageError(name+" is a database snapshot", "Drop it first, or restore under another name.")
	}
	current, err := srv.CurrentDatabase(ctx)
	if err != nil {
		return readError(err)
	}
	if strings.EqualFold(current, target.Name) {
		return usageError(name+" is the connection's own database", "Connect to another database to restore it: add -d master.")
	}
	ag, err := availabilityGroupOf(ctx, srv, cat.ServerInfo(), target.Name)
	if err != nil {
		return readError(err)
	}
	if ag != "" {
		return output.NewError(output.CodeBlocked, fmt.Sprintf("%s is in availability group %s", name, gosmo.QuoteName(ag)),
			fmt.Sprintf("Remove it from the group first: ALTER AVAILABILITY GROUP %s REMOVE DATABASE %s; on the primary.",
				gosmo.QuoteName(ag), name))
	}
	return nil
}

// explicitMoves is --with-move against the set's files: each logical name
// must be one of them (the catalog's spelling is sent).
func explicitMoves(moves []gosmo.RelocateFile, files []*gosmo.BackupFile) ([]gosmo.RelocateFile, error) {
	out := make([]gosmo.RelocateFile, len(moves))
	for i, m := range moves {
		j := slices.IndexFunc(files, func(f *gosmo.BackupFile) bool { return f.LogicalName == m.LogicalName })
		if j < 0 {
			j = slices.IndexFunc(files, func(f *gosmo.BackupFile) bool { return strings.EqualFold(f.LogicalName, m.LogicalName) })
		}
		if j < 0 {
			names := make([]string, len(files))
			for k, f := range files {
				names[k] = f.LogicalName
			}
			return nil, output.NewError(output.CodeNotFound, fmt.Sprintf("the backup set has no file %q", m.LogicalName),
				"Its files: "+strings.Join(names, ", ")+".")
		}
		out[i] = gosmo.RelocateFile{LogicalName: files[j].LogicalName, PhysicalName: m.PhysicalName}
	}
	return out, nil
}

// plannedPaths is where each file goes, by logical name: its MOVE; else,
// restoring over target, the path of target's file of that name (SQL Server
// overwrites an existing database's files where they are — live on 17,
// a REPLACE without MOVE kept the copy's own paths); else the path the
// backup recorded.
func plannedPaths(files []*gosmo.BackupFile, moves []gosmo.RelocateFile, held []*gosmo.InstanceFile, target *gosmo.Database) map[string]string {
	out := make(map[string]string, len(files))
	for _, f := range files {
		out[f.LogicalName] = f.PhysicalName
		if target == nil {
			continue
		}
		if i := slices.IndexFunc(held, func(h *gosmo.InstanceFile) bool {
			return h.DatabaseID == target.ID && strings.EqualFold(h.Name, f.LogicalName)
		}); i >= 0 {
			out[f.LogicalName] = held[i].PhysicalName
		}
	}
	for _, m := range moves {
		out[m.LogicalName] = m.PhysicalName
	}
	return out
}

// pathCollisions refuses a restore whose planned paths collide: two files
// on one path, or a path that held (InstanceFiles) gives to a database other
// than target (the server fails it with Msg 1834 or 3176). Paths
// are compared without case, as on Windows: on Linux this refuses a little
// more than the server would. Files the login cannot see (sys.master_files'
// metadata visibility) are not checked.
func pathCollisions(target string, files []*gosmo.BackupFile, planned map[string]string, held []*gosmo.InstanceFile) error {
	seen := map[string]string{}
	for _, f := range files {
		p := strings.ToLower(planned[f.LogicalName])
		if other, ok := seen[p]; ok {
			return usageError(fmt.Sprintf("the files %s and %s would both go to %s", other, f.LogicalName, planned[f.LogicalName]),
				"Give each its own path with --with-move.")
		}
		seen[p] = f.LogicalName
	}
	for _, h := range held {
		if strings.EqualFold(h.DatabaseName, target) {
			continue
		}
		if logical, ok := seen[strings.ToLower(h.PhysicalName)]; ok {
			return output.NewError(output.CodeBlocked,
				fmt.Sprintf("the file %s would go to %s, which is %s's file %s", logical, h.PhysicalName, gosmo.QuoteName(h.DatabaseName), h.Name),
				"Restore beside it under other paths: --move-to-default (renames the files for the new database), or --with-move "+logical+"=<path>.")
		}
	}
	return nil
}

// verifyBackup is maint restore --verify-only: RESTORE VERIFYONLY of set h.
func verifyBackup(ctx context.Context, inv *spec.Invocation, res *output.Result, srv *gosmo.Server, r *restoreRequest, h *gosmo.BackupHeader) (*output.Result, error) {
	checksum := inv.Values[optChecksum].(bool)
	res.Fields = []output.Field{
		{Key: "from", Value: targetsText(r.devices)},
		{Key: "setNumber", Value: strconv.Itoa(cmp.Or(h.Position, 1))},
		{Key: "backupType", Value: backupTypeName(h.SetType)},
		{Key: "sourceDatabase", Value: h.DatabaseName},
		{Key: "checksum", Value: boolText(checksum)},
	}
	if r.target != "" && !strings.EqualFold(r.target, h.DatabaseName) {
		res.Warnings = append(res.Warnings, fmt.Sprintf("The set is a backup of %s, not of %s.", gosmo.QuoteName(h.DatabaseName), gosmo.QuoteName(r.target)))
	}
	if checksum && !h.HasChecksums {
		return res, usageError(fmt.Sprintf("set %d was backed up without checksums", cmp.Or(h.Position, 1)),
			"Leave --checksum out: VERIFYONLY then checks the set is complete and readable.")
	}
	opts := gosmo.VerifyOptions{FileNumber: h.SetNumber(), Checksum: checksum}
	verify := func(ctx context.Context) error { return srv.VerifyBackup(ctx, opts, r.devices...) }
	if err := applyWrite(ctx, inv, res, verify); err != nil {
		return res, restoreError(err, r.target)
	}
	if !dryRun(inv) {
		res.Fields = append(res.Fields, output.Field{Key: "valid", Value: "true"})
	}
	return res, nil
}

// restoreFields is maint restore's record of the restore it makes, or would.
func restoreFields(r *restoreRequest, h *gosmo.BackupHeader, exists bool) []output.Field {
	recovery := "recovery"
	switch r.opts.Recovery {
	case gosmo.RestoreWithNoRecovery:
		recovery = "norecovery"
	case gosmo.RestoreWithStandBy:
		recovery = "standby"
	}
	return []output.Field{
		{Key: "database", Value: r.opts.Database},
		{Key: "from", Value: targetsText(r.devices)},
		{Key: "setNumber", Value: strconv.Itoa(cmp.Or(h.Position, 1))},
		{Key: "backupType", Value: backupTypeName(h.SetType)},
		{Key: "sourceDatabase", Value: h.DatabaseName},
		{Key: "backupFinish", Value: gridTime(h.BackupFinish)},
		{Key: "targetExists", Value: boolText(exists)},
		{Key: "recovery", Value: recovery},
		{Key: "replace", Value: boolText(r.opts.Replace)},
		{Key: "closeConnections", Value: boolText(r.opts.CloseExistingConnections)},
		{Key: "checksum", Value: boolText(r.opts.Checksum)},
	}
}

// backupMediaSet is the set backup: every set the media holds, the one
// restored (or verified) marked.
func backupMediaSet(headers []*gosmo.BackupHeader, chosen *gosmo.BackupHeader) output.Set {
	set := output.Set{Name: "backup", Columns: []output.Column{
		{Name: "set_number", Type: "int"},
		{Name: "selected", Type: "bit"},
		{Name: "database", Type: "nvarchar(128)"},
		{Name: "type", Type: "nvarchar(30)"},
		{Name: "copy_only", Type: "bit"},
		{Name: "start", Type: "datetime", Nullable: true},
		{Name: "finish", Type: "datetime", Nullable: true},
		{Name: "size_mb", Type: "decimal(19,2)"},
		{Name: "compressed", Type: "bit"},
		{Name: "checksums", Type: "bit"},
		{Name: "server", Type: "nvarchar(128)"},
		{Name: "version", Type: "int", Nullable: true},
	}}
	for _, h := range headers {
		var ver *string
		if h.SoftwareVersionMajor > 0 {
			ver = ptr(strconv.Itoa(h.SoftwareVersionMajor))
		}
		set.Rows = append(set.Rows, []*string{ptr(strconv.Itoa(cmp.Or(h.Position, 1))), bitCell(h == chosen),
			ptr(h.DatabaseName), ptr(backupTypeName(h.SetType)), bitCell(h.IsCopyOnly), cellTime(h.BackupStart),
			cellTime(h.BackupFinish), ptr(mbf(float64(h.BackupSize) / (1 << 20))), bitCell(h.Compressed),
			bitCell(h.HasChecksums), ptr(h.ServerName), ver})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// restoreFilesSet is the set files: the restored set's files and where
// each goes; target_path NULL for a differential or log set, restored onto
// the database's files where they are.
func restoreFilesSet(files []*gosmo.BackupFile, planned map[string]string, places bool) output.Set {
	set := output.Set{Name: "files", Columns: []output.Column{
		{Name: "logical_name", Type: "nvarchar(128)"},
		{Name: "type", Type: "nvarchar(10)"},
		{Name: "filegroup", Type: "nvarchar(128)", Nullable: true},
		{Name: "size_mb", Type: "decimal(19,2)"},
		{Name: "backup_path", Type: "nvarchar(260)"},
		{Name: "target_path", Type: "nvarchar(260)", Nullable: true},
	}}
	kinds := map[gosmo.BackupFileType]string{gosmo.BackupFileData: "data", gosmo.BackupFileLog: "log",
		gosmo.BackupFileFullText: "fulltext", gosmo.BackupFileStream: "filestream"}
	for _, f := range files {
		var to *string
		if places {
			to = ptr(planned[f.LogicalName])
		}
		set.Rows = append(set.Rows, []*string{ptr(f.LogicalName), ptr(cmp.Or(kinds[f.Type], string(f.Type))),
			textOrNull(f.FileGroupName), ptr(mbf(float64(f.Size) / (1 << 20))), ptr(f.PhysicalName), to})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// mediaReadError is readError for a read of backup media (RESTORE
// HEADERONLY, FILELISTONLY): its refusal ends with the contentless Msg 3013
// too, so the error reported, code included, is the cause's — writeError's
// rule for the writes.
func mediaReadError(err error) *output.Error {
	if se, ok := gosmo.AsSQLError(err); ok && isStatementFailed(se.Number) {
		return sqlError(alterCause(se))
	}
	return readError(err)
}

// restoreError is maint restore's failure, the server's refusals it can
// explain given a hint (writeError has already put the cause before Msg
// 3013).
func restoreError(err error, db string) error {
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.SQL == nil {
		return err
	}
	if e.Code == output.CodePermissionDenied {
		e.Hint = "RESTORE needs CREATE DATABASE for a new database (dbcreator has it), and for an existing one " +
			"db_owner of it, dbcreator or sysadmin; RESTORE VERIFYONLY and the media reads need the same. Use a login " +
			"that has one, or have it granted."
		return e
	}
	again := "maint restore " + cmp.Or(db, "<database>") + " --from …"
	switch e.SQL.Number {
	case msgRestoreOtherDatabase:
		e.Hint = "The database exists and the backup is of another one: add --replace to overwrite it, or restore " +
			"under a new name with --move-to-default."
	case msgRestoreTailLog:
		e.Hint = fmt.Sprintf("The database's log has changes no backup holds. Back them up first (maint backup %s "+
			"--type log), or add --replace to discard them.", db)
	case msgRestoreExclusive:
		e.Code = output.CodeBlocked
		e.Hint = "Other sessions are in the database. --close-connections disconnects them, rolling back their " +
			"open transactions: " + again + " --close-connections."
	case msgBackupDeviceFailed:
		e.Hint = "The file is opened by the SQL Server service account, on the server: the path must exist there " +
			"and the account must be able to read it (maint backup-history lists where backups went)."
	case msgRestoreFilePath, msgRestoreFileCannot:
		e.Hint = "A database file cannot be created where the backup recorded it (another server's directory?): " +
			"--move-to-default puts every file in this instance's default directories, --with-move one file anywhere."
	case msgRestoreFileInUse, msgRestoreFileClaimed:
		e.Code = output.CodeBlocked
		e.Hint = "A planned file path is another database's: restore under other paths with --move-to-default or --with-move."
	case msgRestoreNewerVersion:
		e.Code = output.CodeUnsupported
		e.Hint = "A backup restores only onto the same SQL Server version or a newer one (the set backup's version column)."
	case msgRestoreNotReady:
		e.Hint = "A differential or log backup is restored onto a database left RESTORING: restore its full backup " +
			"first with --norecovery, then this one."
	case msgRestoreLogTooEarly, msgRestoreLogTooLate:
		e.Hint = "Log backups are restored in order, each starting where the last restored one ended: " +
			"maint backup-history " + cmp.Or(db, "<database>") + " --type log lists their first_lsn and last_lsn."
	case msgRestoreFamilies, msgMediaFamilies:
		e.Hint = "The backup is striped over several files: name each with --from (maint backup-history lists them under devices)."
	case msgRestoreNotABackup, msgRestoreEmptyVolume:
		e.Hint = "The file is not a SQL Server backup, or is damaged or incomplete."
	case msgRestoreNoChecksum:
		e.Hint = "The backup was taken without checksums: leave --checksum out."
	case msgRestoreChecksum, msgRestoreBadChecksum:
		e.Hint = "The backup is damaged: take a new one, or restore another."
	}
	return e
}
