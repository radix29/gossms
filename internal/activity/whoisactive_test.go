package activity

import (
	"strings"
	"testing"
)

// The embedded script is GPL-3.0 and must keep carrying its author's
// copyright and the notice that goSSMS modified it — dropping either is a
// licence violation, not a formatting change.
func TestWhoIsActiveScriptKeepsItsAttribution(t *testing.T) {
	for _, want := range []string{
		"Adam Machanic",
		"MODIFIED by goSSMS",
		"https://github.com/amachanic/sp_whoisactive",
	} {
		if !strings.Contains(whoIsActiveScript, want) {
			t.Errorf("the embedded sp_WhoIsActive script no longer carries %q", want)
		}
	}
}

// The rename has exactly one place to land. A second copy of the declaration
// line would leave the script creating one name and the tab running another.
func TestWhoIsActiveHeaderAppearsOnce(t *testing.T) {
	if n := strings.Count(whoIsActiveScript, whoIsActiveProcHeader); n != 1 {
		t.Fatalf("the declaration goSSMS rewrites appears %d times, want 1", n)
	}
	tempdb := WhoIsActiveProc.Script(ProcTempDB)
	if strings.Contains(tempdb, whoIsActiveProcHeader) {
		t.Error("the tempdb script still carries the upstream declaration")
	}
	if !strings.Contains(tempdb, "create or alter procedure dbo."+WhoIsActiveProc.TempDBName+"\r\n(") {
		t.Error("the rewritten declaration did not keep the parameter list attached")
	}
}

// The version shown in Help > About is read out of the script, so it cannot
// disagree with the copy actually installed.
func TestWhoIsActiveVersion(t *testing.T) {
	got := WhoIsActiveVersion()
	if got == "" {
		t.Fatal("no version could be read out of the embedded script")
	}
	if !strings.HasPrefix(got, "v") {
		t.Errorf("WhoIsActiveVersion() = %q, want something like %q", got, "v1219.20260409")
	}
}
