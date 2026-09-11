// Package config holds persistent application state (saved connections,
// settings).
package config

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/radix29/gossms/internal/fileutil"
)

// AuthMethod is gossms's auth-method enum for the UI and JSON. Its values are
// independent of gosmo.AuthMethod; internal/db/connection.go maps between them.
type AuthMethod int

const (
	AuthSQLServer             AuthMethod = 0
	AuthWindows               AuthMethod = 1
	AuthEntraDefault          AuthMethod = 2
	AuthEntraPassword         AuthMethod = 3
	AuthEntraMSI              AuthMethod = 4
	AuthEntraServicePrincipal AuthMethod = 5
	AuthEntraInteractive      AuthMethod = 9
	AuthEntraDeviceCode       AuthMethod = 10
	AuthEntraAzCLI            AuthMethod = 11
)

// AuthFields says which Connect-dialog credential fields an auth method reads
// (see db.toGosmoOptions). The dialog greys out the rest, as SSMS does, so no
// field looks live but is never sent.
type AuthFields struct{ User, Password, Tenant, Client bool }

// authMethodInfo is everything about an auth method except its gosmo mapping,
// which db.toGosmoAuth keeps as a switch.
type authMethodInfo struct {
	method AuthMethod
	// label is the Connect dialog's name for it, as SSMS spells it.
	label string
	// tag marks a saved connection's name (see Connection.GeneratedName); empty
	// for SQL Server Authentication.
	tag    string
	entra  bool
	fields AuthFields
}

// authMethods is the single table of auth methods, in Connect-dialog order,
// read by AllAuthMethods, AuthMethodName, IsEntraMethod and FieldsFor.
//
// TenantID is live for every Entra method but Managed Identity, whose tenant is
// the resource's own. ClientID is the app registration's client id for
// Password, MFA and Device Code (empty uses Microsoft's public client), the
// service principal itself, or a user-assigned managed identity. User is MFA's
// optional login hint.
var authMethods = []authMethodInfo{
	{AuthSQLServer, "SQL Server Authentication", "", false, AuthFields{User: true, Password: true}},
	{AuthWindows, "Windows Authentication", "Windows", false, AuthFields{User: true, Password: true}},
	{AuthEntraDefault, "Microsoft Entra Default", "Entra Default", true, AuthFields{Tenant: true}},
	{AuthEntraPassword, "Microsoft Entra Password", "Entra Password", true,
		AuthFields{User: true, Password: true, Tenant: true, Client: true}},
	{AuthEntraMSI, "Microsoft Entra Managed Identity", "Entra Managed Identity", true, AuthFields{Client: true}},
	{AuthEntraServicePrincipal, "Microsoft Entra Service Principal", "Entra Service Principal", true,
		AuthFields{Password: true, Tenant: true, Client: true}},
	{AuthEntraInteractive, "Microsoft Entra MFA", "Entra MFA", true, AuthFields{User: true, Tenant: true, Client: true}},
	{AuthEntraDeviceCode, "Microsoft Entra Device Code", "Entra Device Code", true, AuthFields{Tenant: true, Client: true}},
	{AuthEntraAzCLI, "Microsoft Entra Azure CLI", "Entra Azure CLI", true, AuthFields{Tenant: true}},
}

// authInfo looks m up in authMethods. An unknown method (only from a
// hand-edited config.json) reads as SQL Server Authentication, as
// db.toGosmoAuth dials it, labelled "Unknown".
func authInfo(m AuthMethod) authMethodInfo {
	for _, info := range authMethods {
		if info.method == m {
			return info
		}
	}
	info := authMethods[0]
	info.method, info.label = m, "Unknown"
	return info
}

// AuthMethodName returns the auth method's display label.
func AuthMethodName(m AuthMethod) string { return authInfo(m).label }

// IsEntraMethod reports whether m is one of the Microsoft Entra ID methods.
func IsEntraMethod(m AuthMethod) bool { return authInfo(m).entra }

// FieldsFor reports which credential fields m reads.
func FieldsFor(m AuthMethod) AuthFields { return authInfo(m).fields }

// AllAuthMethods returns all auth methods in display order.
func AllAuthMethods() []AuthMethod {
	out := make([]AuthMethod, len(authMethods))
	for i, info := range authMethods {
		out[i] = info.method
	}
	return out
}

// IconStyle selects the Object Explorer icon glyph set. The zero value,
// IconStyleEmoji, is the default for a config.json missing the field.
type IconStyle int

const (
	IconStyleEmoji IconStyle = iota
	IconStyleSymbols
	IconStylePortable
	IconStyleNone
)

// IconStyleName returns the Options dialog label for s.
func IconStyleName(s IconStyle) string {
	switch s {
	case IconStyleSymbols:
		return "Symbols"
	case IconStylePortable:
		return "Portable"
	case IconStyleNone:
		return "None"
	default:
		return "Emoji"
	}
}

// AllIconStyles returns the icon styles in Options-dialog order.
func AllIconStyles() []IconStyle {
	return []IconStyle{IconStyleEmoji, IconStyleSymbols, IconStylePortable, IconStyleNone}
}

// Connection stores one saved server connection. Password is always plaintext
// in memory; Load and Save encrypt at the JSON boundary (see secret.go).
type Connection struct {
	Name                   string     `json:"name"`
	Server                 string     `json:"server"`
	Port                   int        `json:"port"`
	Database               string     `json:"database"`
	AuthMethod             AuthMethod `json:"auth_method"`
	User                   string     `json:"user"`
	Password               string     `json:"password"`
	TenantID               string     `json:"tenant_id"`
	ClientID               string     `json:"client_id"`
	TrustServerCertificate bool       `json:"trust_server_certificate"`
	// Encrypt is stored as "encrypt_mode"; MarshalJSON also writes the legacy
	// boolean "encrypt".
	Encrypt               EncryptMode `json:"encrypt_mode"`
	HostNameInCertificate string      `json:"host_name_in_certificate,omitempty"`
	ExtraProperties       string      `json:"extra_properties"`

	// sealed is the on-disk ciphertext Load could not open (replaced key file,
	// hand-edited server/user bound into the AAD, truncated write). Save writes
	// it back verbatim instead of encrypting the "" Password holds, so the
	// password stays recoverable.
	//
	// Unexported, so JSON ignores it. Reconnecting goes through AddOrUpdate
	// with sealed empty, so re-entering the password replaces the ciphertext.
	sealed string
}

// EncryptMode is a connection's TLS setting, spelled as SSMS and go-mssqldb's
// "encrypt" parameter spell it.
type EncryptMode string

const (
	// EncryptOptional encrypts only the login packet; everything after —
	// including passwords in CREATE LOGIN, CREATE CREDENTIAL and certificate
	// statements — is clear text.
	EncryptOptional EncryptMode = "optional"
	// EncryptMandatory encrypts the whole session. The default, as in SSMS 20.
	EncryptMandatory EncryptMode = "mandatory"
	// EncryptStrict is TDS 8.0 strict encryption (TLS before any TDS traffic),
	// SQL Server 2022+.
	EncryptStrict EncryptMode = "strict"
)

// AllEncryptModes returns the modes in Connect-dialog order.
func AllEncryptModes() []EncryptMode {
	return []EncryptMode{EncryptOptional, EncryptMandatory, EncryptStrict}
}

// EncryptModeName returns the Connect dialog's label for m.
func EncryptModeName(m EncryptMode) string {
	switch m {
	case EncryptOptional:
		return "Optional"
	case EncryptMandatory:
		return "Mandatory"
	case EncryptStrict:
		return "Strict (SQL Server 2022+)"
	default:
		return string(m)
	}
}

// connectionFields is Connection without methods, so (Un)MarshalJSON don't
// recurse.
type connectionFields Connection

// connectionWire is a Connection as written to config.json, plus the legacy
// boolean "encrypt".
type connectionWire struct {
	connectionFields
	LegacyEncrypt *bool `json:"encrypt,omitempty"`
}

// MarshalJSON writes Encrypt as "encrypt_mode" and also the legacy boolean
// "encrypt" (true unless Optional), for an older gossms reading this file after
// a downgrade: it decodes "encrypt" as a bool, and a string there would make
// Load treat the whole file as corrupt.
func (c Connection) MarshalJSON() ([]byte, error) {
	legacy := c.Encrypt != EncryptOptional && c.Encrypt != ""
	return json.Marshal(connectionWire{connectionFields: connectionFields(c), LegacyEncrypt: &legacy})
}

// UnmarshalJSON maps an entry without "encrypt_mode" from its boolean
// "encrypt": true is Mandatory, false or absent is Optional. "encrypt_mode"
// wins when present.
func (c *Connection) UnmarshalJSON(data []byte) error {
	var w connectionWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*c = Connection(w.connectionFields)
	if c.Encrypt == "" {
		c.Encrypt = EncryptOptional
		if w.LegacyEncrypt != nil && *w.LegacyEncrypt {
			c.Encrypt = EncryptMandatory
		}
	}
	return nil
}

// ConnectionName builds "server,port,database,user", the prefix of a saved
// connection's generated name and the key completion inventories share. Port 0
// is spelled 1433, as dialled, so older entries dedup against today's.
func ConnectionName(server string, port int, database, user string) string {
	if port == 0 {
		port = 1433
	}
	return server + "," + strconv.Itoa(port) + "," + database + "," + user
}

// GeneratedName is the name AddOrUpdate gives c — ConnectionName with c's
// signing-in identity in the user slot, plus the auth method's tag:
// "srv,1433,db,app-id (Entra Service Principal)". It is both the Connect
// autocomplete label and the dedup key, so it must distinguish any two
// different connections (e.g. two service principals on one server, or Windows
// vs Entra Default).
//
// The identity is User when the method reads one and it's set, else ClientID
// when the method reads that; a service principal saved before ClientID existed
// carries its application id in User.
//
// SQL Server Authentication has no tag, so its names are unchanged. An older
// entry under another method dedups once more, against its first save under the
// new name.
func (c Connection) GeneratedName() string {
	info := authInfo(c.AuthMethod)
	identity := ""
	switch {
	case info.fields.User && c.User != "":
		identity = c.User
	case info.fields.Client:
		identity = cmp.Or(c.ClientID, c.User)
	}
	name := ConnectionName(c.Server, c.Port, c.Database, identity)
	if info.tag != "" {
		name += " (" + info.tag + ")"
	}
	return name
}

// PasswordUnreadable reports whether Load could not decrypt a stored password
// (the ciphertext is kept and written back). It separates "no password saved"
// from "saved but unusable", which Password alone can't — Load blanks both, and
// connecting with "" is a guaranteed login failure.
func (c *Connection) PasswordUnreadable() bool {
	return c.Password == "" && c.sealed != ""
}

// DisplayName returns a label for the connection.
func (c *Connection) DisplayName() string {
	if c.Name != "" {
		return c.Name
	}
	if c.Server != "" {
		return c.Server
	}
	return "(unnamed)"
}

// Config is the root configuration structure.
type Config struct {
	Connections   []Connection `json:"connections"`
	IconStyle     IconStyle    `json:"icon_style"`
	MaxCellLength int          `json:"max_cell_length"`
	// IntelliSenseDisabled turns off editor autocomplete. Inverted so the zero
	// value keeps it on.
	IntelliSenseDisabled bool `json:"intellisense_disabled"`

	// unreadable is the error Load hit reading an existing config.json. It
	// write-protects this Config (see Save): the file's contents are missing,
	// so writing back would destroy a likely-intact file. Unexported, so it
	// neither serialises nor survives a copy.
	unreadable error
}

// DefaultMaxCellLength is how many characters a result-grid cell shows before
// truncating, absent an Options override; a column dragged wider shows more.
// Load applies it to a zero MaxCellLength.
const DefaultMaxCellLength = 24

// configPath returns the path to the config file.
func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "gossms", "config.json")
}

// LogFilePath returns the log file path, beside the config file so the launch
// directory doesn't matter. Creates the directory if missing.
func LogFilePath() (string, error) {
	dir := filepath.Dir(configPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "gossms.log"), nil
}

// MaxLogSize is how large gossms.log may grow before OpenLogFile starts a fresh
// one.
const MaxLogSize = 5 << 20

// OpenLogFile opens the log at LogFilePath for appending, first moving a file
// past MaxLogSize to gossms.log.1 (one generation). Rotation happens only at
// startup, so a session never loses its own lines.
//
// The file is 0600 like the config and key: the log records server names,
// logins and error text.
func OpenLogFile() (*os.File, error) {
	path, err := LogFilePath()
	if err != nil {
		return nil, err
	}
	rotateLog(path, MaxLogSize)
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// rotateLog renames path to path+".1" when larger than limit. Best effort: a
// failed rename (e.g. file held open on Windows) just keeps appending.
func rotateLog(path string, limit int64) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() <= limit {
		return
	}
	_ = os.Rename(path, path+".1")
}

// Load reads the config, returning an empty one if there is no file. Passwords
// are decrypted (see secret.go); if the key is unavailable or one password
// won't decrypt, that Password is "" and everything else loads.
//
// A file that exists but can't be read (permissions, EIO, EMFILE) comes back
// unreadable, and Save refuses to write it — otherwise the next unrelated Save
// would overwrite a good file with an empty config.
func Load() *Config {
	path := configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		cfg := new(Config) // Go 1.26: new(expr) — zero-value Config
		cfg.MaxCellLength = DefaultMaxCellLength
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("config: %s exists but could not be read (%v); "+
				"starting with no saved settings and refusing to overwrite it", path, err)
			cfg.unreadable = err
		}
		return cfg
	}
	cfg := new(Config)
	if err := json.Unmarshal(data, cfg); err != nil {
		// Unparseable (hand-edit or interrupted write). Keep the bytes as
		// .corrupt before starting empty: passwords are lost but
		// server/user/database are readable by hand. Written atomically, since
		// it's the only remaining copy.
		_ = fileutil.WriteAtomic(path+".corrupt", data, 0o600)
		cfg = new(Config)
	}
	if cfg.MaxCellLength <= 0 {
		cfg.MaxCellLength = DefaultMaxCellLength
	}

	key, err := loadOrCreateKey(filepath.Dir(path))
	if err != nil {
		// No key: stash every ciphertext in sealed so Save writes it back
		// instead of encrypting "".
		log.Printf("config: saved passwords unavailable: %v", err)
		for i := range cfg.Connections {
			cfg.Connections[i].sealed = cfg.Connections[i].Password
			cfg.Connections[i].Password = ""
		}
		return cfg
	}
	failed := 0
	for i := range cfg.Connections {
		plain, ok := decryptPassword(key, cfg.Connections[i])
		if !ok {
			cfg.Connections[i].sealed = cfg.Connections[i].Password
			failed++
		}
		cfg.Connections[i].Password = plain
	}
	if failed > 0 {
		log.Printf("config: %d saved password(s) could not be decrypted and are "+
			"preserved as-is in %s; re-enter the password to replace one", failed, path)
	}
	return cfg
}

// Save writes the config. Passwords are AES-256-GCM encrypted and
// base64-encoded (see secret.go) on disk only; c keeps plaintext.
//
// An entry whose password Load couldn't open keeps its original ciphertext
// (Connection.sealed), so an unrelated save doesn't destroy passwords a
// restored key file could still open.
func (c *Config) Save() error {
	path := configPath()
	if c.unreadable != nil {
		// Load never saw this file's contents; writing c would replace them
		// with emptiness.
		return fmt.Errorf("config: not saving over %s — it could not be read at startup: %w", path, c.unreadable)
	}
	dir := filepath.Dir(path)
	// 0700, matching loadOrCreateKey's MkdirAll (secret.go). MkdirAll doesn't
	// chmod an existing directory, so whichever runs first decides; both must
	// ask for owner-only.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	key, err := loadOrCreateKey(dir)
	if err != nil {
		return err
	}
	// Copy c wholesale, then replace Connections, so fields added to Config
	// later are never dropped.
	onDisk := *c
	onDisk.Connections = make([]Connection, len(c.Connections))
	for i, conn := range c.Connections {
		if conn.Password == "" && conn.sealed != "" {
			// Load couldn't open this one; write the original bytes back.
			conn.Password = conn.sealed
			onDisk.Connections[i] = conn
			continue
		}
		enc, err := encryptPassword(key, conn)
		if err != nil {
			return err
		}
		conn.Password = enc
		onDisk.Connections[i] = conn
	}

	data, err := json.MarshalIndent(&onDisk, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteAtomic(path, data, 0o600)
}

// MaxSavedConnections caps saved recent connections. Connect saves each
// successful connection automatically.
const MaxSavedConnections = 30

// AddOrUpdate saves a successful connection. Name is set to
// conn.GeneratedName(), the dedup key: a match is replaced, otherwise added.
// The entry moves to the end (most recent) and the list is trimmed from the
// front to MaxSavedConnections. conn is taken by value; the caller's copy is
// untouched.
func (c *Config) AddOrUpdate(conn Connection) {
	conn.Name = conn.GeneratedName()
	for i, existing := range c.Connections {
		if existing.Name == conn.Name {
			c.Connections = slices.Delete(c.Connections, i, i+1)
			break
		}
	}
	c.Connections = append(c.Connections, conn)
	if len(c.Connections) > MaxSavedConnections {
		c.Connections = c.Connections[len(c.Connections)-MaxSavedConnections:]
	}
}

// MatchByServer returns saved connections whose Server has the given
// case-insensitive prefix, most recent first — the Connect dialog's
// autocomplete source.
func (c *Config) MatchByServer(prefix string) []Connection {
	prefix = strings.ToLower(prefix)
	var out []Connection
	for _, conn := range slices.Backward(c.Connections) {
		if strings.HasPrefix(strings.ToLower(conn.Server), prefix) {
			out = append(out, conn)
		}
	}
	return out
}
