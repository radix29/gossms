// Package db wraps gosmo for gossms connection management.
package db

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
)

// maxOpenConns and maxIdleConns bound gosmo's pool per connection. Some detail
// panels fan out one connection per row; uncapped, hundreds of tables open
// hundreds of connections, and gosmo's default MaxIdleConns (2) closes them
// again, paying full setup every refresh. sql.DB queues past MaxOpenConns
// rather than erroring.
const (
	maxOpenConns = 20
	maxIdleConns = 10
)

// connectTimeout bounds one connection attempt (dial, TLS, login, gosmo's
// server-info read). Also written into the DSN, so the driver's limit agrees.
const connectTimeout = 30 * time.Second

// Role says what a connection is for, which decides the program_name it reports
// (Activity Monitor, sp_who2, XEvents), as SSMS names sessions per window type.
type Role int

const (
	// RoleExplorer is Object Explorer and everything reading through it: detail
	// panes, property sheets, dashboards, AG peers.
	RoleExplorer Role = iota
	// RoleQuery is a query panel's own connection.
	RoleQuery
	// RoleActivityMonitor is the Activity Monitor's collectors.
	RoleActivityMonitor
)

// ApplicationName returns the program_name a connection in role r reports.
func (r Role) ApplicationName() string {
	switch r {
	case RoleQuery:
		return "goSSMS - Query"
	case RoleActivityMonitor:
		return "goSSMS - Activity Monitor"
	default:
		return "goSSMS"
	}
}

// ConnectionError is Connect's error type. Err is the underlying failure (via
// Unwrap, for errors.Is/AsType or gosmo.IsRetryable); Cause is the display
// message.
type ConnectionError struct {
	Server string
	Cause  string
	Err    error
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("connect to %s: %s", e.Server, e.Cause)
}

// Unwrap exposes the underlying connect failure to errors.Is/As/AsType.
func (e *ConnectionError) Unwrap() error { return e.Err }

// ServerConn wraps a gosmo server connection plus its config.
type ServerConn struct {
	Opts   config.Connection
	Server *gosmo.Server

	// Login is SUSER_NAME(), fetched at Connect: for Windows/Entra auth
	// Opts.User is often empty or a UPN. Empty if the fetch failed; callers
	// fall back to Opts.User.
	Login string

	// ctx is cancelled by Close so background loads scoped to this connection
	// stop on disconnect. Closing the *sql.DB alone doesn't cancel an in-flight
	// query, which would hold its session open.
	ctx    context.Context
	cancel context.CancelFunc

	closed bool

	// role is what Connect opened this connection for; peers inherit it, so AG
	// replica reads report Object Explorer's program_name.
	role Role

	// peerFields caches connections to other instances in the topology (Always
	// On replicas); see peer.go.
	peerFields

	// capabilityFields caches what the login may do; see capabilities.go.
	capabilityFields
}

// Connect opens an Object Explorer connection: ConnectContext with
// context.Background() and RoleExplorer.
func Connect(opts config.Connection) (*ServerConn, error) {
	return ConnectContext(context.Background(), opts, RoleExplorer)
}

// ConnectContext opens a connection for role. Cancelling ctx aborts the attempt
// in flight; connectTimeout always applies too. ctx covers only the attempt;
// the connection lives until Close.
//
// A NeedsSignIn method signs in first under SignInTimeout (see SignIn), so the
// connect timeout never has to cover finding a phone. An existing sign-in costs
// nothing.
//
// Failures are *ConnectionError, including option errors before dialling (a
// malformed Extra Properties entry, or one naming a dialog-owned setting).
func ConnectContext(ctx context.Context, opts config.Connection, role Role) (*ServerConn, error) {
	co, err := toGosmoOptions(opts, role)
	if err != nil {
		return nil, &ConnectionError{Server: opts.Server, Cause: err.Error(), Err: err}
	}
	if NeedsSignIn(opts.AuthMethod) {
		if err := signIn(ctx, opts, co); err != nil {
			return nil, err
		}
	}
	if config.IsEntraMethod(opts.AuthMethod) {
		entraUsed.Store(true)
	}
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	// A sign-in SignIn couldn't do ahead happens in the dial; a device code
	// shown then cancels the same way.
	ctx = withSignInCanceller(ctx, cancel)

	srv, err := gosmo.ConnectContext(ctx, co)
	if err != nil {
		err = explainExtraProperty(err)
		return nil, &ConnectionError{Server: opts.Server, Cause: err.Error(), Err: err}
	}
	login, _ := srv.CurrentLoginContext(ctx)
	connCtx, connCancel := context.WithCancel(context.Background())
	sc := &ServerConn{Opts: opts, Server: srv, Login: login, ctx: connCtx, cancel: connCancel, role: role}
	sc.ProbeCapabilities()
	return sc, nil
}

// toGosmoOptions is the single conversion from config.Connection to dialled
// gosmo.ConnectionOptions, used by Connect and BuildConnectionString so the
// dialog preview is exactly what's sent.
//
// Dialog fields don't map one-to-one: a service principal's application id is
// gosmo's User (secret as Password); the app registration for Password, MFA and
// Device Code is ApplicationClientID; only a user-assigned managed identity
// uses ClientID. A service principal saved with its id in User is the fallback.
// MFA's User is a login hint. Fields a method doesn't use (config.FieldsFor)
// aren't passed.
//
// Every Entra method gets the shared entraCache and device-code prompt
// (entra.go).
func toGosmoOptions(opts config.Connection, role Role) (gosmo.ConnectionOptions, error) {
	co := gosmo.ConnectionOptions{
		Server:                 resolveServer(opts.Server, opts.Port),
		Database:               opts.Database,
		Auth:                   toGosmoAuth(opts.AuthMethod),
		TrustServerCertificate: opts.TrustServerCertificate,
		Encrypt:                encryptString(opts.Encrypt),
		HostNameInCertificate:  strings.TrimSpace(opts.HostNameInCertificate),
		ApplicationName:        role.ApplicationName(),
		ConnectTimeout:         connectTimeout,
		MaxOpenConns:           maxOpenConns,
		MaxIdleConns:           maxIdleConns,
	}
	switch opts.AuthMethod {
	case config.AuthEntraMSI:
		co.ClientID = opts.ClientID
	case config.AuthEntraServicePrincipal:
		co.User = cmp.Or(opts.ClientID, opts.User)
		co.Password = opts.Password
	case config.AuthEntraPassword:
		co.User, co.Password = opts.User, opts.Password
		co.ApplicationClientID = opts.ClientID
	case config.AuthEntraInteractive:
		co.User = opts.User // the login hint
		co.ApplicationClientID = opts.ClientID
	case config.AuthEntraDeviceCode:
		co.ApplicationClientID = opts.ClientID
	case config.AuthEntraDefault, config.AuthEntraAzCLI:
		// The credential chain / az login supplies the identity.
	default: // SQL Server, Windows
		co.User, co.Password = opts.User, opts.Password
	}
	if config.FieldsFor(opts.AuthMethod).Tenant {
		co.TenantID = opts.TenantID
	}
	if config.IsEntraMethod(opts.AuthMethod) {
		co.EntraCache = entraCache
		co.DeviceCodePrompt = promptDeviceCode
	}
	if err := missingCredential(opts.AuthMethod, co); err != nil {
		return gosmo.ConnectionOptions{}, err
	}

	extra, err := ParseExtraProperties(opts.ExtraProperties)
	if err != nil {
		return gosmo.ConnectionOptions{}, err
	}
	co.ExtraParams = extra
	return co, nil
}

// missingCredential refuses a missing required credential using the Connect
// dialog's field names; gosmo's own error would name "User", a field the dialog
// greys out for a service principal.
func missingCredential(m config.AuthMethod, co gosmo.ConnectionOptions) error {
	var field string
	switch m {
	case config.AuthEntraPassword:
		switch {
		case co.User == "":
			field = "a User (the account's user principal name)"
		case co.Password == "":
			field = "a Password"
		}
	case config.AuthEntraServicePrincipal:
		switch {
		case co.User == "":
			field = "a ClientID (the application's client ID)"
		case co.Password == "":
			field = "a Password (the client secret)"
		}
	}
	if field == "" {
		return nil
	}
	return fmt.Errorf("%s needs %s", config.AuthMethodName(m), field)
}

// ParseExtraProperties parses the Extra Properties text into driver parameters:
// "key=value" entries separated by ';', '&' or newlines, trimmed. Empty entries
// are skipped. Values are literal (no decoding or quoting), so can't contain a
// separator.
//
// An entry without '=' or with an empty key is an error. gosmo refuses keys
// owned by the dialog's fields (ConnectionOptions.ExtraParams).
func ParseExtraProperties(s string) (url.Values, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	out := url.Values{}
	for entry := range strings.FieldsFuncSeq(s, func(r rune) bool {
		return r == ';' || r == '&' || r == '\n' || r == '\r'
	}) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		k, v, ok := strings.Cut(entry, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("extra property %q is not key=value", entry)
		}
		out.Add(k, strings.TrimSpace(v))
	}
	return out, nil
}

// Close disconnects. ctx is cancelled before closing the pool so in-flight
// background loads release their connections promptly.
func (sc *ServerConn) Close() {
	if sc.cancel != nil {
		sc.cancel()
	}
	sc.closePeers()
	if sc.Server != nil {
		sc.Server.Close()
	}
	sc.closed = true
}

// Context returns the context cancelled by Close. Background loads derive their
// timeouts from it so disconnecting cancels them. Never nil (falls back to
// context.Background() for nil or zero sc).
func (sc *ServerConn) Context() context.Context {
	if sc == nil || sc.ctx == nil {
		return context.Background()
	}
	return sc.ctx
}

// IsOpen reports whether sc is non-nil and not yet closed.
func (sc *ServerConn) IsOpen() bool {
	return sc != nil && !sc.closed
}

// Label builds the Object Explorer root label: "host[\instance or ,port] (user,
// SQL Server version)". An instance beats a port; 1433 is never shown. A nil
// sc.Server leaves the version blank.
func (sc *ServerConn) Label() string {
	host, instance, port := gosmo.ParseServerAddress(sc.Opts.Server)
	if port == 0 {
		port = sc.Opts.Port
	}
	name := host
	switch {
	case instance != "":
		name += `\` + instance
	case port != 0 && port != 1433:
		name += fmt.Sprintf(",%d", port)
	}

	// Prefer SUSER_NAME() over Opts.User, which for Windows/Entra is often
	// empty or unresolved.
	user := sc.Login
	if user == "" {
		user = sc.Opts.User
	}
	if user == "" {
		user = config.AuthMethodName(sc.Opts.AuthMethod)
	}

	var version string
	if sc.Server != nil && sc.Server.Info() != nil {
		version = sc.Server.Info().ProductVersion
	}

	return fmt.Sprintf("%s (%s, SQL Server %s)", name, user, version)
}

// resolveServer folds the dialog's Server and Port into gosmo's single address.
// Server may already be any gosmo.ParseServerAddress form; a port it carries
// wins.
//
// Port 0 or 1433 is omitted: the driver defaults to 1433, and a port on
// "host\instance" suppresses the SQL Browser lookup for the instance's dynamic
// port (win10cli\sql2017 listens on 55253; an appended 1433 reaches the default
// instance).
//
// With "\instance" and no port, a non-default port is appended with a comma;
// gosmo reads a colon there as part of the instance name.
func resolveServer(server string, dialogPort int) string {
	host, _, embeddedPort := gosmo.ParseServerAddress(server)
	if embeddedPort != 0 {
		return server
	}
	port := dialogPort
	if port == 0 || port == 1433 {
		return server
	}
	// Comma for a bare IPv6 literal too: in "fe80::1:1500" the ":1500" is
	// another address group.
	sep := ":"
	if strings.ContainsRune(server, '\\') ||
		(strings.ContainsRune(host, ':') && !strings.HasPrefix(host, "[")) {
		sep = ","
	}
	return fmt.Sprintf("%s%s%d", server, sep, port)
}

// encryptString renders the mode as the driver's "encrypt" parameter. ""
// (in-memory entry) dials as Optional, matching how config reads it from disk.
func encryptString(m config.EncryptMode) string {
	if m == "" {
		return string(config.EncryptOptional)
	}
	return string(m)
}

// toGosmoAuth maps config.AuthMethod to gosmo.AuthMethod with an explicit
// switch; the enums aren't guaranteed to align.
func toGosmoAuth(m config.AuthMethod) gosmo.AuthMethod {
	switch m {
	case config.AuthSQLServer:
		return gosmo.AuthSQLServer
	case config.AuthWindows:
		return gosmo.AuthWindows
	case config.AuthEntraDefault:
		return gosmo.AuthEntraDefault
	case config.AuthEntraPassword:
		return gosmo.AuthEntraPassword
	case config.AuthEntraMSI:
		return gosmo.AuthEntraMSI
	case config.AuthEntraServicePrincipal:
		return gosmo.AuthEntraServicePrincipal
	case config.AuthEntraInteractive:
		return gosmo.AuthEntraInteractive
	case config.AuthEntraDeviceCode:
		return gosmo.AuthEntraDeviceCode
	case config.AuthEntraAzCLI:
		return gosmo.AuthEntraAzCLI
	default:
		return gosmo.AuthSQLServer
	}
}

// BuildConnectionString renders the DSN Connect would dial for opts (Object
// Explorer role), via the same toGosmoOptions and gosmo builder, with
// passwords, secrets and tokens masked. Settings Connect would refuse return
// the same error, which the dialog preview shows.
func BuildConnectionString(opts config.Connection) (string, error) {
	co, err := toGosmoOptions(opts, RoleExplorer)
	if err != nil {
		return "", err
	}
	s, err := co.ConnectionString(true)
	return s, explainExtraProperty(err)
}

// extraPropertyError is gosmo's refusal of an Extra Properties entry, worded
// for the Connect dialog; unwraps to *gosmo.ExtraParamError.
type extraPropertyError struct {
	msg string
	err error
}

func (e *extraPropertyError) Error() string { return e.msg }
func (e *extraPropertyError) Unwrap() error { return e.err }

// explainExtraProperty rewords a *gosmo.ExtraParamError in dialog terms
// (gosmo's text names ConnectionOptions and ExtraParams); other errors pass
// through.
func explainExtraProperty(err error) error {
	pe, ok := errors.AsType[*gosmo.ExtraParamError](err)
	if !ok {
		return err
	}
	if pe.Reserved {
		return &extraPropertyError{
			msg: fmt.Sprintf("extra property %q is one of this dialog's own settings — set it in its field instead", pe.Key),
			err: err,
		}
	}
	return &extraPropertyError{msg: "extra properties: " + strings.TrimPrefix(err.Error(), "gosmo: "), err: err}
}
