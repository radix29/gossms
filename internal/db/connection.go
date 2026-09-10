// Package db wraps gosmo to provide connection management for gossms.
package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
)

// maxOpenConns and maxIdleConns bound the pool gosmo opens per connection.
// Several Object Explorer detail panels fan out one connection per row so a slow
// row doesn't hold up the rest; uncapped, a server with hundreds of tables opens
// hundreds of raw connections at once, and gosmo's default MaxIdleConns (2)
// tears nearly all of them down again, paying full TCP+TLS+login setup on every
// refresh. These caps bound the fan-out without changing that behaviour —
// sql.DB queues acquisitions past MaxOpenConns rather than erroring — and let
// connections survive between refreshes.
const (
	maxOpenConns = 20
	maxIdleConns = 10
)

// connectTimeout bounds one connection attempt — the dial, the TLS and login
// handshakes, and the server-info read gosmo does before returning. It is
// also the "connection timeout" written into the DSN, so the driver's own
// per-dial limit and this one agree.
const connectTimeout = 30 * time.Second

// Role says what a connection is for. It decides the application name the
// connection reports — sys.dm_exec_sessions.program_name, what Activity
// Monitor, sp_who2 and an Extended Events session show — the way SSMS names
// its sessions per window type, so a DBA looking at the server can tell a
// query window from the tool's own background reads and from the Activity
// Monitor's polling.
type Role int

const (
	// RoleExplorer is Object Explorer's connection and everything that reads
	// through it: detail panes, property sheets, dashboards, AG peers.
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

// ConnectionError is a typed error returned by Connect.
//
// Err holds the underlying failure and is reachable through Unwrap, so a caller
// can inspect what went wrong — errors.Is against a driver sentinel,
// errors.AsType for an mssql.Error to read its number, or gosmo.IsRetryable.
// Cause is the pre-formatted message for display.
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

	// Login is the server login the connection is authenticated as
	// (SUSER_NAME()), fetched once at Connect time: for Windows/Entra auth
	// Opts.User is often empty or a UPN, not the login SQL Server resolves to.
	// Empty if the best-effort fetch failed, so callers fall back to
	// Opts.User.
	Login string

	// ctx is cancelled by Close, so every background load scoped to this
	// connection (see Context) is torn down on disconnect rather than idling out
	// on its own timeout. Closing the underlying *sql.DB doesn't cancel a query
	// already in flight on a checked-out connection, which would keep that
	// connection and its SQL Server session open until the query finishes.
	ctx    context.Context
	cancel context.CancelFunc

	closed bool

	// role is what Connect was asked to open this connection for; peers
	// inherit it, so an AG replica read on behalf of Object Explorer reports
	// the same program_name as Object Explorer itself.
	role Role

	// peerFields caches connections to other instances in the same topology
	// (Always On replicas) — see peer.go.
	peerFields

	// capabilityFields caches what the connected login may do — see
	// capabilities.go.
	capabilityFields
}

// Connect opens an Object Explorer connection using the given
// config.Connection, with no way to abandon it short of connectTimeout —
// ConnectContext with context.Background() and RoleExplorer.
func Connect(opts config.Connection) (*ServerConn, error) {
	return ConnectContext(context.Background(), opts, RoleExplorer)
}

// ConnectContext opens a connection for role using opts. Cancelling ctx
// aborts the attempt in flight — the dial, the TLS and login handshakes, or
// the server-info read — rather than leaving it to run to connectTimeout,
// which always applies on top of ctx. ctx governs the attempt only: the
// connection returned lives until Close.
//
// A failure is a *ConnectionError, including one opts itself causes before
// anything is dialled (an Extra Properties entry that is not key=value, or
// that names a setting the dialog owns).
func ConnectContext(ctx context.Context, opts config.Connection, role Role) (*ServerConn, error) {
	co, err := toGosmoOptions(opts, role)
	if err != nil {
		return nil, &ConnectionError{Server: opts.Server, Cause: err.Error(), Err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

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

// toGosmoOptions is the one place a config.Connection becomes the
// gosmo.ConnectionOptions that is dialled — Connect uses it, and so does
// BuildConnectionString, which is what makes the Connect dialog's preview the
// connection string actually sent rather than a second rendering that drifts
// from it.
//
// The dialog's fields do not map one-to-one onto gosmo's: each Entra method
// reads its client id from a different option. A service principal's
// application id is gosmo's User (with the secret as Password); an
// interactive or device-code flow's app registration is ApplicationClientID;
// only a user-assigned managed identity reads ClientID. Passing the dialog's
// ClientID straight through, as this once did, connected a service principal
// with an empty user id. A saved service principal that predates the mapping
// may carry its application id in User instead, so that is the fallback.
// Fields a method does not use are not passed at all.
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
		co.User = opts.ClientID
		if co.User == "" {
			co.User = opts.User
		}
		co.Password = opts.Password
	case config.AuthEntraInteractive, config.AuthEntraDeviceCode:
		co.ApplicationClientID = opts.ClientID
	case config.AuthEntraDefault, config.AuthEntraAzCLI:
		// The credential chain / az login supplies the identity.
	default: // SQL Server, Windows, Entra password
		co.User, co.Password = opts.User, opts.Password
	}
	if config.IsEntraMethod(opts.AuthMethod) {
		co.TenantID = opts.TenantID
	}

	extra, err := ParseExtraProperties(opts.ExtraProperties)
	if err != nil {
		return gosmo.ConnectionOptions{}, err
	}
	co.ExtraParams = extra
	return co, nil
}

// ParseExtraProperties reads the Connect dialog's Extra Properties text into
// driver parameters: "key=value" entries separated by ';' (the ADO.NET form),
// '&' (the URL form) or line breaks, each trimmed of surrounding space. Empty
// entries are skipped, so a trailing separator is harmless. Values are taken
// literally — no URL decoding and no quoting — so a value cannot itself
// contain a separator.
//
// An entry without '=' or with an empty key is an error naming it, not
// something silently dropped. Keys the dialog's own fields control are
// refused by gosmo when the options are built (ConnectionOptions.ExtraParams).
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

// Close disconnects from SQL Server. Cancelling ctx before closing the pool is
// what lets a background load in flight notice promptly and let go of its
// checked-out connection, rather than lingering to its own timeout.
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

// Context returns the context governing sc's lifetime, cancelled once Close
// runs. Background loads scoped to this connection derive their per-call timeout
// from it rather than from context.Background(), so disconnecting cancels them.
// Never nil — not for a nil sc nor a zero-value ServerConn — falling back to
// context.Background().
func (sc *ServerConn) Context() context.Context {
	if sc == nil || sc.ctx == nil {
		return context.Background()
	}
	return sc.ctx
}

// IsOpen reports whether sc is a non-nil connection that hasn't been closed —
// true between Connect and Close, whether or not sc is tracked in any list.
func (sc *ServerConn) IsOpen() bool {
	return sc != nil && !sc.closed
}

// Label builds the Object Explorer root-node label for a connected server:
// "host[\instance or ,port] (user, SQL Server version)". An instance name takes
// precedence over a port, and the default port (1433) is never shown. Meant for
// after Connect succeeds; a nil sc.Server leaves the version blank rather than
// panicking.
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

	// Prefer the server's own SUSER_NAME() over Opts.User: for Windows/Entra auth
	// the latter is often empty or not the resolved login name.
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

// resolveServer folds the Connect dialog's separate Server and Port fields into
// the single address gosmo.ConnectionOptions.Server expects. Server alone may
// already be any form gosmo.ParseServerAddress understands, and a port it
// already carries wins rather than having the dialog's default appended on top.
//
// An unset (0) or default (1433) dialogPort is left out of the address rather
// than written into it: the driver already dials 1433 when none is given, and a
// port pinned onto a "host\instance" address suppresses the SQL Browser lookup
// that resolves the instance's real, dynamically assigned port — win10cli\sql2017
// listens on 55253, so an appended 1433 silently reaches the default instance
// instead.
//
// When Server carries a "\instance" but no port, a non-default dialogPort is
// appended with a comma: gosmo recognises a trailing port after an instance name
// only when comma-separated, and a colon becomes part of the instance name.
func resolveServer(server string, dialogPort int) string {
	host, _, embeddedPort := gosmo.ParseServerAddress(server)
	if embeddedPort != 0 {
		return server
	}
	port := dialogPort
	if port == 0 || port == 1433 {
		return server
	}
	// A comma for a bare IPv6 literal too: "fe80::1:1500" is read back as an
	// address, the ":1500" one more group of it.
	sep := ":"
	if strings.ContainsRune(server, '\\') ||
		(strings.ContainsRune(host, ':') && !strings.HasPrefix(host, "[")) {
		sep = ","
	}
	return fmt.Sprintf("%s%s%d", server, sep, port)
}

// encryptString renders a connection's encryption mode as the driver's
// "encrypt" parameter spells it. "" is an entry built in memory without one,
// which config reads back from disk as Optional, so it dials as Optional too.
func encryptString(m config.EncryptMode) string {
	if m == "" {
		return string(config.EncryptOptional)
	}
	return string(m)
}

// toGosmoAuth translates config.AuthMethod to gosmo.AuthMethod. The two enums
// are declared independently with no guarantee their values stay aligned, so
// this is an explicit switch, not a numeric cast.
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

// BuildConnectionString renders the connection string Connect dials for opts
// as an Object Explorer connection — the same toGosmoOptions and the same
// gosmo builder, with every password, secret and token masked, so it is safe
// to show and is exactly what is sent apart from those. A setting Connect
// would refuse comes back as the same error, which the Connect dialog's
// preview shows in place of a string.
func BuildConnectionString(opts config.Connection) (string, error) {
	co, err := toGosmoOptions(opts, RoleExplorer)
	if err != nil {
		return "", err
	}
	s, err := co.ConnectionString(true)
	return s, explainExtraProperty(err)
}

// extraPropertyError is gosmo's refusal of an Extra Properties entry, worded
// for the Connect dialog rather than for gosmo's API. It unwraps to the
// *gosmo.ExtraParamError.
type extraPropertyError struct {
	msg string
	err error
}

func (e *extraPropertyError) Error() string { return e.msg }
func (e *extraPropertyError) Unwrap() error { return e.err }

// explainExtraProperty rewords a *gosmo.ExtraParamError in the Connect
// dialog's terms — gosmo's own text names ConnectionOptions and ExtraParams,
// which a user of the dialog has never seen — and passes anything else
// through unchanged.
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
