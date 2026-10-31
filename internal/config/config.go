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
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

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

// authInfo looks m up in authMethods. An unknown method (a hand-edited
// config.json, or a newer gossms's) is labelled "Unknown" with SQL Server
// Authentication's fields and tag, for display only: db.toGosmoAuth refuses
// to dial it.
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

// ColorTheme selects the UI palette. The zero value, ThemeDark, is the default
// for a config.json missing the field.
type ColorTheme int

const (
	ThemeDark ColorTheme = iota
	ThemeLight
)

// ColorThemeName returns the Options dialog label for t.
func ColorThemeName(t ColorTheme) string {
	if t == ThemeLight {
		return "Light"
	}
	return "Dark"
}

// AllColorThemes returns the color themes in Options-dialog order.
func AllColorThemes() []ColorTheme { return []ColorTheme{ThemeDark, ThemeLight} }

// Connection is one saved server connection. Password is plaintext in memory;
// Load and Save encrypt at the JSON boundary (see secret.go).
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

	// RememberPassword is the Connect dialog's "Remember Password" box: the
	// password is written to config.json only when set (see AddOrUpdate). Absent
	// from connectionAAD so toggling it doesn't invalidate a sealed password
	// (secret.go).
	RememberPassword bool `json:"remember_password"`

	// sealed is the on-disk ciphertext Load could not open (replaced key file,
	// hand-edited server/user bound into the AAD, truncated write). Save writes it
	// back verbatim instead of encrypting the "" Password. Unexported, so JSON
	// ignores it.
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
// "encrypt" (true unless Optional): an older gossms after a downgrade decodes
// "encrypt" as a bool, and a string there would make Load treat the file as
// corrupt.
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
// connection's generated name. Port 0 is spelled 1433, as dialled.
func ConnectionName(server string, port int, database, user string) string {
	if port == 0 {
		port = 1433
	}
	return server + "," + strconv.Itoa(port) + "," + database + "," + user
}

// GeneratedName is the name AddOrUpdate gives c: ConnectionName with c's
// signing-in identity in the user slot, plus the auth method's tag
// ("srv,1433,db,app-id (Entra Service Principal)"). It is both the Connect
// autocomplete label and the dedup key, so it must distinguish any two different
// connections (two service principals on one server, Windows vs Entra Default).
//
// The identity is User when the method reads one and it is set, else ClientID
// when the method reads that; an older service principal entry carries its
// application id in User. SQL Server Authentication has no tag.
func (c Connection) GeneratedName() string {
	identity, tag := c.signInIdentity()
	name := ConnectionName(c.Server, c.Port, c.Database, identity)
	if tag != "" {
		name += " (" + tag + ")"
	}
	return name
}

// signInIdentity is the identity GeneratedName and IdentityKey name c by, and
// its auth method's tag ("" for SQL Server Authentication).
func (c Connection) signInIdentity() (identity, tag string) {
	info := authInfo(c.AuthMethod)
	switch {
	case info.fields.User && c.User != "":
		identity = c.User
	case info.fields.Client:
		identity = cmp.Or(c.ClientID, c.User)
	}
	return identity, info.tag
}

// PasswordUnreadable reports whether Load could not decrypt a stored password
// (the ciphertext is kept and written back). Password alone cannot tell "no
// password saved" from "saved but unusable": Load blanks both.
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
	// Connections is read freely but changed only through AddOrUpdate and
	// RemoveConnection: Save replays the recorded operations, not this slice.
	Connections   []Connection `json:"connections"`
	IconStyle     IconStyle    `json:"icon_style"`
	Theme         ColorTheme   `json:"theme"`
	MaxCellLength int          `json:"max_cell_length"`
	// MaxTextColumnLength is how many characters Results to Text shows of each
	// column, SSMS's "Maximum number of characters displayed in each column".
	// Zero or negative means DefaultMaxTextColumnLength.
	MaxTextColumnLength int `json:"max_text_column_length"`
	// IntelliSenseDisabled turns off editor autocomplete. Inverted so the zero
	// value keeps it on.
	IntelliSenseDisabled bool `json:"intellisense_disabled"`
	// IndentWidth is how many spaces the query editor's Tab, indent/dedent and
	// auto-indent use. Zero, or anything outside 1..MaxIndentWidth, means
	// DefaultIndentWidth.
	IndentWidth int `json:"indent_width"`
	// XEventStoreCapacity is how many events an Extended Events viewer holds
	// before the oldest are dropped. Anything outside
	// MinXEventStoreCapacity..MaxXEventStoreCapacity means
	// DefaultXEventStoreCapacity.
	XEventStoreCapacity int `json:"xevent_store_capacity"`
	// MaxFetchRows is how many rows a query window keeps of each result set, in
	// every results mode (grid, text, file). Reaching it cancels the rest of the
	// script, later GO batches included (query.WithMaxRows). Zero, the default,
	// means unlimited; Load turns a negative value into zero.
	MaxFetchRows int `json:"max_fetch_rows"`
	// XEventHiddenColumns is, per event session, the columns the Extended Events
	// viewer hides ("name", "timestamp", "field:<name>", "action:<name>"). Hidden
	// rather than shown, so a field the session starts collecting later still gets
	// a column. Replace the map, never write into it: base shares it, so an
	// in-place write compares equal to itself and Save skips it.
	XEventHiddenColumns map[string][]string `json:"xevent_hidden_columns,omitempty"`
	// XEventViewSettings are the viewer's saved display settings by name (SSMS's
	// .viewsetting files). Replace the map, as XEventHiddenColumns.
	XEventViewSettings map[string]XEventViewSetting `json:"xevent_view_settings,omitempty"`

	// unreadable is the error Load hit reading an existing config.json. It
	// write-protects this Config (see Save): writing back would destroy a
	// likely-intact file.
	unreadable error

	// ops is this process's AddOrUpdate/RemoveConnection calls since the last
	// successful Save, replayed onto the file as it is now. Writing the whole list
	// would delete what another instance saved meanwhile.
	ops []connOp

	// base is the settings as last loaded or saved by this process, nil for a
	// Config not from Load. Save writes a setting only when it differs from base;
	// otherwise it keeps the file's value, which another instance may have changed.
	base *Config

	// legacySealed is set when Load opened a password sealed under an earlier
	// binding than aadPrefix (see NeedsReseal); the next save clears it.
	legacySealed bool
}

// connOp is one recorded change to Connections: an AddOrUpdate of add, or a
// RemoveConnection of remove.
type connOp struct {
	add    *Connection
	remove string
}

// DefaultMaxCellLength is how many characters a result-grid cell shows before
// truncating, absent an Options override; a column dragged wider shows more.
// Load applies it to a zero MaxCellLength.
const DefaultMaxCellLength = 24

// DefaultMaxTextColumnLength is SSMS's default for the same cap in Results to
// Text. Without it one huge cell would pad every row to its width.
const DefaultMaxTextColumnLength = 256

// XEventViewSetting is one saved Extended Events viewer layout: the hidden
// columns, the filter expression, the grouping columns in nesting order and
// the aggregates. Columns are named as XEventHiddenColumns names them;
// aggregates as FUNC:column ("SUM:field:duration").
type XEventViewSetting struct {
	Hidden     []string `json:"hidden,omitempty"`
	Filter     string   `json:"filter,omitempty"`
	GroupBy    []string `json:"group_by,omitempty"`
	Aggregates []string `json:"aggregates,omitempty"`
}

// DefaultXEventStoreCapacity is the Extended Events viewer's event limit absent
// an Options override; it must agree with xevent.DefaultCapacity, which config
// must not import (TestXEventStoreCapacityComesFromOptions). The bounds stop a
// typo from making a viewer that holds nothing or exhausts memory.
const (
	DefaultXEventStoreCapacity = 100_000
	MinXEventStoreCapacity     = 1_000
	MaxXEventStoreCapacity     = 10_000_000
)

// ClampXEventStoreCapacity returns n, or the default when n is out of bounds.
func ClampXEventStoreCapacity(n int) int {
	if n < MinXEventStoreCapacity || n > MaxXEventStoreCapacity {
		return DefaultXEventStoreCapacity
	}
	return n
}

// DefaultIndentWidth is how many spaces the query editor indents by absent an
// Options override, and MaxIndentWidth the ceiling Load and the Options field
// clamp to. Both must agree with controls.DefaultIndentWidth / MaxIndentWidth,
// which config must not import; TestIndentWidthDefaultsAgree holds them together.
const (
	DefaultIndentWidth = 4
	MaxIndentWidth     = 16
)

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

// RecoveredDir returns the directory App.EmergencySave writes unsaved query
// text to after a panic on the UI goroutine, beside the config file. Creates it
// if missing, owner-only: query text can hold anything the user typed.
func RecoveredDir() (string, error) {
	dir := filepath.Join(filepath.Dir(configPath()), "recovered")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// MaxLogSize is how large gossms.log may grow before OpenLogFile starts a fresh
// one.
const MaxLogSize = 5 << 20

// OpenLogFile opens the log at LogFilePath for appending, first moving a file
// past MaxLogSize to gossms.log.1 (one generation). Rotation happens only at
// startup, so a session never loses its own lines.
//
// The file is 0600 like the config and key: the log records server names, logins
// and error text. OpenFile's mode applies only at create, so a log (or its .1)
// left 0644 by an older build is narrowed here. On Windows Chmod only toggles
// read-only, which the owner bits preserve.
func OpenLogFile() (*os.File, error) {
	path, err := LogFilePath()
	if err != nil {
		return nil, err
	}
	rotateLog(path, MaxLogSize)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err == nil && fi.Mode().Perm()&^0o600 != 0 {
		_ = f.Chmod(fi.Mode().Perm() & 0o600)
	}
	if fi, err := os.Stat(path + ".1"); err == nil && fi.Mode().Perm()&^0o600 != 0 {
		_ = os.Chmod(path+".1", fi.Mode().Perm()&0o600)
	}
	return f, nil
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
// are decrypted (see secret.go); if the key is unavailable or one password won't
// decrypt, that Password is "" and everything else loads.
//
// A file that exists but can't be read (permissions, EIO, EMFILE) comes back
// unreadable, and Save refuses to write it, so the next unrelated Save can't
// overwrite a good file with an empty config.
func Load() *Config {
	path := configPath()
	// A save killed mid-write leaves its temp file (a full config copy);
	// WriteAtomic's sweep only runs on a later save, which a read-only session
	// never makes.
	fileutil.RemoveStaleTemps(path)
	fileutil.RemoveStaleTemps(filepath.Join(filepath.Dir(path), keyFileName))
	data, err := os.ReadFile(path)
	if err != nil {
		cfg := defaultConfig()
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("config: %s exists but could not be read (%v); "+
				"starting with no saved settings and refusing to overwrite it", path, err)
			cfg.unreadable = err
		}
		cfg.base = cfg.settingsSnapshot()
		return cfg
	}
	cfg, err := parseConfig(path, data)
	if err != nil {
		// Not marked unreadable: Save re-reads the file and refuses while it
		// is still corrupt and uncopied, but saves once the user repairs it.
		log.Printf("config: %v; starting with no saved settings and not saving over it", err)
	}
	cfg.base = cfg.settingsSnapshot()

	key, err := loadOrCreateKey(filepath.Dir(path))
	if err != nil {
		log.Printf("config: saved passwords unavailable: %v", err)
		cfg.keepSealed()
		return cfg
	}
	cfg.openPasswords(key, path)
	return cfg
}

// keepSealed is openPasswords with no key: every ciphertext moves to sealed,
// so Save writes it back instead of encrypting "".
func (c *Config) keepSealed() {
	for i := range c.Connections {
		c.Connections[i].sealed = c.Connections[i].Password
		c.Connections[i].Password = ""
	}
}

// defaultConfig is the Config for a missing file.
func defaultConfig() *Config {
	cfg := new(Config) // Go 1.26: new(expr) — zero-value Config
	cfg.MaxCellLength = DefaultMaxCellLength
	cfg.MaxTextColumnLength = DefaultMaxTextColumnLength
	cfg.IndentWidth = DefaultIndentWidth
	cfg.XEventStoreCapacity = DefaultXEventStoreCapacity
	return cfg
}

// keepCorrupt is fileutil.KeepCorrupt; a variable so tests can make the copy
// fail.
var keepCorrupt = fileutil.KeepCorrupt

// parseConfig decodes config.json's bytes, with every setting clamped and
// passwords still sealed.
//
// Unparseable bytes (a hand-edit or interrupted write) are kept aside by
// fileutil.KeepCorrupt before the config starts empty: passwords are lost but
// server/user/database stay readable by hand. The error is non-nil only when
// that copy could not be made: the bytes then exist nowhere else, and the caller
// must not write over path.
func parseConfig(path string, data []byte) (*Config, error) {
	cfg := new(Config)
	var keepErr error
	if err := json.Unmarshal(data, cfg); err != nil {
		cfg = new(Config)
		if kept, kerr := keepCorrupt(path, data, time.Now()); kerr != nil {
			keepErr = fmt.Errorf("%s did not parse (%v) and could not be kept aside: %w", path, err, kerr)
		} else {
			log.Printf("config: %s did not parse (%v); kept as %s", path, err, kept)
		}
	}
	if cfg.MaxCellLength <= 0 {
		cfg.MaxCellLength = DefaultMaxCellLength
	}
	if cfg.MaxTextColumnLength <= 0 {
		cfg.MaxTextColumnLength = DefaultMaxTextColumnLength
	}
	if cfg.IndentWidth < 1 || cfg.IndentWidth > MaxIndentWidth {
		cfg.IndentWidth = DefaultIndentWidth
	}
	cfg.XEventStoreCapacity = ClampXEventStoreCapacity(cfg.XEventStoreCapacity)
	cfg.MaxFetchRows = max(cfg.MaxFetchRows, 0)
	return cfg, keepErr
}

// openPasswords decrypts every connection's password in place. One that won't
// open keeps its ciphertext in sealed, so Save writes it back unchanged. One
// that opens under an earlier binding sets legacySealed.
func (c *Config) openPasswords(key []byte, path string) {
	failed := 0
	for i := range c.Connections {
		stored := c.Connections[i].Password
		plain, ok := decryptPassword(key, c.Connections[i])
		switch {
		case !ok:
			c.Connections[i].sealed = stored
			failed++
		case stored != "" && !strings.HasPrefix(stored, aadPrefix):
			c.legacySealed = true
		}
		c.Connections[i].Password = plain
	}
	if failed > 0 {
		log.Printf("config: %d saved password(s) could not be decrypted and are "+
			"preserved as-is in %s; re-enter the password to replace one", failed, path)
	}
}

// settingsSnapshot copies c without its connections or bookkeeping, for base. A
// whole-struct copy, so a later-added setting is covered without being listed.
func (c *Config) settingsSnapshot() *Config {
	snap := *c
	snap.Connections, snap.ops, snap.base, snap.unreadable = nil, nil, nil, nil
	snap.legacySealed = false
	return &snap
}

// mergeSettings copies into dst every setting cur changed since base: every
// exported field but Connections, by reflection for the same reason
// settingsSnapshot copies whole.
func mergeSettings(cur, base, dst *Config) {
	if base == nil {
		base = new(Config)
	}
	cv, bv, dv := reflect.ValueOf(cur).Elem(), reflect.ValueOf(base).Elem(), reflect.ValueOf(dst).Elem()
	t := cv.Type()
	for i := range t.NumField() {
		if f := t.Field(i); !f.IsExported() || f.Name == "Connections" {
			continue
		}
		if !reflect.DeepEqual(cv.Field(i).Interface(), bv.Field(i).Interface()) {
			dv.Field(i).Set(cv.Field(i))
		}
	}
}

// MaxSavedConnections caps saved recent connections. Connect saves each
// successful connection automatically.
const MaxSavedConnections = 30

// AddOrUpdate saves a successful connection. Name is set to
// conn.GeneratedName(), the dedup key: a match is replaced, otherwise added. The
// entry moves to the end (most recent) and the list is trimmed from the front to
// MaxSavedConnections.
func (c *Config) AddOrUpdate(conn Connection) {
	conn.Name = conn.GeneratedName()
	// Remember Password off: the entry is saved without a password, and the
	// replaced entry's ciphertext goes with it.
	if !conn.RememberPassword {
		conn.Password = ""
	}
	c.Connections = addOrUpdate(c.Connections, conn)
	c.ops = append(c.ops, connOp{add: &conn})
}

// addOrUpdate is AddOrUpdate on a list, for Save to replay onto the file's.
func addOrUpdate(list []Connection, conn Connection) []Connection {
	list, _ = removeConnection(list, conn.Name)
	list = append(list, conn)
	if len(list) > MaxSavedConnections {
		list = list[len(list)-MaxSavedConnections:]
	}
	return list
}

// RemoveConnection deletes the saved connection named name (the GeneratedName
// key AddOrUpdate stores) and its sealed password, and reports whether one was
// found. An older build's entry may carry a different Name, so the generated
// name is matched too.
func (c *Config) RemoveConnection(name string) bool {
	var found bool
	c.Connections, found = removeConnection(c.Connections, name)
	if found {
		c.ops = append(c.ops, connOp{remove: name})
	}
	return found
}

// removeConnection is RemoveConnection on a list. Every match goes: older
// configs can hold distinct custom names that generate the same name, and
// stopping at the first left a duplicate in Recent Connections.
func removeConnection(list []Connection, name string) ([]Connection, bool) {
	n := len(list)
	list = slices.DeleteFunc(list, func(existing Connection) bool {
		return existing.Name == name || existing.GeneratedName() == name
	})
	return list, len(list) != n
}

// MatchByServer returns saved connections whose Server has the given
// case-insensitive prefix, most recent first. The Connect dialog's History pane
// passes an empty prefix, for the ordering.
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
