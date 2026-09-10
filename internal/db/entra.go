package db

// entra.go is gossms's side of Microsoft Entra sign-in: the one
// gosmo.EntraCache every connection in the process shares, the device-code
// prompt the TUI installs, and the sign-in phase ConnectContext runs before
// it dials.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
)

// entraCache is shared by every connection gossms opens. Each ConnectContext
// opens a pool of its own — Object Explorer, every query window, Activity
// Monitor, every Always On peer — and with a cache per pool (gosmo's default
// when none is given) an MFA sign-in would open a browser once per window.
// Keyed by identity, never by server, so one sign-in covers every server in
// the tenant.
var entraCache = gosmo.NewEntraCache()

// entraUsed is whether entraCache may hold a sign-in: set by any Entra
// connection attempt, cleared by ClearEntraSignIns. It gates the menu item
// only, so a sign-in that lands just after a Clear leaving it false costs
// nothing worse than an item greyed until the next Entra connection.
var entraUsed atomic.Bool

// HasEntraSignIns reports whether any Microsoft Entra sign-in may be held.
func HasEntraSignIns() bool { return entraUsed.Load() }

// ClearEntraSignIns forgets every Microsoft Entra sign-in and token, so the
// next Entra connection signs in again — how to switch accounts. Pools
// already open keep the connections they have, but a new physical connection
// in one of them signs in again too.
func ClearEntraSignIns() {
	entraCache.Clear()
	entraUsed.Store(false)
}

// DeviceCodePrompt shows the user the code and URL a Device Code sign-in
// is waiting on. It is called on the connecting goroutine and must return
// promptly; ctx ends when the sign-in does, however it ends, and
// SignInCanceller(ctx) cancels it.
type DeviceCodePrompt func(ctx context.Context, m gosmo.DeviceCodeMessage) error

var deviceCodePrompt atomic.Pointer[DeviceCodePrompt]

// SetDeviceCodePrompt installs the process-wide device-code prompt. One for
// the process, not one per connection: a credential entraCache shares uses
// whichever prompt the latest connection through it passed.
func SetDeviceCodePrompt(p DeviceCodePrompt) {
	deviceCodePrompt.Store(&p)
}

// promptDeviceCode is the DeviceCodePrompt every Entra connection is given.
// With none installed it fails the sign-in rather than letting gosmo fall
// back to printing on standard output, which under the TUI is the terminal
// tcell is drawing on: the code would flash up and be erased by the next
// redraw.
func promptDeviceCode(ctx context.Context, m gosmo.DeviceCodeMessage) error {
	if p := deviceCodePrompt.Load(); p != nil && *p != nil {
		return (*p)(ctx, m)
	}
	return errors.New("no device-code prompt is installed to show the sign-in code")
}

// SignInTimeout bounds a human sign-in: long enough to find a phone and
// type a code, short enough that an abandoned one does not hold a
// connection attempt forever.
const SignInTimeout = 5 * time.Minute

// NeedsSignIn reports whether m signs a person in — a browser for MFA, a
// code for Device Code — which ConnectContext then does as a phase of its
// own, under SignInTimeout rather than the connect timeout.
func NeedsSignIn(m config.AuthMethod) bool {
	return m == config.AuthEntraInteractive || m == config.AuthEntraDeviceCode
}

// SignIn runs the sign-in phase of connecting with opts: for a method that
// NeedsSignIn, the person signs in now, under ctx and at most SignInTimeout,
// and the token is kept for the dial that follows. Anything else returns nil
// at once, as does a sign-in already held.
//
// Before signing in, gosmo opens a login to the server and abandons it once
// the server has named the tenant and token it wants (once per server per
// process; Azure logs each as Error 33155). That is how a sign-in with no
// TenantID reaches the server's tenant, as in SSMS, rather than azidentity's
// "organizations", which refuses a personal Microsoft account. A server that
// cannot be reached therefore fails here, not in the dial.
//
// ConnectContext calls it itself; a caller that shows the phase (the Connect
// dialog) calls it first, and the dial finds the token waiting. The error is
// a *ConnectionError, wrapping context.Canceled when ctx was cancelled.
func SignIn(ctx context.Context, opts config.Connection) error {
	if !NeedsSignIn(opts.AuthMethod) {
		return nil
	}
	co, err := toGosmoOptions(opts, RoleExplorer)
	if err != nil {
		return &ConnectionError{Server: opts.Server, Cause: err.Error(), Err: err}
	}
	return signIn(ctx, opts, co)
}

func signIn(ctx context.Context, opts config.Connection, co gosmo.ConnectionOptions) error {
	ctx, cancel := context.WithTimeout(ctx, SignInTimeout)
	defer cancel()
	entraUsed.Store(true)
	err := entraCache.Warm(withSignInCanceller(ctx, cancel), co)
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return &ConnectionError{Server: opts.Server, Cause: "sign-in cancelled", Err: errors.Join(ctx.Err(), err)}
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return &ConnectionError{Server: opts.Server,
			Cause: fmt.Sprintf("sign-in not completed within %v", SignInTimeout), Err: errors.Join(ctx.Err(), err)}
	}
	cause := "Microsoft Entra sign-in failed: " + strings.TrimPrefix(err.Error(), "gosmo: entra sign-in: ")
	return &ConnectionError{Server: opts.Server, Cause: cause, Err: err}
}

type signInCancelKey struct{}

func withSignInCanceller(ctx context.Context, cancel context.CancelFunc) context.Context {
	return context.WithValue(ctx, signInCancelKey{}, cancel)
}

// SignInCanceller returns what cancels the sign-in ctx belongs to — the ctx a
// DeviceCodePrompt is handed, or one derived from it — or nil for a ctx that
// is not one. Cancelling a sign-in fails the connection attempt it is part of.
func SignInCanceller(ctx context.Context) context.CancelFunc {
	cancel, _ := ctx.Value(signInCancelKey{}).(context.CancelFunc)
	return cancel
}
