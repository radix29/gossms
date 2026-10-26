package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
)

// A table's or view's Triggers folder lists DML triggers, and a disabled one
// fires on nothing while drawing the same icon. The label is the only place
// that says so, IsEnabled picks the menu's Enable/Disable wording, and
// TableName is what that item's gate asks about.
func TestTriggersFolderLabelsTheDisabledOne(t *testing.T) {
	sc, _ := newFakeConn(t, dbTriggerDatabaseRow(), fakeResponse{
		match: "tr.parent_id = OBJECT_ID(@p1)",
		cols:  5,
		rows: [][]driver.Value{
			{"trg_audit", "Orders", "Sales", false, jsonNames("INSERT")},
			{"trg_guard", "Orders", "Sales", true, jsonNames("DELETE")},
		},
	})
	l := loaderCtx{ctx: context.Background(), sc: sc}
	children, err := loadTriggersChildren(l, &explorerNode{data: nodeData{
		Type: NodeTriggers, Schema: "Sales", Name: "Orders", DBName: "appdb", conn: sc}})
	if err != nil {
		t.Fatalf("loadTriggersChildren: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("got %d children, want 2", len(children))
	}
	if children[0].label != "trg_audit" || !children[0].data.IsEnabled {
		t.Errorf("enabled trigger: label %q, IsEnabled %v", children[0].label, children[0].data.IsEnabled)
	}
	if children[1].label != "trg_guard (Disabled)" || children[1].data.IsEnabled {
		t.Errorf("disabled trigger: label %q, IsEnabled %v", children[1].label, children[1].data.IsEnabled)
	}
	for _, c := range children {
		if c.data.Name != strings.TrimSuffix(c.label, " (Disabled)") || c.data.TableName != "Orders" {
			t.Errorf("node %q: Name %q, TableName %q", c.label, c.data.Name, c.data.TableName)
		}
	}
}

// The toggle names the trigger and the object it is on; the node's TriggerRef
// handle looks the parent up first, so the fake answers that lookup. Only a
// disable asks.
func TestTriggerToggleIssuesItsStatement(t *testing.T) {
	for _, c := range []struct {
		wasEnabled bool
		wantStmt   string
		wantStatus string
	}{
		{true, "DISABLE TRIGGER [Sales].[trg_guard] ON [Sales].[Orders]", `Trigger "[Sales].[trg_guard]" is now disabled`},
		{false, "ENABLE TRIGGER [Sales].[trg_guard] ON [Sales].[Orders]", `Trigger "[Sales].[trg_guard]" is now enabled`},
	} {
		t.Run(c.wantStatus, func(t *testing.T) {
			a := newTestApp()
			sc, inst := newFakeConn(t, fakeResponse{match: "JOIN   sys.objects p", cols: 1, rows: [][]driver.Value{{"Orders"}}})
			node := opTestNode(sc, NodeTrigger, "Sales", "trg_guard", "Orders")
			node.data.IsEnabled = c.wasEnabled

			a.toggleTrigger(sc, node)
			if got := a.confirmDialog.Visible(); got != c.wasEnabled {
				t.Fatalf("confirmation shown = %v, want %v (only a disable asks)", got, c.wasEnabled)
			}
			if c.wasEnabled {
				answerConfirm(t, a, false)
			}
			waitAndDrain(t, a)

			stmts := inst.StatementsIn("appdb")
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

// ENABLE/DISABLE TRIGGER checks ALTER on the table or view, not on the
// trigger, so a principal granted ALTER on one table keeps the toggle on that
// table's triggers and loses it on another's — even a trigger that shares the
// granted table's name.
func TestTriggerToggleIsGatedOnTheParent(t *testing.T) {
	sc := objectProbedConn(t, "appdb", []string{"Sales.Orders"}, nil)
	toggleEnabled := func(name, parent string) bool {
		t.Helper()
		node := &explorerNode{data: nodeData{Type: NodeTrigger, Schema: "Sales", Name: name,
			TableName: parent, DBName: "appdb", conn: sc}}
		for _, it := range (&App{}).nodeMenuItems(node) {
			if it.Label == "Enable" {
				return it.Enabled == nil || it.Enabled()
			}
		}
		t.Fatal("a trigger's menu has no Enable item")
		return false
	}
	if !toggleEnabled("trg_audit", "Orders") {
		t.Error("the toggle was withheld on a trigger of the table the principal may alter")
	}
	if toggleEnabled("Orders", "Customers") {
		t.Error("the toggle was offered on another table's trigger — the gate asked about the trigger, not its parent")
	}
}
