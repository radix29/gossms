package tui

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The routing-list text field is the only place in this dialog where the user
// types something that has to become T-SQL structure rather than a value, so
// the parser carries the risk: a name it silently accepts becomes a routing
// list pointing at a replica that doesn't exist.

func TestParseRoutingListText(t *testing.T) {
	replicas := []string{"ubusql1", "ubusql2", "ubusql3"}
	tests := []struct {
		name string
		in   string
		want [][]string
	}{
		{"empty means no routing", "", nil},
		{"whitespace only means no routing", "   ", nil},
		{"single replica", "ubusql2", [][]string{{"ubusql2"}}},
		{"priority order", "ubusql2, ubusql3", [][]string{{"ubusql2"}, {"ubusql3"}}},
		{"load balanced set", "(ubusql2, ubusql3)", [][]string{{"ubusql2", "ubusql3"}}},
		{"mixed", "ubusql1, (ubusql2, ubusql3)", [][]string{{"ubusql1"}, {"ubusql2", "ubusql3"}}},
		{"tolerates missing spaces", "ubusql1,(ubusql2,ubusql3)", [][]string{{"ubusql1"}, {"ubusql2", "ubusql3"}}},
		// The names written back are the replica's own spelling, not the
		// user's: a routing list is matched by name on the server.
		{"normalizes case", "UBUSQL2", [][]string{{"ubusql2"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRoutingListText(tt.in, replicas)
			if err != nil {
				t.Fatalf("parseRoutingListText(%q): %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseRoutingListText(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseRoutingListTextRejects(t *testing.T) {
	replicas := []string{"ubusql1", "ubusql2"}
	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{"unknown replica", "ubusql9", "not a replica"},
		{"duplicate replica", "ubusql1, ubusql1", "more than once"},
		{"duplicate across a set", "ubusql1, (ubusql1, ubusql2)", "more than once"},
		{"unclosed set", "(ubusql1", "missing"},
		{"empty set", "()", "empty load-balanced set"},
		{"missing comma", "ubusql1 (ubusql2)", "separate entries with commas"},
		{"empty entry", "ubusql1,,ubusql2", "empty replica name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseRoutingListText(tt.in, replicas)
			if err == nil {
				t.Fatalf("parseRoutingListText(%q) = nil error, want one", tt.in)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestRoutingListTextRoundTrips(t *testing.T) {
	// The page compares the edited text against the text it rendered to decide
	// whether anything changed, so format(parse(x)) has to be stable or every
	// replica reads as dirty the moment the page loads.
	replicas := []string{"a", "b", "c"}
	for _, in := range []string{"", "a", "a, b", "(a, b)", "a, (b, c)"} {
		list, err := parseRoutingListText(in, replicas)
		if err != nil {
			t.Fatalf("parse(%q): %v", in, err)
		}
		if got := formatRoutingListText(list); got != in {
			t.Errorf("format(parse(%q)) = %q, want %q", in, got, in)
		}
	}
}

func TestBackupPreferenceRadioMatchesKeywords(t *testing.T) {
	for _, pref := range []string{"SECONDARY", "SECONDARY_ONLY", "PRIMARY", "NONE"} {
		i := agBackupPreferenceIndex(pref)
		if got := agBackupPreferenceItems[i].keyword; got != pref {
			t.Errorf("agBackupPreferenceIndex(%q) selected %q", pref, got)
		}
	}
	// An empty or unrecognized preference must land on SQL Server's own
	// default rather than on whatever happens to be first for another reason.
	for _, pref := range []string{"", "SOMETHING_NEW"} {
		if got := agBackupPreferenceItems[agBackupPreferenceIndex(pref)].keyword; got != "SECONDARY" {
			t.Errorf("agBackupPreferenceIndex(%q) selected %q, want the SECONDARY default", pref, got)
		}
	}
	if len(agBackupPreferenceLabels()) != len(agBackupPreferenceItems) {
		t.Error("label list and keyword list have drifted apart")
	}
}

func TestFailureConditionIndexClamps(t *testing.T) {
	for level := 1; level <= 5; level++ {
		if got := agFailureConditionIndex(level); got != level-1 {
			t.Errorf("agFailureConditionIndex(%d) = %d, want %d", level, got, level-1)
		}
	}
	// A level outside the documented range must not reach the widget as a
	// negative or past-the-end index.
	for _, level := range []int{-1, 0, 6, 99} {
		if got := agFailureConditionIndex(level); got < 0 || got >= len(agFailureConditionItems) {
			t.Errorf("agFailureConditionIndex(%d) = %d, out of range", level, got)
		}
	}
}

func TestAGSelectKeepsUnknownServerValues(t *testing.T) {
	// SeedingMode is empty before SQL Server 2016. indexOf's not-found 0 would
	// show "AUTOMATIC" as though the server had said so, and applying it would
	// write a setting the user never chose.
	row := propsheet.Select("Seeding mode", agSeedingModeItems, 0)
	agSetSelect(row, agSeedingModeItems, "")
	if row.Value() != agUnknownItem {
		t.Errorf("empty server value shows as %q, want %q", row.Value(), agUnknownItem)
	}
	if got := agSelectValue(row); got != "" {
		t.Errorf("agSelectValue on the stand-in = %q, want \"\" so it reads as unchanged", got)
	}
	if row.Dirty() {
		t.Error("a row showing the unknown stand-in reports dirty, so Apply would write it")
	}

	// A value this build doesn't know about is still shown verbatim rather
	// than replaced by the first known keyword.
	agSetSelect(row, agSeedingModeItems, "SOMETHING_NEW")
	if row.Value() != "SOMETHING_NEW" {
		t.Errorf("unknown keyword shows as %q, want it verbatim", row.Value())
	}
	if got := agSelectValue(row); got != "SOMETHING_NEW" {
		t.Errorf("agSelectValue = %q, want it round-tripped", got)
	}

	// A known value selects normally and leaves the list alone.
	agSetSelect(row, agSeedingModeItems, "MANUAL")
	if row.Value() != "MANUAL" {
		t.Errorf("known value shows as %q, want MANUAL", row.Value())
	}
}

func TestFailoverModeDropdownIsGatedByClusterType(t *testing.T) {
	// The General page narrows the failover-mode list to what the cluster type
	// accepts, so an illegal value cannot be picked. What must survive the
	// narrowing is a replica that *already* holds an illegal value: it has to
	// stay visible and correctable, not be reported as one of the legal ones.
	for _, tc := range []struct {
		clusterType string
		want        []string
	}{
		{"EXTERNAL", []string{"EXTERNAL"}},
		{"NONE", []string{"MANUAL"}},
		{"WSFC", []string{"MANUAL", "AUTOMATIC"}},
		{"", []string{"MANUAL", "AUTOMATIC"}}, // not reported before SQL Server 2017
	} {
		items, _ := agFailoverModesFor(tc.clusterType)
		if !slices.Equal(items, tc.want) {
			t.Errorf("cluster type %q offers %v, want %v", tc.clusterType, items, tc.want)
		}
	}

	external, _ := agFailoverModesFor("EXTERNAL")
	row := propsheet.Select("Failover mode", external, 0)

	agSetSelect(row, external, "AUTOMATIC")
	if row.Value() != "AUTOMATIC" {
		t.Errorf("a replica already set to AUTOMATIC under EXTERNAL shows as %q, want it verbatim", row.Value())
	}
	if !slices.Contains(row.Items(), "EXTERNAL") {
		t.Errorf("items %v lost the legal value, so the user cannot correct the replica", row.Items())
	}
	if row.Dirty() {
		t.Error("merely displaying the stored value reports dirty, so Apply would rewrite an untouched replica")
	}

	// A replica already legal sees only the one legal choice.
	agSetSelect(row, external, "EXTERNAL")
	if got := row.Items(); !slices.Equal(got, []string{"EXTERNAL"}) {
		t.Errorf("items %v, want only EXTERNAL — Msg 47101 rejects the rest", got)
	}
}

func TestAGReplicaEditDirtyTracksEveryField(t *testing.T) {
	base := &gosmo.AvailabilityReplica{
		ReplicaServerName: "ubusql1",
		AvailabilityMode:  "SYNCHRONOUS_COMMIT", FailoverMode: "EXTERNAL",
		PrimaryRoleAllowConnections: "ALL", SecondaryRoleAllowConnections: "NO",
		SeedingMode: "AUTOMATIC", SessionTimeout: 10,
	}
	if agReplicaEditFrom(base).dirty() {
		t.Fatal("a freshly loaded replica edit reports dirty")
	}
	mutations := map[string]func(*agReplicaEdit){
		"availability mode": func(e *agReplicaEdit) { e.availabilityMode = "ASYNCHRONOUS_COMMIT" },
		"failover mode":     func(e *agReplicaEdit) { e.failoverMode = "MANUAL" },
		"primary role":      func(e *agReplicaEdit) { e.primaryRole = "READ_WRITE" },
		"secondary role":    func(e *agReplicaEdit) { e.secondaryRole = "ALL" },
		"seeding mode":      func(e *agReplicaEdit) { e.seedingMode = "MANUAL" },
		"session timeout":   func(e *agReplicaEdit) { e.sessionTimeout = 20 },
	}
	for name, mutate := range mutations {
		e := agReplicaEditFrom(base)
		mutate(e)
		if !e.dirty() {
			t.Errorf("changing the %s does not mark the edit dirty, so Apply would skip it", name)
		}
	}
}

func TestAGDatabaseRowsKeepDivergentStates(t *testing.T) {
	dbs := []*gosmo.AvailabilityDatabase{
		{DatabaseName: "testdb_1", ReplicaServerName: "ubusql1", SynchronizationState: "SYNCHRONIZED", SynchronizationHealth: "HEALTHY"},
		{DatabaseName: "testdb_1", ReplicaServerName: "ubusql2", SynchronizationState: "SYNCHRONIZING", SynchronizationHealth: "PARTIALLY_HEALTHY"},
		// A database in the group's cluster metadata but not yet seeded
		// anywhere has no state at all, and must still be listed.
		{DatabaseName: "testdb_2", ReplicaServerName: "ubusql1"},
	}
	rows := agDatabaseRows(dbs)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want one per database: %v", len(rows), rows)
	}
	if rows[0][0] != "testdb_1" || rows[0][1] != "SYNCHRONIZED, SYNCHRONIZING" {
		t.Errorf("divergent states collapsed: %v", rows[0])
	}
	if rows[1][0] != "testdb_2" || !strings.Contains(rows[1][1], "not seeded") {
		t.Errorf("unseeded database row = %v, want it named and explained", rows[1])
	}
}

func TestSummarizeAGDatabasesPreservesQueryOrder(t *testing.T) {
	dbs := []*gosmo.AvailabilityDatabase{
		{DatabaseName: "zeta", ReplicaServerName: "a", SynchronizationState: "SYNCHRONIZED"},
		{DatabaseName: "alpha", ReplicaServerName: "a", SynchronizationState: "SYNCHRONIZED"},
		{DatabaseName: "zeta", ReplicaServerName: "b", SynchronizationState: "SYNCHRONIZED", IsSuspended: true},
	}
	got := summarizeAGDatabases(dbs)
	if len(got) != 2 || got[0].Name != "zeta" || got[1].Name != "alpha" {
		t.Fatalf("summarizeAGDatabases = %+v, want zeta then alpha (the query's order)", got)
	}
	if !got[0].Suspended {
		t.Error("a database suspended on any replica must be reported suspended")
	}
	if len(got[0].States) != 1 {
		t.Errorf("identical states across replicas listed %d times", len(got[0].States))
	}
}

// The Properties dialog is reachable only from the availability group node's
// context menu, so a missing case there makes the whole dialog unreachable.
func TestAvailabilityGroupNodeOffersProperties(t *testing.T) {
	a := &App{}
	node := &explorerNode{}
	node.data.Type = NodeAvailabilityGroup
	node.data.AGName = "AAG1"

	var labels []string
	for _, item := range a.contextMenuItemsForNode(node) {
		labels = append(labels, item.Label)
	}
	if !slicesContains(labels, "Properties...") {
		t.Errorf("availability group context menu = %v, want a Properties... item", labels)
	}
}

func TestAGRoutingOpsAreOrderedAroundTheServersValidation(t *testing.T) {
	// SQL Server refuses a routing list naming a replica with no routing URL,
	// and refuses to clear a URL a list still names. Both directions have to
	// work in one Apply, which is only possible in this order — the first
	// version of this code wrote each replica's URL and list together and
	// failed against the live cluster on the very first Apply.
	setURL := &agRoutingEdit{name: "b", url: "TCP://b:1433"}
	clearURL := &agRoutingEdit{name: "c", origURL: "TCP://c:1433"}
	changeList := &agRoutingEdit{name: "a", list: "b"}

	// Deliberately ordered worst-case: the list edit comes first, and the
	// cleared URL before the set one.
	ops := planAGRoutingOps([]*agRoutingEdit{changeList, clearURL, setURL})
	if len(ops) != 3 {
		t.Fatalf("planned %d ops, want 3: %+v", len(ops), ops)
	}
	if ops[0].isList || ops[0].edit != setURL {
		t.Errorf("op 0 = %+v, want the URL being set", ops[0])
	}
	if !ops[1].isList || ops[1].edit != changeList {
		t.Errorf("op 1 = %+v, want the routing list", ops[1])
	}
	if ops[2].isList || ops[2].edit != clearURL {
		t.Errorf("op 2 = %+v, want the URL being cleared", ops[2])
	}

	if ops := planAGRoutingOps([]*agRoutingEdit{{name: "a", url: "x", origURL: "x", list: "y", origList: "y"}}); len(ops) != 0 {
		t.Errorf("an unchanged replica planned %d ops, want none", len(ops))
	}
}
