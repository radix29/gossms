package tui

import "testing"

// The step from gosmo's ServerInfo.IsWindows (pinned there) to the PathRules
// newServerFS installs has no test: it needs a *gosmo.Server whose Info()
// answers, and Info reads state only a real connection's loadInfo can set. Verified live instead — a
// win10cli browse splits "C:\..." correctly, which Posix rules could not do.

// newServerFS must refuse rather than fall back to the local disk: a
// LocalFileSystem here looks like it worked and hands back a path off this
// machine, which the server cannot see and BACKUP cannot write.
func TestNewServerFSRefusesWithoutAConnection(t *testing.T) {
	if fs, ok := newServerFS(nil); ok || fs != nil {
		t.Errorf("newServerFS(nil) = (%v, %v), want (nil, false)", fs, ok)
	}
}
