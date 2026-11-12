package dialogs

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/tuikittest"
)

// cancelColumn is the column the Cancel button starts at on the button row.
func cancelColumn(t *testing.T, scr *tuikittest.Screen, d *ProgressDialog) int {
	t.Helper()
	row := scr.Row(d.ButtonRowY())
	before, _, ok := strings.Cut(row, "[ Cancel ]")
	if !ok {
		t.Fatalf("button row %q has no Cancel", row)
	}
	return len([]rune(before))
}

// Cancel asks the operation to stop exactly once and leaves the dialog up:
// only the operation returning says what it got through, and a dialog that
// closed on Cancel would say it was over. A second Cancel does nothing, and
// the button greys to say so.
func TestProgressDialogCancelAsksOnceAndStaysOpen(t *testing.T) {
	scr := tuikittest.NewScreen(120, 40)
	d := NewProgressDialog(scr)
	calls := 0
	d.ShowProgress("Delete Table", `Deleting table "dbo.Orders"...`, func() { calls++ })

	d.Draw(scr)
	x := cancelColumn(t, scr, d)
	y := d.ButtonRowY()
	if got := scr.StyleAt(x, y).GetForeground(); got == theme.Active().TextDisabled {
		t.Fatal("Cancel is drawn disabled on a cancellable operation")
	}

	d.HandleKey(tuikittest.Key(tcell.KeyEscape))
	if calls != 1 {
		t.Fatalf("onCancel ran %d times after Escape, want 1", calls)
	}
	if !d.Visible() {
		t.Fatal("Cancel closed the dialog — it must stay up until the operation returns")
	}
	if !d.Cancelling() || d.CanCancel() {
		t.Errorf("Cancelling() = %v, CanCancel() = %v after Cancel, want true, false", d.Cancelling(), d.CanCancel())
	}

	d.HandleKey(tuikittest.Key(tcell.KeyEnter))
	d.HandleMouse(tcell.NewEventMouse(x+2, y, tcell.Button1, tcell.ModNone))
	if calls != 1 {
		t.Errorf("onCancel ran %d times, want 1 — a second Cancel asked again", calls)
	}

	scr = tuikittest.NewScreen(120, 40)
	d.Draw(scr)
	if got := scr.StyleAt(x, y).GetForeground(); got != theme.Active().TextDisabled {
		t.Errorf("Cancel foreground after cancelling = %v, want disabled", got)
	}
	if body := scr.Row(d.InnerRect().Y + 3); !strings.Contains(body, "Cancelling") {
		t.Errorf("status line after Cancel = %q, want it to say the dialog is cancelling", body)
	}

	// A new showing starts live again.
	d.ShowProgress("Delete Table", "Deleting...", func() { calls++ })
	d.HandleKey(tuikittest.Key(tcell.KeyEscape))
	if calls != 2 {
		t.Errorf("onCancel ran %d times after a fresh showing's Escape, want 2", calls)
	}
}

// An uninterruptible operation's Cancel is drawn greyed, refuses the keyboard
// and the mouse, and the reason is on screen.
func TestProgressDialogUninterruptibleRefusesCancel(t *testing.T) {
	scr := tuikittest.NewScreen(120, 40)
	d := NewProgressDialog(scr)
	reason := "A failover cannot be safely interrupted."
	d.ShowUninterruptible("Fail Over", `Failing "ag1" over to node2...`, reason)
	d.Draw(scr)
	x := cancelColumn(t, scr, d)
	y := d.ButtonRowY()
	if got := scr.StyleAt(x, y).GetForeground(); got != theme.Active().TextDisabled {
		t.Errorf("Cancel foreground = %v, want disabled", got)
	}

	d.HandleKey(tuikittest.Key(tcell.KeyEscape))
	d.HandleKey(tuikittest.Key(tcell.KeyEnter))
	d.HandleMouse(tcell.NewEventMouse(x+2, y, tcell.Button1, tcell.ModNone))
	if d.Cancelling() || !d.Visible() {
		t.Errorf("Cancelling() = %v, Visible() = %v, want false, true — Cancel acted on an uninterruptible operation", d.Cancelling(), d.Visible())
	}

	var drawn strings.Builder
	for row := d.Rect().Y; row < d.Rect().Bottom(); row++ {
		drawn.WriteString(strings.TrimSpace(scr.Row(row)))
		drawn.WriteString(" ")
	}
	if !strings.Contains(drawn.String(), "cannot be safely") {
		t.Errorf("dialog does not show the reason Cancel is greyed:\n%s", drawn.String())
	}
}

// Before RevealDelay has passed the dialog draws nothing and acts on nothing —
// an Escape the user aimed at the tree must not cancel an operation they have
// not yet been shown — but it still swallows the input, so the tree does not
// get it either.
func TestProgressDialogSwallowsInputBeforeItIsRevealed(t *testing.T) {
	scr := tuikittest.NewScreen(120, 40)
	scr.Fill('~', tcell.StyleDefault) // so a drawn space shows too
	blank := scr.Snapshot()
	d := NewProgressDialog(scr)
	d.RevealDelay = time.Hour
	calls := 0
	d.ShowProgress("Delete Table", "Deleting...", func() { calls++ })

	if !d.HandleKey(tuikittest.Key(tcell.KeyEscape)) {
		t.Error("HandleKey returned false before the reveal — the key would reach whatever is underneath")
	}
	if !d.HandleMouse(tcell.NewEventMouse(1, 1, tcell.Button1, tcell.ModNone)) {
		t.Error("HandleMouse returned false before the reveal")
	}
	if calls != 0 {
		t.Errorf("onCancel ran %d times before the dialog was on screen, want 0", calls)
	}
	d.Draw(scr)
	if scr.Snapshot() != blank {
		t.Error("Draw painted before the reveal, want nothing")
	}

	d.RevealDelay = 0
	d.Draw(scr)
	if scr.Snapshot() == blank {
		t.Error("Draw painted nothing once revealed")
	}
}

// A batch rewrites the message per object; the dialog widens for a long name
// but does not shrink back for the next short one, or the box would jump
// sideways under the user. A new showing starts from its own message.
func TestProgressDialogNeverNarrowsWithinAShowing(t *testing.T) {
	d := NewProgressDialog(tuikittest.NewScreen(200, 50))
	d.ShowProgress("Delete Objects", "Deleting 1 of 3: a", func() {})
	base := d.Rect().W
	d.SetMessage("Deleting 2 of 3: " + strings.Repeat("x", 80))
	wide := d.Rect().W
	if wide <= base {
		t.Fatalf("width after a long message = %d, want wider than %d", wide, base)
	}
	d.SetMessage("Deleting 3 of 3: c")
	if got := d.Rect().W; got != wide {
		t.Errorf("width after a short message = %d, want it held at %d", got, wide)
	}
	d.ShowProgress("Delete Table", "Deleting c", func() {})
	if got := d.Rect().W; got != base {
		t.Errorf("width on a new showing = %d, want %d", got, base)
	}
}

func TestFormatElapsed(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0:00"},
		{-time.Second, "0:00"},
		{59 * time.Second, "0:59"},
		{61 * time.Second, "1:01"},
		{3661 * time.Second, "1:01:01"},
	} {
		if got := formatElapsed(c.d); got != c.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
