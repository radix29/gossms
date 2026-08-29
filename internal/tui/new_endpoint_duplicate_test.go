package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// hintText finds the page's advisory row, so a test can read what the Add
// Instance button said back.
func hintText(t *testing.T, f *propsheet.Form) string {
	t.Helper()
	for _, r := range f.Rows() {
		if hr, ok := r.(*propsheet.HintRow); ok {
			return hr.Text()
		}
	}
	t.Fatal("no hint row on this page")
	return ""
}

// Add Instance checks the *typed* name against the list, but the name that
// reaches the list is the instance's own @@SERVERNAME — addInstance replaces it
// so the certificate, login and user names match on both sides. An alias, an
// address, or a bare hostname for an instance already listed therefore gets
// past the typed-name check, and before the guard this pins, was appended
// anyway.
//
// Two entries for one instance are not cosmetic. configure's pairwise loop
// skips a pair only on pointer identity (p == other), so the instance is
// treated as its own peer: it imports its own certificate, and <inst>_login and
// <inst>_user are created in its own master and granted CONNECT on its own
// endpoint. A scripted run emits two groups for the one instance.
//
// AGAddReplicaDialog.connect has the same guard against the same mistake.
func TestAddInstanceRejectsAnInstanceAlreadyListedUnderAnotherName(t *testing.T) {
	d := &NewEndpointDialog{instances: []*newEndpointInstance{
		{name: "UBUSQL1", local: true},
		{name: "UBUSQL2"},
	}}
	// The instance answers as UBUSQL2 whatever it was reached by — an alias,
	// an address, a short hostname. That is the case the typed-name check
	// cannot see.
	d.resolveInstance = func(name string, done func(*newEndpointInstance, error)) {
		done(&newEndpointInstance{name: "UBUSQL2"}, nil)
	}

	rows, _ := d.instanceRows()
	f := propsheet.NewForm(rows...)

	textRow(t, f, "Instance name").Edit("10.0.0.2")
	clickButton(t, f, "Add Instance")

	if len(d.instances) != 2 {
		names := make([]string, len(d.instances))
		for i, inst := range d.instances {
			names[i] = inst.name
		}
		t.Fatalf("instances = %v, want UBUSQL2 rejected — a second entry for one "+
			"instance makes it its own peer in the certificate exchange", names)
	}
	if got := hintText(t, f); !strings.Contains(got, "already in the list") {
		t.Errorf("hint = %q, want it to say the instance answers as one already listed", got)
	}
}

// The same button on an instance that really is new, so the test above cannot
// pass by rejecting everything.
func TestAddInstanceAcceptsANewInstance(t *testing.T) {
	d := &NewEndpointDialog{instances: []*newEndpointInstance{
		{name: "UBUSQL1", local: true},
	}}
	d.resolveInstance = func(name string, done func(*newEndpointInstance, error)) {
		done(&newEndpointInstance{name: "UBUSQL2"}, nil)
	}

	rows, _ := d.instanceRows()
	f := propsheet.NewForm(rows...)

	textRow(t, f, "Instance name").Edit("ubusql2.example.com")
	clickButton(t, f, "Add Instance")

	if len(d.instances) != 2 || d.instances[1].name != "UBUSQL2" {
		t.Fatalf("instances = %+v, want UBUSQL2 added under the name it answers to", d.instances)
	}
}
