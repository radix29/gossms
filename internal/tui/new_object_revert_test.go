package tui

import (
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The New Availability Group and New Database Mirroring Endpoint dialogs keep
// their editable state on the dialog, not in the grid that displays it — the
// list of databases to include, the list of replicas, the list of instances in
// the certificate exchange. A GridRow reverts none of that on its own, so each
// of those pages has to supply a RevertFn.
//
// Without one the failure is quiet and points the wrong way: Ctrl+Z reverted
// every ordinary row, PropertySheet reported "Reverted to the loaded values",
// and the dialog still created the group with the databases the user had just
// told it to forget. Reproduced against ubusql1 on 2026-08-18 before the fix,
// which is what these pin.

func TestNewAGDatabasesRevertToTheLoadedInclusions(t *testing.T) {
	d := &NewAGDialog{databases: []newAGDatabase{
		{name: "backup_test"}, {name: "zz_revert_a"}, {name: "zz_revert_b"},
	}}
	gridRow, includeRow, commit := d.databaseRows()
	f := propsheet.NewForm(propsheet.Section("Availability databases"), gridRow, includeRow)

	// wireGridEditor's initial sync leaves the first row current, which is
	// what the checkbox edits.
	check, ok := includeRow.(*propsheet.CheckRow)
	if !ok {
		t.Fatalf("include row = %T, want *propsheet.CheckRow", includeRow)
	}
	check.SetChecked(true)
	commit()
	if !d.databases[0].included {
		t.Fatalf("commit did not include the selected database — test drives the wrong row")
	}
	if !f.Dirty() {
		t.Fatalf("form is not dirty after including a database, so Ctrl+Z would not revert it")
	}

	f.Revert()

	for _, db := range d.databases {
		if db.included {
			t.Errorf("database %q is still included after Revert", db.name)
		}
	}
	if f.Dirty() {
		t.Errorf("form still reports dirty after Revert")
	}
}

func TestNewAGReplicasRevertToTheLoadedList(t *testing.T) {
	d := &NewAGDialog{replicas: newAGReplicaPair()}
	rows, _ := d.replicaRows()
	f := propsheet.NewForm(rows...)

	// The shape an Add Replica click leaves behind, without the round trip it
	// makes to reach the instance.
	d.replicas = append(d.replicas, &newAGReplica{name: "ubusql3", endpointURL: "tcp://ubusql3:5022"})
	d.replicas[1].availabilityMode = "ASYNCHRONOUS_COMMIT"

	f.Revert()

	if len(d.replicas) != 2 {
		t.Fatalf("replicas = %d after Revert, want the 2 the page loaded with", len(d.replicas))
	}
	if got := d.replicas[1].availabilityMode; got != "" {
		t.Errorf("replica availability mode = %q after Revert, want the loaded value", got)
	}
}

// The baseline must be a copy, not an alias: d.replicas holds pointers, so a
// snapshot that shared them would be edited by the very changes Revert exists
// to undo and would restore nothing.
func TestCloneAGReplicasCopiesEachReplica(t *testing.T) {
	original := newAGReplicaPair()
	clone := cloneAGReplicas(original)

	original[0].availabilityMode = "ASYNCHRONOUS_COMMIT"
	if clone[0].availabilityMode == "ASYNCHRONOUS_COMMIT" {
		t.Error("clone shares its replicas with the original")
	}
	if clone[0].name != original[0].name || len(clone) != len(original) {
		t.Errorf("clone = %+v, want a copy of %+v", clone[0], original[0])
	}
}

func TestNewEndpointInstancesRevertToTheLoadedList(t *testing.T) {
	d := &NewEndpointDialog{instances: []*newEndpointInstance{
		{name: "ubusql1", local: true},
	}}
	rows, _ := d.instanceRows()
	f := propsheet.NewForm(rows...)

	d.instances = append(d.instances, &newEndpointInstance{name: "ubusql2"})
	if !f.Dirty() {
		t.Fatalf("form is not dirty after adding an instance, so Ctrl+Z would not revert it")
	}

	f.Revert()

	if len(d.instances) != 1 || d.instances[0].name != "ubusql1" {
		t.Errorf("instances = %+v after Revert, want only this connection's own", d.instances)
	}
	if f.Dirty() {
		t.Errorf("form still reports dirty after Revert")
	}
}
