package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/controls"
)

// menuWriteVerbs are the first words of a context-menu item that changes
// server state. An item whose label starts with one must carry a permission
// gate — see TestEveryWriteMenuItemIsGated.
var menuWriteVerbs = []string{
	"Delete", "Drop", "Enable", "Disable", "Start", "Stop", "Take", "Bring",
	"Remove", "Suspend", "Resume", "Failover", "Fail", "Force", "Cycle", "Join",
}

// ungatedWriteItems are the write-verb labels deliberately offered without a
// permission gate, each with the reason. An entry here is a decision, not a way
// to make the test pass.
var ungatedWriteItems = map[string]string{
	// Clears the tree's own client-side filter on a folder; nothing is sent to
	// the server, so there is no right to ask for.
	"Remove Filter": "client-side tree filter",
}

// isWriteLabel reports whether label starts with one of menuWriteVerbs as a
// whole word, so "Start Job" counts and "Startup Parameters" would not.
func isWriteLabel(label string) bool {
	first, _, _ := strings.Cut(label, " ")
	first = strings.TrimSuffix(first, "...")
	return slices.Contains(menuWriteVerbs, first)
}

// TestEveryWriteMenuItemIsGated builds every node type's full context menu —
// its nodeMenus builder plus the Script, Rename/Delete and Filter groups
// contextMenuItemsForNode splices in — and fails on any item whose label names a write and which carries no permission gate.
// ui-rules says every menu item must be context-gated, and the page-gating
// meta-test covers pages only: Start/Stop/Enable/Disable/Delete on the SQL
// Agent leaves shipped ungated beside a gated Rename and New … on the same
// nodes, failing open for a login in no msdb Agent role.
//
// "Carries a gate" is read from Note: gateOnAll is the one place a menu item's
// Note is set, and it sets it whenever the item has rights to ask about —
// whether or not they are held, so an unprobed connection is enough here.
func TestEveryWriteMenuItemIsGated(t *testing.T) {
	sc, _ := newFakeConn(t)
	a := newTestApp()
	for nt := NodeType(0); nt <= NodeError; nt++ {
		node := &explorerNode{label: "x", data: nodeData{
			Type: nt, conn: sc, DBName: "db1", Schema: "dbo", Name: "x", TableName: "t",
		}}
		var walk func([]controls.MenuItem)
		walk = func(items []controls.MenuItem) {
			for _, it := range items {
				if len(it.Sub) > 0 {
					walk(it.Sub)
					continue
				}
				if it.Divider || !isWriteLabel(it.Label) {
					continue
				}
				if _, ok := ungatedWriteItems[it.Label]; ok {
					continue
				}
				if it.Note == "" {
					t.Errorf("node type %v: %q is a write with no permission gate", nt, it.Label)
				}
			}
		}
		walk(a.contextMenuItemsForNode(node))
	}
}

func TestIsWriteLabel(t *testing.T) {
	for label, want := range map[string]bool{
		"Start Job":          true,
		"Delete Job...":      true,
		"Disable Schedule":   true,
		"Startup Parameters": false,
		"Properties...":      false,
		"New Job...":         false,
		"View History":       false,
	} {
		if got := isWriteLabel(label); got != want {
			t.Errorf("isWriteLabel(%q) = %v, want %v", label, got, want)
		}
	}
}
