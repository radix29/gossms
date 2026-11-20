package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/core"
	"github.com/radix29/tuikit/dialogs"
	"github.com/radix29/tuikit/theme"
)

// DeviceCodeDialog shows the code a Microsoft Entra Device Code sign-in is waiting
// on, for the user to enter at Microsoft's page on any device — a phone, or a
// browser on another machine when gossms runs over SSH.
//
// It lives exactly as long as the sign-in: App.promptDeviceCode opens it and
// closes it when the sign-in's context ends, whether the code was entered, the
// attempt timed out, or it was cancelled. Left to azidentity, the code is printed
// to standard output — the terminal tcell draws on — and erased by the next
// redraw.
type DeviceCodeDialog struct {
	dialogs.MessageDialog
	app *App

	msg gosmo.DeviceCodeMessage
	// cancel cancels the sign-in the code belongs to (db.SignInCanceller);
	// nil when the prompt came without one, and Cancel then only closes.
	cancel context.CancelFunc
	// done identifies the sign-in this showing belongs to, so the end of an
	// earlier one does not close the dialog a later one opened.
	done <-chan struct{}
}

const (
	deviceCodeDialogMinW = 64
	deviceCodeDialogH    = 13
)

var deviceCodeButtons = []string{"Copy Code", "Cancel Sign-in"}

// NewDeviceCodeDialog creates the dialog.
func NewDeviceCodeDialog(app *App) *DeviceCodeDialog {
	d := &DeviceCodeDialog{app: app}
	d.InitMessage(dialogs.MessageDialogSpec{
		Title: "Microsoft Entra Sign-in", W: deviceCodeDialogMinW, H: deviceCodeDialogH,
		Buttons:  deviceCodeButtons,
		OnButton: d.doButton,
		// Escape cancels the sign-in, as it cancels the Connect dialog's
		// attempt: the dialog has no other reason to close.
		OnEscape: d.cancelSignIn,
	})
	return d
}

// promptDeviceCode is the app's db.DeviceCodePrompt. It runs on the
// connecting goroutine and must not block it — the sign-in only starts
// polling for the user once it returns — so it posts the dialog and returns.
func (a *App) promptDeviceCode(ctx context.Context, m gosmo.DeviceCodeMessage) error {
	cancel, done := db.SignInCanceller(ctx), ctx.Done()
	a.postAndWake(func() { a.deviceCodeDialog().showCode(m, cancel, done) })
	if done != nil {
		a.safego("waiting for a device-code sign-in to end", func() {
			<-done
			a.postAndWake(func() { a.deviceCodeDialog().signInEnded(done) })
		})
	}
	return nil
}

// showCode opens the dialog on m, replacing whatever code it showed.
func (d *DeviceCodeDialog) showCode(m gosmo.DeviceCodeMessage, cancel context.CancelFunc, done <-chan struct{}) {
	d.msg, d.cancel, d.done = m, cancel, done
	d.SetLines(deviceCodeLines(m))
	d.SetButtons(deviceCodeButtons, 0)
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

// deviceCodeLines lays out m: the page and the code, each indented under
// its own sentence.
func deviceCodeLines(m gosmo.DeviceCodeMessage) []dialogs.MessageLine {
	if m.UserCode == "" || m.VerificationURL == "" {
		// Nothing to lay out; Microsoft's own sentence carries whatever
		// there is.
		return []dialogs.MessageLine{{Text: m.Message, Indent: 1, Wrap: true}}
	}
	return []dialogs.MessageLine{
		{Text: "To sign in, open this page in a browser on any device:", Indent: 1},
		{Text: m.VerificationURL, Indent: 3, Style: theme.StyleDialog().Bold(true)},
		{},
		{Text: "and enter the code:", Indent: 1},
		{Text: " " + m.UserCode + " ", Indent: 3, Style: theme.StyleSelected().Bold(true)},
		{},
		{Text: "The connection continues once the sign-in completes.", Indent: 1},
	}
}

// FocusClipboardTargetAt implements core.ClipboardFocuser: a right-click
// anywhere in the dialog's content, above its buttons, offers the code's Copy.
func (d *DeviceCodeDialog) FocusClipboardTargetAt(x, y int) bool {
	return d.msg.UserCode != "" && d.InnerRect().Contains(x, y) && y < d.ButtonRowY()
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

// CanCopy, CanCut and CanPaste implement core.ClipboardTarget: the code is
// copied, never edited, so its right-click menu is Copy alone.
func (d *DeviceCodeDialog) CanCopy() bool      { return d.HasSelection() }
func (d *DeviceCodeDialog) CanCut() bool       { return false }
func (d *DeviceCodeDialog) CanPaste() bool     { return false }
func (d *DeviceCodeDialog) CanSelectAll() bool { return false }
