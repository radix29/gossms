package activity

import (
	"strings"
	"testing"
)

// The GPL-3.0 script must keep its author's copyright and the modification
// notice.
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

// The declaration line must appear once, or the script creates one name while
// the tab runs another.
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

// The About version comes from the script, so it matches the installed copy.
func TestWhoIsActiveVersion(t *testing.T) {
	got := WhoIsActiveVersion()
	if got == "" {
		t.Fatal("no version could be read out of the embedded script")
	}
	if !strings.HasPrefix(got, "v") {
		t.Errorf("WhoIsActiveVersion() = %q, want something like %q", got, "v1219.20260409")
	}
}
