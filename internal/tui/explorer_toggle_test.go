package tui

import (
	"slices"
	"strings"
	"testing"
)

// The server trigger, database trigger and security policy toggles were once
// three hand-copied bodies; they now go through toggleEnabledState with their
// own statement and wording. These pin what each one hands the shared half:
// the statement that reaches the server, that only a disable asks first, and
// the name the status line shows — a policy's is schema-qualified.

func TestTriggerTogglesIssueTheirOwnStatement(t *testing.T) {
	for _, c := range []struct {
		name       string
		typ        NodeType
		toggle     func(a *App, n *explorerNode)
		wasEnabled bool
		wantStmt   string
		wantStatus string
	}{
		{"server trigger disable", NodeServerTrigger,
			func(a *App, n *explorerNode) { a.toggleServerTrigger(n.data.conn, n) }, true,
			"DISABLE TRIGGER [trg_logon] ON ALL SERVER", `Server trigger "trg_logon" is now disabled`},
		{"server trigger enable", NodeServerTrigger,
			func(a *App, n *explorerNode) { a.toggleServerTrigger(n.data.conn, n) }, false,
			"ENABLE TRIGGER [trg_logon] ON ALL SERVER", `Server trigger "trg_logon" is now enabled`},
		{"database trigger disable", NodeDatabaseTrigger,
			func(a *App, n *explorerNode) { a.toggleDatabaseTrigger(n.data.conn, n) }, true,
			"DISABLE TRIGGER [trg_logon] ON DATABASE", `Database trigger "trg_logon" is now disabled`},
		{"database trigger enable", NodeDatabaseTrigger,
			func(a *App, n *explorerNode) { a.toggleDatabaseTrigger(n.data.conn, n) }, false,
			"ENABLE TRIGGER [trg_logon] ON DATABASE", `Database trigger "trg_logon" is now enabled`},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp()
			sc, inst := opTestConn(t)
			node := opTestNode(sc, c.typ, "", "trg_logon", "")
			node.data.IsEnabled = c.wasEnabled

			c.toggle(a, node)
			if got := a.confirmDialog.Visible(); got != c.wasEnabled {
				t.Fatalf("confirmation shown = %v, want %v (only a disable asks)", got, c.wasEnabled)
			}
			if c.wasEnabled {
				answerConfirm(t, a, false)
			}
			waitAndDrain(t, a)

			stmts := inst.Statements()
			if c.typ == NodeDatabaseTrigger {
				stmts = inst.StatementsIn("appdb")
			}
			if !slices.ContainsFunc(stmts, func(s string) bool { return strings.Contains(s, c.wantStmt) }) {
				t.Errorf("statements = %q, want one containing %q", stmts, c.wantStmt)
			}
			if a.statusText != c.wantStatus {
				t.Errorf("status = %q, want %q", a.statusText, c.wantStatus)
			}
			if node.data.IsEnabled == c.wasEnabled {
				t.Error("the node's cached state did not flip")
			}
		})
	}
}

// A security policy is the one toggle whose display name is not node.data.Name:
// the prompt and the failure both name it schema-qualified. The fake has no
// sys.security_policies row, so the lookup fails — which is the path that
// shows the name on the status line without the write happening.
func TestSecurityPolicyToggleNamesThePolicyQualified(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)
	node := opTestNode(sc, NodeSecurityPolicy, "rls", "pol_orders", "")
	node.data.IsEnabled = true

	a.toggleSecurityPolicy(sc, node)
	if !a.confirmDialog.Visible() {
		t.Fatal("disabling a security policy ran without asking")
	}
	answerConfirm(t, a, false)
	waitAndDrain(t, a)

	if want := `Failed to disable "[rls].[pol_orders]": `; !strings.HasPrefix(a.statusText, want) {
		t.Errorf("status = %q, want prefix %q", a.statusText, want)
	}
	if !node.data.IsEnabled {
		t.Error("a failed toggle flipped the node's cached state")
	}
	if stmts := inst.StatementsIn("appdb"); len(stmts) != 0 {
		t.Errorf("a failed lookup still wrote: %q", stmts)
	}
}
