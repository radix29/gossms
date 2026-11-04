package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// db create and db drop (phase 8, D9): both admin, as query run classes
// CREATE and DROP DATABASE (D1).

// db create's and db drop's own options.
const (
	optCollation        = "collation"
	optRecovery         = "recovery"
	optCompat           = "compat"
	optCloseConnections = "close-connections"
)

// recoveryModels is --recovery's enum, each with the model it sets.
var recoveryModels = map[string]gosmo.RecoveryModel{
	"simple":      gosmo.RecoveryModelSimple,
	"full":        gosmo.RecoveryModelFull,
	"bulk-logged": gosmo.RecoveryModelBulkLogged,
}

func dbCreateCommands(d Deps) []*spec.Command {
	nameArg := spec.Arg{Name: "name", Help: "Database name", Required: true}
	return []*spec.Command{
		{
			Path:    []string{"db", "create"},
			Summary: "Create a database, its files at the instance's default paths",
			Description: "CREATE DATABASE through gosmo, then ALTER DATABASE … SET RECOVERY and COMPATIBILITY_LEVEL " +
				"for the options given. Needs --allow admin (query run classes CREATE DATABASE so too) and CREATE " +
				"DATABASE or CREATE ANY DATABASE (without: exit 9). The files are the model database's, at the " +
				"instance's default data and log paths; the collation is the server's and the recovery model and " +
				"compatibility level model's unless given. A name already on the server is exit 2 before anything " +
				"runs; a compatibility level above the server's own is exit 2. --dry-run prints the statements and " +
				"what the database would be (model and the server read for the defaults); otherwise the record is the " +
				"database read back from sys.databases, a value other than the one asked for a warning. The " +
				"statements are not one transaction (CREATE DATABASE cannot run in one): when a SET after the CREATE " +
				"fails, the database stays and a warning says so.",
			Args: []spec.Arg{nameArg},
			Options: slices.Concat(ConnectOptions(), writeOptions(), []spec.Option{
				{Name: optCollation, Type: spec.TypeString, Help: "Collation, such as Latin1_General_100_CI_AS_SC_UTF8; default the server's"},
				{Name: optRecovery, Type: spec.TypeEnum, Enum: slices.Sorted(maps.Keys(recoveryModels)), Help: "Recovery model; default model's"},
				{Name: optCompat, Type: spec.TypeInt, Help: "Compatibility level, 100 up to the server's own in steps of 10; default model's"},
			}),
			Class: spec.ClassAdmin,
			Run:   d.dbCreate,
			Examples: []string{
				"gossms db create Scratch -p dev --allow admin --dry-run",
				"gossms db create Scratch --recovery simple --compat 150 -p dev --allow admin",
			},
		},
		{
			Path:    []string{"db", "drop"},
			Summary: "Drop a database, its files deleted",
			Description: "DROP DATABASE through gosmo, run from master. Needs --allow admin, --confirm with the " +
				"database's name exactly as given (exit 7 without, before connecting), and CONTROL on the database or " +
				"ALTER ANY DATABASE (without: exit 9). Refused with exit 7 before anything runs: a system database " +
				"(master, model, msdb, tempdb, a distribution database), a database in an availability group (remove " +
				"it from the group first), and a database with snapshots (drop them first; the message names them). " +
				"The connection's own database is exit 2: connect elsewhere (-d master). The record is the database " +
				"as it stands before the drop — state, recovery model, size, last full backup from msdb — with a " +
				"warning when msdb holds no full backup of it. Without --close-connections a database in use is exit " +
				"7, the hint naming it; --close-connections sets it SINGLE_USER WITH ROLLBACK IMMEDIATE first, in the " +
				"same batch, disconnecting its sessions and rolling back their open transactions. SQL Server deletes " +
				"the files of an ONLINE database only: an OFFLINE one leaves them on disk (a warning says so). " +
				"--dry-run prints the statements without running them; otherwise dropped says whether sys.databases " +
				"still holds the name afterwards.",
			Args: []spec.Arg{nameArg},
			Options: slices.Concat(ConnectOptions(), writeOptions(), []spec.Option{
				{Name: optConfirm, Type: spec.TypeString, Help: "The database's name again, exactly as given: accepts the drop"},
				{Name: optCloseConnections, Type: spec.TypeBool,
					Help: "Disconnect the database's sessions, rolling back their work, instead of failing while it is in use"},
			}),
			Class: spec.ClassAdmin,
			Run:   d.dbDrop,
			Examples: []string{
				"gossms db drop Scratch --confirm Scratch -p dev --allow admin --dry-run",
				"gossms db drop Scratch --confirm Scratch --close-connections -p dev --allow admin",
			},
		},
	}
}

// dbCreateRequest is db create's request from its options, checked before
// connecting: --compat a level gosmo knows the shape of (checked against the
// server once connected).
func dbCreateRequest(inv *spec.Invocation) (gosmo.CreateDatabaseRequest, error) {
	req := gosmo.CreateDatabaseRequest{Name: inv.Args[0]}
	if strings.TrimSpace(req.Name) == "" {
		return req, usageError("the database name is empty", "")
	}
	if inv.Given[optCollation] {
		req.Collation = inv.Values[optCollation].(string)
		if req.Collation == "" {
			return req, usageError("--collation: empty", "")
		}
	}
	if r, _ := inv.Values[optRecovery].(string); r != "" {
		req.RecoveryModel = recoveryModels[r]
	}
	if inv.Given[optCompat] {
		n := inv.Values[optCompat].(int)
		if n < minCompatLevel || n%10 != 0 {
			return req, usageError(fmt.Sprintf("--compat %d: a compatibility level is %d, %d, … in steps of 10", n, minCompatLevel, minCompatLevel+10), "")
		}
		req.CompatLevel = gosmo.CompatibilityLevel(n)
	}
	return req, nil
}

// dbCreateFields is the record of a database db create made, or would make
// (state "": null).
func dbCreateFields(name, state, recovery string, compat gosmo.CompatibilityLevel, collation string) []output.Field {
	return []output.Field{
		{Key: "name", Value: name},
		{Key: "state", Value: state, Null: state == ""},
		{Key: "recoveryModel", Value: recovery},
		{Key: "compatibilityLevel", Value: strconv.Itoa(int(compat))},
		{Key: "collation", Value: collation},
	}
}

func (d Deps) dbCreate(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	req, err := dbCreateRequest(inv)
	if err != nil {
		return nil, err
	}
	res := &output.Result{}
	sc, cat, w, err := d.connectWriter(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	// A level above the server's own (its major × 10); Azure and an
	// unread version are left to the server, as db set does.
	if info := cat.ServerInfo(); req.CompatLevel != 0 && info != nil && !info.IsAzure() &&
		info.VersionMajor >= int(gosmo.SQLServer2016) && int(req.CompatLevel) > info.VersionMajor*10 {
		return res, usageError(fmt.Sprintf("--compat %d is above this server's %d", req.CompatLevel, info.VersionMajor*10),
			fmt.Sprintf("Give a level from %d to %d.", minCompatLevel, info.VersionMajor*10))
	}
	switch taken, err := cat.DatabaseByName(ctx, req.Name); {
	case err == nil:
		return res, usageError(fmt.Sprintf("database %s already exists", gosmo.QuoteName(taken.Name)),
			"Choose another name, or drop it first with db drop.")
	case !errors.Is(err, gosmo.ErrNotFound):
		return res, readError(err)
	}

	create := func(ctx context.Context) error {
		err := w.CreateDatabase(ctx, req)
		if gosmo.IsPermissionDenied(err) {
			return output.NewError(output.CodePermissionDenied, err.Error(),
				"CREATE DATABASE needs CREATE DATABASE in master, CREATE ANY DATABASE or ALTER ANY DATABASE "+
					"(dbcreator has it). Use a login that has it, or have it granted.")
		}
		return err
	}
	if dryRun(inv) {
		// What the database would be: model's recovery and level, the
		// server's collation, unless given.
		model, err := cat.DatabaseByName(ctx, "model")
		if err != nil {
			return res, readError(err)
		}
		recovery, compat, collation := model.RecoveryModel, model.CompatibilityLevel, req.Collation
		if req.RecoveryModel != "" {
			recovery = req.RecoveryModel
		}
		if req.CompatLevel != 0 {
			compat = req.CompatLevel
		}
		if info := cat.ServerInfo(); collation == "" && info != nil {
			collation = info.Collation
		}
		res.Fields = dbCreateFields(req.Name, "", string(recovery), compat, collation)
		return res, applyWrite(ctx, inv, res, create)
	}

	werr := applyWrite(ctx, inv, res, create)
	made, err := cat.DatabaseByName(ctx, req.Name)
	if err != nil {
		if werr != nil {
			return res, werr
		}
		e := readError(err)
		e.Message = "the database was created, but could not be read back: " + e.Message
		return res, e
	}
	res.Database = made.Name
	res.Fields = slices.Concat(dbCreateFields(made.Name, string(made.State), string(made.RecoveryModel),
		made.CompatibilityLevel, made.Collation), res.Fields)
	if werr != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("The database %s was created and stays: the change after "+
			"the CREATE failed. Fix it with db set, or drop it with db drop.", gosmo.QuoteName(made.Name)))
		return res, werr
	}
	if req.RecoveryModel != "" && made.RecoveryModel != req.RecoveryModel {
		res.Warnings = append(res.Warnings, fmt.Sprintf("The recovery model is %s, not the %s requested.", made.RecoveryModel, req.RecoveryModel))
	}
	if req.CompatLevel != 0 && made.CompatibilityLevel != req.CompatLevel {
		res.Warnings = append(res.Warnings, fmt.Sprintf("The compatibility level is %d, not the %d requested.", made.CompatibilityLevel, req.CompatLevel))
	}
	if req.Collation != "" && !strings.EqualFold(made.Collation, req.Collation) {
		res.Warnings = append(res.Warnings, fmt.Sprintf("The collation is %s, not the %s requested.", made.Collation, req.Collation))
	}
	return res, nil
}

// dropConfirmed is db drop's --confirm check, before connecting: it must
// repeat the name exactly as given (§6.3), else BLOCKED.
func dropConfirmed(inv *spec.Invocation) error {
	name := inv.Args[0]
	confirm, _ := inv.Values[optConfirm].(string)
	if confirm == name {
		return nil
	}
	msg := "db drop deletes " + gosmo.QuoteName(name) + " and its files"
	if inv.Given[optConfirm] {
		msg += fmt.Sprintf("; --confirm %q is not the database's name as given", confirm)
	}
	return output.NewError(output.CodeBlocked, msg, "Add --confirm "+name+" to accept it.")
}

// SQL Server's refusals of a DROP DATABASE that db drop explains.
const (
	msgDatabaseInUse     = 3702 // Cannot drop database because it is currently in use.
	msgDatabaseSnapshots = 3709 // Cannot drop the database while it is being used for a snapshot.
	msgDatabaseInAG      = 3752 // The database is currently joined to an availability group.
)

// availabilityGroupOf is the availability group name belongs to, "" when
// none. Read only on an instance with HADR enabled.
func availabilityGroupOf(ctx context.Context, cat Catalog, name string) (string, error) {
	if info := cat.ServerInfo(); info == nil || !info.IsHADREnabled {
		return "", nil
	}
	ags, err := cat.AvailabilityGroups(ctx)
	if err != nil {
		return "", err
	}
	for _, ag := range ags {
		dbs, err := cat.AvailabilityGroupDatabases(ctx, ag)
		if err != nil {
			return "", err
		}
		if slices.ContainsFunc(dbs, func(d *gosmo.AvailabilityDatabase) bool { return strings.EqualFold(d.DatabaseName, name) }) {
			return ag.Name, nil
		}
	}
	return "", nil
}

func (d Deps) dbDrop(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	if err := dropConfirmed(inv); err != nil {
		return nil, err
	}
	closeConns := inv.Values[optCloseConnections].(bool)
	res := &output.Result{}
	sc, cat, w, err := d.connectWriter(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	db, err := namedDatabase(ctx, cat, inv.Args[0], res)
	if err != nil {
		return res, err
	}
	name := gosmo.QuoteName(db.Name)
	if db.IsSystem() {
		return res, output.NewError(output.CodeBlocked, name+" is a system database: db drop does not drop one", "")
	}
	if db.IsDistributor {
		return res, output.NewError(output.CodeBlocked, name+" is a replication distribution database: db drop does not drop one",
			"A distribution database is removed with sp_dropdistributiondb, once nothing publishes through it.")
	}
	current, err := cat.CurrentDatabase(ctx)
	if err != nil {
		return res, readError(err)
	}
	if strings.EqualFold(current, db.Name) {
		return res, usageError(name+" is the connection's own database",
			"Connect to another database to drop it: add -d master.")
	}
	ag, err := availabilityGroupOf(ctx, cat, db.Name)
	if err != nil {
		return res, readError(err)
	}
	if ag != "" {
		return res, output.NewError(output.CodeBlocked, fmt.Sprintf("%s is in availability group %s", name, gosmo.QuoteName(ag)),
			fmt.Sprintf("Remove it from the group first: ALTER AVAILABILITY GROUP %s REMOVE DATABASE %s; on the primary.",
				gosmo.QuoteName(ag), name))
	}
	if !db.IsSnapshot() {
		snaps, err := cat.SnapshotsOf(ctx, db.Name)
		if err != nil {
			return res, readError(err)
		}
		if len(snaps) > 0 {
			return res, snapshotsBlock(name, snaps)
		}
	}

	res.Fields = dropFields(ctx, cat, db, res)
	res.Fields = append(res.Fields, output.Field{Key: "closeConnections", Value: boolText(closeConns)})
	term := gosmo.TerminationNone
	if closeConns {
		term = gosmo.TerminationRollbackImmediate
	}
	drop := func(ctx context.Context) error {
		err := w.DropDatabase(ctx, db, term)
		if gosmo.IsMissingOrDenied(err) || gosmo.IsPermissionDenied(err) {
			return output.NewError(output.CodePermissionDenied, err.Error(),
				"DROP DATABASE needs CONTROL on the database (db_owner has it) or ALTER ANY DATABASE. "+
					"Use a login that has it, or have it granted.")
		}
		return err
	}
	if err := applyWrite(ctx, inv, res, drop); err != nil {
		return res, dropError(err, db.Name)
	}
	if dryRun(inv) {
		return res, nil
	}
	_, err = cat.DatabaseByName(ctx, db.Name)
	gone := errors.Is(err, gosmo.ErrNotFound)
	switch {
	case err != nil && !gone:
		res.Warnings = append(res.Warnings, "Whether the database is gone was not read back: "+err.Error())
		res.Fields = append(res.Fields, output.Field{Key: "dropped", Null: true})
	default:
		res.Fields = append(res.Fields, output.Field{Key: "dropped", Value: boolText(gone)})
		if !gone {
			res.Warnings = append(res.Warnings, name+" is still in sys.databases after the drop.")
		}
	}
	return res, nil
}

// dropFields is the record db drop prints of db before dropping it, with a
// warning in res for a database msdb holds no full backup of, and for one
// whose files the drop leaves behind.
func dropFields(ctx context.Context, cat Catalog, db *gosmo.Database, res *output.Result) []output.Field {
	name := gosmo.QuoteName(db.Name)
	f := []output.Field{
		{Key: "name", Value: db.Name},
		{Key: "databaseId", Value: strconv.Itoa(db.ID)},
		{Key: "state", Value: string(db.State)},
		{Key: "recoveryModel", Value: string(db.RecoveryModel)},
	}
	size := output.Field{Key: "sizeMb", Null: true}
	if sizes, err := cat.DatabaseSizes(ctx); err != nil {
		res.Warnings = append(res.Warnings, "The size was not read: "+err.Error())
	} else if s, ok := sizes[db.ID]; ok {
		size = output.Field{Key: "sizeMb", Value: mb(s.DataKB + s.LogKB)}
	}
	f = append(f, size)

	last := output.Field{Key: "lastFullBackup", Null: true}
	if db.IsSnapshot() {
		// A snapshot cannot be backed up, and LastBackups leaves it out.
		f = append(f, last)
	} else {
		backups, err := cat.LastBackups(ctx)
		i := slices.IndexFunc(backups, func(b *gosmo.DatabaseLastBackups) bool { return b.DatabaseName == db.Name })
		switch {
		case err != nil:
			res.Warnings = append(res.Warnings, "The last backup was not read from msdb: "+err.Error())
		case i >= 0 && backups[i].Full != nil:
			last.Null, last.Value = false, gridTime(backups[i].Full.BackupFinish)
		default:
			res.Warnings = append(res.Warnings, fmt.Sprintf("msdb holds no full backup of %s: once dropped, what it "+
				"holds cannot be restored.", name))
		}
		f = append(f, last)
	}
	if db.State != gosmo.DatabaseOnline && db.State != "" {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s is %s: SQL Server deletes the files of an ONLINE database "+
			"only, so its files stay on disk.", name, db.State))
	}
	return f
}

// snapshotsBlock is BLOCKED for a database with snapshots, naming them.
func snapshotsBlock(name string, snaps []*gosmo.DatabaseSnapshot) *output.Error {
	names := make([]string, len(snaps))
	for i, s := range snaps {
		names[i] = gosmo.QuoteName(s.Name)
	}
	return output.NewError(output.CodeBlocked,
		fmt.Sprintf("%s has database snapshots: %s", name, strings.Join(names, ", ")),
		"Drop the snapshots first, each with db drop <snapshot> --confirm <snapshot>.")
}

// dropError is db drop's failure, the server's refusals it can explain
// made BLOCKED with the way past them.
func dropError(err error, db string) error {
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.SQL == nil {
		return err
	}
	switch e.SQL.Number {
	case msgDatabaseInUse:
		e.Code = output.CodeBlocked
		e.Hint = fmt.Sprintf("Other sessions are in the database. --close-connections disconnects them, rolling back "+
			"their open transactions: db drop %s --confirm %s --close-connections.", db, db)
	case msgDatabaseSnapshots:
		e.Code = output.CodeBlocked
		e.Hint = "A snapshot of it was made since it was read: drop the snapshots first (db list shows them, under snapshot_of)."
	case msgDatabaseInAG:
		e.Code = output.CodeBlocked
		e.Hint = "Remove it from its availability group first (ALTER AVAILABILITY GROUP … REMOVE DATABASE, on the primary)."
	}
	return e
}
