package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// db scoped-config's own option.
const optForSecondary = "for-secondary"

// scopedConfigText is what db scoped-config list and get share in their
// descriptions.
const scopedConfigText = "value and value_for_secondary are sys.database_scoped_configurations' own, as text: a " +
	"switch (type bit) reads 1 or 0, a number its digits, a keyword (type nvarchar) the keyword. " +
	"value_for_secondary is null where readable secondaries use the primary's value. is_default is null on " +
	"SQL Server 2016, whose view has no is_value_default. The database must be ONLINE (exit 8 otherwise) and " +
	"the login need a user in it (exit 9 otherwise)."

func dbScopedConfigCommands(d Deps) []*spec.Command {
	dbArg := spec.Arg{Name: "name", Help: "Database name", Required: true}
	return []*spec.Command{
		{
			Path:    []string{"db", "scoped-config"},
			Summary: "List, show and change a database's scoped configurations",
			Class:   spec.ClassRead,
		},
		{
			Path:        []string{"db", "scoped-config", "list"},
			Summary:     "List a database's scoped configurations: value, secondary value, default",
			Description: "One row per database scoped configuration, by name. " + scopedConfigText,
			Args:        []spec.Arg{dbArg},
			Options:     ConnectOptions(),
			Class:       spec.ClassRead,
			Run:         d.dbScopedConfigList,
			Examples: []string{
				"gossms db scoped-config list AdventureWorks -p dev",
			},
		},
		{
			Path:    []string{"db", "scoped-config", "get"},
			Summary: "Show a database's scoped configurations by name",
			Description: "The list's row for each configuration named, in the order given; a name ignores case, " +
				"and an unknown one is exit 8 naming up to 3 that contain it. " + scopedConfigText,
			Args: []spec.Arg{dbArg,
				{Name: "setting", Help: "A configuration's name, such as MAXDOP; more may follow", Required: true, Repeat: true}},
			Options: ConnectOptions(),
			Class:   spec.ClassRead,
			Run:     d.dbScopedConfigGet,
			Examples: []string{
				"gossms db scoped-config get AdventureWorks MAXDOP -p dev",
				"gossms db scoped-config get AdventureWorks LEGACY_CARDINALITY_ESTIMATION PARAMETER_SNIFFING -p dev -o json",
			},
		},
		{
			Path:    []string{"db", "scoped-config", "set"},
			Summary: "Change one database scoped configuration, for the primary or readable secondaries",
			Description: "ALTER DATABASE SCOPED CONFIGURATION, run inside the database. Needs --allow admin, and " +
				"ALTER ANY DATABASE SCOPED CONFIGURATION in the database (without it: exit 9). The name is as for " +
				"get. The value is checked against the configuration's type before anything runs (exit 2): a " +
				"switch takes ON or OFF (1 and 0 are sent as ON and OFF: the statement refuses a number there), a " +
				"number a whole number of 0 or more, a keyword a word of letters, digits and _; a quoted value " +
				"(a URL) is not supported here. --for-secondary sets the value readable secondaries use (FOR " +
				"SECONDARY), and PRIMARY, only there, makes them follow the primary again; the server accepts it " +
				"for a database in no availability group too, and refuses it for a configuration that has no " +
				"secondary value. --dry-run prints the statement and a planned_change set (name, replica, current, " +
				"new) without running it. Otherwise the change set holds before, requested and after, from the " +
				"view read before and after. Both sets spell values as the statement does: ON or OFF for a switch, " +
				"PRIMARY for a secondary value that follows the primary.",
			Args: []spec.Arg{dbArg,
				{Name: "setting", Help: "The configuration's name, such as MAXDOP", Required: true},
				{Name: "value", Help: "Its new value: ON, OFF, a number or a keyword (PRIMARY with --for-secondary)", Required: true}},
			Options: slices.Concat(ConnectOptions(), writeOptions(), []spec.Option{
				{Name: optForSecondary, Type: spec.TypeBool, Help: "Set the value readable secondary replicas use (FOR SECONDARY)"},
			}),
			Class: spec.ClassAdmin,
			Run:   d.dbScopedConfigSet,
			Examples: []string{
				"gossms db scoped-config set AdventureWorks MAXDOP 4 -p dev --allow admin --dry-run",
				"gossms db scoped-config set AdventureWorks MAXDOP 2 --for-secondary -p dev --allow admin",
				"gossms db scoped-config set AdventureWorks LEGACY_CARDINALITY_ESTIMATION ON -p dev --allow admin",
			},
		},
	}
}

// scopedConfigs is the database name names and its scoped configurations,
// by name. The database must be readable: the db table-sizes rules (P5-9).
func scopedConfigs(ctx context.Context, cat Catalog, name string, res *output.Result) (*gosmo.Database, []*gosmo.DatabaseScopedConfig, error) {
	t, err := storageDatabase(ctx, cat, name, res)
	if err == nil && t.Unreadable != "" {
		err = t.unreadableError()
	}
	if err != nil {
		return nil, nil, err
	}
	cfgs, err := cat.DatabaseScopedConfigs(ctx, t.DB)
	if err != nil {
		return nil, nil, readError(err)
	}
	cfgs = slices.Clone(cfgs)
	slices.SortFunc(cfgs, func(a, b *gosmo.DatabaseScopedConfig) int {
		return strings.Compare(strings.ToUpper(a.Name), strings.ToUpper(b.Name))
	})
	return t.DB, cfgs, nil
}

// --- db scoped-config list/get ----------------------------------------------

func (d Deps) dbScopedConfigList(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	_, cfgs, err := scopedConfigs(ctx, cat, inv.Args[0], res)
	if err != nil {
		return res, err
	}
	res.Sets = []output.Set{scopedConfigSet(cfgs, scopedDefaultKnown(cat.ServerInfo()))}
	return res, nil
}

func (d Deps) dbScopedConfigGet(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	db, cfgs, err := scopedConfigs(ctx, cat, inv.Args[0], res)
	if err != nil {
		return res, err
	}
	var rows []*gosmo.DatabaseScopedConfig
	for _, name := range inv.Args[1:] {
		c, err := resolveScopedConfig(db.Name, cfgs, name)
		if err != nil {
			return res, err
		}
		if !slices.Contains(rows, c) {
			rows = append(rows, c)
		}
	}
	res.Sets = []output.Set{scopedConfigSet(rows, scopedDefaultKnown(cat.ServerInfo()))}
	return res, nil
}

// scopedDefaultKnown reports whether is_value_default was read: it was added
// in SQL Server 2017, and gosmo reads false for every row on 2016. An unread
// version or an Azure one counts as newest, as gosmo's gates count it.
func scopedDefaultKnown(info *gosmo.ServerInfo) bool {
	return info == nil || info.IsAzure() || info.VersionMajor == 0 || info.VersionMajor >= int(gosmo.SQLServer2017)
}

// scopedConfigSet is rows as list's and get's result set.
func scopedConfigSet(rows []*gosmo.DatabaseScopedConfig, defaultKnown bool) output.Set {
	set := output.Set{Columns: []output.Column{
		{Name: "name", Type: "nvarchar(60)"},
		{Name: "value", Type: "nvarchar(256)", Nullable: true},
		{Name: "value_for_secondary", Type: "nvarchar(256)", Nullable: true},
		{Name: "is_default", Type: "bit", Nullable: true},
		{Name: "type", Type: "nvarchar(128)", Nullable: true},
	}}
	for _, c := range rows {
		var isDefault *string
		if defaultKnown {
			isDefault = bitCell(c.IsValueDefault)
		}
		set.Rows = append(set.Rows, []*string{ptr(c.Name), ptr(c.Value), nonEmpty(c.ValueForSecondary), isDefault, nonEmpty(c.ValueType)})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// resolveScopedConfig is the configuration in cfgs that name means, ignoring
// case. Anything else is NOT_FOUND naming up to maxConfigMatches that
// contain it.
func resolveScopedConfig(database string, cfgs []*gosmo.DatabaseScopedConfig, name string) (*gosmo.DatabaseScopedConfig, error) {
	var matches []string
	for _, c := range cfgs {
		if strings.EqualFold(c.Name, name) {
			return c, nil
		}
		if len(matches) < maxConfigMatches && strings.Contains(strings.ToUpper(c.Name), strings.ToUpper(name)) {
			matches = append(matches, c.Name)
		}
	}
	hint := fmt.Sprintf("Run `gossms db scoped-config list %s` for the names this server has.", database)
	if len(matches) > 0 {
		hint = "Did you mean " + strings.Join(matches, ", ") + "?"
	}
	return nil, output.NewError(output.CodeNotFound,
		fmt.Sprintf("no database scoped configuration %q in %s", name, gosmo.QuoteName(database)), hint)
}

// --- db scoped-config set ---------------------------------------------------

// scopedPrimary is the SET value that makes readable secondaries follow the
// primary; value_for_secondary reads NULL then.
const scopedPrimary = "PRIMARY"

// scopedKind is how a configuration's value is spelled, by its sql_variant
// base type: a switch, a number, or a keyword (anything else, including a
// NULL value with no type).
type scopedKind int

const (
	scopedKeyword scopedKind = iota
	scopedSwitch
	scopedNumber
)

func scopedKindOf(baseType string) scopedKind {
	switch baseType {
	case "bit":
		return scopedSwitch
	case "tinyint", "smallint", "int", "bigint":
		return scopedNumber
	}
	return scopedKeyword
}

// scopedValue is value as the SET clause takes it for c, or USAGE_ERROR. A
// switch is ON or OFF — the statement refuses 1 and 0 (Msg 102, found live
// on 13 and 17), so they are sent as ON and OFF. PRIMARY is a secondary's
// value only: the server refuses it for the primary (Msg 12109).
func scopedValue(c *gosmo.DatabaseScopedConfig, value string, secondary bool) (string, error) {
	v := strings.ToUpper(value)
	if v == scopedPrimary {
		if !secondary {
			return "", usageError(fmt.Sprintf("%s: PRIMARY is a value for readable secondaries only, making them use the primary's", c.Name),
				"Add --for-secondary, or give the primary a value of its own.")
		}
		return v, nil
	}
	switch scopedKindOf(c.ValueType) {
	case scopedSwitch:
		switch v {
		case "ON", "1":
			return "ON", nil
		case "OFF", "0":
			return "OFF", nil
		}
		return "", usageError(fmt.Sprintf("%s is a switch: %q is not ON or OFF", c.Name, value), "")
	case scopedNumber:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 {
			return "", usageError(fmt.Sprintf("%s is a number: %q is not a whole number of 0 or more", c.Name, value), "")
		}
		return strconv.FormatInt(n, 10), nil
	}
	if !scopedWord(v) {
		return "", usageError(fmt.Sprintf("%s takes a keyword: %q is not a word of letters, digits and _", c.Name, value),
			"A quoted value (a URL) is not supported here: run the ALTER DATABASE SCOPED CONFIGURATION with "+
				"`gossms query run --allow admin`.")
	}
	return v, nil
}

// scopedWord reports whether s is a keyword the SET clause takes bare: ASCII
// letters, digits and _, not starting with a digit.
func scopedWord(s string) bool {
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return s != ""
}

// scopedSetForm is c's value for the replica as the SET clause spells it:
// ON or OFF for a switch, PRIMARY for a secondary value that follows the
// primary (NULL in the view).
func scopedSetForm(c *gosmo.DatabaseScopedConfig, secondary bool) string {
	raw, typ := c.Value, c.ValueType
	if secondary {
		if c.ValueForSecondary == "" {
			return scopedPrimary
		}
		raw, typ = c.ValueForSecondary, c.ValueForSecondaryType
	}
	if scopedKindOf(typ) == scopedSwitch {
		switch raw {
		case "1":
			return "ON"
		case "0":
			return "OFF"
		}
	}
	return raw
}

// replicaName is the replica column's value.
func replicaName(secondary bool) string {
	if secondary {
		return "secondary"
	}
	return "primary"
}

func (d Deps) dbScopedConfigSet(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	secondary := inv.Values[optForSecondary].(bool)
	res := &output.Result{}
	sc, cat, w, err := d.connectWriter(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	db, cfgs, err := scopedConfigs(ctx, cat, inv.Args[0], res)
	if err != nil {
		return res, err
	}
	c, err := resolveScopedConfig(db.Name, cfgs, inv.Args[1])
	if err != nil {
		return res, err
	}
	value, err := scopedValue(c, inv.Args[2], secondary)
	if err != nil {
		return res, err
	}
	replica := gosmo.ScopedConfigPrimary
	if secondary {
		replica = gosmo.ScopedConfigSecondary
	}
	// The server's refusal (Msg 15247, live on 17 and 13) reads "missing or
	// denied"; the configuration was just read, so it is denied.
	apply := func(ctx context.Context) error {
		err := w.SetDatabaseScopedConfig(ctx, db, c.Name, value, replica)
		if gosmo.IsMissingOrDenied(err) {
			return output.NewError(output.CodePermissionDenied, err.Error(), "")
		}
		return err
	}
	columns := func(cols ...string) []output.Column {
		out := []output.Column{{Name: "name", Type: "nvarchar(60)"}, {Name: "replica", Type: "varchar(9)"}}
		for _, col := range cols {
			out = append(out, output.Column{Name: col, Type: "nvarchar(256)", Nullable: col == "after"})
		}
		return out
	}
	if dryRun(inv) {
		res.Sets = []output.Set{{Name: "planned_change", Columns: columns("current", "new"), TotalRowCount: 1,
			Rows: [][]*string{{ptr(c.Name), ptr(replicaName(secondary)), ptr(scopedSetForm(c, secondary)), ptr(value)}}}}
		return res, applyWrite(ctx, inv, res, apply)
	}

	// One statement: a failure changed nothing, so there is nothing to read
	// back.
	if err := applyWrite(ctx, inv, res, apply); err != nil {
		if e, ok := errors.AsType[*output.Error](err); ok {
			switch {
			case e.Code == output.CodePermissionDenied:
				e.Hint = "ALTER DATABASE SCOPED CONFIGURATION needs ALTER ANY DATABASE SCOPED CONFIGURATION in the " +
					"database (db_owner has it). Use a login that has it, or have it granted."
			case e.SQL != nil && e.SQL.Number == 12110:
				e.Hint = c.Name + " has no secondary value: set it without --for-secondary."
			case e.SQL != nil && e.SQL.Number == 3906:
				// Found live on ubusql2, an availability group's readable
				// secondary.
				e.Hint = "The database is read-only. On a readable secondary, run the change on the primary: " +
					"--for-secondary there sets what the secondaries use."
			}
		}
		return res, err
	}
	after, err := cat.DatabaseScopedConfigs(ctx, db)
	if err != nil {
		e := readError(err)
		e.Message = "the change ran, but sys.database_scoped_configurations could not be read back: " + e.Message
		return res, e
	}
	row := []*string{ptr(c.Name), ptr(replicaName(secondary)), ptr(scopedSetForm(c, secondary)), ptr(value), nil}
	if i := slices.IndexFunc(after, func(a *gosmo.DatabaseScopedConfig) bool { return a.Name == c.Name }); i < 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s is gone from sys.database_scoped_configurations after the change.", c.Name))
	} else {
		now := scopedSetForm(after[i], secondary)
		row[4] = ptr(now)
		if !strings.EqualFold(now, value) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s is %s for the %s, not the %s requested.", c.Name, now, replicaName(secondary), value))
		}
	}
	res.Sets = []output.Set{{Name: "change", Columns: columns("before", "requested", "after"), TotalRowCount: 1, Rows: [][]*string{row}}}
	return res, nil
}

// --- db set -----------------------------------------------------------------

// db set's own options.
const (
	optOption            = "option"
	optRollbackImmediate = "rollback-immediate"
	optConfirm           = "confirm"
)

func dbSetCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"db", "set"},
		Summary: "Change database options: recovery, compatibility level, read-only, Query Store, ALTER DATABASE SET switches",
		Description: "ALTER DATABASE … SET, one statement per --option KEY=VALUE, in the order given. Needs " +
			"--allow admin, and ALTER on the database (without it: exit 9). Keys and values ignore case. " +
			"RECOVERY takes FULL, SIMPLE or BULK_LOGGED; COMPATIBILITY_LEVEL a level from 100 up to the server's " +
			"own, in steps of 10; READ_ONLY ON or OFF; QUERY_STORE ON (as READ_WRITE), READ_WRITE, READ_ONLY or " +
			"OFF, keeping every other Query Store option as it is; PAGE_VERIFY CHECKSUM, TORN_PAGE_DETECTION or " +
			"NONE; CURSOR_DEFAULT LOCAL or GLOBAL; CONTAINMENT NONE or PARTIAL; every other key ON or OFF. An " +
			"unknown key or a value the key does not take is exit 2 before anything runs. The statements run " +
			"one by one, outside any transaction (ALTER DATABASE cannot run in one): the first failure stops " +
			"the run, the ones before it stay applied, and the change set shows them (exit 5). " +
			"READ_COMMITTED_SNAPSHOT, CONTAINMENT and READ_ONLY need the database to themselves: they wait for " +
			"its other sessions to leave, up to --timeout (exit 6). --rollback-immediate disconnects those " +
			"sessions instead, rolling back their open transactions, and needs --confirm with the database's " +
			"name as given (exit 7 without). --dry-run prints the statements and a planned_change set (option, " +
			"current, new) without running them; otherwise the change set holds before, requested and after, " +
			"from sys.databases (and sys.database_query_store_options) read before and after. USER_ACCESS, " +
			"OFFLINE and ONLINE are not offered.",
		Args: []spec.Arg{{Name: "name", Help: "Database name", Required: true}},
		Options: slices.Concat(ConnectOptions(), writeOptions(), []spec.Option{
			{Name: optOption, Type: spec.TypeRepeat, Help: "KEY=VALUE, such as RECOVERY=FULL; repeat for more, applied in order"},
			{Name: optTimeout, Type: spec.TypeInt, Default: strconv.Itoa(defaultTimeout),
				Help: "Seconds the changes may take in all (an option needing the database to itself waits for its other sessions); 0 is no limit"},
			{Name: optRollbackImmediate, Type: spec.TypeBool,
				Help: "Disconnect the database's other sessions, rolling back their work, for READ_COMMITTED_SNAPSHOT, CONTAINMENT and READ_ONLY"},
			{Name: optConfirm, Type: spec.TypeString, Help: "The database's name, as given: accepts --rollback-immediate"},
		}),
		Class: spec.ClassAdmin,
		Run:   d.dbSet,
		Examples: []string{
			"gossms db set AdventureWorks --option COMPATIBILITY_LEVEL=160 -p dev --allow admin --dry-run",
			"gossms db set AdventureWorks --option RECOVERY=FULL -p dev --allow admin",
			"gossms db set AdventureWorks --option QUERY_STORE=ON -p dev --allow admin",
			"gossms db set AdventureWorks --option AUTO_UPDATE_STATISTICS_ASYNC=ON --option PAGE_VERIFY=CHECKSUM -p dev --allow admin",
			"gossms db set AdventureWorks --option READ_COMMITTED_SNAPSHOT=ON --rollback-immediate --confirm AdventureWorks -p dev --allow admin",
		},
	}
}

// dbSetKind is which gosmo call a db set key goes through (D8).
type dbSetKind int

const (
	dbSetOption     dbSetKind = iota // SetDatabaseOption
	dbSetRecovery                    // SetRecoveryModel
	dbSetCompat                      // SetCompatibilityLevel
	dbSetReadOnly                    // SetReadOnly
	dbSetQueryStore                  // QueryStore().Options → DesiredState → SetOptions
)

// dbSetState is what db set reads of the database, before and after.
type dbSetState struct {
	db   *gosmo.Database
	opts *gosmo.DatabaseOptions
	qs   *gosmo.QueryStoreInfo // read only when QUERY_STORE is set
}

// dbSetKey is one key db set takes: its gosmo call, the values it accepts
// (nil for COMPATIBILITY_LEVEL, a number), and its value in a state, spelled
// as the SET clause takes it.
type dbSetKey struct {
	kind    dbSetKind
	values  []string
	current func(dbSetState) string
}

var onOff = []string{"ON", "OFF"}

func onOffOf(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}

// switchKey is a SetDatabaseOption key taking ON or OFF, read from f.
func switchKey(f func(*gosmo.DatabaseOptions) bool) dbSetKey {
	return dbSetKey{values: onOff, current: func(s dbSetState) string { return onOffOf(f(s.opts)) }}
}

// dbSetKeys is D8's table: SetDatabaseOption's 21 options, and the four
// keys with a call of their own.
var dbSetKeys = map[string]dbSetKey{
	string(gosmo.DBOptAutoClose):                 switchKey(func(o *gosmo.DatabaseOptions) bool { return o.AutoClose }),
	string(gosmo.DBOptAutoShrink):                switchKey(func(o *gosmo.DatabaseOptions) bool { return o.AutoShrink }),
	string(gosmo.DBOptAutoCreateStatistics):      switchKey(func(o *gosmo.DatabaseOptions) bool { return o.AutoCreateStats }),
	string(gosmo.DBOptAutoUpdateStatistics):      switchKey(func(o *gosmo.DatabaseOptions) bool { return o.AutoUpdateStats }),
	string(gosmo.DBOptAutoUpdateStatisticsAsync): switchKey(func(o *gosmo.DatabaseOptions) bool { return o.AutoUpdateStatsAsync }),
	string(gosmo.DBOptANSINullDefault):           switchKey(func(o *gosmo.DatabaseOptions) bool { return o.ANSINullDefault }),
	string(gosmo.DBOptANSINulls):                 switchKey(func(o *gosmo.DatabaseOptions) bool { return o.ANSINulls }),
	string(gosmo.DBOptANSIPadding):               switchKey(func(o *gosmo.DatabaseOptions) bool { return o.ANSIPadding }),
	string(gosmo.DBOptANSIWarnings):              switchKey(func(o *gosmo.DatabaseOptions) bool { return o.ANSIWarnings }),
	string(gosmo.DBOptArithAbort):                switchKey(func(o *gosmo.DatabaseOptions) bool { return o.ArithAbort }),
	string(gosmo.DBOptConcatNullYieldsNull):      switchKey(func(o *gosmo.DatabaseOptions) bool { return o.ConcatNullYieldsNull }),
	string(gosmo.DBOptNumericRoundAbort):         switchKey(func(o *gosmo.DatabaseOptions) bool { return o.NumericRoundAbort }),
	string(gosmo.DBOptQuotedIdentifier):          switchKey(func(o *gosmo.DatabaseOptions) bool { return o.QuotedIdentifier }),
	string(gosmo.DBOptRecursiveTriggers):         switchKey(func(o *gosmo.DatabaseOptions) bool { return o.RecursiveTriggers }),
	string(gosmo.DBOptCursorCloseOnCommit):       switchKey(func(o *gosmo.DatabaseOptions) bool { return o.CursorCloseOnCommit }),
	string(gosmo.DBOptTrustworthy):               switchKey(func(o *gosmo.DatabaseOptions) bool { return o.IsTrustworthy }),
	string(gosmo.DBOptReadCommittedSnapshot):     switchKey(func(o *gosmo.DatabaseOptions) bool { return o.ReadCommittedSnapshot }),
	// snapshot_isolation_state_desc: ON, OFF, or IN_TRANSITION_TO_… while
	// open transactions finish.
	string(gosmo.DBOptSnapshotIsolation): {values: onOff, current: func(s dbSetState) string { return s.opts.SnapshotIsolation }},
	string(gosmo.DBOptPageVerify): {values: []string{"CHECKSUM", "TORN_PAGE_DETECTION", "NONE"},
		current: func(s dbSetState) string { return s.opts.PageVerify }},
	string(gosmo.DBOptCursorDefault): {values: []string{"LOCAL", "GLOBAL"},
		current: func(s dbSetState) string { return s.opts.DefaultCursor }},
	string(gosmo.DBOptContainment): {values: []string{"NONE", "PARTIAL"},
		current: func(s dbSetState) string { return s.opts.Containment }},

	"RECOVERY": {kind: dbSetRecovery,
		values:  []string{string(gosmo.RecoveryModelFull), string(gosmo.RecoveryModelSimple), string(gosmo.RecoveryModelBulkLogged)},
		current: func(s dbSetState) string { return string(s.db.RecoveryModel) }},
	"COMPATIBILITY_LEVEL": {kind: dbSetCompat,
		current: func(s dbSetState) string { return strconv.Itoa(int(s.db.CompatibilityLevel)) }},
	"READ_ONLY": {kind: dbSetReadOnly, values: onOff,
		current: func(s dbSetState) string { return onOffOf(s.db.IsReadOnly) }},
	"QUERY_STORE": {kind: dbSetQueryStore,
		values:  []string{"ON", string(gosmo.QueryStoreReadWrite), string(gosmo.QueryStoreReadOnly), string(gosmo.QueryStoreOff)},
		current: func(s dbSetState) string { return string(s.qs.DesiredState) }},
}

// exclusive reports whether setting key needs the database to itself, so
// that --rollback-immediate applies to it: gosmo's own list, and READ_ONLY.
// Every other key is sent with no termination clause (ALLOW_SNAPSHOT_ISOLATION
// refuses one).
func (k dbSetKey) exclusive(key string) bool {
	return k.kind == dbSetReadOnly || k.kind == dbSetOption && gosmo.DatabaseOption(key).NeedsExclusiveAccess()
}

// exclusiveKeys is the keys exclusive reports, for messages.
const exclusiveKeys = "READ_COMMITTED_SNAPSHOT, CONTAINMENT and READ_ONLY"

// minCompatLevel is the lowest compatibility level every supported server
// (2016 on) accepts.
const minCompatLevel = int(gosmo.CompatLevel2008)

// dbSetChange is one --option: the key and the value as sent, upper-cased
// (QUERY_STORE ON is READ_WRITE, a level its digits).
type dbSetChange struct {
	key, value string
}

// parseDBSet is db set's --option values in order, each checked against its
// key; anything wrong is USAGE_ERROR before connecting. A compatibility
// level's upper bound is the server's, checked once connected.
func parseDBSet(inv *spec.Invocation) ([]dbSetChange, error) {
	raw, _ := inv.Values[optOption].([]string)
	if len(raw) == 0 {
		return nil, usageError("db set: give at least one --option KEY=VALUE", dbSetKeysHint())
	}
	var out []dbSetChange
	for _, r := range raw {
		k, v, ok := strings.Cut(r, "=")
		k, v = strings.ToUpper(strings.TrimSpace(k)), strings.ToUpper(strings.TrimSpace(v))
		if !ok || k == "" || v == "" {
			return nil, usageError(fmt.Sprintf("--option %q is not KEY=VALUE", r), "For example --option RECOVERY=FULL.")
		}
		key, known := dbSetKeys[k]
		if !known {
			return nil, usageError(fmt.Sprintf("--option %s: db set has no key %s", r, k), dbSetKeysHint())
		}
		if slices.ContainsFunc(out, func(c dbSetChange) bool { return c.key == k }) {
			return nil, usageError(fmt.Sprintf("--option %s: %s is given twice", r, k), "Give each key once.")
		}
		switch {
		case key.kind == dbSetCompat:
			n, err := strconv.Atoi(v)
			if err != nil || n < minCompatLevel || n%10 != 0 {
				return nil, usageError(fmt.Sprintf("--option %s: a compatibility level is %d, %d, … in steps of 10", r, minCompatLevel, minCompatLevel+10), "")
			}
			v = strconv.Itoa(n)
		case !slices.Contains(key.values, v):
			return nil, usageError(fmt.Sprintf("--option %s: %s takes %s", r, k, strings.Join(key.values, ", ")), "")
		case key.kind == dbSetQueryStore && v == "ON":
			v = string(gosmo.QueryStoreReadWrite)
		}
		out = append(out, dbSetChange{key: k, value: v})
	}
	return out, nil
}

func dbSetKeysHint() string {
	keys := slices.Sorted(maps.Keys(dbSetKeys))
	return "Keys: " + strings.Join(keys, ", ") + "."
}

// dbSetTermination is the termination --rollback-immediate asks for, checked
// before connecting: it applies only when a change needs the database to
// itself, and it kills sessions, so it needs --confirm with the database's
// name exactly as given (§6.3) — BLOCKED otherwise.
func dbSetTermination(inv *spec.Invocation, changes []dbSetChange) (gosmo.Termination, error) {
	rollback := inv.Values[optRollbackImmediate].(bool)
	confirm, _ := inv.Values[optConfirm].(string)
	if !rollback {
		if confirm != "" {
			return 0, usageError("--confirm accepts --rollback-immediate, which was not given", "")
		}
		return gosmo.TerminationNone, nil
	}
	if !slices.ContainsFunc(changes, func(c dbSetChange) bool { return dbSetKeys[c.key].exclusive(c.key) }) {
		return 0, usageError("--rollback-immediate applies only to "+exclusiveKeys+", and none is set", "")
	}
	name := inv.Args[0]
	if confirm != name {
		msg := "--rollback-immediate disconnects every other session in " + gosmo.QuoteName(name) +
			" and rolls back its open transactions"
		if confirm != "" {
			msg += fmt.Sprintf("; --confirm %q is not the database's name as given", confirm)
		}
		return 0, output.NewError(output.CodeBlocked, msg, fmt.Sprintf("Add --confirm %s to accept it.", name))
	}
	return gosmo.TerminationRollbackImmediate, nil
}

// checkCompatLevels is USAGE_ERROR for a compatibility level above the
// server's own (its major version × 10). An Azure server or an unread
// version is left to the server.
func checkCompatLevels(changes []dbSetChange, info *gosmo.ServerInfo) error {
	if info == nil || info.IsAzure() || info.VersionMajor < int(gosmo.SQLServer2016) {
		return nil
	}
	top := info.VersionMajor * 10
	for _, c := range changes {
		if n, _ := strconv.Atoi(c.value); dbSetKeys[c.key].kind == dbSetCompat && n > top {
			return usageError(fmt.Sprintf("COMPATIBILITY_LEVEL %d is above this server's %d", n, top),
				fmt.Sprintf("Give a level from %d to %d.", minCompatLevel, top))
		}
	}
	return nil
}

// readDBSetState is the database's state for changes: sys.databases, its
// options, and Query Store's when QUERY_STORE is among them.
func readDBSetState(ctx context.Context, cat Catalog, d *gosmo.Database, changes []dbSetChange) (dbSetState, error) {
	s := dbSetState{db: d}
	var err error
	if s.opts, err = cat.DatabaseOptions(ctx, d); err != nil {
		return s, err
	}
	if slices.ContainsFunc(changes, func(c dbSetChange) bool { return c.key == "QUERY_STORE" }) {
		s.qs, err = cat.QueryStoreOptions(ctx, d)
	}
	return s, err
}

// applyDBSet makes one change through its gosmo call. QUERY_STORE re-sends
// qs's options with the new state, so nothing else about Query Store
// changes (verified live on 13, 14 and 17).
func applyDBSet(ctx context.Context, w Writer, d *gosmo.Database, c dbSetChange, term gosmo.Termination, qs *gosmo.QueryStoreInfo) error {
	key := dbSetKeys[c.key]
	if !key.exclusive(c.key) {
		term = gosmo.TerminationNone
	}
	switch key.kind {
	case dbSetRecovery:
		return w.SetRecoveryModel(ctx, d, gosmo.RecoveryModel(c.value))
	case dbSetCompat:
		n, _ := strconv.Atoi(c.value)
		return w.SetCompatibilityLevel(ctx, d, gosmo.CompatibilityLevel(n))
	case dbSetReadOnly:
		return w.SetReadOnly(ctx, d, c.value == "ON", term)
	case dbSetQueryStore:
		return w.SetQueryStoreOptions(ctx, d, gosmo.QueryStoreOptions{
			DesiredState: gosmo.QueryStoreState(c.value), MaxStorageMB: qs.MaxStorageMB, CaptureMode: qs.CaptureMode,
			SizeCleanupMode: qs.SizeCleanupMode, StaleThresholdDays: qs.StaleThresholdDays,
			FlushIntervalSec: qs.FlushIntervalSec, IntervalMinutes: qs.IntervalMinutes,
			MaxPlansPerQuery: qs.MaxPlansPerQuery, WaitStatsCaptureMode: qs.WaitStatsCaptureMode,
			CapturePolicyExecCount: qs.CapturePolicyExecCount, CapturePolicyCompileCPUMs: qs.CapturePolicyCompileCPUMs,
			CapturePolicyExecCPUMs: qs.CapturePolicyExecCPUMs, CapturePolicyStaleHours: qs.CapturePolicyStaleHours,
		})
	}
	return w.SetDatabaseOption(ctx, d, gosmo.DatabaseOption(c.key), c.value, term)
}

func (d Deps) dbSet(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	changes, err := parseDBSet(inv)
	if err != nil {
		return nil, err
	}
	timeout, err := readTimeout(inv)
	if err != nil {
		return nil, err
	}
	term, err := dbSetTermination(inv, changes)
	if err != nil {
		return nil, err
	}
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
	if err := checkCompatLevels(changes, cat.ServerInfo()); err != nil {
		return res, err
	}
	before, err := readDBSetState(ctx, cat, db, changes)
	if err != nil {
		return res, readError(err)
	}
	// The values before, spelled now: a gosmo setter updates db's own
	// fields as it succeeds.
	was := make([]string, len(changes))
	for i, c := range changes {
		was[i] = dbSetKeys[c.key].current(before)
	}

	done := 0 // changes applied
	apply := func(ctx context.Context) error {
		for _, c := range changes {
			err := applyDBSet(ctx, w, db, c, term, before.qs)
			// Msg 5011 reads "missing or denied"; the database was just
			// read, so it is denied.
			if gosmo.IsMissingOrDenied(err) {
				return output.NewError(output.CodePermissionDenied, err.Error(),
					"ALTER DATABASE needs ALTER on the database (db_owner has it). Use a login that has it, or have it granted.")
			}
			if err != nil {
				return err
			}
			done++
		}
		return nil
	}
	column := func(name string) output.Column { return output.Column{Name: name, Type: "nvarchar(60)"} }
	if dryRun(inv) {
		set := output.Set{Name: "planned_change", Columns: []output.Column{column("option"), column("current"), column("new")}}
		var waits []string
		for i, c := range changes {
			set.Rows = append(set.Rows, []*string{ptr(c.key), ptr(was[i]), ptr(c.value)})
			if dbSetKeys[c.key].exclusive(c.key) && term == gosmo.TerminationNone {
				waits = append(waits, c.key)
			}
		}
		set.TotalRowCount = len(set.Rows)
		res.Sets = []output.Set{set}
		if len(waits) > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s needs the database to itself: run for real, it waits "+
				"up to --timeout for the database's other sessions to leave (--rollback-immediate disconnects them).",
				strings.Join(waits, ", ")))
		}
		return res, applyWrite(ctx, inv, res, apply)
	}

	tctx, cancel := withTimeout(ctx, timeout)
	werr := applyWrite(tctx, inv, res, apply)
	timedOut := tctx.Err() == context.DeadlineExceeded
	cancel()
	if werr != nil {
		werr = dbSetError(werr, changes[done], inv.Args[0], timedOut, timeout)
	}

	// A failure stops the run after the changes before it took, so the
	// state is read again either way.
	after, err := cat.DatabaseByName(ctx, db.Name)
	var now dbSetState
	if err == nil {
		now, err = readDBSetState(ctx, cat, after, changes)
	}
	if err != nil {
		if werr != nil {
			return res, werr
		}
		e := readError(err)
		e.Message = "the change ran, but the database's options could not be read back: " + e.Message
		return res, e
	}
	set := output.Set{Name: "change", Columns: []output.Column{column("option"), column("before"), column("requested"), column("after")}}
	for i, c := range changes {
		got := dbSetKeys[c.key].current(now)
		set.Rows = append(set.Rows, []*string{ptr(c.key), ptr(was[i]), ptr(c.value), ptr(got)})
		if i < done && got != c.value {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s is %s, not the %s requested.", c.key, got, c.value))
		}
		if i < done && c.key == "QUERY_STORE" && now.qs.ActualState != now.qs.DesiredState {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Query Store's actual state is %s, not the desired %s "+
				"(readonly_reason %d).", now.qs.ActualState, now.qs.DesiredState, now.qs.ReadOnlyReason))
		}
	}
	set.TotalRowCount = len(set.Rows)
	res.Sets = []output.Set{set}
	if werr != nil && done+1 < len(changes) {
		var skipped []string
		for _, c := range changes[done+1:] {
			skipped = append(skipped, c.key)
		}
		res.Warnings = append(res.Warnings, fmt.Sprintf("Not attempted after %s failed: %s.", changes[done].key, strings.Join(skipped, ", ")))
	}
	return res, werr
}

// dbSetError is werr, the failure of c, with what db set knows of it: a
// wait cut by --timeout is TIMEOUT, a refused lock (CONTAINMENT's Msg 5061)
// points at --rollback-immediate, and a read-only database (Msg 3906) at the
// order of the options.
func dbSetError(werr error, c dbSetChange, name string, timedOut bool, timeout time.Duration) error {
	exclusive := dbSetKeys[c.key].exclusive(c.key)
	rollback := fmt.Sprintf("--rollback-immediate --confirm %s disconnects them, rolling back their open transactions.", name)
	if timedOut {
		e := output.NewError(output.CodeTimeout,
			fmt.Sprintf("%s=%s ran longer than --timeout %d s and was cancelled", c.key, c.value, int(timeout/time.Second)),
			"Raise --timeout (0 is no limit).")
		if exclusive {
			e.Hint = c.key + " waits for every other session in the database to leave. Raise --timeout, or end " +
				"them: " + rollback
		}
		return e
	}
	if e, ok := errors.AsType[*output.Error](werr); ok && e.SQL != nil {
		switch e.SQL.Number {
		case 5061:
			e.Hint = "Other sessions are using the database. " + rollback
		case 3906:
			// Found live on 14, after READ_ONLY=ON earlier in the same run.
			e.Hint = "The database is read-only: the options apply in order, so give READ_ONLY=ON last, or " +
				"READ_ONLY=OFF first."
		}
	}
	return werr
}
