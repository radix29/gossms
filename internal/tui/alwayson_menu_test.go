package tui

import (
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// menuLabels renders a node's menu as labels, marking disabled items.
func menuLabels(t *testing.T, node *explorerNode) []string {
	t.Helper()
	a := &App{}
	var out []string
	for _, item := range a.contextMenuItemsForNode(node) {
		if item.Divider {
			continue
		}
		label := item.Label
		if item.Enabled != nil && !item.Enabled() {
			label += " [disabled]"
		}
		out = append(out, label)
	}
	return out
}

func agNode(t NodeType, name, agName string) *explorerNode {
	n := &explorerNode{}
	n.data.Type = t
	n.data.Name = name
	n.data.AGName = agName
	return n
}

// Every operation is reachable only from a context menu.
func TestAlwaysOnMenusOfferEveryOperation(t *testing.T) {
	tests := []struct {
		name string
		node *explorerNode
		want []string
	}{
		{"group", agNode(NodeAvailabilityGroup, "AAG1", "AAG1"),
			[]string{"Add Database...", "Add Listener...", "Delete Availability Group...", "Show Dashboard", "Properties..."}},
		{"databases folder", agNode(NodeAvailabilityDatabases, "", "AAG1"),
			[]string{"Add Database..."}},
		{"listeners folder", agNode(NodeAGListeners, "", "AAG1"),
			[]string{"Add Listener..."}},
		{"database", agNode(NodeAvailabilityDatabase, "testdb_1", "AAG1"),
			[]string{"Suspend Data Movement...", "Remove Database from Group..."}},
		{"listener", agNode(NodeAGListener, "ubuaag", "AAG1"),
			[]string{"Remove Listener...", "Properties..."}},
		{"replica", agNode(NodeAvailabilityReplica, "ubusql2", "AAG1"),
			[]string{"Fail Over to This Replica...", "Force Failover to This Replica...", "Remove Replica from Group..."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := menuLabels(t, tt.node)
			for _, want := range tt.want {
				if !slicesContains(labels, want) {
					t.Errorf("%s context menu = %v, want a %q item", tt.name, labels, want)
				}
			}
		})
	}
}

// Only one of Suspend/Resume is offered; the other can only fail.
func TestAGDatabaseMenuOffersOneMovementItem(t *testing.T) {
	running := agNode(NodeAvailabilityDatabase, "testdb_1", "AAG1")
	suspended := agNode(NodeAvailabilityDatabase, "testdb_1", "AAG1")
	suspended.data.AGSuspended = true

	if labels := menuLabels(t, running); !slicesContains(labels, "Suspend Data Movement...") || slicesContains(labels, "Resume Data Movement") {
		t.Errorf("running database menu = %v, want Suspend and no Resume", labels)
	}
	if labels := menuLabels(t, suspended); !slicesContains(labels, "Resume Data Movement") || slicesContains(labels, "Suspend Data Movement...") {
		t.Errorf("suspended database menu = %v, want Resume and no Suspend", labels)
	}
}

// A primary can't be failed over to, and REMOVE REPLICA on it fails (41190);
// both are gated in the tree.
func TestAGPrimaryReplicaMenuGatesFailoverAndRemoval(t *testing.T) {
	primary := agNode(NodeAvailabilityReplica, "ubusql1", "AAG1")
	primary.data.AGIsPrimary = true

	for _, label := range []string{"Fail Over to This Replica...", "Force Failover to This Replica...", "Remove Replica from Group..."} {
		if labels := menuLabels(t, primary); !slicesContains(labels, label+" [disabled]") {
			t.Errorf("primary replica menu = %v, want %q disabled", labels, label)
		}
	}
	secondary := agNode(NodeAvailabilityReplica, "ubusql2", "AAG1")
	if labels := menuLabels(t, secondary); slicesContains(labels, "Fail Over to This Replica... [disabled]") {
		t.Errorf("secondary replica menu = %v, want failover enabled", labels)
	}
}

// The cluster type decides which failover statements exist; a wrong table hides
// a working operation or offers a raw error.
func TestAGFailoverRefusal(t *testing.T) {
	tests := []struct {
		clusterType string
		force       bool
		wantSubstr  string
	}{
		{"EXTERNAL", false, "Pacemaker"},
		{"EXTERNAL", true, "Pacemaker"},
		// Sometimes reported lowercase; the gate must not care.
		{"external", false, "47104"},
		{"NONE", false, "Force Failover"},
		// Forced failover is a read-scale group's only kind.
		{"NONE", true, ""},
		{"WSFC", false, ""},
		{"WSFC", true, ""},
		// Empty before SQL Server 2017, meaning WSFC.
		{"", false, ""},
	}
	for _, tt := range tests {
		got := agFailoverRefusal(tt.clusterType, tt.force)
		if tt.wantSubstr == "" {
			if got != "" {
				t.Errorf("agFailoverRefusal(%q, force=%v) refused with %q, want it allowed", tt.clusterType, tt.force, got)
			}
			continue
		}
		if !strings.Contains(got, tt.wantSubstr) {
			t.Errorf("agFailoverRefusal(%q, force=%v) = %q, want it to mention %q", tt.clusterType, tt.force, got, tt.wantSubstr)
		}
	}
}

// From the primary suspend reaches every secondary, from a secondary only
// itself; the wordings must differ.
func TestAGSuspendScopeSaysWhichInstance(t *testing.T) {
	onPrimary := agSuspendScope("ubusql1", true)
	onSecondary := agSuspendScope("ubusql2", false)
	if !strings.Contains(onPrimary, "EVERY secondary") {
		t.Errorf("primary scope = %q, want it to say every secondary is affected", onPrimary)
	}
	if !strings.Contains(onSecondary, "only its own copy") {
		t.Errorf("secondary scope = %q, want it to say only this replica is affected", onSecondary)
	}
}

// -- Add Database ----------------------------------------------------------

// Every exclusion needs an actionable reason; an unexplained empty list looks
// like a bug.
func TestAGEligibleDatabases(t *testing.T) {
	dbs := []agDBCandidate{
		{Name: "payroll", RecoveryModel: "FULL", State: "ONLINE", LogChainStarted: true},
		{Name: "scratch", RecoveryModel: "SIMPLE", State: "ONLINE"},
		{Name: "archive", RecoveryModel: "FULL", State: "RESTORING", LogChainStarted: true},
		{Name: "testdb_1", RecoveryModel: "FULL", State: "ONLINE", LogChainStarted: true},
		{Name: "fresh", RecoveryModel: "FULL", State: "ONLINE"},
		{Name: "master", RecoveryModel: "SIMPLE", State: "ONLINE", IsSystem: true},
	}
	eligible, excluded := agEligibleDatabases(dbs, map[string]bool{"testdb_1": true})

	if want := []string{"payroll"}; !slices.Equal(eligible, want) {
		t.Errorf("eligible = %v, want %v", eligible, want)
	}
	joined := strings.Join(excluded, "\n")
	for _, want := range []string{"scratch — recovery model is SIMPLE", "archive — database is restoring", "testdb_1 — already in an availability group", "fresh — no full backup"} {
		if !strings.Contains(joined, want) {
			t.Errorf("excluded = %v, want an entry %q", excluded, want)
		}
	}
	// System databases aren't listed as excluded.
	if strings.Contains(joined, "master") {
		t.Errorf("excluded = %v, want system databases left out silently", excluded)
	}
}

// The log backup chain is the one prerequisite the database's metadata doesn't
// reveal: FULL, ONLINE and ungrouped can still fail with Msg 1475 without a
// full backup since entering FULL (verified live for ADD DATABASE and CREATE
// AVAILABILITY GROUP ... FOR DATABASE).
func TestAGEligibleDatabasesExcludesAnUnbackedUpDatabase(t *testing.T) {
	dbs := []agDBCandidate{
		{Name: "backed_up", RecoveryModel: "FULL", State: "ONLINE", LogChainStarted: true},
		{Name: "never_backed_up", RecoveryModel: "FULL", State: "ONLINE"},
	}
	eligible, excluded := agEligibleDatabases(dbs, nil)

	if want := []string{"backed_up"}; !slices.Equal(eligible, want) {
		t.Errorf("eligible = %v, want %v", eligible, want)
	}
	joined := strings.Join(excluded, "\n")
	if !strings.Contains(joined, "never_backed_up — no full backup") {
		t.Errorf("excluded = %v, want the unbacked-up database with a reason", excluded)
	}
	// The reason must say what to do.
	if !strings.Contains(joined, "back it up first") {
		t.Errorf("excluded = %v, want the reason to name the fix", excluded)
	}
}

// -- Add Listener ----------------------------------------------------------

func TestAGListenerSpecFrom(t *testing.T) {
	spec, err := agListenerSpecFrom("ubuaag", "1433", agListenerModeStatic, nil, "192.168.178.99", "255.255.255.0")
	if err != nil {
		t.Fatalf("static IPv4: %v", err)
	}
	if spec.DNSName != "ubuaag" || spec.Port != 1433 || spec.DHCP {
		t.Errorf("spec = %+v, want a static listener on port 1433", spec)
	}
	if len(spec.IPAddresses) != 1 || spec.IPAddresses[0].SubnetMask != "255.255.255.0" {
		t.Errorf("addresses = %+v, want one with its mask", spec.IPAddresses)
	}

	// IPv6 takes no mask; gosmo emits the one-element form.
	v6, err := agListenerSpecFrom("ubuaag", "1433", agListenerModeStatic, nil, "2001:db8::1", "")
	if err != nil {
		t.Fatalf("static IPv6: %v", err)
	}
	if len(v6.IPAddresses) != 1 || v6.IPAddresses[0].SubnetMask != "" {
		t.Errorf("IPv6 addresses = %+v, want one with no mask", v6.IPAddresses)
	}

	dhcp, err := agListenerSpecFrom("ubuaag", "5022", agListenerModeDHCP, nil, "", "")
	if err != nil {
		t.Fatalf("dhcp: %v", err)
	}
	if !dhcp.DHCP || len(dhcp.IPAddresses) != 0 || dhcp.Port != 5022 {
		t.Errorf("spec = %+v, want a DHCP listener on port 5022", dhcp)
	}
	// DHCP ignores address fields, so leftover text doesn't block.
	if _, err := agListenerSpecFrom("ubuaag", "1433", agListenerModeDHCP, nil, "not an address", "junk"); err != nil {
		t.Errorf("DHCP with leftover address text: %v", err)
	}
}

func TestAGListenerSpecFromRejects(t *testing.T) {
	tests := []struct {
		name      string
		dns, port string
		mode      int
		ip, mask  string
		wantErr   string
	}{
		{name: "no name", port: "1433", mode: agListenerModeStatic, ip: "10.0.0.9", mask: "255.255.255.0", wantErr: "DNS name is required"},
		{name: "bad port", dns: "l", port: "0", mode: agListenerModeStatic, ip: "10.0.0.9", mask: "255.255.255.0", wantErr: "1 to 65535"},
		{name: "non-numeric port", dns: "l", port: "http", mode: agListenerModeStatic, ip: "10.0.0.9", mask: "255.255.255.0", wantErr: "1 to 65535"},
		{name: "bad address", dns: "l", port: "1433", mode: agListenerModeStatic, ip: "192.168.1", mask: "255.255.255.0", wantErr: "not a valid IP address"},
		// Without this, a mask-less IPv4 would be emitted as IPv6 and fail on
		// the server.
		{name: "IPv4 without a mask", dns: "l", port: "1433", mode: agListenerModeStatic, ip: "10.0.0.9", wantErr: "needs a subnet mask"},
		{name: "IPv6 with a mask", dns: "l", port: "1433", mode: agListenerModeStatic, ip: "2001:db8::1", mask: "255.255.255.0", wantErr: "takes no subnet mask"},
		{name: "bad mask", dns: "l", port: "1433", mode: agListenerModeStatic, ip: "10.0.0.9", mask: "255.255", wantErr: "not a valid IPv4 subnet mask"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := agListenerSpecFrom(tt.dns, tt.port, tt.mode, nil, tt.ip, tt.mask)
			if err == nil {
				t.Fatalf("accepted %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestAGListenerSpecFromCombinesAddedAndTypedAddresses(t *testing.T) {
	added := []gosmo.AvailabilityListenerIPSpec{{IPAddress: "10.0.0.9", SubnetMask: "255.255.255.0"}}

	// Typed fields count without a button press.
	spec, err := agListenerSpecFrom("ubuaag", "1433", agListenerModeStatic, nil, "10.0.0.9", "255.255.255.0")
	if err != nil {
		t.Fatalf("typed only: %v", err)
	}
	if len(spec.IPAddresses) != 1 {
		t.Errorf("typed only gave %d addresses, want 1", len(spec.IPAddresses))
	}

	// Added plus typed is a two-subnet listener, in that order.
	both, err := agListenerSpecFrom("ubuaag", "1433", agListenerModeStatic, added, "10.1.0.9", "255.255.255.0")
	if err != nil {
		t.Fatalf("added plus typed: %v", err)
	}
	if len(both.IPAddresses) != 2 || both.IPAddresses[0].IPAddress != "10.0.0.9" || both.IPAddresses[1].IPAddress != "10.1.0.9" {
		t.Errorf("addresses = %+v, want the added one then the typed one", both.IPAddresses)
	}

	// Added alone with cleared fields isn't "no address".
	only, err := agListenerSpecFrom("ubuaag", "1433", agListenerModeStatic, added, "", "")
	if err != nil {
		t.Fatalf("added only: %v", err)
	}
	if len(only.IPAddresses) != 1 {
		t.Errorf("added only gave %d addresses, want 1", len(only.IPAddresses))
	}

	// A duplicate address is named here, unlike the server's error.
	if _, err := agListenerSpecFrom("ubuaag", "1433", agListenerModeStatic, added, "10.0.0.9", "255.255.255.0"); err == nil {
		t.Error("a duplicate address was accepted")
	}

	// Static with nothing, which gosmo would reject vaguely.
	if _, err := agListenerSpecFrom("ubuaag", "1433", agListenerModeStatic, nil, "", ""); err == nil {
		t.Error("a static listener with no address was accepted")
	}

	// DHCP ignores leftover text.
	dhcp, err := agListenerSpecFrom("ubuaag", "1433", agListenerModeDHCP, added, "junk", "junk")
	if err != nil {
		t.Fatalf("dhcp: %v", err)
	}
	if !dhcp.DHCP || len(dhcp.IPAddresses) != 0 {
		t.Errorf("spec = %+v, want DHCP with no addresses", dhcp)
	}
}

// Join/Unjoin act on the local copy: neither works on the primary (its copy is
// the source), and an unjoined copy has no movement to suspend.
func TestAGDatabaseMenuGatesJoinOnTheLocalCopy(t *testing.T) {
	tests := []struct {
		name              string
		secondary, joined bool
		want, unwant      []string
	}{
		{
			name: "primary", secondary: false, joined: false,
			want:   []string{"Suspend Data Movement...", "Remove Database from Group..."},
			unwant: []string{"Join to Availability Group", "Remove Secondary Database from Group..."},
		},
		{
			name: "secondary, joined", secondary: true, joined: true,
			want:   []string{"Suspend Data Movement...", "Remove Secondary Database from Group..."},
			unwant: []string{"Join to Availability Group"},
		},
		{
			name: "secondary, not joined", secondary: true, joined: false,
			want:   []string{"Join to Availability Group"},
			unwant: []string{"Suspend Data Movement...", "Resume Data Movement", "Remove Secondary Database from Group..."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := agNode(NodeAvailabilityDatabase, "testdb_1", "AAG1")
			n.data.AGLocalSecondary = tt.secondary
			n.data.AGLocalJoined = tt.joined
			labels := menuLabels(t, n)
			for _, want := range tt.want {
				if !slicesContains(labels, want) {
					t.Errorf("menu = %v, want a %q item", labels, want)
				}
			}
			for _, unwant := range tt.unwant {
				if slicesContains(labels, unwant) {
					t.Errorf("menu = %v, want no %q item", labels, unwant)
				}
			}
		})
	}
}

// The two removals are one item apart and one word different; the group-wide
// wording must never be the only one for the per-replica case.
func TestAGDatabaseMenuKeepsBothRemovalsDistinct(t *testing.T) {
	n := agNode(NodeAvailabilityDatabase, "testdb_1", "AAG1")
	n.data.AGLocalSecondary, n.data.AGLocalJoined = true, true
	labels := menuLabels(t, n)
	if !slicesContains(labels, "Remove Secondary Database from Group...") ||
		!slicesContains(labels, "Remove Database from Group...") {
		t.Errorf("menu = %v, want both removal items", labels)
	}
}
