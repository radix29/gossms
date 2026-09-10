package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

func newDeviceCodeTestApp() *App {
	a := newTestApp()
	a.deviceCodeDialog = NewDeviceCodeDialog(a)
	return a
}

var testDeviceCode = gosmo.DeviceCodeMessage{
	UserCode: "F7KQ2ZP3M", VerificationURL: "https://microsoft.com/devicelogin",
	Message: "To sign in, use a web browser to open the page https://microsoft.com/devicelogin and enter the code F7KQ2ZP3M to authenticate.",
}

// The dialog lives as long as the sign-in: the prompt opens it, and the
// sign-in's context ending — the code entered, a timeout, a cancel — closes
// it, without anyone pressing a key.
func TestDeviceCodeDialogClosesWhenTheSignInEnds(t *testing.T) {
	a := newDeviceCodeTestApp()
	ctx, cancel := context.WithCancel(context.Background())
	if err := a.promptDeviceCode(ctx, testDeviceCode); err != nil {
		t.Fatal(err)
	}
	a.drainPending()
	d := a.deviceCodeDialog
	if !d.Visible() || d.msg.UserCode != testDeviceCode.UserCode {
		t.Fatalf("after the prompt: visible %v, code %q", d.Visible(), d.msg.UserCode)
	}
	cancel()
	drainUntil(t, a, func() bool { return !d.Visible() }, "the dialog to close")
}

// A sign-in ending must not close the dialog a later one opened: one code
// replaced by another — a retry, a second connection's sign-in — would
// otherwise vanish as the first one's context wound down.
func TestDeviceCodeDialogIgnoresTheEndOfAnEarlierSignIn(t *testing.T) {
	a := newDeviceCodeTestApp()
	first, cancelFirst := context.WithCancel(context.Background())
	second, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	_ = a.promptDeviceCode(first, testDeviceCode)
	later := testDeviceCode
	later.UserCode = "SECOND"
	_ = a.promptDeviceCode(second, later)
	a.drainPending()

	cancelFirst()
	// The first sign-in's end is posted by a goroutine; give it the chance.
	time.Sleep(20 * time.Millisecond)
	a.drainPending()
	if d := a.deviceCodeDialog; !d.Visible() || d.msg.UserCode != "SECOND" {
		t.Errorf("after the first sign-in ended: visible %v, code %q; want the second still showing", d.Visible(), d.msg.UserCode)
	}
}

// Escape and Cancel Sign-in cancel the sign-in itself, through the
// canceller the prompt's context carries (db.SignInCanceller, tested in db) —
// the Connect dialog's own Cancel is underneath and cannot be reached while
// this is open.
func TestDeviceCodeDialogCancelCancelsTheSignIn(t *testing.T) {
	for name, act := range map[string]func(d *DeviceCodeDialog){
		"Escape": func(d *DeviceCodeDialog) {
			d.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
		},
		"Cancel Sign-in": func(d *DeviceCodeDialog) {
			d.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
			d.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
		},
	} {
		a := newDeviceCodeTestApp()
		ctx, cancel := context.WithCancel(context.Background())
		a.deviceCodeDialog.showCode(testDeviceCode, cancel, ctx.Done())
		act(a.deviceCodeDialog)
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Errorf("%s: the sign-in was not cancelled", name)
		}
		if a.deviceCodeDialog.Visible() {
			t.Errorf("%s: the dialog is still open", name)
		}
		cancel()
	}
}

// Ctrl+C copies the code: the dialog answers the clipboard with it, and
// Paste cannot change it.
func TestDeviceCodeDialogCopiesTheCode(t *testing.T) {
	a := newDeviceCodeTestApp()
	a.deviceCodeDialog.showCode(testDeviceCode, nil, nil)
	a.dialogStack = []Dialog{a.deviceCodeDialog}
	target := a.activeClipboardTarget()
	if target == nil || !target.HasSelection() || target.SelectedText() != testDeviceCode.UserCode {
		t.Fatalf("clipboard target %v, want the code %q", target, testDeviceCode.UserCode)
	}
	target.Paste("other")
	if target.SelectedText() != testDeviceCode.UserCode {
		t.Errorf("after Paste: %q", target.SelectedText())
	}
}

func TestFirstErrorLineAndTidyErrorText(t *testing.T) {
	msg := "\nMicrosoft Entra sign-in failed: DeviceCodeCredential: authentication failed:\n" +
		"POST https://login.microsoftonline.com/organizations/oauth2/v2.0/token\n" +
		"--------------------------------------------------------------------------------\n" +
		"RESPONSE 400: 400 Bad Request\n" +
		"--------------------------------------------------------------------------------\n" +
		"To troubleshoot, visit https://aka.ms/azsdk/go/identity/troubleshoot#dcc"
	if got, want := firstErrorLine(msg), "Microsoft Entra sign-in failed: DeviceCodeCredential: authentication failed:"; got != want {
		t.Errorf("firstErrorLine = %q, want %q", got, want)
	}
	want := "\nMicrosoft Entra sign-in failed: DeviceCodeCredential: authentication failed:\n" +
		"POST https://login.microsoftonline.com/organizations/oauth2/v2.0/token\n" +
		"RESPONSE 400: 400 Bad Request\n" +
		"To troubleshoot, visit https://aka.ms/azsdk/go/identity/troubleshoot#dcc"
	if got := tidyErrorText(msg); got != want {
		t.Errorf("tidyErrorText =\n%q\nwant\n%q", got, want)
	}
	// A leading "--" in real text is not a separator row.
	if got := tidyErrorText("-- a comment"); got != "-- a comment" {
		t.Errorf("tidyErrorText dropped text: %q", got)
	}
}

// A method that does not sign a person in has no sign-in phase: nothing is
// posted, so the dialog's spinner keeps saying "Connecting...".
func TestSignInPhaseIsOnlyForSignInMethods(t *testing.T) {
	a := newTestApp()
	var labels []string
	for _, m := range config.AllAuthMethods() {
		if db.NeedsSignIn(m) {
			continue
		}
		if err := a.signInPhase(context.Background(), config.Connection{Server: "s", AuthMethod: m},
			func(l string) { labels = append(labels, l) }); err != nil {
			t.Errorf("%s: %v", config.AuthMethodName(m), err)
		}
	}
	a.drainPending()
	if len(labels) != 0 {
		t.Errorf("phases reported for methods with no sign-in: %q", labels)
	}

	// One that does reports its phase first; a sign-in that fails — here the
	// server cannot even be asked for its tenant (port 1 on the loopback
	// refuses at once) — returns the error without moving on to
	// "Connecting...". The hand-off after a completed sign-in needs a live
	// tenant (docs/open-threads.md § Azure SQL Managed Instance).
	if err := a.signInPhase(context.Background(),
		config.Connection{Server: "127.0.0.1", Port: 1, AuthMethod: config.AuthEntraInteractive},
		func(l string) { labels = append(labels, l) }); err == nil {
		t.Fatal("sign-in at an unreachable server: want an error, got nil")
	}
	a.drainPending()
	if len(labels) != 1 || labels[0] != "Signing in..." {
		t.Errorf("phases = %q, want only Signing in...", labels)
	}
}
