package tui

import (
	"strings"
	"testing"
)

// TestAnnotateEndpointScriptGroupsByInstance is the whole point of replacing
// the shell's Script Changes: the statements belong to two instances, and run
// whole against one of them the certificate imports fail and the endpoint
// GRANTs land on the wrong endpoint.
func TestAnnotateEndpointScriptGroupsByInstance(t *testing.T) {
	out := annotateEndpointScript([]endpointScriptGroup{
		{instance: "UBUSQL1", stmts: []string{"CREATE CERTIFICATE [UBUSQL1_Cert]", "CREATE ENDPOINT one"}},
		{instance: "UBUSQL2", stmts: []string{"CREATE CERTIFICATE [UBUSQL2_Cert]"}},
	})

	first := strings.Index(out, "-- on UBUSQL1")
	second := strings.Index(out, "-- on UBUSQL2")
	if first < 0 || second < 0 {
		t.Fatalf("both instance headers must appear:\n%s", out)
	}
	if first > second {
		t.Error("instances are emitted out of order")
	}
	// Each statement has to sit under its own instance's header, which is the
	// assertion a flat "does the text contain it" check would not make.
	for _, tc := range []struct{ stmt, instance string }{
		{"CREATE CERTIFICATE [UBUSQL1_Cert]", "UBUSQL1"},
		{"CREATE ENDPOINT one", "UBUSQL1"},
		{"CREATE CERTIFICATE [UBUSQL2_Cert]", "UBUSQL2"},
	} {
		if got := instanceSectionFor(out, tc.stmt); got != tc.instance {
			t.Errorf("%q is under %q, want %q", tc.stmt, got, tc.instance)
		}
	}
	if n := strings.Count(out, "\nGO\n"); n != 3 {
		t.Errorf("script has %d GO separators, want one per statement (3)", n)
	}
}

// instanceSectionFor reports which "-- on X" section stmt appears in, or "" if
// it appears before any of them.
func instanceSectionFor(script, stmt string) string {
	section := ""
	for _, line := range strings.Split(script, "\n") {
		if after, ok := strings.CutPrefix(line, "-- on "); ok {
			section = after
		}
		if line == stmt {
			return section
		}
	}
	return "(not found)"
}

// TestAnnotateEndpointScriptStatesTheGap pins the honest half. A certificate
// has to exist before its public key can be read, so the first Script Changes
// on a fresh pair cannot contain the exchange — and a script that looks
// complete and configures nothing is worse than one that says so.
func TestAnnotateEndpointScriptStatesTheGap(t *testing.T) {
	out := annotateEndpointScript([]endpointScriptGroup{
		{instance: "UBUSQL1", stmts: []string{"CREATE CERTIFICATE [UBUSQL1_Cert]"},
			certPending: true, certSkipped: []string{"UBUSQL2"}},
		{instance: "UBUSQL2", stmts: []string{"CREATE CERTIFICATE [UBUSQL2_Cert]"},
			certPending: true, certSkipped: []string{"UBUSQL1"}},
	})

	if !strings.Contains(out, "THIS SCRIPT IS INCOMPLETE") {
		t.Errorf("a partial script does not say so:\n%s", out)
	}
	if !strings.Contains(out, "press Script Changes again") {
		t.Error("the script does not say what to do about the gap")
	}
	// Each instance's note has to name what is missing *there* — a generic
	// warning would not tell the user which half to re-run.
	if got := instanceSectionFor(out, "-- UBUSQL1 has no certificate yet. Its public key is what every other instance"); got != "UBUSQL1" {
		t.Errorf("the pending-certificate note for UBUSQL1 is under %q", got)
	}
	if !strings.Contains(out, "the certificate, login and user for UBUSQL2") {
		t.Errorf("UBUSQL1's script does not name the peer whose import it is missing:\n%s", out)
	}
}

// TestAnnotateEndpointScriptSaysNothingWhenComplete is the other direction: a
// second press, after the certificates exist, produces a script with no
// warning and no instruction to press again.
func TestAnnotateEndpointScriptSaysNothingWhenComplete(t *testing.T) {
	out := annotateEndpointScript([]endpointScriptGroup{
		{instance: "UBUSQL1", stmts: []string{"CREATE CERTIFICATE [UBUSQL2_Cert] FROM BINARY = 0xAB"}},
		{instance: "UBUSQL2", stmts: []string{"CREATE CERTIFICATE [UBUSQL1_Cert] FROM BINARY = 0xCD"}},
	})

	for _, unwanted := range []string{"INCOMPLETE", "press Script Changes again", "has no certificate yet", "Missing here"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a complete script still warns about %q:\n%s", unwanted, out)
		}
	}
}

// TestAnnotateEndpointScriptSkipsAnInstanceWithNothingToDo pins that an
// instance already fully configured is left out rather than emitted as an
// empty section — an "-- on X" heading with nothing under it reads as a step
// the user failed to notice.
func TestAnnotateEndpointScriptSkipsAnInstanceWithNothingToDo(t *testing.T) {
	out := annotateEndpointScript([]endpointScriptGroup{
		{instance: "UBUSQL1", stmts: []string{"CREATE ENDPOINT one"}},
		{instance: "UBUSQL2"},
	})
	if strings.Contains(out, "-- on UBUSQL2") {
		t.Errorf("an instance with no statements was emitted as an empty section:\n%s", out)
	}
	if !strings.Contains(out, "-- on UBUSQL1") {
		t.Error("the instance that does have work lost its section")
	}
}
