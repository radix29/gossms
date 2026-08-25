// Package db wraps gosmo to provide connection management for gossms.
package db

import (
	"context"
	"fmt"
	"net/url"
	"strings"

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

	// peerFields caches connections to other instances in the same topology
	// (Always On replicas) — see peer.go.
	peerFields

	// capabilityFields caches what the connected login may do — see
	// capabilities.go.
	capabilityFields
}

// Connect opens a connection using the given config.Connection.
func Connect(opts config.Connection) (*ServerConn, error) {
	co := gosmo.ConnectionOptions{
		Server:                 resolveServer(opts.Server, opts.Port),
		User:                   opts.User,
		Password:               opts.Password,
		TrustServerCertificate: opts.TrustServerCertificate,
		Encrypt:                encryptString(opts.Encrypt),
		Auth:                   toGosmoAuth(opts.AuthMethod),
		TenantID:               opts.TenantID,
		ClientID:               opts.ClientID,
		MaxOpenConns:           maxOpenConns,
		MaxIdleConns:           maxIdleConns,
	}
	if opts.Database != "" {
		co.Database = opts.Database
	}

	srv, err := gosmo.Connect(co)
	if err != nil {
		return nil, &ConnectionError{Server: opts.Server, Cause: err.Error(), Err: err}
	}
	login, _ := srv.CurrentLogin()
	ctx, cancel := context.WithCancel(context.Background())
	sc := &ServerConn{Opts: opts, Server: srv, Login: login, ctx: ctx, cancel: cancel}
	sc.ProbeCapabilities()
	return sc, nil
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
// When Server carries a "\instance" but no port, a non-default dialogPort is
// appended with a comma: gosmo recognises a trailing port after an instance name
// only when comma-separated, and a colon becomes part of the instance name.
func resolveServer(server string, dialogPort int) string {
	if _, _, embeddedPort := gosmo.ParseServerAddress(server); embeddedPort != 0 {
		return server
	}
	port := dialogPort
	if port == 0 {
		port = 1433
	}
	if port == 1433 {
		return server
	}
	sep := ":"
	if strings.ContainsRune(server, '\\') {
		sep = ","
	}
	return fmt.Sprintf("%s%s%d", server, sep, port)
}

// encryptString converts config.Connection's boolean Encrypt into the string
// gosmo.ConnectionOptions.Encrypt expects, mirroring go-mssqldb's "encrypt" DSN
// parameter.
func encryptString(encrypt bool) string {
	if encrypt {
		return "true"
	}
	return "false"
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

// BuildConnectionString produces a DSN string for the connection options. User,
// Password and Database are URL-encoded via net/url, as gosmo's own buildDSN
// does, so a value containing "@" or "&" can't corrupt the result.
// opts.ExtraProperties is appended verbatim after a "&".
//
// A "\instance" in opts.Server is carried as a URL path segment
// (sqlserver://host:port/instance) rather than embedded in Host, where a literal
// backslash percent-escapes to a misleading "%5C".
func BuildConnectionString(opts config.Connection) string {
	host, instance, port := gosmo.ParseServerAddress(resolveServer(opts.Server, opts.Port))
	if port == 0 {
		port = 1433
	}
	encrypt := "false"
	if opts.Encrypt {
		encrypt = "true"
	}
	trustCert := "false"
	if opts.TrustServerCertificate {
		trustCert = "true"
	}

	q := url.Values{}
	// Omitted when empty, matching Connect, which sets
	// ConnectionOptions.Database only for a non-empty value: a preview carrying a
	// bare "database=" would not be the DSN actually dialed.
	if opts.Database != "" {
		q.Set("database", opts.Database)
	}
	q.Set("encrypt", encrypt)
	q.Set("TrustServerCertificate", trustCert)

	u := &url.URL{
		Scheme: "sqlserver",
		Host:   fmt.Sprintf("%s:%d", host, port),
	}
	if instance != "" {
		u.Path = "/" + instance
	}

	switch opts.AuthMethod {
	case config.AuthWindows:
		q.Set("integrated security", "true")
	case config.AuthEntraDefault, config.AuthEntraPassword, config.AuthEntraMSI,
		config.AuthEntraServicePrincipal, config.AuthEntraInteractive,
		config.AuthEntraDeviceCode, config.AuthEntraAzCLI:
		q.Set("fedauth", fedauthForMethod(opts.AuthMethod))
		if opts.User != "" && opts.Password != "" {
			u.User = url.UserPassword(opts.User, opts.Password)
		}
	default:
		if opts.User != "" {
			u.User = url.UserPassword(opts.User, opts.Password)
		}
	}
	u.RawQuery = q.Encode()

	connStr := u.String()
	if opts.ExtraProperties != "" {
		connStr += "&" + opts.ExtraProperties
	}
	return connStr
}

func fedauthForMethod(m config.AuthMethod) string {
	switch m {
	case config.AuthEntraDefault:
		return "ActiveDirectoryDefault"
	case config.AuthEntraPassword:
		return "ActiveDirectoryPassword"
	case config.AuthEntraMSI:
		return "ActiveDirectoryManagedIdentity"
	case config.AuthEntraServicePrincipal:
		return "ActiveDirectoryServicePrincipal"
	case config.AuthEntraInteractive:
		return "ActiveDirectoryInteractive"
	case config.AuthEntraDeviceCode:
		return "ActiveDirectoryDeviceCode"
	case config.AuthEntraAzCLI:
		return "ActiveDirectoryAzCli"
	}
	return ""
}
