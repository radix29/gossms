package tui

import (
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// menuLabels renders a node's context menu as the labels a user would see,
// with disabled items marked, so a test can assert both presence and gating.
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

// Every operation added in this phase is reachable only from a context menu, so
// a missing case makes the whole operation unreachable — the same failure the
// dashboard and Properties tests guard against.
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

// A database's menu offers one of Suspend/Resume, never both: the other one can
// only fail, and a menu item that can only fail is the thing context-gating
// exists to prevent.
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

// A primary cannot be failed over to, and REMOVE REPLICA against the primary is
// refused by the server with 41190. Both have to be gated off in the tree,
// where the role is already known.
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

// The cluster type decides which failover statements exist at all, and getting
// this table wrong either hides a working operation or offers one the server
// answers with a raw error code.
func TestAGFailoverRefusal(t *testing.T) {
	tests := []struct {
		clusterType string
		force       bool
		wantSubstr  string
	}{
		{"EXTERNAL", false, "Pacemaker"},
		{"EXTERNAL", true, "Pacemaker"},
		// Reported in lower case by sys.availability_groups on some paths;
		// the gate must not depend on which one it came through.
		{"external", false, "47104"},
		{"NONE", false, "Force Failover"},
		// The forced form is the only failover a read-scale group has, so it
		// must not be refused.
		{"NONE", true, ""},
		{"WSFC", false, ""},
		{"WSFC", true, ""},
		// Empty before SQL Server 2017, which only ever meant WSFC.
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

// Suspending from the primary reaches every secondary and from a secondary only
// itself. The confirmation is the only place the user finds that out, so the
// two wordings must actually differ.
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

// agEligibleDatabases is what decides whether a database appears in the
// dropdown at all, so every exclusion has to carry a reason the user can act
// on — an empty list with no explanation is indistinguishable from a bug.
func TestAGEligibleDatabases(t *testing.T) {
	dbs := []agDBCandidate{
		{Name: "payroll", RecoveryModel: "FULL", State: "ONLINE"},
		{Name: "scratch", RecoveryModel: "SIMPLE", State: "ONLINE"},
		{Name: "archive", RecoveryModel: "FULL", State: "RESTORING"},
		{Name: "testdb_1", RecoveryModel: "FULL", State: "ONLINE"},
		{Name: "master", RecoveryModel: "SIMPLE", State: "ONLINE", IsSystem: true},
	}
	eligible, excluded := agEligibleDatabases(dbs, map[string]bool{"testdb_1": true})

	if want := []string{"payroll"}; !slices.Equal(eligible, want) {
		t.Errorf("eligible = %v, want %v", eligible, want)
	}
	joined := strings.Join(excluded, "\n")
	for _, want := range []string{"scratch — recovery model is SIMPLE", "archive — database is restoring", "testdb_1 — already in an availability group"} {
		if !strings.Contains(joined, want) {
			t.Errorf("excluded = %v, want an entry %q", excluded, want)
		}
	}
	// A system database is not "excluded" — nobody was expecting it, and
	// listing four of them would bury the reasons that matter.
	if strings.Contains(joined, "master") {
		t.Errorf("excluded = %v, want system databases left out silently", excluded)
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

	// An IPv6 address takes no mask at all, and gosmo emits the one-element
	// form for exactly that case.
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
	// DHCP ignores the address fields rather than failing on them, so leftover
	// text from a mode switch cannot block the dialog.
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
		// The one that matters: without this, a mistyped IPv4 address with no
		// mask is emitted as the IPv6 form and fails on the server instead.
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

	// The typed fields count without a button press — the single-subnet case
	// must not need one, or it fails as "no static address".
	spec, err := agListenerSpecFrom("ubuaag", "1433", agListenerModeStatic, nil, "10.0.0.9", "255.255.255.0")
	if err != nil {
		t.Fatalf("typed only: %v", err)
	}
	if len(spec.IPAddresses) != 1 {
		t.Errorf("typed only gave %d addresses, want 1", len(spec.IPAddresses))
	}

	// Added plus still-typed is a two-subnet listener, in that order.
	both, err := agListenerSpecFrom("ubuaag", "1433", agListenerModeStatic, added, "10.1.0.9", "255.255.255.0")
	if err != nil {
		t.Fatalf("added plus typed: %v", err)
	}
	if len(both.IPAddresses) != 2 || both.IPAddresses[0].IPAddress != "10.0.0.9" || both.IPAddresses[1].IPAddress != "10.1.0.9" {
		t.Errorf("addresses = %+v, want the added one then the typed one", both.IPAddresses)
	}

	// Added alone, with the fields cleared by Add Address, is not "no address".
	only, err := agListenerSpecFrom("ubuaag", "1433", agListenerModeStatic, added, "", "")
	if err != nil {
		t.Fatalf("added only: %v", err)
	}
	if len(only.IPAddresses) != 1 {
		t.Errorf("added only gave %d addresses, want 1", len(only.IPAddresses))
	}

	// The same address twice would be rejected by the server; saying so here
	// names which address, which the server's error does not.
	if _, err := agListenerSpecFrom("ubuaag", "1433", agListenerModeStatic, added, "10.0.0.9", "255.255.255.0"); err == nil {
		t.Error("a duplicate address was accepted")
	}

	// Static mode with nothing at all is the case gosmo would reject with a
	// less specific message.
	if _, err := agListenerSpecFrom("ubuaag", "1433", agListenerModeStatic, nil, "", ""); err == nil {
		t.Error("a static listener with no address was accepted")
	}

	// DHCP takes no address list, and leftover text must not block it.
	dhcp, err := agListenerSpecFrom("ubuaag", "1433", agListenerModeDHCP, added, "junk", "junk")
	if err != nil {
		t.Fatalf("dhcp: %v", err)
	}
	if !dhcp.DHCP || len(dhcp.IPAddresses) != 0 {
		t.Errorf("spec = %+v, want DHCP with no addresses", dhcp)
	}
}

// Join and Unjoin act on the local copy, so both are gated on what the tree's
// own instance holds. On the primary neither can work at all — the primary's
// copy is the group's source, not a member to be joined — and a copy that has
// not joined has no data movement to suspend either.
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

// The two removals are one item apart and one word different, and they do very
// different things — the group-wide one must never be the only wording offered
// for the per-replica case.
func TestAGDatabaseMenuKeepsBothRemovalsDistinct(t *testing.T) {
	n := agNode(NodeAvailabilityDatabase, "testdb_1", "AAG1")
	n.data.AGLocalSecondary, n.data.AGLocalJoined = true, true
	labels := menuLabels(t, n)
	if !slicesContains(labels, "Remove Secondary Database from Group...") ||
		!slicesContains(labels, "Remove Database from Group...") {
		t.Errorf("menu = %v, want both removal items", labels)
	}
}
