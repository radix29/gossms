// Package db wraps gosmo to provide connection management for gossms.
package db

import (
	"fmt"
	"net/url"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
)

// ConnectionError is a typed error returned by Connect.
// Go 1.26: errors.AsType[*ConnectionError] can check for this without reflection.
type ConnectionError struct {
	Server string
	Cause  string
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("connect to %s: %s", e.Server, e.Cause)
}

// ServerConn wraps a gosmo server connection plus its config.
type ServerConn struct {
	Opts   config.Connection
	Server *gosmo.Server

	closed bool
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
	}
	if opts.Database != "" {
		co.Database = opts.Database
	}

	srv, err := gosmo.Connect(co)
	if err != nil {
		return nil, &ConnectionError{Server: opts.Server, Cause: err.Error()}
	}
	return &ServerConn{Opts: opts, Server: srv}, nil
}

// Close disconnects from SQL Server.
func (sc *ServerConn) Close() {
	if sc.Server != nil {
		sc.Server.Close()
	}
	sc.closed = true
}

// IsOpen reports whether sc is a non-nil connection that hasn't been
// closed — true for the lifetime between Connect and Close, regardless of
// whether sc is tracked in any particular list (e.g. a query panel's own
// dedicated connection, never added to App.connections).
func (sc *ServerConn) IsOpen() bool {
	return sc != nil && !sc.closed
}

// Label builds the Object Explorer root-node label for a connected
// server: "host[\instance or ,port] (user, SQL Server version)" — the
// bracketed instance name takes precedence over a bracketed port number
// when both would otherwise apply, and the default port (1433) is never
// shown. Meant to be called only after Connect has succeeded, so
// sc.Server.Info() has real data; if sc.Server is nil the version is
// simply left blank rather than panicking.
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

	user := sc.Opts.User
	if user == "" {
		user = config.AuthMethodName(sc.Opts.AuthMethod)
	}

	var version string
	if sc.Server != nil && sc.Server.Info() != nil {
		version = sc.Server.Info().ProductVersion
	}

	return fmt.Sprintf("%s (%s, SQL Server %s)", name, user, version)
}

// resolveServer folds the Connect dialog's separate Server and Port fields
// into the single address gosmo.ConnectionOptions.Server expects. Server
// alone may already be any form gosmo.ParseServerAddress understands
// ("host", "host:port", "host,port", "host\instance", "host\instance,port")
// — if it already carries its own port, that wins outright rather than
// blindly appending the dialog's default (1433) on top and corrupting an
// otherwise-valid address. When Server carries a "\instance" but no port,
// a non-default dialogPort is appended with a comma ("host\instance,port")
// rather than a colon — gosmo only recognises a trailing port after an
// instance name when it's comma-separated; a colon there would just
// become part of the instance name instead.
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

// encryptString converts the boolean Encrypt setting from config.Connection
// into the string value gosmo.ConnectionOptions.Encrypt expects
// (mirroring the go-mssqldb "encrypt" DSN parameter: "true"/"false").
func encryptString(encrypt bool) string {
	if encrypt {
		return "true"
	}
	return "false"
}

// toGosmoAuth translates config.AuthMethod to gosmo.AuthMethod. The two
// enums are declared independently with no guarantee their values stay
// aligned, so this is an explicit switch, not a numeric cast.
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

// BuildConnectionString produces a DSN string for the connection options.
// User, Password, and Database are properly URL-encoded via net/url (the
// same approach gosmo's own buildDSN uses for the DSN it actually
// connects with) so a value containing a reserved character like "@" or
// "&" can't corrupt the result. If opts.ExtraProperties is set, it's
// appended to the end verbatim, preceded by a "&" separator.
//
// A "\instance" in opts.Server is carried as a URL path segment
// (sqlserver://host:port/instance), not embedded in Host — a literal
// backslash there would get percent-escaped into an ugly, misleading
// "%5C" in the preview (this mirrors gosmo's own buildDSN, which needs
// the same treatment for the DSN to actually parse).
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
	q.Set("database", opts.Database)
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
