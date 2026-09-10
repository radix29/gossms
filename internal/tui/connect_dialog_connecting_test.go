package tui

import (
	"context"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// connectingDialog is a shown ConnectDialog put into the state a pressed
// Connect leaves it in, without dialling anything: startConnect's own dial is
// what the state exists for, and no test may reach a server.
func connectingDialog(t *testing.T) *ConnectDialog {
	t.Helper()
	d := NewConnectDialog(newTestApp())
	d.Show()
	d.fServer.SetValue("srv")
	d.connecting = true
	d.btnFocus = 1
	return d
}

func typeKey(d *ConnectDialog, r string) {
	d.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
}

// TestConnectingDisablesEveryControlButCancel pins the rule that an attempt in
// flight owns the dialog: the fields it is connecting with must not change
// under it, and Tab must not walk focus into a control that is drawn disabled.
func TestConnectingDisablesEveryControlButCancel(t *testing.T) {
	d := connectingDialog(t)

	typeKey(d, "x")
	if got := d.fServer.Value(); got != "srv" {
		t.Fatalf("server field = %q after typing during a connect attempt, want it unchanged (%q)", got, "srv")
	}
	d.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
	if d.focusIdx != 0 {
		t.Fatalf("focus moved to index %d on Tab during a connect attempt, want it pinned to 0", d.focusIdx)
	}
	if d.btnFocus != 1 {
		t.Fatalf("button focus = %d during a connect attempt, want 1 (Cancel)", d.btnFocus)
	}
}

// TestEscapeCancelsAnAttemptInFlight covers the one control that stays live.
// Cancel closes the dialog and drops the attempt — connectAttempt going nil is
// what makes the dial's callback answer "not wanted" and wind itself back.
//
// The dial itself is aborted too: its context is cancelled, so the attempt does
// not run on to the 30-second connect timeout behind a dialog the user closed.
func TestEscapeCancelsAnAttemptInFlight(t *testing.T) {
	d := connectingDialog(t)
	d.connectAttempt = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	d.connectCancel = cancel

	d.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
	if d.Visible() {
		t.Fatal("the dialog is still visible after Escape during a connect attempt, want it closed")
	}
	if d.connecting || d.connectAttempt != nil {
		t.Fatal("the attempt was not abandoned on Cancel — its callback would still act on the dialog")
	}
	if ctx.Err() == nil {
		t.Fatal("the dial's context is still live after Cancel — the attempt runs on to its timeout")
	}
}

// TestFailedAttemptLeavesTheDialogOpen is the whole point of connecting in
// place: a wrong password is corrected in the fields as typed, not retyped into
// a dialog that closed itself. Enter must reach Connect again afterwards, so
// the button focus Cancel-only mode moved has to come back.
func TestFailedAttemptLeavesTheDialogOpen(t *testing.T) {
	d := connectingDialog(t)
	d.connectAttempt = make(chan struct{})

	// What connectServer's done callback does on a failure.
	d.stopConnecting()
	d.btnFocus = 0

	if !d.Visible() {
		t.Fatal("the dialog closed on a failed attempt, want it left open with the typed values")
	}
	if got := d.fServer.Value(); got != "srv" {
		t.Fatalf("server field = %q after a failed attempt, want the typed value kept (%q)", got, "srv")
	}
	if d.connecting {
		t.Fatal("still in the connecting state after the attempt resolved — the spinner would never stop")
	}
	typeKey(d, "x")
	if got := d.fServer.Value(); got != "srvx" {
		t.Fatalf("server field = %q after typing once the attempt failed, want the field editable again", got)
	}
}

// An attempt moves the button focus to Cancel for its duration; one that is
// cancelled (or succeeds) closes the dialog with it still there. Reopened,
// Enter must reach Connect again — it used to close the dialog instead.
func TestReopenedDialogEnterConnectsAgain(t *testing.T) {
	d := connectingDialog(t)
	d.connectAttempt = make(chan struct{})
	d.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))

	d.Show()
	if d.btnFocus != 0 {
		t.Fatalf("button focus = %d on a reopened dialog, want 0 (Connect)", d.btnFocus)
	}
}
