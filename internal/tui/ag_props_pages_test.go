package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Availability Group Properties, all three pages, driven end to end.
//
// ag_props_test.go covers the pieces in isolation — the routing-list parser,
// the preference table, the failover-mode gate, planAGRoutingOps' ordering.
// What only a page test can show is the statement that reaches the primary:
// which replica the ALTER names, and in what order the routing writes go out.
// Every one of these settings is cluster configuration, so the wrong replica
// is not a cosmetic mistake — it is the failover behaviour of a production
// group changed without anyone seeing it happen.

// selectReplica moves the grid cursor onto the named replica, which commits
// whatever the detail rows held for the row it left.
func selectReplica(t *testing.T, grid *controls.DataGrid, name string) {
	t.Helper()
	selectGridRow(t, grid, agReplicaNameCol, name)
}

// -- General -----------------------------------------------------------------

func loadAGGeneralPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form, *controls.DataGrid) {
	return loadAGPage(t, func(sc *db.ServerConn) propPage { return pageAGGeneral(sc, agFixtureName) })
}

// TestAGGeneralFailureConditionLevelWritesTheLevelNotTheIndex. The dropdown's
// rows are levels 1-5 and the row index is one less than the level, so an
// off-by-one here writes a different failure policy than the one on screen —
// and the labels all read plausibly whichever way round it is.
func TestAGGeneralFailureConditionLevelWritesTheLevelNotTheIndex(t *testing.T) {
	inst, apply, form, _ := loadAGGeneralPage(t)

	editSelect(t, form, "Failure condition level", "4 - Moderate server errors")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "ALTER AVAILABILITY GROUP [AAG1] SET (FAILURE_CONDITION_LEVEL = 4)")
}

// TestAGGeneralDTCSupportWritesTheKeyword. The checkbox is a bool and the
// server takes PER_DB or NONE, so the mapping is the page's.
func TestAGGeneralDTCSupportWritesTheKeyword(t *testing.T) {
	inst, apply, form, _ := loadAGGeneralPage(t)

	editCheck(t, form, "Per database DTC support", true)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "ALTER AVAILABILITY GROUP [AAG1] SET (DTC_SUPPORT = PER_DB)")
}

// TestAGGeneralRequiredSyncSecondariesIsCappedByTheReplicaCount. A synchronous
// secondary can only be required to commit if one exists; the ceiling is the
// replica count less the primary, and raising the setting past the number of
// healthy secondaries stops the primary accepting writes.
func TestAGGeneralRequiredSyncSecondariesIsCappedByTheReplicaCount(t *testing.T) {
	_, _, form, _ := loadAGGeneralPage(t)

	row := textRow(t, form, "Required sync secondaries")
	row.Edit("3")
	if err := row.Validate(); err == nil {
		t.Error("3 required sync secondaries was accepted with only two secondaries in the group")
	}
}

// TestAGGeneralWritesOnlyTheReplicaSettingThatChanged. Each replica setting is
// its own ALTER, and the page diffs against the values it loaded — writing all
// six would reassert settings someone else may have changed since.
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

// TestAGGeneralEditsLandOnTheReplicaTheRowWasOn is the commit-on-move
// contract, across two replicas: the detail rows belong to whichever row the
// cursor was on when the typing happened, and a page that filed them under the
// newly selected row would reconfigure the wrong member of the cluster.
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

// TestAGGeneralEverySettingWritesItsOwnOption. Six detail rows map onto six
// distinct ALTER options, three of which are nested inside a role clause. A
// row wired to the neighbouring setter still produces a statement the server
// accepts — it just changes something else.
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

// TestAGGeneralUntouchedPageWritesNothing. Six detail rows are seeded from the
// selected replica on load, and a value that did not survive that round trip
// would rewrite a live replica's configuration on every OK.
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

// TestAGBackupPreferenceWritesTheKeywordBehindTheLabel. agBackupPreferenceItems
// is a label/keyword table, and the radio is read back by index into it — the
// shape where a round-trip test proves nothing, since both halves would read
// the same rotated table. Naming the label and asserting the keyword is what
// catches it.
func TestAGBackupPreferenceWritesTheKeywordBehindTheLabel(t *testing.T) {
	inst, apply, form, _ := loadAGBackupPage(t)

	editRadio(t, form, "Where should backups occur?", "Secondary only")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "ALTER AVAILABILITY GROUP [AAG1] SET (AUTOMATED_BACKUP_PREFERENCE = SECONDARY_ONLY)")
}

// TestAGBackupPreferenceEveryLabelWritesItsOwnKeyword walks the whole table
// rather than one entry, since a rotation only shows up on the entries a
// single-value test does not visit.
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
			// The group loads on SECONDARY, so that one label is already
			// selected and cannot be made dirty — assert it is where the page
			// put the dot instead.
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

// TestAGBackupPriorityLandsOnTheReplicaTheRowIsOn — the third replica, whose
// priority differs from the other two, so a page reading back the wrong row
// writes a number that is visibly not the one on screen.
func TestAGBackupPriorityLandsOnTheReplicaTheRowIsOn(t *testing.T) {
	inst, apply, form, grid := loadAGBackupPage(t)

	selectReplica(t, grid, agAsyncPeer)
	editText(t, form, "Backup priority", "90")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "MODIFY REPLICA ON N'ubusql3' WITH (BACKUP_PRIORITY = 90)")
}

// TestAGBackupPriorityZeroExcludesTheReplica. 0 is the single value behind
// SSMS's separate Exclude Replica checkbox, and the grid's Excluded column
// only reports it — so the column has to follow the edit, or the page shows a
// replica as included while staging its exclusion.
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

// TestAGRoutingShowsEachReplicasOwnList. The list is a per-replica query, and
// the page would happily show one replica's list against all three if it were
// not scoped — which is also why the fixture answers it by replica id.
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

// TestAGRoutingWritesURLsBeforeListsAndClearsAfterThem. SQL Server refuses a
// routing list naming a replica with no routing URL, and refuses to clear a
// URL a list still references — so the ordering is the difference between the
// page working and the server rejecting it halfway through, leaving the group
// half-configured.
func TestAGRoutingWritesURLsBeforeListsAndClearsAfterThem(t *testing.T) {
	inst, apply, form, grid := loadAGRoutingPage(t)

	// Give the async peer a URL, point the primary's list at it alone, and
	// drop the URL the old list depended on.
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

// TestAGRoutingLoadBalancedSetKeepsItsGrouping. The parentheses the user types
// are the difference between priority order and a load-balanced set, and they
// have to survive into the T-SQL as nested parentheses.
func TestAGRoutingLoadBalancedSetKeepsItsGrouping(t *testing.T) {
	inst, apply, form, grid := loadAGRoutingPage(t)

	selectReplica(t, grid, agPrimary)
	editText(t, form, "Read-only routing list", "(ubusql2, ubusql3)")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, `READ_ONLY_ROUTING_LIST = ((N'ubusql2', N'ubusql3'))`)
}

// TestAGRoutingListLandsOnTheReplicaTheRowWasOn. A routing list is a
// primary-role property, so every replica needs its own for the group to keep
// routing after a failover — which means the page is edited on rows other than
// the current primary, and the commit has to follow the cursor.
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

// TestAGRoutingClearingAListWritesNONE. Neither NULL nor an empty string works
// for either of these — both are server errors — so "cleared" has to become
// the bare keyword.
func TestAGRoutingClearingAListWritesNONE(t *testing.T) {
	inst, apply, form, grid := loadAGRoutingPage(t)

	selectReplica(t, grid, agPrimary)
	editText(t, form, "Read-only routing list", "")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "PRIMARY_ROLE (READ_ONLY_ROUTING_LIST = NONE)")
}

// TestAGRoutingRejectsAListNamingSomethingThatIsNotAReplica at the row, not at
// Apply: a name the page accepted would become a routing list pointing at a
// replica that does not exist.
func TestAGRoutingRejectsAListNamingSomethingThatIsNotAReplica(t *testing.T) {
	_, _, form, grid := loadAGRoutingPage(t)

	selectReplica(t, grid, agPrimary)
	row := textRow(t, form, "Read-only routing list")
	row.Edit("ubusql9")
	if err := row.Validate(); err == nil {
		t.Error("a routing list naming a non-replica was accepted")
	}
}

// TestAGRoutingUntouchedPageWritesNothing. Both fields are seeded from the
// server — the list through formatRoutingListText, which has to round-trip
// with the parser, or opening the page and pressing OK rewrites every
// replica's routing.
func TestAGRoutingUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _, _ := loadAGRoutingPage(t)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}
