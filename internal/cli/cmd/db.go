package cmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// The db commands' options.
const (
	optIncludeSystem = "include-system"
	optState         = "state"
)

// databaseStates is --state's enum: sys.databases.state_desc, lower-cased.
var databaseStates = []gosmo.DatabaseState{
	gosmo.DatabaseOnline, gosmo.DatabaseOffline, gosmo.DatabaseRestoring, gosmo.DatabaseRecovering,
	gosmo.DatabaseRecoveryPending, gosmo.DatabaseSuspect, gosmo.DatabaseEmergency,
	gosmo.DatabaseCopying, gosmo.DatabaseOfflineSecondary,
}

func stateNames() []string {
	out := make([]string, len(databaseStates))
	for i, s := range databaseStates {
		out[i] = strings.ToLower(string(s))
	}
	return out
}

// DBCommands is the db group: its node and its read commands.
func DBCommands(d Deps) []*spec.Command {
	return []*spec.Command{
		{
			Path:    []string{"db"},
			Summary: "List databases and show one database's properties",
			Class:   spec.ClassRead,
		},
		{
			Path:    []string{"db", "list"},
			Summary: "List databases: state, size, recovery model, compatibility level, collation",
			Description: "One row per database, by name. System databases (master, model, msdb, tempdb and a " +
				"replication distribution database) are left out unless --include-system; snapshots are listed, " +
				"with snapshot_of naming their source. Sizes are the allocated file sizes in MB from " +
				"sys.master_files, so they answer for a database in any state (tempdb's are its startup sizes); " +
				"they are NULL for a database the login cannot see the files of.",
			Options: append(ConnectOptions(),
				spec.Option{Name: optIncludeSystem, Type: spec.TypeBool, Help: "Also list the system databases"},
				spec.Option{Name: optState, Type: spec.TypeEnum, Enum: stateNames(), Help: "Only databases in this state"},
			),
			Class: spec.ClassRead,
			Run:   d.dbList,
			Examples: []string{
				"gossms db list -p dev",
				"gossms db list -p dev --include-system -o json",
				"gossms db list -p dev --state offline",
			},
		},
		{
			Path:    []string{"db", "info"},
			Summary: "Show one database's properties: state, sizes, recovery, collation, owner and options",
			Description: "A record of the Database Properties dialog's facts: the sys.databases fields, the allocated " +
				"data and log sizes, the space used and free inside the files, and the ALTER DATABASE SET options. " +
				"A database that is not ONLINE still answers what the server catalog holds; what needs the database " +
				"itself (space used) is named under warnings instead.",
			Args:    []spec.Arg{{Name: "name", Help: "Database name", Required: true}},
			Options: ConnectOptions(),
			Class:   spec.ClassRead,
			Run:     d.dbInfo,
			Examples: []string{
				"gossms db info -p dev AdventureWorks",
				"gossms db info -p dev tempdb -o json",
			},
		},
	}
}

func boolText(b bool) string { return strconv.FormatBool(b) }

// bitCell is a bit column's cell.
func bitCell(b bool) *string {
	if b {
		return ptr("1")
	}
	return ptr("0")
}

// mb is size in KB as MB with two decimals, as every size column shows it.
func mb(kb int64) string { return strconv.FormatFloat(float64(kb)/1024, 'f', 2, 64) }

// mbf is an MB figure with two decimals.
func mbf(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// gridTime is a datetime as the grid shows it.
func gridTime(t time.Time) string { return t.Format("2006-01-02 15:04:05.000") }

func (d Deps) dbList(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	dbs, err := cat.Databases(ctx)
	if err != nil {
		return res, readError(err)
	}
	sizes, err := cat.DatabaseSizes(ctx)
	if err != nil {
		res.Warnings = append(res.Warnings, "Sizes were not read: "+err.Error())
	}
	names := make(map[int]string, len(dbs))
	for _, db := range dbs {
		names[db.ID] = db.Name
	}

	set := output.Set{Columns: []output.Column{
		{Name: "name", Type: "sysname"}, {Name: "database_id", Type: "int"}, {Name: "state", Type: "nvarchar(60)"},
		{Name: "recovery_model", Type: "nvarchar(60)"}, {Name: "compatibility_level", Type: "tinyint"},
		{Name: "collation", Type: "sysname", Nullable: true}, {Name: "read_only", Type: "bit"},
		{Name: "size_mb", Type: "decimal(18,2)", Nullable: true}, {Name: "data_mb", Type: "decimal(18,2)", Nullable: true},
		{Name: "log_mb", Type: "decimal(18,2)", Nullable: true}, {Name: "created", Type: "datetime"},
		{Name: "is_system", Type: "bit"}, {Name: "snapshot_of", Type: "sysname", Nullable: true},
	}}
	includeSystem := inv.Values[optIncludeSystem].(bool)
	state, _ := inv.Values[optState].(string)
	for _, db := range dbs {
		system := db.IsSystem() || db.IsDistributor
		if (system && !includeSystem) || (state != "" && !strings.EqualFold(string(db.State), state)) {
			continue
		}
		var size, data, log *string
		if s, ok := sizes[db.ID]; ok {
			size, data, log = ptr(mb(s.DataKB+s.LogKB)), ptr(mb(s.DataKB)), ptr(mb(s.LogKB))
		}
		var snapshotOf *string
		if db.IsSnapshot() {
			snapshotOf = ptr(cmpOr(names[db.SourceDatabaseID], strconv.Itoa(db.SourceDatabaseID)))
		}
		set.Rows = append(set.Rows, []*string{
			ptr(db.Name), ptr(strconv.Itoa(db.ID)), ptr(string(db.State)), ptr(string(db.RecoveryModel)),
			ptr(strconv.Itoa(int(db.CompatibilityLevel))), nonEmpty(db.Collation), bitCell(db.IsReadOnly),
			size, data, log, ptr(gridTime(db.CreateDate)), bitCell(system), snapshotOf,
		})
	}
	set.TotalRowCount = len(set.Rows)
	res.Sets = []output.Set{set}
	return res, nil
}

// nonEmpty is s as a cell, NULL when empty: a collation sys.databases reports
// NULL for a database that is not ONLINE.
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (d Deps) dbInfo(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	name := inv.Args[0]
	db, err := cat.DatabaseByName(ctx, name)
	if errors.Is(err, gosmo.ErrNotFound) {
		return res, output.NewError(output.CodeNotFound, fmt.Sprintf("no database %s on the server", gosmo.QuoteName(name)),
			"Run `gossms db list --include-system` for the databases the login can see.")
	}
	if err != nil {
		return res, readError(err)
	}
	res.Database = db.Name

	f := []output.Field{
		{Key: "name", Value: db.Name},
		{Key: "databaseId", Value: strconv.Itoa(db.ID)},
		{Key: "state", Value: string(db.State)},
		{Key: "recoveryModel", Value: string(db.RecoveryModel)},
		{Key: "compatibilityLevel", Value: strconv.Itoa(int(db.CompatibilityLevel))},
		{Key: "collation", Value: db.Collation},
		{Key: "catalogCollation", Value: db.CatalogCollation},
		{Key: "readOnly", Value: boolText(db.IsReadOnly)},
		{Key: "created", Value: gridTime(db.CreateDate)},
		{Key: "isSystem", Value: boolText(db.IsSystem() || db.IsDistributor)},
		{Key: "isDistributor", Value: boolText(db.IsDistributor)},
		{Key: "snapshotOf", Value: ""},
	}
	if db.IsSnapshot() {
		f[len(f)-1].Value = strconv.Itoa(db.SourceDatabaseID)
		if dbs, err := cat.Databases(ctx); err == nil {
			for _, src := range dbs {
				if src.ID == db.SourceDatabaseID {
					f[len(f)-1].Value = src.Name
				}
			}
		}
	}

	// Allocated sizes come from the server catalog and answer in any state.
	sizeFields := []output.Field{{Key: "sizeMb"}, {Key: "dataMb"}, {Key: "logMb"}}
	if sizes, err := cat.DatabaseSizes(ctx); err != nil {
		res.Warnings = append(res.Warnings, "Sizes were not read: "+err.Error())
	} else if s, ok := sizes[db.ID]; ok {
		sizeFields[0].Value, sizeFields[1].Value, sizeFields[2].Value = mb(s.DataKB+s.LogKB), mb(s.DataKB), mb(s.LogKB)
	} else {
		res.Warnings = append(res.Warnings, "Sizes were not read: the login cannot see the database's files (VIEW ANY DEFINITION).")
	}
	f = append(f, sizeFields...)

	// Space used is read inside the database, which needs it ONLINE (P3-7).
	spaceFields := []output.Field{{Key: "dataUnallocatedMb"}, {Key: "logAvailableMb"}}
	if db.State != gosmo.DatabaseOnline {
		res.Warnings = append(res.Warnings, fmt.Sprintf("Space used was not read: the database is %s.", db.State))
	} else if si, err := cat.SpaceUsed(ctx, db); err != nil {
		res.Warnings = append(res.Warnings, "Space used was not read: "+err.Error())
	} else {
		spaceFields[0].Value, spaceFields[1].Value = mbf(si.UnallocatedMB), mbf(si.AvailLogMB)
	}
	f = append(f, spaceFields...)

	optionFields := []string{"owner", "userAccess", "pageVerify", "containment", "snapshotIsolation",
		"readCommittedSnapshot", "autoClose", "autoShrink", "autoCreateStats", "autoUpdateStats",
		"autoUpdateStatsAsync", "parameterization", "isTrustworthy", "dbChaining", "isBrokerEnabled",
		"isEncrypted", "delayedDurability", "targetRecoveryTimeSeconds", "acceleratedDatabaseRecovery", "isLedger"}
	values := map[string]string{}
	if o, err := cat.DatabaseOptions(ctx, db); err != nil {
		res.Warnings = append(res.Warnings, "Options were not read: "+err.Error())
	} else {
		parameterization := "SIMPLE"
		if o.ForcedParameterization {
			parameterization = "FORCED"
		}
		target := ""
		if o.TargetRecoveryTimeSeconds >= 0 {
			target = strconv.Itoa(o.TargetRecoveryTimeSeconds)
		}
		values = map[string]string{
			"owner": o.Owner, "userAccess": string(o.UserAccess), "pageVerify": o.PageVerify,
			"containment": o.Containment, "snapshotIsolation": o.SnapshotIsolation,
			"readCommittedSnapshot": boolText(o.ReadCommittedSnapshot), "autoClose": boolText(o.AutoClose),
			"autoShrink": boolText(o.AutoShrink), "autoCreateStats": boolText(o.AutoCreateStats),
			"autoUpdateStats": boolText(o.AutoUpdateStats), "autoUpdateStatsAsync": boolText(o.AutoUpdateStatsAsync),
			"parameterization": parameterization, "isTrustworthy": boolText(o.IsTrustworthy),
			"dbChaining": boolText(o.DBChaining), "isBrokerEnabled": boolText(o.IsBrokerEnabled),
			"isEncrypted": boolText(o.IsEncrypted), "delayedDurability": o.DelayedDurability,
			"targetRecoveryTimeSeconds": target, "acceleratedDatabaseRecovery": boolText(o.AcceleratedDatabaseRecovery),
			"isLedger": boolText(o.IsLedger),
		}
	}
	for _, k := range optionFields {
		f = append(f, output.Field{Key: k, Value: values[k]})
	}
	res.Fields = f
	return res, nil
}
