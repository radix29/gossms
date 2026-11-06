package cmd

import (
	"slices"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
)

// newScopedServer is a 2025 server whose app database has the scoped
// configurations scopedConfigs17 lists.
func newScopedServer(t *testing.T) *dbServer {
	s := newDBServer(t)
	s.setMajor(17)
	s.cfgs["app"] = scopedConfigs17()
	return s
}

func TestDBScopedConfigList(t *testing.T) {
	s := newScopedServer(t)
	res, _, err := s.run(t, "scoped-config list app")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"ELEVATE_ONLINE", "OFF", "NULL", "1", "nvarchar"},
		{"IDENTITY_CACHE", "1", "NULL", "1", "bit"},
		{"LEGACY_CARDINALITY_ESTIMATION", "0", "NULL", "1", "bit"},
		{"MAXDOP", "0", "2", "0", "int"},
		{"PARAMETER_SNIFFING", "1", "NULL", "1", "bit"},
	}
	if got := setRows(res.Sets[0]); !slices.EqualFunc(got, want, slices.Equal) || res.Database != "app" {
		t.Errorf("rows %q, want %q (database %q)", got, want, res.Database)
	}
	// 2016 has no is_value_default: NULL, not the false gosmo reads.
	s.setMajor(13)
	res, _, _ = s.run(t, "scoped-config list app")
	for _, r := range setRows(res.Sets[0]) {
		if r[3] != "NULL" {
			t.Errorf("13: %q", r)
		}
	}
	// Azure reports 12 and has the column.
	s.Info.ProductVersion, s.Info.EngineEdition = "12.0.2000.8", int(gosmo.EngineAzureManagedInst)
	res, _, _ = s.run(t, "scoped-config list app")
	if r := setRows(res.Sets[0])[0]; r[3] != "1" {
		t.Errorf("Azure: %q", r)
	}
}

func TestDBScopedConfigGet(t *testing.T) {
	res, _, err := newScopedServer(t).run(t, "scoped-config get app maxdop PARAMETER_SNIFFING MAXDOP")
	if err != nil {
		t.Fatal(err)
	}
	if got := setRows(res.Sets[0]); len(got) != 2 || got[0][0] != "MAXDOP" || got[1][0] != "PARAMETER_SNIFFING" {
		t.Errorf("rows %q", got)
	}
	_, _, err = newScopedServer(t).run(t, "scoped-config get app cardinality")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeNotFound || e.Hint != "Did you mean LEGACY_CARDINALITY_ESTIMATION?" {
		t.Errorf("%v", err)
	}
	_, _, err = newScopedServer(t).run(t, "scoped-config get app nothing_like_it")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeNotFound || !strings.Contains(e.Hint, "db scoped-config list app") {
		t.Errorf("%v", err)
	}
}

// The database must be there, ONLINE and accessible: 8, 8 and 9, and its
// configurations are never read otherwise.
func TestDBScopedConfigUnreadableDatabase(t *testing.T) {
	for argv, code := range map[string]output.Code{
		"list nosuch": output.CodeNotFound, "list archive": output.CodeNotFound, "get hr MAXDOP": output.CodePermissionDenied,
		"set archive MAXDOP 4 --allow admin": output.CodeNotFound,
	} {
		s := newScopedServer(t)
		_, _, err := s.run(t, "scoped-config "+argv)
		if errCode(err) != code || s.scopedReads != 0 || len(s.writes) != 0 {
			t.Errorf("%q: %v (want %s), %d reads, writes %q", argv, err, code, s.scopedReads, s.writes)
		}
	}
}

func TestDBScopedConfigSet(t *testing.T) {
	const alter = "ALTER DATABASE SCOPED CONFIGURATION "
	for _, tc := range []struct {
		argv, write string
		row         []string
	}{
		{"set app maxdop 4", alter + "SET MAXDOP = 4", []string{"MAXDOP", "primary", "0", "4", "4"}},
		{"set app legacy_cardinality_estimation on", alter + "SET LEGACY_CARDINALITY_ESTIMATION = ON",
			[]string{"LEGACY_CARDINALITY_ESTIMATION", "primary", "OFF", "ON", "ON"}},
		// 1 and 0 are sent as ON and OFF: the statement refuses a number.
		{"set app PARAMETER_SNIFFING 0", alter + "SET PARAMETER_SNIFFING = OFF", []string{"PARAMETER_SNIFFING", "primary", "ON", "OFF", "OFF"}},
		{"set app ELEVATE_ONLINE when_supported", alter + "SET ELEVATE_ONLINE = WHEN_SUPPORTED",
			[]string{"ELEVATE_ONLINE", "primary", "OFF", "WHEN_SUPPORTED", "WHEN_SUPPORTED"}},
		// A secondary value of its own, and PRIMARY (NULL in the view) back.
		{"set app MAXDOP 1 --for-secondary", alter + "FOR SECONDARY SET MAXDOP = 1", []string{"MAXDOP", "secondary", "2", "1", "1"}},
		{"set app MAXDOP primary --for-secondary", alter + "FOR SECONDARY SET MAXDOP = PRIMARY", []string{"MAXDOP", "secondary", "2", "PRIMARY", "PRIMARY"}},
		{"set app PARAMETER_SNIFFING 1 --for-secondary", alter + "FOR SECONDARY SET PARAMETER_SNIFFING = ON",
			[]string{"PARAMETER_SNIFFING", "secondary", "PRIMARY", "ON", "ON"}},
	} {
		s := newScopedServer(t)
		res, dials, err := s.run(t, "scoped-config "+tc.argv+" --allow admin")
		if err != nil {
			t.Fatalf("%q: %v", tc.argv, err)
		}
		set := res.Sets[0]
		if dials != 1 || !slices.Equal(s.writes, []string{tc.write}) || s.scopedReads != 2 || set.Name != "change" {
			t.Errorf("%q: %d dials, writes %q, %d reads, set %q", tc.argv, dials, s.writes, s.scopedReads, set.Name)
		}
		if got := setRows(set); !slices.EqualFunc(got, [][]string{tc.row}, slices.Equal) {
			t.Errorf("%q: rows %q, want %q", tc.argv, got, tc.row)
		}
		if fieldValue(res, "dryRun") != "false" || hasField(res, "tsql") || len(res.Warnings) != 0 {
			t.Errorf("%q: fields %+v, warnings %q", tc.argv, res.Fields, res.Warnings)
		}
	}
}

// Without --allow admin nothing dials (the 5.b gate); list and get need none.
func TestDBScopedConfigGate(t *testing.T) {
	for _, argv := range []string{"set app MAXDOP 4", "set app MAXDOP 4 --dry-run", "set app MAXDOP 4 --allow write"} {
		s := newScopedServer(t)
		_, dials, err := s.run(t, "scoped-config "+argv)
		if errCode(err) != output.CodeBlocked || dials != 0 || len(s.writes) != 0 {
			t.Errorf("%q: %v, %d dials, writes %q", argv, err, dials, s.writes)
		}
	}
}

// A value the configuration's type does not take is exit 2, before anything
// runs; so is PRIMARY for the primary (Msg 12109 on the server).
func TestDBScopedConfigSetValues(t *testing.T) {
	for _, argv := range []string{
		"set app MAXDOP PRIMARY", "set app PARAMETER_SNIFFING 2", "set app PARAMETER_SNIFFING yes",
		"set app MAXDOP ON", "set app MAXDOP 1.5", "set app ELEVATE_ONLINE 'x'",
		"set app ELEVATE_ONLINE 1ON", "set app ELEVATE_ONLINE WHEN-SUPPORTED",
	} {
		s := newScopedServer(t)
		_, _, err := s.run(t, "scoped-config "+argv+" --allow admin")
		if errCode(err) != output.CodeUsageError || len(s.writes) != 0 || s.scopedReads != 1 {
			t.Errorf("%q: %v, writes %q, %d reads", argv, err, s.writes, s.scopedReads)
		}
	}
	// A negative number follows --, after the options.
	s := newScopedServer(t)
	if _, _, err := s.run(t, "scoped-config set app MAXDOP --allow admin -- -1"); errCode(err) != output.CodeUsageError || len(s.writes) != 0 {
		t.Errorf("-1: %v, writes %q", err, s.writes)
	}
	_, _, err := newScopedServer(t).run(t, "scoped-config set app MAXDOOP 4 --allow admin")
	if errCode(err) != output.CodeNotFound {
		t.Errorf("unknown name: %v", err)
	}
}

// --dry-run: gosmo's statement in the database, the planned change, nothing
// applied and no second read.
func TestDBScopedConfigDryRun(t *testing.T) {
	s := newScopedServer(t)
	res, _, err := s.run(t, "scoped-config set app MAXDOP 2 --for-secondary --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.writes) != 0 || s.scopedReads != 1 {
		t.Errorf("a dry run applied %q, %d reads", s.writes, s.scopedReads)
	}
	if got, want := fieldValue(res, "tsql"), "USE [app];\nGO\nALTER DATABASE SCOPED CONFIGURATION FOR SECONDARY SET MAXDOP = 2\nGO\n"; got != want {
		t.Errorf("tsql %q, want %q", got, want)
	}
	set := res.Sets[0]
	if got := setRows(set); set.Name != "planned_change" || fieldValue(res, "dryRun") != "true" ||
		!slices.EqualFunc(got, [][]string{{"MAXDOP", "secondary", "2", "2"}}, slices.Equal) {
		t.Errorf("set %q rows %q, fields %+v", set.Name, got, res.Fields)
	}
}

// A change the second read does not show is a warning.
func TestDBScopedConfigNotTaken(t *testing.T) {
	s := newScopedServer(t)
	s.ignore = true
	res, _, err := s.run(t, "scoped-config set app MAXDOP 4 --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if got := setRows(res.Sets[0]); !slices.EqualFunc(got, [][]string{{"MAXDOP", "primary", "0", "4", "0"}}, slices.Equal) {
		t.Errorf("rows %q", got)
	}
	if !slices.Equal(res.Warnings, []string{"MAXDOP is 0 for the primary, not the 4 requested."}) {
		t.Errorf("warnings %q", res.Warnings)
	}
}

// The server's refusals: no permission (Msg 15247, live-captured on 17 and
// 13) is exit 9 with the permission named;
// a configuration with no secondary value (Msg 12110, live-captured on 17)
// gets a hint; neither reads back.
func TestDBScopedConfigRefused(t *testing.T) {
	s := newScopedServer(t)
	s.failAt, s.writeErr = 1, sqlErr(15247, 16, "User does not have permission to perform this action.")
	_, _, err := s.run(t, "scoped-config set app MAXDOP 4 --allow admin")
	if errCode(err) != output.CodePermissionDenied || !strings.Contains(err.(*output.Error).Hint, "ALTER ANY DATABASE SCOPED CONFIGURATION") || s.scopedReads != 1 {
		t.Errorf("%v, %d reads", err, s.scopedReads)
	}
	s = newScopedServer(t)
	s.failAt, s.writeErr = 1, sqlErr(12110, 16, "Statement 'ALTER DATABASE SCOPED CONFIGURATION' failed, because it attempted "+
		"to set the 'IDENTITY_CACHE' option for the secondaries replica while this option is only allowed to be set for the primary.")
	_, _, err = s.run(t, "scoped-config set app IDENTITY_CACHE OFF --for-secondary --allow admin")
	if errCode(err) != output.CodeSQLError || err.(*output.Error).Hint != "IDENTITY_CACHE has no secondary value: set it without --for-secondary." {
		t.Errorf("%v", err)
	}
}

// On a readable secondary the database is read-only (Msg 3906, live on
// ubusql2): the hint sends the change to the primary.
func TestDBScopedConfigOnSecondary(t *testing.T) {
	s := newScopedServer(t)
	s.failAt, s.writeErr = 1, sqlErr(3906, 16, `Failed to update database "app" because the database is read-only.`)
	_, _, err := s.run(t, "scoped-config set app MAXDOP 1 --for-secondary --allow admin")
	if errCode(err) != output.CodeSQLError || !strings.Contains(err.(*output.Error).Hint, "run the change on the primary") {
		t.Errorf("%v", err)
	}
}

// A failed second read after a change that ran says so.
func TestDBScopedConfigReadBackFails(t *testing.T) {
	s := newScopedServer(t)
	s.scopedErr = sqlErr(229, 14, "The SELECT permission was denied on the object 'database_scoped_configurations'.")
	_, _, err := s.run(t, "scoped-config set app MAXDOP 4 --allow admin")
	if e, ok := err.(*output.Error); !ok || !strings.HasPrefix(e.Message, "the change ran, but sys.database_scoped_configurations could not be read back") {
		t.Errorf("%v", err)
	}
}

// --- db set -----------------------------------------------------------------

// newDBSetServer is a 2025 server whose app database is SIMPLE at level 160
// with torn-page detection and Query Store off.
func newDBSetServer(t *testing.T) *dbServer {
	s := newDBServer(t)
	s.setMajor(17)
	s.db("app").RecoveryModel = gosmo.RecoveryModelSimple
	s.opts["app"] = &gosmo.DatabaseOptions{PageVerify: "TORN_PAGE_DETECTION", DefaultCursor: "GLOBAL", Containment: "NONE",
		SnapshotIsolation: "OFF", UserAccess: gosmo.UserAccessMulti, AutoCreateStats: true, AutoUpdateStats: true}
	return s
}

// The statements db set sends for each option, as gosmo spells them.
const (
	alterApp  = "ALTER DATABASE [app] SET "
	storeKeep = "OPERATION_MODE = READ_WRITE, MAX_STORAGE_SIZE_MB = 123, DATA_FLUSH_INTERVAL_SECONDS = 300, " +
		"INTERVAL_LENGTH_MINUTES = 15, MAX_PLANS_PER_QUERY = 77, SIZE_BASED_CLEANUP_MODE = OFF, " +
		"QUERY_CAPTURE_MODE = ALL, CLEANUP_POLICY = (STALE_QUERY_THRESHOLD_DAYS = 11), WAIT_STATS_CAPTURE_MODE = OFF"
)

// The changes run in order, one statement each, keys and values ignoring
// case; QUERY_STORE=ON is READ_WRITE and re-sends every other option as read;
// the change set comes from a second read.
func TestDBSet(t *testing.T) {
	s := newDBSetServer(t)
	res, dials, err := s.run(t, "set app --option recovery=full --option PAGE_VERIFY=checksum "+
		"--option QUERY_STORE=on --option COMPATIBILITY_LEVEL=170 --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	wantWrites := []string{alterApp + "RECOVERY FULL", alterApp + "PAGE_VERIFY CHECKSUM",
		alterApp + "QUERY_STORE = ON (" + storeKeep + ")", alterApp + "COMPATIBILITY_LEVEL = 170"}
	if dials != 1 || !slices.Equal(s.writes, wantWrites) || s.optionReads != 2 {
		t.Errorf("%d dials, writes %q, %d reads", dials, s.writes, s.optionReads)
	}
	want := [][]string{
		{"RECOVERY", "SIMPLE", "FULL", "FULL"},
		{"PAGE_VERIFY", "TORN_PAGE_DETECTION", "CHECKSUM", "CHECKSUM"},
		{"QUERY_STORE", "OFF", "READ_WRITE", "READ_WRITE"},
		{"COMPATIBILITY_LEVEL", "160", "170", "170"},
	}
	set := res.Sets[0]
	if got := setRows(set); set.Name != "change" || !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("set %s %q", set.Name, got)
	}
	if fieldValue(res, "dryRun") != "false" || hasField(res, "tsql") || len(res.Warnings) != 0 || res.Database != "app" {
		t.Errorf("fields %v, warnings %q, database %q", res.Fields, res.Warnings, res.Database)
	}
}

// Every argument error is exit 2 before connecting; a --rollback-immediate
// without its --confirm is BLOCKED, also before connecting.
func TestDBSetArgumentErrors(t *testing.T) {
	for argv, code := range map[string]output.Code{
		"app":                                                           output.CodeUsageError,
		"app --option RECOVERY":                                         output.CodeUsageError,
		"app --option =FULL":                                            output.CodeUsageError,
		"app --option RECOVERY=":                                        output.CodeUsageError,
		"app --option USER_ACCESS=SINGLE_USER":                          output.CodeUsageError,
		"app --option RECOVERY=PARTIAL":                                 output.CodeUsageError,
		"app --option AUTO_CLOSE=1":                                     output.CodeUsageError,
		"app --option QUERY_STORE=ERROR":                                output.CodeUsageError,
		"app --option PAGE_VERIFY=ON":                                   output.CodeUsageError,
		"app --option COMPATIBILITY_LEVEL=90":                           output.CodeUsageError,
		"app --option COMPATIBILITY_LEVEL=155":                          output.CodeUsageError,
		"app --option COMPATIBILITY_LEVEL=abc":                          output.CodeUsageError,
		"app --option RECOVERY=FULL --option recovery=SIMPLE":           output.CodeUsageError,
		"app --option RECOVERY=FULL --timeout -1":                       output.CodeUsageError,
		"app --option RECOVERY=FULL --confirm app":                      output.CodeUsageError,
		"app --option RECOVERY=FULL --rollback-immediate --confirm app": output.CodeUsageError,
		"app --option ALLOW_SNAPSHOT_ISOLATION=ON --rollback-immediate --confirm app": output.CodeUsageError,
		"app --option READ_COMMITTED_SNAPSHOT=ON --rollback-immediate":                output.CodeBlocked,
		"app --option READ_COMMITTED_SNAPSHOT=ON --rollback-immediate --confirm APP":  output.CodeBlocked,
		"app --option READ_ONLY=ON --rollback-immediate --confirm other":              output.CodeBlocked,
	} {
		s := newDBSetServer(t)
		_, dials, err := s.run(t, "set "+argv+" --allow admin")
		if errCode(err) != code || dials != 0 || len(s.writes) != 0 {
			t.Errorf("%q: %v, %d dials, writes %q", argv, err, dials, s.writes)
		}
	}
	_, _, err := newDBSetServer(t).run(t, "set app --option NOSUCH=ON --allow admin")
	if e, ok := err.(*output.Error); !ok || !strings.Contains(e.Hint, "AUTO_CLOSE, AUTO_CREATE_STATISTICS,") ||
		!strings.Contains(e.Hint, "QUERY_STORE, QUOTED_IDENTIFIER, READ_COMMITTED_SNAPSHOT, READ_ONLY, RECOVERY,") {
		t.Errorf("%v", err)
	}
	_, _, err = newDBSetServer(t).run(t, "set app --option READ_ONLY=ON --rollback-immediate --allow admin")
	if e, ok := err.(*output.Error); !ok || e.Hint != "Add --confirm app to accept it." || !strings.Contains(e.Message, "disconnects every other session in [app]") {
		t.Errorf("%v", err)
	}
}

// Without --allow admin nothing dials (the 5.b gate), --dry-run included.
func TestDBSetGate(t *testing.T) {
	for _, argv := range []string{"app --option RECOVERY=FULL", "app --option RECOVERY=FULL --dry-run",
		"app --option RECOVERY=FULL --allow ddl"} {
		s := newDBSetServer(t)
		_, dials, err := s.run(t, "set "+argv)
		if errCode(err) != output.CodeBlocked || dials != 0 || len(s.writes) != 0 {
			t.Errorf("%q: %v, %d dials, writes %q", argv, err, dials, s.writes)
		}
	}
}

// Once connected: an unknown database is exit 8, a level above the server's
// exit 2; nothing is written. Azure's levels are the server's to check.
func TestDBSetServerChecks(t *testing.T) {
	s := newDBSetServer(t)
	if _, _, err := s.run(t, "set nosuch --option RECOVERY=FULL --allow admin"); errCode(err) != output.CodeNotFound || len(s.writes) != 0 {
		t.Errorf("%v, writes %q", err, s.writes)
	}
	s = newDBSetServer(t)
	s.setMajor(13)
	_, _, err := s.run(t, "set app --option RECOVERY=FULL --option COMPATIBILITY_LEVEL=140 --allow admin")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeUsageError || e.Hint != "Give a level from 100 to 130." || len(s.writes) != 0 {
		t.Errorf("%v, writes %q", err, s.writes)
	}
	s = newDBSetServer(t)
	s.Info.ProductVersion, s.Info.EngineEdition = "12.0.2000.8", int(gosmo.EngineAzureManagedInst)
	if _, _, err := s.run(t, "set app --option COMPATIBILITY_LEVEL=170 --allow admin"); err != nil ||
		!slices.Equal(s.writes, []string{alterApp + "COMPATIBILITY_LEVEL = 170"}) {
		t.Errorf("Azure: %v, writes %q", err, s.writes)
	}
}

// --rollback-immediate reaches only the options that need the database to
// themselves; the others are sent without a termination clause.
func TestDBSetRollbackImmediate(t *testing.T) {
	s := newDBSetServer(t)
	_, _, err := s.run(t, "set app --option ALLOW_SNAPSHOT_ISOLATION=ON --option READ_COMMITTED_SNAPSHOT=ON "+
		"--option READ_ONLY=ON --rollback-immediate --confirm app --allow admin")
	want := []string{alterApp + "ALLOW_SNAPSHOT_ISOLATION ON", alterApp + "READ_COMMITTED_SNAPSHOT ON WITH ROLLBACK IMMEDIATE",
		alterApp + "READ_ONLY WITH ROLLBACK IMMEDIATE"}
	if err != nil || !slices.Equal(s.writes, want) {
		t.Errorf("%v, writes %q", err, s.writes)
	}
}

// A dry run reads the state, writes nothing, and prints gosmo's statements
// in order with the planned_change set; a key that would wait is warned of.
func TestDBSetDryRun(t *testing.T) {
	s := newDBSetServer(t)
	res, _, err := s.run(t, "set app --option RECOVERY=FULL --option READ_COMMITTED_SNAPSHOT=ON --option QUERY_STORE=OFF --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.writes) != 0 || s.optionReads != 1 {
		t.Errorf("writes %q, %d reads", s.writes, s.optionReads)
	}
	tsql := fieldValue(res, "tsql")
	r, rcsi, qs := strings.Index(tsql, "SET RECOVERY FULL"), strings.Index(tsql, "SET READ_COMMITTED_SNAPSHOT ON"), strings.Index(tsql, "SET QUERY_STORE = OFF")
	if r < 0 || rcsi < r || qs < rcsi || strings.Contains(tsql, "ROLLBACK") || fieldValue(res, "dryRun") != "true" {
		t.Errorf("tsql %q", tsql)
	}
	want := [][]string{{"RECOVERY", "SIMPLE", "FULL"}, {"READ_COMMITTED_SNAPSHOT", "OFF", "ON"}, {"QUERY_STORE", "OFF", "OFF"}}
	if got := setRows(res.Sets[0]); res.Sets[0].Name != "planned_change" || !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("set %q", got)
	}
	if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "READ_COMMITTED_SNAPSHOT needs the database to itself") {
		t.Errorf("warnings %q", res.Warnings)
	}

	res, _, _ = newDBSetServer(t).run(t, "set app --option READ_ONLY=ON --rollback-immediate --confirm app --allow admin --dry-run")
	if tsql := fieldValue(res, "tsql"); !strings.Contains(tsql, "SET READ_ONLY WITH ROLLBACK IMMEDIATE") || len(res.Warnings) != 0 {
		t.Errorf("tsql %q, warnings %q", tsql, res.Warnings)
	}
}

// The first failure stops the run: exit 5, the change set shows every key
// after a second read, and the keys not attempted are named.
func TestDBSetPartialFailure(t *testing.T) {
	s := newDBSetServer(t)
	// The server's last message is the contentless Msg 5069; the error is
	// the first, which says why (live on 17).
	cause := mssql.Error{Number: 12824, Class: 16, Message: "The sp_configure value 'contained database authentication' must be set to 1."}
	failed := mssql.Error{Number: 5069, Class: 16, Message: "ALTER DATABASE statement failed."}
	failed.All = []mssql.Error{cause, failed}
	s.failAt, s.writeErr = 2, failed
	res, _, err := s.run(t, "set app --option RECOVERY=FULL --option CONTAINMENT=PARTIAL --option PAGE_VERIFY=CHECKSUM --allow admin")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeSQLError || e.SQL.Number != 12824 || e.Message != cause.Message ||
		len(s.writes) != 2 || s.optionReads != 2 {
		t.Fatalf("%v, writes %q, %d reads", err, s.writes, s.optionReads)
	}
	want := [][]string{
		{"RECOVERY", "SIMPLE", "FULL", "FULL"},
		{"CONTAINMENT", "NONE", "PARTIAL", "NONE"},
		{"PAGE_VERIFY", "TORN_PAGE_DETECTION", "CHECKSUM", "TORN_PAGE_DETECTION"},
	}
	if got := setRows(res.Sets[0]); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("set %q", got)
	}
	if !slices.Equal(res.Warnings, []string{"Not attempted after CONTAINMENT failed: PAGE_VERIFY."}) {
		t.Errorf("warnings %q", res.Warnings)
	}

	// A failed read-back keeps the write's error.
	readDenied := sqlErr(229, 14, "The SELECT permission was denied on the object 'databases'.")
	s = newDBSetServer(t)
	s.optionsErr, s.failAt, s.writeErr = readDenied, 1, failed
	if _, _, err := s.run(t, "set app --option CONTAINMENT=PARTIAL --allow admin"); errCode(err) != output.CodeSQLError {
		t.Errorf("%v", err)
	}
	// ... and after a change that ran, says so.
	s = newDBSetServer(t)
	s.optionsErr = readDenied
	_, _, err = s.run(t, "set app --option RECOVERY=FULL --allow admin")
	if e, ok := err.(*output.Error); !ok || !strings.HasPrefix(e.Message, "the change ran, but the database's options could not be read back") {
		t.Errorf("%v", err)
	}
}

// The server's refusals: no ALTER on the database (Msg 5011, which gosmo
// reads as missing or denied) is exit 9; a lock refused (Msg 5061) points at
// --rollback-immediate; a database made read-only earlier in the run (Msg
// 3906) at the order.
func TestDBSetRefused(t *testing.T) {
	s := newDBSetServer(t)
	s.failAt, s.writeErr = 1, mssql.Error{Number: 5011, Class: 14,
		Message: "User does not have permission to alter database 'app', the database does not exist, or the database is not in a state that allows access checks."}
	_, _, err := s.run(t, "set app --option RECOVERY=FULL --allow admin")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodePermissionDenied || !strings.Contains(e.Hint, "ALTER on the database") {
		t.Errorf("%v", err)
	}
	s = newDBSetServer(t)
	s.failAt, s.writeErr = 1, mssql.Error{Number: 5061, Class: 16,
		Message: "ALTER DATABASE failed because a lock could not be placed on database 'app'. Try again later."}
	_, _, err = s.run(t, "set app --option CONTAINMENT=PARTIAL --allow admin")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeSQLError || !strings.Contains(e.Hint, "--rollback-immediate --confirm app") {
		t.Errorf("%v", err)
	}
	s = newDBSetServer(t)
	s.failAt, s.writeErr = 2, mssql.Error{Number: 3906, Class: 16,
		Message: `Failed to update database "app" because the database is read-only.`}
	_, _, err = s.run(t, "set app --option READ_ONLY=ON --option COMPATIBILITY_LEVEL=150 --allow admin")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeSQLError || !strings.Contains(e.Hint, "give READ_ONLY=ON last") {
		t.Errorf("%v", err)
	}
}

// A change waiting past --timeout is TIMEOUT; for an option needing the
// database to itself the hint names --rollback-immediate. The read-back
// still runs.
func TestDBSetTimeout(t *testing.T) {
	s := newDBSetServer(t)
	s.block = true
	res, _, err := s.run(t, "set app --option READ_COMMITTED_SNAPSHOT=ON --timeout 1 --allow admin")
	e, ok := err.(*output.Error)
	if !ok || e.Code != output.CodeTimeout || !strings.Contains(e.Hint, "--rollback-immediate --confirm app") ||
		e.Message != "READ_COMMITTED_SNAPSHOT=ON ran longer than --timeout 1 s and was cancelled" {
		t.Fatalf("%v", err)
	}
	if s.optionReads != 2 || len(res.Sets) != 1 {
		t.Errorf("%d reads, sets %v", s.optionReads, res.Sets)
	}
}

// A change the server took without error but did not make is a warning.
func TestDBSetNotTaken(t *testing.T) {
	s := newDBSetServer(t)
	s.ignore = true
	res, _, err := s.run(t, "set app --option RECOVERY=FULL --allow admin")
	if err != nil || !slices.Equal(res.Warnings, []string{"RECOVERY is SIMPLE, not the FULL requested."}) {
		t.Errorf("%v, warnings %q", err, res.Warnings)
	}
}
