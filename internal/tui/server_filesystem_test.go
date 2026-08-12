package tui

import (
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// TestServerIsWindows pins all three branches. Getting this wrong is not a
// cosmetic slip: it picks the PathRules the whole browse runs on, and Posix
// rules over a Windows host make every "C:\..." path unsplittable — the
// dialog then lists the wrong directory and BACKUP is handed a destination
// the server cannot write.
func TestServerIsWindows(t *testing.T) {
	for _, tc := range []struct {
		what     string
		platform string
		backup   string
		want     bool
	}{
		// Platform is the direct answer and outranks the path, which is the
		// only ordering that survives a Linux instance whose backup directory
		// happens to be a UNC-looking share name.
		{"reported Windows", "Windows", `C:\Backup`, true},
		{"reported Linux", "Linux", "/var/opt/mssql/data", false},
		{"reported Windows despite a posix path", "Windows", "/var/opt/mssql", true},
		{"reported Linux despite a backslash", "Linux", `/var/opt/odd\name`, false},
		// Unreported: fall back to reading the default backup path.
		{"unreported, windows path", "", `C:\Program Files\Backup`, true},
		{"unreported, posix path", "", "/var/opt/mssql/data", false},
		// Nothing to go on at all. Posix is the safer guess: its rules leave a
		// Windows path in one piece as a single name, where Windows rules
		// applied to a posix path split it at separators that aren't there.
		{"nothing reported", "", "", false},
		// An unrecognized platform string is not a third answer — it falls
		// through to the path like an empty one.
		{"unknown platform, windows path", "Darwin", `D:\Backup`, true},
	} {
		info := &gosmo.ServerInfo{Platform: tc.platform, DefaultBackupPath: tc.backup}
		if got := serverIsWindows(info); got != tc.want {
			t.Errorf("%s: serverIsWindows(%q, %q) = %v, want %v",
				tc.what, tc.platform, tc.backup, got, tc.want)
		}
	}
}

// The step from serverIsWindows to the PathRules newServerFS installs has no
// test: it needs a *gosmo.Server whose Info() answers, and Info reads state
// only a real connection's loadInfo can set. Verified live instead — a
// win10cli browse splits "C:\..." correctly, which Posix rules could not do.

// newServerFS must refuse rather than fall back to the local disk: a
// LocalFileSystem here looks like it worked and hands back a path off this
// machine, which the server cannot see and BACKUP cannot write.
func TestNewServerFSRefusesWithoutAConnection(t *testing.T) {
	if fs, ok := newServerFS(nil); ok || fs != nil {
		t.Errorf("newServerFS(nil) = (%v, %v), want (nil, false)", fs, ok)
	}
}
