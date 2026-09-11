package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Availability Group Properties, all three pages, end to end. ag_props_test.go
// covers the parts; this checks the statement that reaches the primary — which
// replica each ALTER names and routing write order. The wrong replica silently
// changes a production group's failover behaviour.

// selectReplica moves the cursor onto a replica, committing the detail rows for
// the row left.
func selectReplica(t *testing.T, grid *controls.DataGrid, name string) {
	t.Helper()
	selectGridRow(t, grid, agReplicaNameCol, name)
}

// -- General -----------------------------------------------------------------

func loadAGGeneralPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form, *controls.DataGrid) {
	return loadAGPage(t, func(sc *db.ServerConn) propPage { return pageAGGeneral(sc, agFixtureName) })
}

// Rows are levels 1-5 at index+1; an off-by-one writes a different,
// plausible-looking policy.
func TestAGGeneralFailureConditionLevelWritesTheLevelNotTheIndex(t *testing.T) {
	inst, apply, form, _ := loadAGGeneralPage(t)

	editSelect(t, form, "Failure condition level", "4 - Moderate server errors")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "ALTER AVAILABILITY GROUP [AAG1] SET (FAILURE_CONDITION_LEVEL = 4)")
}

// The checkbox maps to PER_DB or NONE.
func TestAGGeneralDTCSupportWritesTheKeyword(t *testing.T) {
	inst, apply, form, _ := loadAGGeneralPage(t)

	editCheck(t, form, "Per database DTC support", true)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "ALTER AVAILABILITY GROUP [AAG1] SET (DTC_SUPPORT = PER_DB)")
}

// The ceiling is the replica count less the primary; requiring more than the
// healthy secondaries stops the primary accepting writes.
func TestAGGeneralRequiredSyncSecondariesIsCappedByTheReplicaCount(t *testing.T) {
	_, _, form, _ := loadAGGeneralPage(t)

	row := textRow(t, form, "Required sync secondaries")
	row.Edit("3")
	if err := row.Validate(); err == nil {
		t.Error("3 required sync secondaries was accepted with only two secondaries in the group")
	}
}

// Each setting is its own ALTER, diffed against loaded values; writing all six
// would reassert settings others may have changed.
func TestAGGeneralWritesOnlyTheReplicaSettingThatChanged(t *testing.T) {
	inst, apply, form, grid := loadAGGeneralPage(t)

	selectReplica(t, grid, agAsyncPeer)
	editSelect(t, form, "Availability mode", "SYNCHRONOUS_COMMIT")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst,
		"ALTER AVAILABILITY GROUP [AAG1] MODIFY REPLICA ON N'ubusql3' WITH (AVAILABILITY_MODE = SYNCHRONOUS_COMMIT)")
}

// Detail rows belong to the row the cursor was on while typing; filing them
// under the new row reconfigures the wrong replica.
func TestAGGeneralEditsLandOnTheReplicaTheRowWasOn(t *testing.T) {
	inst, apply, form, grid := loadAGGeneralPage(t)

	selectReplica(t, grid, agSecondary)
	editSelect(t, form, "Readable secondary", "ALL")
	selectReplica(t, grid, agAsyncPeer)
	editSelect(t, form, "Failover mode", "AUTOMATIC")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("want two statements, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	want := []string{
		"MODIFY REPLICA ON N'ubusql2' WITH (SECONDARY_ROLE (ALLOW_CONNECTIONS = ALL))",
		"MODIFY REPLICA ON N'ubusql3' WITH (FAILOVER_MODE = AUTOMATIC)",
	}
	for i, w := range want {
		if !strings.Contains(stmts[i], w) {
			t.Errorf("statement %d:\n%s\nwant it to contain: %s", i+1, stmts[i], w)
		}
	}
}

// Six rows map to six ALTER options (three inside a role clause); a row wired
// to its neighbour's setter still produces an accepted statement.
func TestAGGeneralEverySettingWritesItsOwnOption(t *testing.T) {
	inst, apply, form, grid := loadAGGeneralPage(t)

	selectReplica(t, grid, agAsyncPeer)
	editSelect(t, form, "Availability mode", "SYNCHRONOUS_COMMIT")
	editSelect(t, form, "Failover mode", "AUTOMATIC")
	editSelect(t, form, "Connections in primary role", "READ_WRITE")
	editSelect(t, form, "Readable secondary", "READ_ONLY")
	editSelect(t, form, "Seeding mode", "AUTOMATIC")
	editText(t, form, "Session timeout", "45")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 6 {
		t.Fatalf("want six statements, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	want := []string{
		"AVAILABILITY_MODE = SYNCHRONOUS_COMMIT",
		"FAILOVER_MODE = AUTOMATIC",
		"PRIMARY_ROLE (ALLOW_CONNECTIONS = READ_WRITE)",
		"SECONDARY_ROLE (ALLOW_CONNECTIONS = READ_ONLY)",
		"SEEDING_MODE = AUTOMATIC",
		"SESSION_TIMEOUT = 45",
	}
	for i, w := range want {
		if !strings.Contains(stmts[i], w) {
			t.Errorf("statement %d:\n%s\nwant it to contain: %s", i+1, stmts[i], w)
		}
		if !strings.Contains(stmts[i], "MODIFY REPLICA ON N'ubusql3'") {
			t.Errorf("statement %d names the wrong replica:\n%s", i+1, stmts[i])
		}
	}
}

// Six detail rows are seeded on load; a value that doesn't round-trip rewrites
// a live replica on every OK.
func TestAGGeneralUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _, _ := loadAGGeneralPage(t)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// -- Backup Preferences ------------------------------------------------------

func loadAGBackupPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form, *controls.DataGrid) {
	return loadAGPage(t, func(sc *db.ServerConn) propPage { return pageAGBackupPreferences(sc, agFixtureName) })
}

// The radio is read back by index into a label/keyword table; name the label
// and assert the keyword, since a round trip can't see a rotation.
func TestAGBackupPreferenceWritesTheKeywordBehindTheLabel(t *testing.T) {
	inst, apply, form, _ := loadAGBackupPage(t)

	editRadio(t, form, "Where should backups occur?", "Secondary only")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "ALTER AVAILABILITY GROUP [AAG1] SET (AUTOMATED_BACKUP_PREFERENCE = SECONDARY_ONLY)")
}

// Walk the whole table; a rotation shows only on entries a single test skips.
func TestAGBackupPreferenceEveryLabelWritesItsOwnKeyword(t *testing.T) {
	want := map[string]string{
		"Prefer Secondary": "SECONDARY",
		"Secondary only":   "SECONDARY_ONLY",
		"Primary":          "PRIMARY",
		"Any Replica":      "NONE",
	}
	for label, keyword := range want {
		t.Run(label, func(t *testing.T) {
			inst, apply, form, _ := loadAGBackupPage(t)
			// The group loads on SECONDARY, which can't be made dirty; assert
			// it's selected instead.
			if keyword == "SECONDARY" {
				if got := radioRow(t, form, "Where should backups occur?").Selected(); got != 0 {
					t.Fatalf("a group set to SECONDARY selects option %d, want %q", got, label)
				}
				return
			}
			editRadio(t, form, "Where should backups occur?", label)
			if err := apply(t.Context()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			assertOneStatement(t, inst, "AUTOMATED_BACKUP_PREFERENCE = "+keyword+")")
		})
	}
}

// The third replica, whose priority differs, so reading the wrong row is
// visible.
func TestAGBackupPriorityLandsOnTheReplicaTheRowIsOn(t *testing.T) {
	inst, apply, form, grid := loadAGBackupPage(t)

	selectReplica(t, grid, agAsyncPeer)
	editText(t, form, "Backup priority", "90")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "MODIFY REPLICA ON N'ubusql3' WITH (BACKUP_PRIORITY = 90)")
}

// 0 is SSMS's Exclude Replica; the Excluded column must follow the edit.
func TestAGBackupPriorityZeroExcludesTheReplica(t *testing.T) {
	inst, apply, form, grid := loadAGBackupPage(t)

	selectReplica(t, grid, agSecondary)
	editText(t, form, "Backup priority", "0")
	selectReplica(t, grid, agAsyncPeer)

	row := grid.Row(gridRowIndex(t, grid, agReplicaNameCol, agSecondary))
	if got := row[2]; got != boolStr(true) {
		t.Errorf("the Excluded column for %s reads %q after setting priority 0", agSecondary, got)
	}
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "MODIFY REPLICA ON N'ubusql2' WITH (BACKUP_PRIORITY = 0)")
}

// TestAGBackupPreferencesUntouchedPageWritesNothing.
func TestAGBackupPreferencesUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _, _ := loadAGBackupPage(t)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// -- Read-Only Routing -------------------------------------------------------

func loadAGRoutingPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form, *controls.DataGrid) {
	return loadAGPage(t, func(sc *db.ServerConn) propPage { return pageAGReadOnlyRouting(sc, agFixtureName) })
}

// The list is per replica; unscoped, one list would show for all three (hence
// the fixture answers by replica id).
func TestAGRoutingShowsEachReplicasOwnList(t *testing.T) {
	_, _, _, grid := loadAGRoutingPage(t)

	const listCol = 2
	want := map[string]string{
		agPrimary:   "ubusql2, ubusql3",
		agSecondary: "",
		agAsyncPeer: "",
	}
	for name, list := range want {
		row := grid.Row(gridRowIndex(t, grid, agReplicaNameCol, name))
		if row[listCol] != list {
			t.Errorf("replica %s shows routing list %q, want %q", name, row[listCol], list)
		}
	}
}

// SQL Server refuses a list naming a replica without a URL and refuses to clear
// a URL a list still names, so order decides whether Apply works or fails
// halfway.
func TestAGRoutingWritesURLsBeforeListsAndClearsAfterThem(t *testing.T) {
	inst, apply, form, grid := loadAGRoutingPage(t)

	// Give the async peer a URL, point the primary's list at it alone, drop the
	// old URL.
	selectReplica(t, grid, agAsyncPeer)
	editText(t, form, "Read-only routing URL", "TCP://ubusql3:1433")
	selectReplica(t, grid, agSecondary)
	editText(t, form, "Read-only routing URL", "")
	selectReplica(t, grid, agPrimary)
	editText(t, form, "Read-only routing list", agAsyncPeer)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 3 {
		t.Fatalf("want three statements, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	want := []string{
		"MODIFY REPLICA ON N'ubusql3' WITH (SECONDARY_ROLE (READ_ONLY_ROUTING_URL = N'TCP://ubusql3:1433'))",
		`MODIFY REPLICA ON N'FAKE\SQL' WITH (PRIMARY_ROLE (READ_ONLY_ROUTING_LIST = (N'ubusql3')))`,
		"MODIFY REPLICA ON N'ubusql2' WITH (SECONDARY_ROLE (READ_ONLY_ROUTING_URL = NONE))",
	}
	for i, w := range want {
		if !strings.Contains(stmts[i], w) {
			t.Errorf("statement %d:\n%s\nwant it to contain: %s", i+1, stmts[i], w)
		}
	}
}

// Typed parentheses (load-balanced set vs priority order) must survive as
// nested parentheses.
func TestAGRoutingLoadBalancedSetKeepsItsGrouping(t *testing.T) {
	inst, apply, form, grid := loadAGRoutingPage(t)

	selectReplica(t, grid, agPrimary)
	editText(t, form, "Read-only routing list", "(ubusql2, ubusql3)")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, `READ_ONLY_ROUTING_LIST = ((N'ubusql2', N'ubusql3'))`)
}

// A routing list is a primary-role property every replica needs for after
// failover, so edits happen on non-primary rows and must follow the cursor.
func TestAGRoutingListLandsOnTheReplicaTheRowWasOn(t *testing.T) {
	inst, apply, form, grid := loadAGRoutingPage(t)

	selectReplica(t, grid, agAsyncPeer)
	editText(t, form, "Read-only routing list", agSecondary)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst,
		`MODIFY REPLICA ON N'ubusql3' WITH (PRIMARY_ROLE (READ_ONLY_ROUTING_LIST = (N'ubusql2')))`)
}

// Neither NULL nor "" works; cleared must become the NONE keyword.
func TestAGRoutingClearingAListWritesNONE(t *testing.T) {
	inst, apply, form, grid := loadAGRoutingPage(t)

	selectReplica(t, grid, agPrimary)
	editText(t, form, "Read-only routing list", "")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "PRIMARY_ROLE (READ_ONLY_ROUTING_LIST = NONE)")
}

// A name that isn't a replica is rejected at the row, not at Apply.
func TestAGRoutingRejectsAListNamingSomethingThatIsNotAReplica(t *testing.T) {
	_, _, form, grid := loadAGRoutingPage(t)

	selectReplica(t, grid, agPrimary)
	row := textRow(t, form, "Read-only routing list")
	row.Edit("ubusql9")
	if err := row.Validate(); err == nil {
		t.Error("a routing list naming a non-replica was accepted")
	}
}

// The list is seeded via formatRoutingListText, which must round-trip with the
// parser or OK rewrites every replica's routing.
func TestAGRoutingUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _, _ := loadAGRoutingPage(t)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}
