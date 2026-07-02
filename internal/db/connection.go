// Package db wraps gosmo to provide connection management for gossms.
package db

import (
	"fmt"

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
}

// Connect opens a connection using the given config.Connection.
func Connect(opts config.Connection) (*ServerConn, error) {
	port := opts.Port
	if port == 0 {
		port = 1433
	}
	server := opts.Server
	if port != 1433 {
		server = fmt.Sprintf("%s:%d", opts.Server, port)
	}

	co := gosmo.ConnectionOptions{
		Server:                 server,
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

// toGosmoAuth translates config.AuthMethod to gosmo.AuthMethod via an
// explicit switch rather than a raw numeric conversion. The two enums are
// declared independently in separate packages with no guarantee their
// iota values stay aligned — a bare gosmo.AuthMethod(opts.AuthMethod)
// cast would compile fine but silently select the wrong auth strategy at
// runtime if either enum's ordering ever changes. This mapping is the
// single place that assumption is made explicit and checkable.
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
func BuildConnectionString(opts config.Connection) string {
	port := opts.Port
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

	switch opts.AuthMethod {
	case config.AuthWindows:
		return fmt.Sprintf(
			"sqlserver://%s:%d?database=%s&encrypt=%s&TrustServerCertificate=%s&integrated security=true",
			opts.Server, port, opts.Database, encrypt, trustCert,
		)
	case config.AuthEntraDefault, config.AuthEntraPassword, config.AuthEntraMSI,
		config.AuthEntraServicePrincipal, config.AuthEntraInteractive,
		config.AuthEntraDeviceCode, config.AuthEntraAzCLI:
		fedauth := fedauthForMethod(opts.AuthMethod)
		if opts.User != "" && opts.Password != "" {
			return fmt.Sprintf(
				"sqlserver://%s:%s@%s:%d?database=%s&encrypt=%s&TrustServerCertificate=%s&fedauth=%s",
				opts.User, opts.Password, opts.Server, port, opts.Database, encrypt, trustCert, fedauth,
			)
		}
		return fmt.Sprintf(
			"sqlserver://%s:%d?database=%s&encrypt=%s&TrustServerCertificate=%s&fedauth=%s",
			opts.Server, port, opts.Database, encrypt, trustCert, fedauth,
		)
	default:
		if opts.User != "" {
			return fmt.Sprintf(
				"sqlserver://%s:%s@%s:%d?database=%s&encrypt=%s&TrustServerCertificate=%s",
				opts.User, opts.Password, opts.Server, port, opts.Database, encrypt, trustCert,
			)
		}
		return fmt.Sprintf(
			"sqlserver://%s:%d?database=%s&encrypt=%s&TrustServerCertificate=%s",
			opts.Server, port, opts.Database, encrypt, trustCert,
		)
	}
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
