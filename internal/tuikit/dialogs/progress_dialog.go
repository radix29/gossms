package dialogs

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// ---------------------------------------------------------------------------
// ProgressDialog — spinner, elapsed time and a Cancel button
// ---------------------------------------------------------------------------

// progressDialogMinW is the narrowest the dialog gets: room for the title, the
// status line and the Cancel button. progressDialogBaseH is its height with
// the message on one line and no reason line: border, blank, message, blank,
// status, blank, separator, buttons, blank, border. A reason adds its lines
// under the status line.
const (
	progressDialogMinW  = 56
	progressDialogBaseH = 10
)

// progressButtons is the dialog's button row: Cancel, and nothing else.
var progressButtons = []string{"Cancel"}

// ProgressDialog stands in front of the application while one long operation
// runs — the wait after a confirmation was answered, a delete or a failover
// running on the server. It shows what is happening, a spinner and the
// elapsed time, and has a single Cancel button; nothing else can be reached
// until the host hides it.
//
// Like every control here it runs nothing itself. The host starts the work,
// calls SetMessage as it moves through it, drives the redraw at
// Spinner.Period (the spinner is drawn from elapsed time, see widgets.Spinner),
// and calls Hide once the work has returned. Cancel does not close the
// dialog: it calls the onCancel the showing was given and switches to
// "Cancelling...", because what the operation got through is only known once
// it returns, and hiding before then would say it was over when it is not.
type ProgressDialog struct {
	ModalDialog
	message  string
	msgLines []string
	// shownW is the widest the dialog has been this showing. A batch rewrites
	// the message for every object, and a width recomputed from each one
	// would make the box jump sideways as the names change length.
	shownW int

	started time.Time
	// onCancel stops the operation; nil for an uninterruptible one, whose
	// Cancel draws greyed and reason says why.
	onCancel    func()
	reason      string
	reasonLines []string
	cancelling  bool

	// Spinner is the busy indicator; the host redraws at its Period.
	Spinner widgets.Spinner
	// RevealDelay holds the dialog back from the screen for the first moments
	// of a showing, so an operation that returns at once — most single DROPs
	// do — never flashes a box. The dialog is open, and swallows every key and
	// click, from ShowProgress on; it only draws once the delay has passed,
	// and Cancel cannot be pressed on a dialog the user cannot see.
	RevealDelay time.Duration
}

// NewProgressDialog creates a ProgressDialog.
func NewProgressDialog(s tcell.Screen) *ProgressDialog {
	d := new(ProgressDialog{Spinner: widgets.SpinnerBraille})
	d.InitModal(s, "Working", progressDialogMinW, progressDialogBaseH)
	return d
}

// ShowProgress opens the dialog for an operation onCancel can stop. onCancel
// runs once, on the first Cancel (or Escape); the dialog stays open, showing
// that it is cancelling, until the host hides it.
func (d *ProgressDialog) ShowProgress(title, message string, onCancel func()) {
	d.show(title, message, onCancel, "")
}

// ShowUninterruptible opens the dialog for an operation that must not be
// stopped halfway: Cancel draws greyed and does nothing, and reason is shown
// under the elapsed time to say why.
func (d *ProgressDialog) ShowUninterruptible(title, message, reason string) {
	d.show(title, message, nil, reason)
}

func (d *ProgressDialog) show(title, message string, onCancel func(), reason string) {
	d.SetTitle(title)
	d.message = message
	d.onCancel = onCancel
	d.reason = reason
	d.cancelling = false
	d.started = time.Now()
	d.shownW = 0
	d.fit()
	d.ModalDialog.Show()
}

// SetMessage replaces the line saying what is happening — the object a batch
// has reached, say. The dialog widens to fit it but never narrows within one
// showing.
func (d *ProgressDialog) SetMessage(message string) {
	d.message = message
	d.fit()
}

// Message returns the line saying what is happening.
func (d *ProgressDialog) Message() string { return d.message }

// Cancelling reports whether Cancel has been pressed this showing.
func (d *ProgressDialog) Cancelling() bool { return d.cancelling }

// CanCancel reports whether pressing Cancel now would stop the operation: it
// has a way to be stopped and has not already been asked to.
func (d *ProgressDialog) CanCancel() bool { return d.onCancel != nil && !d.cancelling }

// Revealed reports whether the dialog is drawing yet — see RevealDelay.
func (d *ProgressDialog) Revealed() bool {
	return d.visible && time.Since(d.started) >= d.RevealDelay
}

// Relayout re-wraps the message for the new screen width, then recentres.
func (d *ProgressDialog) Relayout() {
	d.shownW = 0
	d.fit()
}

// indent is the room the spinner takes in front of the message.
func (d *ProgressDialog) indent() int { return d.Spinner.Width() + 1 }

// fit sizes the dialog to its message, the way fitMessage does for the other
// message dialogs, with the spinner's column taken off the wrap width.
func (d *ProgressDialog) fit() {
	w := max(progressDialogMinW, core.DisplayWidth(d.message)+d.indent()+messageBoxOverhead,
		core.DisplayWidth(d.reason)+messageBoxOverhead, d.shownW)
	maxLines := 0
	if d.screen != nil {
		if sw, sh := d.screen.Size(); sw > 0 {
			if maxW := sw * maxMessageWidthNum / maxMessageWidthDen; w > maxW {
				w = max(progressDialogMinW, maxW)
			}
			maxLines = max(1, sh-progressDialogBaseH+1)
		}
	}
	// The reason is short and fixed, the message is the part that can run
	// long, so the reason is wrapped whole and the message gets what is left.
	d.reasonLines = nil
	if d.reason != "" {
		d.reasonLines = core.WrapText(d.reason, w-messageBoxOverhead)
		if maxLines > 0 {
			maxLines = max(1, maxLines-len(d.reasonLines))
		}
	}
	contentW := max(1, w-messageBoxOverhead-d.indent())
	if maxLines > 0 {
		d.msgLines = core.WrapTextLimit(d.message, contentW, maxLines)
	} else {
		d.msgLines = core.WrapText(d.message, contentW)
	}
	d.shownW = w
	d.SetSize(w, progressDialogBaseH+max(1, len(d.msgLines))-1+len(d.reasonLines))
}

// statusLine is the line under the message: the elapsed time, and once Cancel
// has been pressed, that the dialog is waiting for the operation to stop.
func (d *ProgressDialog) statusLine() string {
	elapsed := formatElapsed(time.Since(d.started))
	if d.cancelling {
		return "Cancelling — waiting for the server to stop (" + elapsed + ")"
	}
	return "Elapsed " + elapsed
}

// formatElapsed is m:ss, or h:mm:ss past the hour.
func formatElapsed(e time.Duration) string {
	s := int(max(0, e) / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// Draw renders the dialog, once RevealDelay has passed.
func (d *ProgressDialog) Draw(s tcell.Screen) {
	if !d.Revealed() {
		return
	}
	d.DrawBase(s)
	p := theme.Active()
	msgStyle := theme.StyleDialog()
	dimStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
	inner := d.InnerRect()
	x := inner.X + 1
	contentW := inner.W - 2
	y := inner.Y + 1
	d.Spinner.DrawSince(s, x, y, msgStyle, d.started)
	for i, line := range d.msgLines {
		core.DrawTextClipped(s, x+d.indent(), y+i, contentW-d.indent(), msgStyle, line)
	}
	y += max(1, len(d.msgLines)) + 1
	core.DrawTextClipped(s, x, y, contentW, dimStyle, d.statusLine())
	for i, line := range d.reasonLines {
		core.DrawTextClipped(s, x, y+1+i, contentW, dimStyle, line)
	}
	d.DrawSeparator(s)
	d.DrawButtonsGated(s, progressButtons, 0, []bool{!d.CanCancel()})
}

// cancel is the Cancel button, and Escape: ask the operation to stop, once.
func (d *ProgressDialog) cancel() {
	if !d.CanCancel() {
		return
	}
	d.cancelling = true
	d.onCancel()
}

// HandleKey swallows every key while the dialog is open; Escape, Enter and
// Space press Cancel once it is on screen.
func (d *ProgressDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.visible {
		return false
	}
	if !d.Revealed() {
		return true
	}
	switch {
	case ev.Key() == tcell.KeyEscape, ev.Key() == tcell.KeyEnter,
		ev.Key() == tcell.KeyRune && ev.Str() == " ":
		d.cancel()
	}
	return true
}

// HandleMouse swallows every click while the dialog is open; a click on
// Cancel presses it once the dialog is on screen.
func (d *ProgressDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.visible {
		return false
	}
	if d.ConsumeOutsideClick(ev) || !d.Revealed() {
		return true
	}
	if d.ButtonClicked(ev, progressButtons) == 0 {
		d.cancel()
	}
	return true
}
