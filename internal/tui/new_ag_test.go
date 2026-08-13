package tui

import (
	"strings"
	"testing"
)

// New Availability Group's real risk is not the statement text — gosmo pins
// that — but the two things only this layer knows: which of three instances
// each statement belongs to, and which combinations the cluster type forbids.

func newAGReplicaPair() []*newAGReplica {
	return []*newAGReplica{
		{name: "ubusql1", endpointURL: "tcp://ubusql1:5022", failoverMode: "EXTERNAL", seedingMode: "AUTOMATIC", isPrimary: true},
		{name: "ubusql2", endpointURL: "tcp://ubusql2:5022", failoverMode: "EXTERNAL", seedingMode: "AUTOMATIC"},
	}
}

func TestValidateNewAG(t *testing.T) {
	existing := map[string]bool{"aag1": true}
	tests := []struct {
		name        string
		groupName   string
		clusterType string
		edit        func([]*newAGReplica) []*newAGReplica
		wantErr     string
	}{
		{name: "valid external", groupName: "AAG2", clusterType: "EXTERNAL"},
		{name: "valid none", groupName: "AAG2", clusterType: "NONE", edit: func(rs []*newAGReplica) []*newAGReplica {
			for _, r := range rs {
				r.failoverMode = "MANUAL"
			}
			return rs
		}},
		{name: "no name", clusterType: "EXTERNAL", wantErr: "name is required"},
		// Matched case-insensitively: SQL Server group names are, and letting
		// a differently-cased duplicate through just moves the error to CREATE.
		{name: "duplicate name", groupName: "aAg1", clusterType: "EXTERNAL", wantErr: "already exists"},
		{name: "only the primary", groupName: "AAG2", clusterType: "EXTERNAL",
			edit:    func(rs []*newAGReplica) []*newAGReplica { return rs[:1] },
			wantErr: "at least one secondary"},
		// The rule that costs a real CREATE if it is missed: EXTERNAL forbids
		// every failover mode but EXTERNAL, and the server's error says
		// neither which replica nor why.
		{name: "external with manual failover", groupName: "AAG2", clusterType: "EXTERNAL",
			edit: func(rs []*newAGReplica) []*newAGReplica {
				rs[1].failoverMode = "MANUAL"
				return rs
			},
			wantErr: "ubusql2 cannot use failover mode MANUAL"},
		// CLUSTER_TYPE = NONE takes MANUAL and nothing else — not just "not
		// automatic". EXTERNAL here is the case the first version of this
		// check let through, and the server answered with Msg 47101.
		{name: "none with automatic failover", groupName: "AAG2", clusterType: "NONE",
			edit: func(rs []*newAGReplica) []*newAGReplica {
				rs[1].failoverMode = "AUTOMATIC"
				return rs
			},
			wantErr: "no cluster manager"},
		{name: "none with external failover", groupName: "AAG2", clusterType: "NONE",
			wantErr: "set it to MANUAL"},
		{name: "wsfc with external failover", groupName: "AAG2", clusterType: "WSFC",
			wantErr: "set it to MANUAL or AUTOMATIC"},
		{name: "wsfc with automatic failover", groupName: "AAG2", clusterType: "WSFC",
			edit: func(rs []*newAGReplica) []*newAGReplica {
				for _, r := range rs {
					r.failoverMode = "AUTOMATIC"
				}
				return rs
			}},
		{name: "replica with no endpoint", groupName: "AAG2", clusterType: "EXTERNAL",
			edit: func(rs []*newAGReplica) []*newAGReplica {
				rs[1].endpointURL = ""
				return rs
			},
			wantErr: "no endpoint URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replicas := newAGReplicaPair()
			if tt.edit != nil {
				replicas = tt.edit(replicas)
			}
			err := validateNewAG(tt.groupName, tt.clusterType, replicas, existing)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateNewAG: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// The request is assembled from two pages' state; the parts worth pinning are
// the ones a plain field copy would get wrong.
func TestNewAGRequest(t *testing.T) {
	d := &NewAGDialog{
		groupName:        "  AAG2  ",
		clusterType:      "EXTERNAL",
		requiredSync:     0,
		dbFailover:       true,
		backupPreference: "SECONDARY_ONLY",
		replicas:         newAGReplicaPair(),
		databases: []newAGDatabase{
			{name: "testdb_1", included: true},
			{name: "scratch"},
			{name: "payroll", included: true},
		},
	}
	d.replicas[1].backupPriority = 0

	req, err := d.request()
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if req.Name != "AAG2" {
		t.Errorf("name = %q, want the trimmed %q", req.Name, "AAG2")
	}
	// Zero is a real value for this option and gosmo omits it only when
	// negative, so the dialog's 0 has to survive as 0.
	if req.RequiredSynchronizedSecondariesToCommit != 0 {
		t.Errorf("required synchronized secondaries = %d, want 0",
			req.RequiredSynchronizedSecondariesToCommit)
	}
	if got, want := strings.Join(req.Databases, ","), "testdb_1,payroll"; got != want {
		t.Errorf("databases = %q, want only the included ones %q", got, want)
	}
	if len(req.Replicas) != 2 || req.Replicas[0].ServerName != "ubusql1" {
		t.Fatalf("replicas = %+v, want the primary first", req.Replicas)
	}
	// Backup priority is the Backup Preferences page's, carried on the same
	// replica the General page edits — one CREATE, not a create plus an ALTER.
	if req.Replicas[1].BackupPriority != 0 {
		t.Errorf("secondary backup priority = %d, want the page's 0",
			req.Replicas[1].BackupPriority)
	}
	if req.AutomatedBackupPreference != "SECONDARY_ONLY" {
		t.Errorf("backup preference = %q, want the page's", req.AutomatedBackupPreference)
	}
}

// CREATE runs on the instance the dialog is connected to, so a replica list
// whose first entry is not that instance would create the group with the wrong
// primary — silently, since SQL Server does not check the order against
// anything.
func TestNewAGRequestRequiresThePrimaryFirst(t *testing.T) {
	d := &NewAGDialog{groupName: "AAG2", replicas: newAGReplicaPair()}
	d.replicas[0].isPrimary = false
	if _, err := d.request(); err == nil {
		t.Fatal("accepted a replica list that does not start with the local instance")
	}
}

// The script is three instances' worth of statements in one window. Without the
// per-statement labels, running it whole against the primary either errors or
// joins the primary to its own group.
func TestAnnotateScriptLabelsEachInstance(t *testing.T) {
	d := &NewAGDialog{replicas: newAGReplicaPair()}
	d.replicas[1].seedingMode = "AUTOMATIC"

	got := d.annotateScript([]string{
		"CREATE AVAILABILITY GROUP [AAG2] ...",
		"ALTER AVAILABILITY GROUP [AAG2] JOIN WITH (CLUSTER_TYPE = EXTERNAL)",
		"ALTER AVAILABILITY GROUP [AAG2] GRANT CREATE ANY DATABASE",
	})

	// The CREATE belongs to the local instance; both statements after it to the
	// secondary, in the order createGroup issues them.
	wantOrder := []string{"-- on ubusql1", "CREATE AVAILABILITY GROUP", "-- on ubusql2", "JOIN", "-- on ubusql2", "GRANT CREATE ANY DATABASE"}
	pos := 0
	for _, want := range wantOrder {
		i := strings.Index(got[pos:], want)
		if i < 0 {
			t.Fatalf("script is missing %q after position %d:\n%s", want, pos, got)
		}
		pos += i + len(want)
	}
}

// A MANUAL-seeding secondary gets no GRANT, so the labels must shift with it —
// a fixed two-per-secondary assumption would mislabel every statement after the
// first such replica.
func TestAnnotateScriptSkipsTheGrantForManualSeeding(t *testing.T) {
	d := &NewAGDialog{replicas: newAGReplicaPair()}
	d.replicas[1].seedingMode = "MANUAL"

	got := d.annotateScript([]string{"CREATE ...", "JOIN ..."})
	if strings.Contains(got, "(unknown instance)") {
		t.Errorf("script labelled a statement as unknown:\n%s", got)
	}
	if n := strings.Count(got, "-- on ubusql2"); n != 1 {
		t.Errorf("got %d statements labelled for the secondary, want 1:\n%s", n, got)
	}
}

// New Availability Group is reachable only from the Availability Groups
// folder's context menu.
func TestAvailabilityGroupsFolderOffersNewGroup(t *testing.T) {
	node := &explorerNode{}
	node.data.Type = NodeAvailabilityGroups
	if labels := menuLabels(t, node); !slicesContains(labels, "New Availability Group...") {
		t.Errorf("Availability Groups folder menu = %v, want a New Availability Group... item", labels)
	}
}

// The endpoint URL became editable so an instance whose short name the other
// replicas cannot resolve can be given its FQDN — which also makes it the one
// field in the dialog a typo reaches the server through. ADD REPLICA stores a
// malformed URL without complaint and the replica then never connects, so the
// refusal has to happen here.
func TestValidateEndpointURL(t *testing.T) {
	good := []string{
		"tcp://ubusql1:5022",
		"TCP://ubusql1:5022",
		"tcp://sqlnode1.corp.example.com:5022",
		"tcp://10.0.0.7:5022",
	}
	for _, u := range good {
		if err := validateEndpointURL(u); err != nil {
			t.Errorf("validateEndpointURL(%q) = %v, want nil", u, err)
		}
	}
	bad := []string{
		"",                    // never connected, or cleared
		"ubusql1:5022",        // the scheme is not optional
		"http://ubusql1:5022", // mirroring endpoints are TCP only
		"tcp://ubusql1",       // no port
		"tcp://:5022",         // no host
		"tcp://ubusql1:abc",   // port is not a number
		"tcp://ubusql1:0",     // out of range
		"tcp://ubusql1:70000",
	}
	for _, u := range bad {
		if err := validateEndpointURL(u); err == nil {
			t.Errorf("validateEndpointURL(%q) = nil, want an error", u)
		}
	}
}

// A hand-typed URL must not let the dialog skip Connect: the endpoint still has
// to have been read from the instance, which is what proves it exists and is
// STARTED. Connect is what sets the name, so an empty name is the tell.
func TestAddReplicaStillNeedsConnectDespiteAnEditableURL(t *testing.T) {
	pf := &agAddReplicaPrefetch{clusterType: "EXTERNAL", existing: map[string]bool{}}
	r := newAGReplica{endpointURL: "tcp://ubusql2:5022", failoverMode: "EXTERNAL"}
	err := validateAddReplica(r, pf)
	if err == nil {
		t.Fatal("validateAddReplica accepted a typed URL with no connected instance")
	}
	if !strings.Contains(err.Error(), "Connect") {
		t.Errorf("error = %q, want it to name Connect", err)
	}
}
