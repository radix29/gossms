package tui

import (
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// The Always On tree is mostly label rendering over data only a live cluster
// produces, so these tests pin the part that carries judgement: what a label
// claims when the DMVs report *less* than the full picture. Getting that wrong
// does not crash — it asserts a healthy topology that isn't there, which is
// the failure mode worth a test.

func TestAGLabelDistinguishesUnknownPrimaryFromSecondary(t *testing.T) {
	tests := []struct {
		name           string
		primaryReplica string
		isLocalPrimary bool
		want           string
	}{
		{"local instance is the primary", "ubusql1", true, "AAG1 (Primary)"},
		{"primary is elsewhere", "ubusql1", false, "AAG1 (Secondary)"},
		// The one that matters: no primary visible is not the same as being a
		// secondary, and must not render as one.
		{"no primary visible", "", false, "AAG1 (Not synchronizing)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agLabel("AAG1", tt.primaryReplica, tt.isLocalPrimary); got != tt.want {
				t.Errorf("agLabel(%q, %v) = %q, want %q", tt.primaryReplica, tt.isLocalPrimary, got, tt.want)
			}
		})
	}
}

func TestReplicaLabelOmitsRoleWhenDMVHasNone(t *testing.T) {
	// A secondary sees no state row for its peers, so Role is empty. The label
	// must fall back to the mode alone rather than rendering an empty "()".
	r := &gosmo.AvailabilityReplica{
		ReplicaServerName: "ubusql1",
		AvailabilityMode:  "SYNCHRONOUS_COMMIT",
	}
	if got, want := replicaLabel(r), "ubusql1 (Synchronous commit)"; got != want {
		t.Errorf("replicaLabel with no role = %q, want %q", got, want)
	}

	r.Role = "PRIMARY"
	if got, want := replicaLabel(r), "ubusql1 (Primary, Synchronous commit)"; got != want {
		t.Errorf("replicaLabel with role = %q, want %q", got, want)
	}

	bare := &gosmo.AvailabilityReplica{ReplicaServerName: "ubusql3"}
	if got, want := replicaLabel(bare), "ubusql3"; got != want {
		t.Errorf("replicaLabel with neither role nor mode = %q, want %q", got, want)
	}
}

func TestCommitModeName(t *testing.T) {
	for in, want := range map[string]string{
		"SYNCHRONOUS_COMMIT":  "Synchronous commit",
		"ASYNCHRONOUS_COMMIT": "Asynchronous commit",
		"CONFIGURATION_ONLY":  "Configuration only",
		"SOMETHING_NEW":       "Something new",
	} {
		if got := commitModeName(in); got != want {
			t.Errorf("commitModeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAGDatabaseLabelShowsEveryDistinctState(t *testing.T) {
	// Collapsing divergent states to one would hide exactly the replica that
	// is behind, which is the whole reason to look at this folder.
	got := agDatabaseLabel("testdb_1", []string{"SYNCHRONIZED", "SYNCHRONIZING"}, false, false)
	if want := "testdb_1 (Synchronized, Synchronizing)"; got != want {
		t.Errorf("agDatabaseLabel = %q, want %q", got, want)
	}

	if got, want := agDatabaseLabel("testdb_1", []string{"SYNCHRONIZED"}, false, false), "testdb_1 (Synchronized)"; got != want {
		t.Errorf("single state: %q, want %q", got, want)
	}
	if got := agDatabaseLabel("testdb_1", []string{"NOT SYNCHRONIZING"}, true, true); !strings.Contains(got, "Suspended") {
		t.Errorf("suspended database label %q does not mention Suspended", got)
	}
	// Suspended already explains the unhealthy state; saying both is noise.
	if got := agDatabaseLabel("testdb_1", nil, true, true); strings.Contains(got, "Not healthy") {
		t.Errorf("label %q reports both Suspended and Not healthy", got)
	}
	if got, want := agDatabaseLabel("testdb_1", nil, false, false), "testdb_1"; got != want {
		t.Errorf("no state at all: %q, want %q", got, want)
	}
}

func TestListenerLabel(t *testing.T) {
	li := &gosmo.AvailabilityGroupListener{
		DNSName: "ubuaag", Port: 1433,
		IPAddresses: []gosmo.AvailabilityListenerIP{{IPAddress: "192.168.178.99", SubnetMask: "255.255.255.0"}},
	}
	if got, want := listenerLabel(li), "ubuaag (192.168.178.99, port 1433)"; got != want {
		t.Errorf("listenerLabel = %q, want %q", got, want)
	}

	dhcp := &gosmo.AvailabilityGroupListener{
		DNSName: "l2", Port: 5000,
		IPAddresses: []gosmo.AvailabilityListenerIP{{IsDHCP: true}},
	}
	if got, want := listenerLabel(dhcp), "l2 (DHCP, port 5000)"; got != want {
		t.Errorf("DHCP listener = %q, want %q", got, want)
	}

	// A listener with no address rows still has to render its port rather than
	// an empty parenthesis.
	none := &gosmo.AvailabilityGroupListener{DNSName: "l3", Port: 1433}
	if got, want := listenerLabel(none), "l3 (port 1433)"; got != want {
		t.Errorf("addressless listener = %q, want %q", got, want)
	}
}

func TestAGViewNoteReportsProvenance(t *testing.T) {
	l := loaderCtx{}

	// Read locally: no note, because there is nothing to explain.
	local := agView{ag: &gosmo.AvailabilityGroup{Name: "AAG1"}}
	if n := local.followNote(l); n != nil {
		t.Errorf("local read produced note %q, want none", n.label)
	}

	followed := agView{ag: &gosmo.AvailabilityGroup{Name: "AAG1", PrimaryReplicaServerName: "ubusql1"}, followed: true}
	n := followed.followNote(l)
	if n == nil || !strings.Contains(n.label, "ubusql1") {
		t.Fatalf("followed read note = %v, want one naming ubusql1", n)
	}

	// The degraded case must be visibly an error, not a neutral note: the tree
	// below it is incomplete and silently showing blanks is the bug this
	// guards against.
	partial := agView{ag: &gosmo.AvailabilityGroup{Name: "AAG1"}, unreachable: "ubusql1"}
	pn := partial.followNote(l)
	if pn == nil {
		t.Fatal("unreachable primary produced no note")
	}
	if pn.data.Type != NodeError {
		t.Errorf("unreachable-primary note has type %v, want NodeError", pn.data.Type)
	}
	if !strings.Contains(pn.label, "ubusql1") || !strings.Contains(pn.label, "partial") {
		t.Errorf("unreachable-primary note = %q, want it to name the host and say it is partial", pn.label)
	}
}

// The Always On folder must be reachable from the server root, and must be a
// sibling of Databases rather than nested under Server Objects.
func TestServerRootIncludesAlwaysOn(t *testing.T) {
	children, err := loadServerChildren(loaderCtx{}, nil)
	if err != nil {
		t.Fatalf("loadServerChildren: %v", err)
	}
	var found *explorerNode
	for _, c := range children {
		if c.data.Type == NodeAlwaysOn {
			found = c
		}
	}
	if found == nil {
		t.Fatal("server root has no NodeAlwaysOn child")
	}
	if found.label != alwaysOnRootLabel {
		t.Errorf("Always On node label = %q, want %q", found.label, alwaysOnRootLabel)
	}
	if _, ok := childLoaders[NodeAlwaysOn]; !ok {
		t.Error("NodeAlwaysOn has no registered child loader, so the folder would expand to nothing")
	}
}

// Every Always On container must have a loader, and every leaf must not claim
// to have children — a leaf that does renders a permanently unexpandable [+].
func TestAlwaysOnNodeTypesAreWiredConsistently(t *testing.T) {
	containers := []NodeType{
		NodeAlwaysOn, NodeAvailabilityGroups, NodeAvailabilityGroup,
		NodeAvailabilityReplicas, NodeAvailabilityDatabases, NodeAGListeners,
	}
	for _, nt := range containers {
		if _, ok := childLoaders[nt]; !ok {
			t.Errorf("container node type %v has no child loader", nt)
		}
		if !hasChildren(nt) {
			t.Errorf("container node type %v reports hasChildren=false", nt)
		}
	}

	leaves := []NodeType{NodeAvailabilityReplica, NodeAvailabilityDatabase, NodeAGListener}
	for _, nt := range leaves {
		if hasChildren(nt) {
			t.Errorf("leaf node type %v reports hasChildren=true, so it draws an expander that never fills", nt)
		}
	}
}

// A database is in the group cluster-wide from the moment ADD DATABASE runs on
// the primary, so the local copy of one that has not been joined yet still
// produces a row — with no synchronization state, because the DMV has no row
// for it. Reading that empty state as "joined" would offer Suspend on a
// database that is not in the group here, and hide the Join that is the only
// thing to do with it.
func TestAGJoinedCopiesReadsTheLocalRowsOnly(t *testing.T) {
	dbs := []*gosmo.AvailabilityDatabase{
		{ReplicaServerName: "ubusql1", DatabaseName: "testdb_1", SynchronizationState: "SYNCHRONIZED"},
		{ReplicaServerName: "ubusql2", DatabaseName: "testdb_1", SynchronizationState: "SYNCHRONIZED"},
		{ReplicaServerName: "ubusql1", DatabaseName: "testdb_2", SynchronizationState: "SYNCHRONIZED"},
		{ReplicaServerName: "ubusql2", DatabaseName: "testdb_2", SynchronizationState: ""},
	}
	joined := agJoinedCopies(dbs, "ubusql2")
	if !joined["testdb_1"] {
		t.Error("testdb_1 reads as not joined on ubusql2")
	}
	if joined["testdb_2"] {
		t.Error("testdb_2 has no state row on ubusql2 but reads as joined")
	}
	if len(joined) != 2 {
		t.Errorf("joined = %v, want only ubusql2's two rows", joined)
	}
}

// The catalog reports instance names in whatever case they were registered
// with, and @@SERVERNAME need not match it — a case-sensitive comparison here
// leaves every database looking unjoined and offers Join on all of them.
func TestAGJoinedCopiesMatchesTheReplicaNameCaseInsensitively(t *testing.T) {
	dbs := []*gosmo.AvailabilityDatabase{
		{ReplicaServerName: "UBUSQL2", DatabaseName: "TestDB_1", SynchronizationState: "SYNCHRONIZED"},
	}
	joined := agJoinedCopies(dbs, "ubusql2")
	if !joined["testdb_1"] {
		t.Errorf("joined = %v, want testdb_1 keyed lower-case and matched", joined)
	}
}

// The peer cache is dropped on a Refresh anywhere in the Always On subtree —
// that is the only tree whose loaders read through db.ServerConn.Peer, and the
// only place a user who has just repaired the network has to be able to say
// "try again" instead of waiting out peerFailureTTL.
//
// Pinned by name rather than by the contiguous run of NodeType values it
// happens to be: a type inserted between them would silently join or leave the
// set.
func TestIsAlwaysOnNodeCoversTheSubtree(t *testing.T) {
	in := []NodeType{
		NodeAlwaysOn, NodeAvailabilityGroups, NodeAvailabilityGroup,
		NodeAvailabilityReplicas, NodeAvailabilityReplica,
		NodeAvailabilityDatabases, NodeAvailabilityDatabase,
		NodeAGListeners, NodeAGListener,
	}
	for _, tp := range in {
		if !isAlwaysOnNode(tp) {
			t.Errorf("isAlwaysOnNode(%v) = false; a Refresh there leaves the peer cache stale", tp)
		}
	}
	// A Refresh outside the subtree must not throw the cache away: the entry is
	// there to collapse a burst of reads for one unreachable primary, and
	// refreshing Databases says nothing about whether it came back.
	out := []NodeType{
		NodeServer, NodeDatabases, NodeDatabase, NodeTables, NodeTable,
		NodeSecurity, NodeManagement, NodeError, NodeLoading,
	}
	for _, tp := range out {
		if isAlwaysOnNode(tp) {
			t.Errorf("isAlwaysOnNode(%v) = true; an unrelated Refresh drops the peer cache", tp)
		}
	}
}
