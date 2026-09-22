package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/radix29/gosmo"
)

func addReplicaPrefetch() *agAddReplicaPrefetch {
	return &agAddReplicaPrefetch{
		primary:     "ubusql1",
		clusterType: "EXTERNAL",
		existing:    map[string]bool{"ubusql1": true},
	}
}

func resolvedReplica() newAGReplica {
	return newAGReplica{
		name:             "ubusql2",
		endpointURL:      "TCP://ubusql2.fritz.box:5022",
		availabilityMode: "SYNCHRONOUS_COMMIT",
		failoverMode:     "EXTERNAL",
		seedingMode:      "AUTOMATIC",
		primaryRole:      "ALL",
		secondaryRole:    "NO",
		backupPriority:   50,
		sessionTimeout:   10,
	}
}

// The three server rejections, each explained better than the server does.
func TestValidateAddReplica(t *testing.T) {
	tests := []struct {
		name string
		edit func(*newAGReplica, *agAddReplicaPrefetch)
		want string
	}{
		{"not connected yet", func(r *newAGReplica, _ *agAddReplicaPrefetch) {
			r.name, r.endpointURL = "", ""
		}, "press Connect"},
		{"no endpoint read", func(r *newAGReplica, _ *agAddReplicaPrefetch) {
			r.endpointURL = ""
		}, "press Connect"},
		{"already a replica", func(r *newAGReplica, pf *agAddReplicaPrefetch) {
			pf.existing["ubusql2"] = true
		}, "already a replica"},
		{"failover mode the cluster type forbids", func(r *newAGReplica, _ *agAddReplicaPrefetch) {
			r.failoverMode = "AUTOMATIC"
		}, "cannot use failover mode AUTOMATIC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, pf := resolvedReplica(), addReplicaPrefetch()
			tt.edit(&r, pf)
			err := validateAddReplica(r, pf)
			if err == nil {
				t.Fatalf("accepted %+v", r)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q, want it to mention %q", err, tt.want)
			}
		})
	}

	if err := validateAddReplica(resolvedReplica(), addReplicaPrefetch()); err != nil {
		t.Errorf("rejected a valid replica: %v", err)
	}
}

// Case-insensitive on both sides: the catalog keeps registration case, and a
// duplicate under another spelling gets a raw server error.
func TestValidateAddReplicaMatchesExistingCaseInsensitively(t *testing.T) {
	pf := addReplicaPrefetch()
	pf.existing["ubusql2"] = true
	r := resolvedReplica()
	r.name = "UbuSQL2"
	if err := validateAddReplica(r, pf); err == nil {
		t.Fatal("accepted a replica already in the group under another case")
	}
}

// The default is the only mode the cluster type allows. WSFC allows two, and
// defaulting to automatic would silently change failover behaviour.
func TestAGDefaultFailoverMode(t *testing.T) {
	tests := map[string]string{
		"EXTERNAL": "EXTERNAL",
		"NONE":     "MANUAL",
		"WSFC":     "MANUAL",
		"":         "MANUAL",
	}
	for clusterType, want := range tests {
		if got := agDefaultFailoverMode(clusterType); got != want {
			t.Errorf("agDefaultFailoverMode(%q) = %q, want %q", clusterType, got, want)
		}
	}
}

// scriptAddReplica runs Add Replica's pipeline under Script Changes against
// the AG fixture, whose primary is the fake connection itself.
func scriptAddReplica(t *testing.T, r newAGReplica) string {
	t.Helper()
	sc, _ := newFakeConn(t, agResponses()...)
	d := &AGAddReplicaDialog{agName: agFixtureName, resolved: r}
	d.sc = sc
	scriptCtx, script := gosmo.WithScript(context.Background())
	if err := d.addReplica(scriptCtx); err != nil {
		t.Fatalf("addReplica under WithScript: %v", err)
	}
	return multiInstanceScript("Add Replica", script)
}

// The script must name each statement's instance; run whole on the primary,
// JOIN errors or joins the primary to its own group.
func TestAddReplicaScriptNamesEachInstance(t *testing.T) {
	got := scriptAddReplica(t, resolvedReplica())

	wantOrder := []string{
		"-- on " + agPrimary + "\nALTER AVAILABILITY GROUP [AAG1] ADD REPLICA",
		"\nGO\n",
		"-- on ubusql2\nALTER AVAILABILITY GROUP [AAG1] JOIN",
		"\nGO\n",
		"\nALTER AVAILABILITY GROUP [AAG1] GRANT CREATE ANY DATABASE",
		"\nGO\n",
	}
	pos := 0
	for _, want := range wantOrder {
		i := strings.Index(got[pos:], want)
		if i < 0 {
			t.Fatalf("script is missing %q after position %d:\n%s", want, pos, got)
		}
		pos += i + len(want)
	}
	if n := strings.Count(got, "-- on "); n != 2 {
		t.Errorf("got %d instance labels, want 2 (the primary, then the new replica):\n%s", n, got)
	}
}

// MANUAL seeding has no GRANT, and nothing after it is mislabelled.
func TestAddReplicaScriptWithoutTheGrant(t *testing.T) {
	r := resolvedReplica()
	r.seedingMode = "MANUAL"
	got := scriptAddReplica(t, r)
	if strings.Contains(got, "GRANT CREATE ANY DATABASE") {
		t.Errorf("manual-seeding script mentions the grant:\n%s", got)
	}
	if !strings.Contains(got, "-- on ubusql2\nALTER AVAILABILITY GROUP [AAG1] JOIN") {
		t.Errorf("the JOIN is not labelled with the new replica:\n%s", got)
	}
	if strings.Contains(got, "(unknown instance)") {
		t.Errorf("script has an unlabelled statement:\n%s", got)
	}
}

// The ADD REPLICA spec carries every collected value; a dropped one is silently
// defaulted.
func TestAddReplicaSpecCarriesEveryValue(t *testing.T) {
	r := resolvedReplica()
	spec := r.spec()
	if spec.ServerName != "ubusql2" || spec.EndpointURL != "TCP://ubusql2.fritz.box:5022" {
		t.Errorf("identity lost: %+v", spec)
	}
	if spec.AvailabilityMode != "SYNCHRONOUS_COMMIT" || spec.FailoverMode != "EXTERNAL" ||
		spec.SeedingMode != "AUTOMATIC" || spec.PrimaryRoleAllowConnections != "ALL" ||
		spec.SecondaryRoleAllowConnections != "NO" {
		t.Errorf("modes lost: %+v", spec)
	}
	if spec.BackupPriority != 50 || spec.SessionTimeout != 10 {
		t.Errorf("numbers lost: %+v", spec)
	}
}
