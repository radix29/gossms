package tui

import (
	"context"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// DeviceCodeDialog shows the code a Microsoft Entra Device Code sign-in is
// waiting on, for the user to enter at Microsoft's page on any device — a
// phone, or a browser on another machine when gossms runs over SSH.
//
// It lives exactly as long as the sign-in: App.promptDeviceCode opens it and
// closes it again when the sign-in's context ends, whether the code was
// entered, the attempt timed out, or it was cancelled. Left to azidentity,
// the code is printed to standard output — the terminal tcell draws on — and
// erased by the next redraw.
type DeviceCodeDialog struct {
	dialogs.ModalDialog
	app *App

	msg gosmo.DeviceCodeMessage
	// cancel cancels the sign-in the code belongs to (db.SignInCanceller);
	// nil when the prompt came without one, and Cancel then only closes.
	cancel context.CancelFunc
	// done identifies the sign-in this showing belongs to, so the end of an
	// earlier one does not close the dialog a later one opened.
	done     <-chan struct{}
	btnFocus int
}

const (
	deviceCodeDialogMinW = 64
	deviceCodeDialogH    = 13
)

var deviceCodeButtons = []string{"Copy Code", "Cancel Sign-in"}

// NewDeviceCodeDialog creates the dialog.
func NewDeviceCodeDialog(app *App) *DeviceCodeDialog {
	d := &DeviceCodeDialog{app: app}
	d.InitModal(app.screen, "Microsoft Entra Sign-in", deviceCodeDialogMinW, deviceCodeDialogH)
	return d
}

// promptDeviceCode is the app's db.DeviceCodePrompt. It runs on the
// connecting goroutine and must not block it — the sign-in only starts
// polling for the user once it returns — so it posts the dialog and returns.
func (a *App) promptDeviceCode(ctx context.Context, m gosmo.DeviceCodeMessage) error {
	cancel, done := db.SignInCanceller(ctx), ctx.Done()
	a.postAndWake(func() { a.deviceCodeDialog.showCode(m, cancel, done) })
	if done != nil {
		a.safego("waiting for a device-code sign-in to end", func() {
			<-done
			a.postAndWake(func() { a.deviceCodeDialog.signInEnded(done) })
		})
	}
	return nil
}

// showCode opens the dialog on m, replacing whatever code it showed.
func (d *DeviceCodeDialog) showCode(m gosmo.DeviceCodeMessage, cancel context.CancelFunc, done <-chan struct{}) {
	d.msg, d.cancel, d.done = m, cancel, done
	d.btnFocus = 0
	d.SetSize(max(deviceCodeDialogMinW, core.DisplayWidth(m.VerificationURL)+8), deviceCodeDialogH)
	d.Show()
}

// signInEnded closes the dialog if it still shows the sign-in done belongs to.
func (d *DeviceCodeDialog) signInEnded(done <-chan struct{}) {
	if d.Visible() && d.done == done {
		d.close()
	}
}

func (d *DeviceCodeDialog) close() {
	d.Hide()
	d.cancel, d.done = nil, nil
}

// cancelSignIn fails the sign-in — and so the connection attempt waiting on
// it — and closes the dialog.
func (d *DeviceCodeDialog) cancelSignIn() {
	if d.cancel != nil {
		d.cancel()
	}
	d.close()
}

func (d *DeviceCodeDialog) doButton(i int) {
	switch i {
	case 0:
		d.app.copyWithStatus(d.msg.UserCode)
	case 1:
		d.cancelSignIn()
	}
}

// Draw renders the dialog.
func (d *DeviceCodeDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	p := theme.Active()
	text := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	strong := text.Bold(true)
	inner := d.InnerRect()
	x, w := inner.X+2, inner.W-4
	y := inner.Y + 1
	if d.msg.UserCode == "" || d.msg.VerificationURL == "" {
		// Nothing to lay out; Microsoft's own sentence carries whatever
		// there is.
		for i, line := range core.WrapTextLimit(d.msg.Message, w, 6) {
			core.DrawTextClipped(s, x, y+i, w, text, line)
		}
	} else {
		core.DrawTextClipped(s, x, y, w, text, "To sign in, open this page in a browser on any device:")
		core.DrawTextClipped(s, x+2, y+1, w-2, strong, d.msg.VerificationURL)
		core.DrawTextClipped(s, x, y+3, w, text, "and enter the code:")
		core.DrawTextClipped(s, x+2, y+4, w-2, theme.StyleSelected().Bold(true), " "+d.msg.UserCode+" ")
		core.DrawTextClipped(s, x, y+6, w, text, "The connection continues once the sign-in completes.")
	}
	d.DrawSeparator(s)
	d.DrawButtons(s, deviceCodeButtons, d.btnFocus)
}

// HandleKey handles keyboard events. Escape cancels the sign-in, as it
// cancels the Connect dialog's attempt: the dialog has no other reason to
// close.
func (d *DeviceCodeDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		d.cancelSignIn()
	case tcell.KeyEnter:
		d.doButton(d.btnFocus)
	case tcell.KeyTab, tcell.KeyRight:
		d.btnFocus = (d.btnFocus + 1) % len(deviceCodeButtons)
	case tcell.KeyBacktab, tcell.KeyLeft:
		d.btnFocus = (d.btnFocus + len(deviceCodeButtons) - 1) % len(deviceCodeButtons)
	}
	return true
}

// HandleMouse handles mouse events.
func (d *DeviceCodeDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if i := d.ButtonClicked(ev, deviceCodeButtons); i >= 0 {
		d.btnFocus = i
		d.doButton(i)
	}
	return true
}

// FocusedClipboardTarget implements core.ClipboardHost: the code, so Ctrl+C
// copies it wherever focus is — through the OS clipboard, or OSC 52 to the
// terminal's own over SSH.
func (d *DeviceCodeDialog) FocusedClipboardTarget() core.ClipboardTarget {
	if d.msg.UserCode == "" {
		return nil
	}
	return d
}

// The code is the dialog's one piece of text, always selected and never
// editable: Cut copies it and Paste does nothing.

func (d *DeviceCodeDialog) HasSelection() bool   { return d.msg.UserCode != "" }
func (d *DeviceCodeDialog) SelectedText() string { return d.msg.UserCode }
func (d *DeviceCodeDialog) Cut() string          { return d.msg.UserCode }
func (d *DeviceCodeDialog) Paste(string)         {}
func (d *DeviceCodeDialog) SelectAll()           {}
