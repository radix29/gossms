package tui

import (
	"strings"
	"testing"
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

// The script must name each statement's instance; run whole on the primary,
// JOIN errors or joins the primary to its own group.
func TestAddReplicaScriptNamesEachInstance(t *testing.T) {
	d := &AGAddReplicaDialog{agName: "AAG1", resolved: resolvedReplica()}
	d.prefetch = addReplicaPrefetch()

	got := d.annotateScript([]string{
		"ALTER AVAILABILITY GROUP [AAG1] ADD REPLICA ON N'ubusql2' WITH (...)",
		"ALTER AVAILABILITY GROUP [AAG1] JOIN WITH (CLUSTER_TYPE = EXTERNAL)",
		"ALTER AVAILABILITY GROUP [AAG1] GRANT CREATE ANY DATABASE",
	})

	for _, want := range []string{
		"-- on ubusql1\nALTER AVAILABILITY GROUP [AAG1] ADD REPLICA",
		"-- on ubusql2\nALTER AVAILABILITY GROUP [AAG1] JOIN",
		"-- on ubusql2\nALTER AVAILABILITY GROUP [AAG1] GRANT CREATE ANY DATABASE",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("script does not contain %q:\n%s", want, got)
		}
	}
}

// MANUAL seeding has no GRANT; no label for a missing statement.
func TestAddReplicaScriptWithoutTheGrant(t *testing.T) {
	d := &AGAddReplicaDialog{agName: "AAG1", resolved: resolvedReplica()}
	d.resolved.seedingMode = "MANUAL"
	d.prefetch = addReplicaPrefetch()

	got := d.annotateScript([]string{
		"ALTER AVAILABILITY GROUP [AAG1] ADD REPLICA ON N'ubusql2' WITH (...)",
		"ALTER AVAILABILITY GROUP [AAG1] JOIN WITH (CLUSTER_TYPE = EXTERNAL)",
	})
	if strings.Contains(got, "GRANT CREATE ANY DATABASE") {
		t.Errorf("manual-seeding script mentions the grant:\n%s", got)
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
