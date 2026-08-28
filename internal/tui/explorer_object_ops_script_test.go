package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// The delete confirmation's Script button, driven the way a user drives it. What
// these pin is that Script is not a Yes: nothing reaches the server, and what
// opens in the query window is the statement the Yes would have run — including
// the checkbox's effect on it, which a script built separately from the drop
// closures would be free to ignore.

// answerScript answers the open confirmation with Script, optionally ticking its
// checkbox first. Script is the last of the three buttons and the checkbox sits
// last in the cycle, so one Backtab reaches the box and Tabs from there walk
// Yes, No, Script.
func answerScript(t *testing.T, a *App, tickOption bool) {
	t.Helper()
	if !a.confirmDialog.Visible() {
		t.Fatal("no confirmation is open")
	}
	key := func(k tcell.Key, r string) {
		a.confirmDialog.HandleKey(tcell.NewEventKey(k, r, tcell.ModNone))
	}
	if tickOption {
		key(tcell.KeyBacktab, "")
		key(tcell.KeyRune, " ")
		key(tcell.KeyTab, "")
	}
	key(tcell.KeyTab, "")
	key(tcell.KeyTab, "")
	key(tcell.KeyEnter, "")
}

// scriptedText is the text of the query panel the Script answer opened, and
// fails when none did.
func scriptedText(t *testing.T, a *App, before int) string {
	t.Helper()
	if a.panels.Count() != before+1 {
		t.Fatalf("panels = %d, want one more than the %d open before Script", a.panels.Count(), before)
	}
	qp, ok := a.panels.PanelAt(a.panels.Count() - 1).(*QueryPanel)
	if !ok {
		t.Fatalf("the new panel is %T, want a query panel", a.panels.PanelAt(a.panels.Count()-1))
	}
	return qp.editor.Text()
}

func TestScriptingADeleteRunsNothing(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)
	node := opTestNode(sc, NodeTable, "sales", "Orders", "")
	before := a.panels.Count()

	a.deleteObject(node)
	answerScript(t, a, false)
	waitAndDrain(t, a)

	if stmts := inst.StatementsIn("appdb"); len(stmts) != 0 {
		t.Errorf("Script executed %q — it must run nothing at all", stmts)
	}
	if got := scriptedText(t, a, before); !strings.Contains(got, "DROP TABLE [sales].[Orders]") {
		t.Errorf("the query window holds %q, want the DROP the Yes would have run", got)
	}
}

// The checkbox changes the statements, so the script has to answer the question
// as it stands on screen — a generator that ignored it would hand the user a
// script that does less than the button beside it.
func TestScriptedDeleteFollowsTheCheckbox(t *testing.T) {
	for _, c := range []struct {
		name     string
		tick     bool
		wantCasc bool
	}{
		{"left unticked", false, false},
		{"ticked", true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp()
			sc, inst := opTestConn(t)
			node := opTestNode(sc, NodeTable, "sales", "Orders", "")
			before := a.panels.Count()

			a.deleteObject(node)
			answerScript(t, a, c.tick)
			waitAndDrain(t, a)

			if stmts := inst.StatementsIn("appdb"); len(stmts) != 0 {
				t.Errorf("Script executed %q", stmts)
			}
			text := scriptedText(t, a, before)
			if got := strings.Contains(text, "sys.foreign_keys"); got != c.wantCasc {
				t.Errorf("script covers the referencing foreign keys = %v, want %v:\n%s",
					got, c.wantCasc, text)
			}
		})
	}
}

// A selection scripts as a selection: every object, not just the one the batch
// happened to start with.
func TestScriptingASelectionCoversEveryObject(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)
	first := opTestNode(sc, NodeView, "sales", "vOrders", "")
	second := opTestNode(sc, NodeView, "sales", "vArchive", "")
	before := a.panels.Count()

	a.confirmDeleteObjects(sc, []nodeData{first.data, second.data}, func() {})
	answerScript(t, a, false)
	waitAndDrain(t, a)

	if stmts := inst.StatementsIn("appdb"); len(stmts) != 0 {
		t.Errorf("Script executed %q", stmts)
	}
	text := scriptedText(t, a, before)
	for _, want := range []string{"[sales].[vOrders]", "[sales].[vArchive]"} {
		if !strings.Contains(text, want) {
			t.Errorf("the script leaves out %s:\n%s", want, text)
		}
	}
}

// The typed confirmation's Script button is not gated on the retyped name: it
// runs nothing, so there is nothing for the retyping to protect.
func TestScriptingATypedDeleteNeedsNoTypedName(t *testing.T) {
	a := newTestApp()
	sc, inst := opTestConn(t)
	node := opTestNode(sc, NodeDatabase, "", "appdb", "")
	node.data.DBName = "appdb"
	before := a.panels.Count()

	a.deleteObject(node)
	if !a.confirmTypedDialog.Visible() {
		t.Fatal("deleting a database should ask for the name to be retyped")
	}
	// Input, Confirm, Cancel, Script: three Tabs from the input reach it, with
	// the field left empty.
	for range 3 {
		a.confirmTypedDialog.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
	}
	a.confirmTypedDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if a.confirmTypedDialog.Visible() {
		t.Fatal("Script left the confirmation open — it was treated as an unmatched Confirm")
	}
	waitAndDrain(t, a)

	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("Script executed %q", stmts)
	}
	if got := scriptedText(t, a, before); !strings.Contains(got, "DROP DATABASE [appdb]") {
		t.Errorf("the query window holds %q, want the database's DROP", got)
	}
}
