package db

import (
	"context"
	"errors"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
)

// Only the two person-facing methods get their own phase; the rest sign in
// silently in the dial.
func TestNeedsSignIn(t *testing.T) {
	for _, m := range config.AllAuthMethods() {
		want := m == config.AuthEntraInteractive || m == config.AuthEntraDeviceCode
		if got := NeedsSignIn(m); got != want {
			t.Errorf("NeedsSignIn(%s) = %v, want %v", config.AuthMethodName(m), got, want)
		}
	}
}

// With no prompt installed the code is refused, not printed to stdout where
// tcell would erase it.
func TestDeviceCodePromptIsRoutedToTheInstalledOne(t *testing.T) {
	old := deviceCodePrompt.Load()
	t.Cleanup(func() { deviceCodePrompt.Store(old) })

	deviceCodePrompt.Store(nil)
	if err := promptDeviceCode(context.Background(), gosmo.DeviceCodeMessage{UserCode: "ABC"}); err == nil {
		t.Error("no prompt installed: want an error, got nil")
	}

	var got gosmo.DeviceCodeMessage
	SetDeviceCodePrompt(func(_ context.Context, m gosmo.DeviceCodeMessage) error { got = m; return nil })
	if err := promptDeviceCode(context.Background(), gosmo.DeviceCodeMessage{UserCode: "ABC"}); err != nil || got.UserCode != "ABC" {
		t.Errorf("installed prompt: err %v, got %+v", err, got)
	}
}

// The prompt's ctx cancels its sign-in and so the attempt; the code dialog has
// its own Cancel, and the Connect dialog is unreachable beneath it.
func TestSignInCancellerCancelsTheSignIn(t *testing.T) {
	if SignInCanceller(context.Background()) != nil {
		t.Fatal("a plain context has a sign-in canceller")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	derived, stop := context.WithCancel(withSignInCanceller(ctx, cancel))
	defer stop()
	c := SignInCanceller(derived)
	if c == nil {
		t.Fatal("no canceller on a context derived from a sign-in")
	}
	c()
	if !errors.Is(derived.Err(), context.Canceled) {
		t.Errorf("after the canceller: ctx.Err() = %v, want Canceled", derived.Err())
	}
}

// SignIn asks the server for its tenant first, so an unreachable server fails
// SignIn with a *ConnectionError naming it. Either way it marks a sign-in as
// possibly held; SQL Server auth doesn't.
func TestSignInAsksTheServerFirst(t *testing.T) {
	ClearEntraSignIns()
	if HasEntraSignIns() {
		t.Fatal("HasEntraSignIns after Clear")
	}
	// Loopback port 1 refuses at once.
	const unreachable = "127.0.0.1"
	if err := SignIn(context.Background(), config.Connection{Server: unreachable, Port: 1, AuthMethod: config.AuthSQLServer}); err != nil {
		t.Fatalf("SQL Server auth: %v", err)
	}
	if HasEntraSignIns() {
		t.Error("a SQL Server connection marked an Entra sign-in as held")
	}
	for _, m := range []config.AuthMethod{config.AuthEntraInteractive, config.AuthEntraDeviceCode} {
		err := SignIn(context.Background(), config.Connection{Server: unreachable, Port: 1, AuthMethod: m})
		ce, ok := errors.AsType[*ConnectionError](err)
		if !ok || !strings.Contains(ce.Cause, "asking "+unreachable) {
			t.Errorf("%s at an unreachable server: err = %v, want a *ConnectionError from asking it", config.AuthMethodName(m), err)
		}
	}
	if !HasEntraSignIns() {
		t.Error("an Entra sign-in did not mark one as held")
	}
	ClearEntraSignIns()
	if HasEntraSignIns() {
		t.Error("HasEntraSignIns after Clear")
	}
}

// Refused options fail SignIn before any sign-in, as a *ConnectionError.
func TestSignInRefusesBadOptionsFirst(t *testing.T) {
	err := SignIn(context.Background(), config.Connection{Server: "x.database.windows.net",
		AuthMethod: config.AuthEntraInteractive, ExtraProperties: "oops"})
	if _, ok := errors.AsType[*ConnectionError](err); !ok {
		t.Errorf("err = %v (%T), want a *ConnectionError", err, err)
	}
}
