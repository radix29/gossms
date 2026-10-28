// Package connect turns a command line into a SQL Server connection: the
// connection options, a saved profile with the caller's overrides, the
// password sources, and Connect's failures as exit codes (docs/cli.md
// § Connecting).
//
// The precedence is command line > environment variable > profile > built-in
// default. A profile's saved password is sealed to where it may be sent
// (config.connectionAAD); Resolve keeps that promise after decryption by
// dropping it whenever an override changes the destination or weakens TLS.
package connect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// Option names, as Invocation.Values keys them.
const (
	OptServer        = "server"
	OptPort          = "port"
	OptDatabase      = "database"
	OptAuth          = "auth"
	OptUser          = "user"
	OptTenantID      = "tenant-id"
	OptClientID      = "client-id"
	OptPasswordEnv   = "password-env"
	OptPasswordStdin = "password-stdin"
	OptEncrypt       = "encrypt"
	OptTrustCert     = "trust-server-certificate"
	OptHostNameCert  = "host-name-in-certificate"
	OptAppIntent     = "application-intent"
	OptTimeout       = "connect-timeout"
)

// DefaultConnectTimeout is --connect-timeout's default, in seconds, and
// MaxConnectTimeout its ceiling: db.Connect bounds every attempt at 30 s
// itself, so a longer value would be a promise nothing keeps.
const (
	DefaultConnectTimeout = 15
	MaxConnectTimeout     = 30
)

// authNames maps --auth to config.AuthMethod, in help order. The two prompting
// methods are listed so that asking for one gets the reason it is refused
// (refusedAuth), not "not a valid value".
var authNames = []struct {
	name   string
	method config.AuthMethod
}{
	{"sql", config.AuthSQLServer},
	{"windows", config.AuthWindows},
	{"entra-default", config.AuthEntraDefault},
	{"entra-password", config.AuthEntraPassword},
	{"entra-msi", config.AuthEntraMSI},
	{"entra-sp", config.AuthEntraServicePrincipal},
	{"entra-azcli", config.AuthEntraAzCLI},
	{"entra-interactive", config.AuthEntraInteractive},
	{"entra-device-code", config.AuthEntraDeviceCode},
}

// AuthName is m's --auth spelling; "unknown" for a method this build does not
// know (a hand-edited config.json).
func AuthName(m config.AuthMethod) string {
	for _, a := range authNames {
		if a.method == m {
			return a.name
		}
	}
	return "unknown"
}

func authEnum() []string {
	out := make([]string, len(authNames))
	for i, a := range authNames {
		out[i] = a.name
	}
	return out
}

func authMethod(name string) config.AuthMethod {
	for _, a := range authNames {
		if a.name == name {
			return a.method
		}
	}
	return -1
}

// Options is the connection options a command that connects takes, after
// --profile (or its positional profile argument).
func Options() []spec.Option {
	encrypt := make([]string, 0, 3)
	for _, m := range config.AllEncryptModes() {
		encrypt = append(encrypt, string(m))
	}
	return []spec.Option{
		{Name: OptServer, Short: "S", Type: spec.TypeString, Env: "GOSSMS_SERVER",
			Help: `Server: host, host\instance, host,port (the Connect dialog's syntax)`},
		{Name: OptPort, Type: spec.TypeInt, Help: "Port, when --server does not name one"},
		{Name: OptDatabase, Short: "d", Type: spec.TypeString, Env: "GOSSMS_DATABASE", Help: "Initial database"},
		{Name: OptAuth, Type: spec.TypeEnum, Enum: authEnum(), Env: "GOSSMS_AUTH",
			Help: "Authentication method; entra-interactive and entra-device-code prompt, so they are refused"},
		{Name: OptUser, Short: "U", Type: spec.TypeString, Env: "GOSSMS_USER", Help: "Login name (sql, windows, entra-password)"},
		{Name: OptTenantID, Type: spec.TypeString, Env: "GOSSMS_TENANT_ID", Help: "Microsoft Entra tenant ID"},
		{Name: OptClientID, Type: spec.TypeString, Env: "GOSSMS_CLIENT_ID",
			Help: "Microsoft Entra client ID (service principal, user-assigned managed identity)"},
		{Name: OptPasswordEnv, Type: spec.TypeString,
			Help: "Name of the environment variable holding the password or client secret"},
		{Name: OptPasswordStdin, Type: spec.TypeBool, Help: "Read the password or client secret from stdin"},
		{Name: OptEncrypt, Type: spec.TypeEnum, Enum: encrypt, Env: "GOSSMS_ENCRYPT", Help: "Connection encryption"},
		{Name: OptTrustCert, Type: spec.TypeBool, Help: "Do not validate the server certificate (development only)"},
		{Name: OptHostNameCert, Type: spec.TypeString, Help: "Host name the server certificate must name"},
		{Name: OptAppIntent, Type: spec.TypeEnum, Enum: []string{"readwrite", "readonly"},
			Help: "readonly routes an availability group listener to a readable secondary"},
		{Name: OptTimeout, Type: spec.TypeInt, Default: fmt.Sprint(DefaultConnectTimeout),
			Help: fmt.Sprintf("Connect timeout in seconds, 1 to %d", MaxConnectTimeout)},
	}
}

// Target is a resolved connection: what to dial, and what the answer needs
// to say about where it came from.
type Target struct {
	Conn    config.Connection
	Profile string // the profile name as saved, or the generated name used; empty ad hoc
	// Ceiling is the profile's MaxAllow ceiling (config.Connection.Ceiling);
	// AllowAdmin ad hoc.
	Ceiling config.AllowClass
	// Password says where the password came from: "profile", "env:<VAR>",
	// "stdin", or "none".
	Password string
	// SavedPasswordDropped is set when the profile had a password the
	// overrides made unusable.
	SavedPasswordDropped bool
	Timeout              time.Duration
}

// Resolve builds the connection inv asks for: profile (a saved connection
// found by config.Config.ProfileByName; empty for ad hoc) with every given
// connection option laid over it. Every failure is an *output.Error: a usage
// error, NOT_FOUND for an unknown profile, AUTH_FAILED for a refused method or
// a missing password.
func Resolve(inv *spec.Invocation, cfg *config.Config, profile string) (*Target, error) {
	t := &Target{Ceiling: config.AllowAdmin, Password: "none"}
	var base config.Connection
	if profile != "" {
		p, err := cfg.ProfileByName(profile)
		if err != nil {
			return nil, profileNotFound(cfg, profile)
		}
		base = p
		t.Profile = cmpOr(p.Profile, p.GeneratedName())
		t.Ceiling = p.Ceiling()
	} else {
		base = config.Connection{AuthMethod: config.AuthSQLServer, Encrypt: config.EncryptMandatory}
	}

	c := base
	given := func(name string) bool { return inv.Given[name] }
	str := func(name string) string { s, _ := inv.Values[name].(string); return s }

	if given(OptServer) {
		host, instance, port := gosmo.ParseServerAddress(strings.TrimSpace(str(OptServer)))
		if host == "" {
			return nil, usage("--server is empty", "")
		}
		c.Server = host
		if instance != "" {
			c.Server += `\` + instance
		}
		c.Port = port
		if given(OptPort) && port != 0 && port != inv.Values[OptPort].(int) {
			return nil, usage(fmt.Sprintf("--server names port %d and --port %d", port, inv.Values[OptPort].(int)),
				"Give the port once.")
		}
	}
	if given(OptPort) {
		p := inv.Values[OptPort].(int)
		if p < 1 || p > 65535 {
			return nil, usage(fmt.Sprintf("--port %d: a port is 1 to 65535", p), "")
		}
		c.Port = p
	}
	if c.Server == "" {
		return nil, usage("no server: give --server, or a profile", "Run `gossms profile list` for the saved profiles.")
	}
	if given(OptDatabase) {
		c.Database = str(OptDatabase)
	}
	if given(OptAuth) {
		c.AuthMethod = authMethod(str(OptAuth))
	}
	if given(OptUser) {
		c.User = str(OptUser)
	}
	if given(OptTenantID) {
		c.TenantID = str(OptTenantID)
	}
	if given(OptClientID) {
		c.ClientID = str(OptClientID)
	}
	if given(OptEncrypt) {
		c.Encrypt = config.EncryptMode(str(OptEncrypt))
	}
	if given(OptTrustCert) {
		c.TrustServerCertificate = inv.Values[OptTrustCert].(bool)
	}
	if given(OptHostNameCert) {
		c.HostNameInCertificate = str(OptHostNameCert)
	}
	if given(OptAppIntent) {
		extra, err := withApplicationIntent(c.ExtraProperties, str(OptAppIntent))
		if err != nil {
			return nil, usage(err.Error(), "")
		}
		c.ExtraProperties = extra
	}
	secs := inv.Values[OptTimeout].(int)
	if secs < 1 || secs > MaxConnectTimeout {
		return nil, usage(fmt.Sprintf("--connect-timeout %d: give 1 to %d seconds", secs, MaxConnectTimeout), "")
	}
	t.Timeout = time.Duration(secs) * time.Second

	if err := refusedAuth(c.AuthMethod); err != nil {
		return nil, err
	}
	// A field the method does not read is not sent, as in the Connect dialog.
	f := config.FieldsFor(c.AuthMethod)
	if !f.User {
		c.User = ""
	}
	if !f.Tenant {
		c.TenantID = ""
	}
	if !f.Client {
		c.ClientID = ""
	}

	if profile != "" && redirected(base, c) {
		t.SavedPasswordDropped = base.Password != "" || base.PasswordUnreadable()
		c.Password = ""
	} else if c.Password != "" {
		t.Password = "profile"
	}
	if err := readPassword(inv, f, &c, t); err != nil {
		return nil, err
	}
	if err := missingPassword(c, t); err != nil {
		return nil, err
	}
	t.Conn = c
	return t, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// redirected reports whether c sends the password somewhere base's sealing
// did not agree to: another server, port, login, method, tenant or client, or
// weaker TLS — Encrypt lowered, the certificate no longer validated, or
// validated against another name. The database and application intent are
// the same destination.
func redirected(base, c config.Connection) bool {
	return !strings.EqualFold(base.Server, c.Server) || config.DialPort(base.Port) != config.DialPort(c.Port) ||
		base.User != c.User || base.AuthMethod != c.AuthMethod ||
		base.TenantID != c.TenantID || base.ClientID != c.ClientID ||
		encryptRank(c.Encrypt) < encryptRank(base.Encrypt) ||
		(c.TrustServerCertificate && !base.TrustServerCertificate) ||
		base.HostNameInCertificate != c.HostNameInCertificate
}

// encryptRank orders the modes by strength; "" dials as Optional.
func encryptRank(m config.EncryptMode) int {
	return max(slices.Index(config.AllEncryptModes(), m), 0)
}

// readPassword takes the password from --password-env or --password-stdin
// (at most one). Either replaces a saved one.
func readPassword(inv *spec.Invocation, f config.AuthFields, c *config.Connection, t *Target) error {
	name, _ := inv.Values[OptPasswordEnv].(string)
	stdin, _ := inv.Values[OptPasswordStdin].(bool)
	if name == "" && !stdin {
		return nil
	}
	if name != "" && stdin {
		return usage("--password-env and --password-stdin both given", "Give one password source.")
	}
	if !f.Password {
		return usage(fmt.Sprintf("%s takes no password", AuthName(c.AuthMethod)), "")
	}
	if name != "" {
		v := ""
		if inv.Getenv != nil {
			v = inv.Getenv(name)
		}
		if v == "" {
			return output.NewError(output.CodeAuthFailed, fmt.Sprintf("--password-env %s: the variable is not set or empty", name), "")
		}
		c.Password, t.Password = v, "env:"+name
		return nil
	}
	if inv.Stdin == nil {
		return output.NewError(output.CodeAuthFailed, "--password-stdin: no stdin", "")
	}
	data, err := io.ReadAll(io.LimitReader(inv.Stdin, 64<<10))
	if err != nil {
		return output.NewError(output.CodeAuthFailed, "--password-stdin: "+err.Error(), "")
	}
	// One line: what `echo pw |` and PowerShell's pipe send.
	data, _, _ = bytes.Cut(data, []byte("\n"))
	data = bytes.TrimSuffix(data, []byte("\r"))
	if len(data) == 0 {
		return output.NewError(output.CodeAuthFailed, "--password-stdin: stdin held no password", "")
	}
	c.Password, t.Password = string(data), "stdin"
	return nil
}

// missingPassword refuses, before dialling, a method that needs a secret and
// has none: SQL Server Authentication, Entra Password and Service Principal.
// Windows Authentication without one is integrated sign-in.
func missingPassword(c config.Connection, t *Target) error {
	switch c.AuthMethod {
	case config.AuthSQLServer, config.AuthEntraPassword, config.AuthEntraServicePrincipal:
	default:
		return nil
	}
	if c.Password != "" {
		return nil
	}
	msg := AuthName(c.AuthMethod) + " authentication needs a password, and none was given"
	switch {
	case t.SavedPasswordDropped:
		msg += " (the profile's saved password is not sent to an overridden server, login or weaker TLS)"
	case t.Profile != "":
		msg += fmt.Sprintf(" (profile %s has no saved password it can open)", t.Profile)
	}
	return output.NewError(output.CodeAuthFailed, msg,
		"Pass --password-env <VAR> or --password-stdin, or save the password with the connection (Remember Password) in the TUI.")
}

// refusedAuth is AUTH_FAILED for a method the command line cannot use: the
// two that prompt a person (and whose token cache lives one process, so every
// call would prompt again), and an unknown one.
func refusedAuth(m config.AuthMethod) error {
	switch m {
	case config.AuthEntraInteractive, config.AuthEntraDeviceCode:
		return output.NewError(output.CodeAuthFailed,
			AuthName(m)+" authentication prompts for a sign-in, which the command line cannot do",
			"Use entra-default, entra-azcli (after `az login`), entra-sp or entra-msi.")
	}
	if AuthName(m) == "unknown" {
		return output.NewError(output.CodeAuthFailed,
			fmt.Sprintf("unknown authentication method %d in the saved connection", int(m)),
			"Choose a method in the TUI's Connect dialog, or pass --auth.")
	}
	return nil
}

// withApplicationIntent sets ApplicationIntent in an Extra Properties text,
// replacing any spelling of it already there.
func withApplicationIntent(extra, intent string) (string, error) {
	props, err := db.ParseExtraProperties(extra)
	if err != nil {
		return "", err
	}
	var entries []string
	for k, vs := range props {
		norm := strings.ToLower(strings.ReplaceAll(k, " ", ""))
		if norm == "applicationintent" {
			continue
		}
		for _, v := range vs {
			entries = append(entries, k+"="+v)
		}
	}
	slices.Sort(entries)
	value := "ReadWrite"
	if intent == "readonly" {
		value = "ReadOnly"
	}
	return strings.Join(append(entries, "ApplicationIntent="+value), ";"), nil
}

// Address is the server c dials, spelled as --server takes it: host,
// host\instance, with ",port" when the port is not 1433.
func Address(c config.Connection) string {
	if _, _, port := gosmo.ParseServerAddress(c.Server); port != 0 || config.DialPort(c.Port) == 0 {
		return c.Server
	}
	return fmt.Sprintf("%s,%d", c.Server, c.Port)
}

func usage(msg, hint string) *output.Error {
	return output.NewError(output.CodeUsageError, msg, hint)
}

// profileNotFound is NOT_FOUND for name, with a did-you-mean over the saved
// profile names.
func profileNotFound(cfg *config.Config, name string) *output.Error {
	var names []string
	for _, p := range cfg.Profiles() {
		names = append(names, p.Profile)
	}
	hint := "Run `gossms profile list` for the saved profiles (`--all` adds unnamed recent connections)."
	if s := args.Suggest(name, names); s != "" {
		hint = fmt.Sprintf("Did you mean %s? ", s) + hint
	}
	return output.NewError(output.CodeNotFound, fmt.Sprintf("no profile or saved connection named %q", name), hint)
}

// ProfileNotFound is the NOT_FOUND error Resolve answers for an unknown
// profile, for commands that look one up themselves.
func ProfileNotFound(cfg *config.Config, name string) error { return profileNotFound(cfg, name) }

// ConnectFunc is db.Connect's shape, so tests can stand in for a server.
type ConnectFunc func(ctx context.Context, opts config.Connection, role db.Role) (*db.ServerConn, error)

// Open dials t as a command-line connection (db.RoleCLI) within t.Timeout,
// mapping a failure to its exit code (Classify).
func Open(ctx context.Context, t *Target, connect ConnectFunc) (*db.ServerConn, error) {
	dctx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()
	sc, err := connect(dctx, t.Conn, db.RoleCLI)
	if err != nil {
		timedOut := ctx.Err() == nil && errors.Is(dctx.Err(), context.DeadlineExceeded)
		return nil, Classify(err, timedOut, t)
	}
	return sc, nil
}

// Login failures (Msg 18456 and its relatives): the server refused the
// credentials, not the network.
var loginFailed = []int32{18456, 18452, 18470, 18486, 18487, 18488}

// The login's database cannot be opened: 4060, or 4063 when the server falls
// back to the login's default database and the driver refuses the switch (what
// SQL Server 2016 to 2025 send, seen live).
var cannotOpenDatabase = []int32{4060, 4063}

// Classify maps a connect failure to an *output.Error: TIMEOUT when the
// attempt ran out of time (timedOut, or a network timeout), AUTH_FAILED when
// the server or Entra refused the credentials, NOT_FOUND when the login's
// database cannot be opened (cannotOpenDatabase), CONNECTION_FAILED otherwise. Keyed on error
// types and numbers, never the text.
func Classify(err error, timedOut bool, t *Target) *output.Error {
	msg := err.Error()
	if se, ok := gosmo.AsSQLError(err); ok {
		switch {
		case slices.Contains(loginFailed, se.Number):
			hint := "Check the login and password."
			if t != nil && t.Password == "profile" {
				hint = "The saved password was sent; if it changed, reconnect in the TUI with the new one, or pass --password-env/--password-stdin."
			}
			return output.NewError(output.CodeAuthFailed, msg, hint)
		case slices.Contains(cannotOpenDatabase, se.Number):
			return output.NewError(output.CodeNotFound, msg,
				"The database does not exist, or the login cannot open it. Pass --database master to connect anyway.")
		}
	}
	if _, ok := errors.AsType[*azidentity.AuthenticationFailedError](err); ok {
		return output.NewError(output.CodeAuthFailed, msg, "")
	}
	ne, isNet := errors.AsType[net.Error](err)
	if timedOut || errors.Is(err, context.DeadlineExceeded) || (isNet && ne.Timeout()) {
		hint := ""
		if t != nil {
			hint = fmt.Sprintf("Raise --connect-timeout (now %d s, at most %d).", int(t.Timeout/time.Second), MaxConnectTimeout)
		}
		return output.NewError(output.CodeTimeout, msg, hint)
	}
	return output.NewError(output.CodeConnectionFailed, msg, "")
}
