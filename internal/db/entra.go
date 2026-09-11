package db

// entra.go is gossms's Microsoft Entra sign-in: the process-wide
// gosmo.EntraCache, the TUI's device-code prompt, and ConnectContext's pre-dial
// sign-in phase.

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

// entraCache is shared by every connection. Each ConnectContext opens its own
// pool (Object Explorer, each query window, Activity Monitor, AG peers); a
// cache per pool would open an MFA browser per window. Keyed by identity, so
// one sign-in covers every server in the tenant.
var entraCache = gosmo.NewEntraCache()

// entraUsed is whether entraCache may hold a sign-in: set by any Entra attempt,
// cleared by ClearEntraSignIns. It only gates the menu item, so a race just
// greys it until the next Entra connection.
var entraUsed atomic.Bool

// HasEntraSignIns reports whether any Microsoft Entra sign-in may be held.
func HasEntraSignIns() bool { return entraUsed.Load() }

// ClearEntraSignIns forgets every Entra sign-in and token, so the next Entra
// connection signs in again (how to switch accounts). Open pools keep their
// connections, but new physical connections sign in again.
func ClearEntraSignIns() {
	entraCache.Clear()
	entraUsed.Store(false)
}

// DeviceCodePrompt shows the code and URL a Device Code sign-in waits on.
// Called on the connecting goroutine; must return promptly. ctx ends with the
// sign-in; SignInCanceller(ctx) cancels it.
type DeviceCodePrompt func(ctx context.Context, m gosmo.DeviceCodeMessage) error

var deviceCodePrompt atomic.Pointer[DeviceCodePrompt]

// SetDeviceCodePrompt installs the process-wide prompt (a credential shared
// through entraCache uses whichever prompt its latest connection passed).
func SetDeviceCodePrompt(p DeviceCodePrompt) {
	deviceCodePrompt.Store(&p)
}

// promptDeviceCode is every Entra connection's DeviceCodePrompt. With none
// installed it fails the sign-in rather than let gosmo print to stdout, which
// tcell would immediately overdraw.
func promptDeviceCode(ctx context.Context, m gosmo.DeviceCodeMessage) error {
	if p := deviceCodePrompt.Load(); p != nil && *p != nil {
		return (*p)(ctx, m)
	}
	return errors.New("no device-code prompt is installed to show the sign-in code")
}

// SignInTimeout bounds a human sign-in: time to find a phone, but not forever.
const SignInTimeout = 5 * time.Minute

// NeedsSignIn reports whether m signs a person in (MFA browser, Device Code);
// ConnectContext runs that as its own phase under SignInTimeout.
func NeedsSignIn(m config.AuthMethod) bool {
	return m == config.AuthEntraInteractive || m == config.AuthEntraDeviceCode
}

// SignIn runs the sign-in phase for opts: a NeedsSignIn method signs in now,
// under ctx and at most SignInTimeout, keeping the token for the dial. Anything
// else, or an existing sign-in, returns nil at once.
//
// First gosmo opens and abandons a login to learn the server's tenant and token
// (once per server per process; Azure logs Error 33155), so a sign-in without
// TenantID reaches the server's tenant as in SSMS instead of azidentity's
// "organizations", which refuses personal accounts. An unreachable server
// therefore fails here.
//
// ConnectContext calls it; the Connect dialog calls it first to show the phase.
// Errors are *ConnectionError, wrapping context.Canceled on cancel.
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

// SignInCanceller returns the cancel func of the sign-in ctx belongs to (a
// DeviceCodePrompt's ctx or derived), or nil. Cancelling fails the connection
// attempt.
func SignInCanceller(ctx context.Context) context.CancelFunc {
	cancel, _ := ctx.Value(signInCancelKey{}).(context.CancelFunc)
	return cancel
}
