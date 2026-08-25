package tui

import "testing"

// TestConnectSurvivesADeniedSysInfoRead is gossms's half of the connect fix.
// gosmo reads sys.dm_os_sys_info in its own statement so a login without VIEW
// SERVER STATE can still connect; this pins that the app layer gets a usable
// *ServerConn out of it, since until 2026-08-25 a db_owner with no
// server-level rights was refused at the Connect dialog with "load server
// info: the user does not have permission to perform this action".
func TestConnectSurvivesADeniedSysInfoRead(t *testing.T) {
	sc, _ := newFakeConnWithoutSysInfo(t)

	info := sc.Server.Info()
	if !info.SysInfoUnavailable {
		t.Fatal("SysInfoUnavailable = false: the denied DMV read went unnoticed")
	}
	if got := info.ProductVersion; got != "16.0.4085.2" {
		t.Errorf("ProductVersion = %q, want the SERVERPROPERTY half to survive", got)
	}
}

// TestUnreadableSysInfoRendersAsNA pins the display side. The two fields are
// zero when the read was refused, and formatting them as numbers reports a
// machine with no CPUs and no memory as fact — the failure mode this whole
// change exists to avoid.
func TestUnreadableSysInfoRendersAsNA(t *testing.T) {
	denied, _ := newFakeConnWithoutSysInfo(t)
	info := denied.Server.Info()

	if got := sysInfoInt(info, int64(info.LogicalCPUCount)); got != "N/A" {
		t.Errorf("CPU count = %q, want %q", got, "N/A")
	}
	if got := sysInfoMB(info); got != "N/A" {
		t.Errorf("memory = %q, want %q", got, "N/A")
	}

	// And the readable case must still show the numbers, or the placeholder
	// has simply replaced the feature.
	ok, _ := newFakeConn(t)
	okInfo := ok.Server.Info()
	if got := sysInfoInt(okInfo, int64(okInfo.LogicalCPUCount)); got != "8" {
		t.Errorf("CPU count = %q, want %q", got, "8")
	}
	if got := sysInfoMB(okInfo); got != "16,384 MB" {
		t.Errorf("memory = %q, want %q", got, "16,384 MB")
	}
}
