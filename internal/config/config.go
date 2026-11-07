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
// (see db.toGosmoOptions); the dialog greys out the rest.
type AuthFields struct{ User, Password, Tenant, Client bool }

// authMethodInfo is an auth method's table row; db.toGosmoAuth holds the gosmo mapping.
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

// authInfo looks m up in authMethods. An unknown method (hand-edited or newer
// config.json) is labelled "Unknown" with SQL Server Authentication's fields
// and tag, for display only: db.toGosmoAuth refuses to dial it.
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

// IconStyle selects the Object Explorer icon glyph set; the zero value is the default.
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

// ColorTheme selects the UI palette; the zero value is the default.
type ColorTheme int

const (
	ThemeDark ColorTheme = iota
	ThemeLight
	// ThemeMCRetro keeps the original "Midnight Commander" theme's saved value.
	ThemeMCRetro
	ThemeMC
)

// ColorThemeName returns the Options dialog label for t.
func ColorThemeName(t ColorTheme) string {
	switch t {
	case ThemeLight:
		return "Light"
	case ThemeMC:
		return "mc"
	case ThemeMCRetro:
		return "mc - retro"
	}
	return "Dark"
}

// AllColorThemes returns the color themes in Options-dialog order.
func AllColorThemes() []ColorTheme {
	return []ColorTheme{ThemeDark, ThemeLight, ThemeMC, ThemeMCRetro}
}

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

	// Profile is the name the command line addresses this connection by
	// (`gossms profile test <name>`), set in the Connect dialog's Profile dialog; empty
	// for a plain recent connection. MaxAllow is the highest operation class
	// the command line may run through it, "" meaning AllowAdmin (see
	// Connection.Ceiling). Both survive a reconnect (addOrUpdate) and exempt the
	// entry from the MaxSavedConnections trim. Absent from connectionAAD:
	// naming a connection must not invalidate its sealed password.
	Profile  string     `json:"profile,omitempty"`
	MaxAllow AllowClass `json:"max_allow,omitempty"`

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

// MarshalJSON writes Encrypt as "encrypt_mode" plus the legacy boolean
// "encrypt" (true unless Optional), which a downgraded gossms decodes as a bool.
func (c Connection) MarshalJSON() ([]byte, error) {
	legacy := c.Encrypt != EncryptOptional && c.Encrypt != ""
	return json.Marshal(connectionWire{connectionFields: connectionFields(c), LegacyEncrypt: &legacy})
}

// UnmarshalJSON reads "encrypt_mode"; without it, the boolean "encrypt" maps
// true to Mandatory, false or absent to Optional.
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
// sign-in identity in the user slot, plus the auth method's tag
// ("srv,1433,db,app-id (Entra Service Principal)"). It is the Connect
// autocomplete label and the dedup key, so it must distinguish any two
// connections.
//
// The identity is User when the method reads one and it is set, else ClientID
// (older service principal entries keep the application id in User).
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
// (the ciphertext is kept). Password alone can't tell "none saved" from
// "unusable": Load blanks both.
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
	// MaxTextColumnLength is how many characters Results to Text shows per
	// column. Zero or negative means DefaultMaxTextColumnLength.
	MaxTextColumnLength int `json:"max_text_column_length"`
	// IntelliSenseDisabled turns off editor autocomplete. Inverted so the zero
	// value keeps it on.
	IntelliSenseDisabled bool `json:"intellisense_disabled"`
	// IndentWidth is the query editor's indent in spaces. Zero or outside
	// 1..MaxIndentWidth means DefaultIndentWidth.
	IndentWidth int `json:"indent_width"`
	// SQLFormatStyle is the format.Style ID for Edit > Format Document. Empty or
	// unknown means the first registered style (format.StyleByID owns the
	// fallback; config does not import format).
	SQLFormatStyle string `json:"sql_format_style,omitempty"`
	// XEventStoreCapacity is how many events an Extended Events viewer holds
	// before dropping the oldest. Outside Min..MaxXEventStoreCapacity means
	// DefaultXEventStoreCapacity.
	XEventStoreCapacity int `json:"xevent_store_capacity"`
	// MaxFetchRows is how many rows a query window keeps per result set in every
	// results mode. Reaching it cancels the rest of the script, later GO batches
	// included (query.WithMaxRows). Zero means unlimited; Load turns negatives
	// into zero.
	MaxFetchRows int `json:"max_fetch_rows"`
	// XEventHiddenColumns is, per event session, the viewer's hidden columns
	// ("name", "timestamp", "field:<name>", "action:<name>"). Hidden rather than
	// shown, so a field collected later still gets a column.
	XEventHiddenColumns map[string][]string `json:"xevent_hidden_columns,omitempty"`
	// XEventViewSettings are the viewer's saved display settings by name.
	XEventViewSettings map[string]XEventViewSetting `json:"xevent_view_settings,omitempty"`

	// unreadable is Load's error reading an existing config.json; it
	// write-protects this Config (see Save) to spare a likely-intact file.
	unreadable error

	// ops is this process's AddOrUpdate/RemoveConnection calls since the last
	// Save, replayed onto the file as it is now; writing the whole list would
	// delete what another instance saved.
	ops []connOp

	// base is the settings as last loaded or saved, nil for a Config not from
	// Load. Save writes a setting only when it differs from base, else keeps the
	// file's value (another instance may have changed it).
	base *Config

	// legacySealed: Load opened a password sealed under an earlier binding than
	// aadPrefix (see NeedsReseal); the next save clears it.
	legacySealed bool

	// readOnly is set by LoadReadOnly: Save refuses.
	readOnly bool
}

// connOp is one recorded change to Connections: an AddOrUpdate of add, a
// RemoveConnection of remove, or a SetProfile of profile.
type connOp struct {
	add     *Connection
	remove  string
	profile *profileOp
}

// DefaultMaxCellLength is how many characters a result-grid cell shows before
// truncating; Load applies it to a zero MaxCellLength.
const DefaultMaxCellLength = 24

// DefaultMaxTextColumnLength caps Results to Text columns, so one huge cell
// doesn't pad every row.
const DefaultMaxTextColumnLength = 256

// XEventViewSetting is one saved Extended Events viewer layout: hidden columns,
// filter, grouping columns in nesting order, and aggregates. Columns are named
// as in XEventHiddenColumns; aggregates as FUNC:column ("SUM:field:duration").
type XEventViewSetting struct {
	Hidden     []string `json:"hidden,omitempty"`
	Filter     string   `json:"filter,omitempty"`
	GroupBy    []string `json:"group_by,omitempty"`
	Aggregates []string `json:"aggregates,omitempty"`
}

// DefaultXEventStoreCapacity must agree with xevent.DefaultCapacity, which
// config must not import (TestXEventStoreCapacityComesFromOptions). The bounds
// stop a typo from holding nothing or exhausting memory.
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

// DefaultIndentWidth and MaxIndentWidth (the clamp ceiling) must agree with
// controls.DefaultIndentWidth / MaxIndentWidth, which config must not import;
// TestIndentWidthDefaultsAgree holds them together.
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

// LogFilePath returns the log file path, beside the config file. Creates the
// directory if missing.
func LogFilePath() (string, error) {
	dir := filepath.Dir(configPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "gossms.log"), nil
}

// RecoveredDir returns the directory App.EmergencySave writes unsaved query
// text to after a UI-goroutine panic. Created owner-only if missing.
func RecoveredDir() (string, error) {
	dir := filepath.Join(filepath.Dir(configPath()), "recovered")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// MaxLogSize is the size past which OpenLogFile starts a fresh log.
const MaxLogSize = 5 << 20

// OpenLogFile opens the log for appending, first moving a file past MaxLogSize
// to gossms.log.1 (one generation). Rotation happens only at startup, so a
// session never loses its own lines.
//
// The file is 0600 (it records server names, logins and errors). OpenFile's
// mode applies only at create, so a log (or its .1) left 0644 by an older
// build is narrowed here. On Windows Chmod only toggles read-only, which the
// owner bits preserve.
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
// are decrypted (see secret.go); if the key is unavailable or a password won't
// decrypt, that Password is "" and everything else loads.
//
// A file that exists but can't be read comes back unreadable, and Save refuses
// to write it, so a later Save can't overwrite a good file with an empty config.
func Load() *Config {
	path := configPath()
	// A save killed mid-write leaves a temp copy; WriteAtomic's sweep runs only
	// on a later save, which a read-only session never makes.
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
		// Not marked unreadable: Save re-reads the file, refusing while it is
		// corrupt and uncopied, saving once repaired.
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

// parseConfig decodes config.json's bytes, settings clamped, passwords still
// sealed.
//
// Unparseable bytes are kept aside by fileutil.KeepCorrupt before the config
// starts empty. The error is non-nil only when that copy failed: the bytes then
// exist nowhere else, and the caller must not write over path.
func parseConfig(path string, data []byte) (*Config, error) {
	cfg, err := decodeConfig(data)
	var keepErr error
	if err != nil {
		if kept, kerr := keepCorrupt(path, data, time.Now()); kerr != nil {
			keepErr = fmt.Errorf("%s did not parse (%v) and could not be kept aside: %w", path, err, kerr)
		} else {
			log.Printf("config: %s did not parse (%v); kept as %s", path, err, kept)
		}
	}
	return cfg, keepErr
}

// decodeConfig is parseConfig without the corrupt-file copy: bytes that do
// not parse give an empty, clamped Config and the error.
func decodeConfig(data []byte) (*Config, error) {
	cfg := new(Config)
	err := json.Unmarshal(data, cfg)
	if err != nil {
		cfg = new(Config)
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
	return cfg, err
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

// settingsSnapshot deep-copies c without its connections or bookkeeping, for
// base. Whole-struct, so new settings are covered unlisted; deep, because a
// map or slice shared with c would see in-place writes, compare equal in
// mergeSettings, and the setting would silently not be saved.
func (c *Config) settingsSnapshot() *Config {
	snap := *c
	snap.Connections, snap.ops, snap.base, snap.unreadable = nil, nil, nil, nil
	snap.legacySealed, snap.readOnly = false, false
	sv := reflect.ValueOf(&snap).Elem()
	for i := range sv.NumField() {
		if f := sv.Field(i); f.CanSet() {
			f.Set(deepCopy(f))
		}
	}
	return &snap
}

// deepCopy returns a copy of v sharing no map, slice or pointer with it,
// through exported struct fields (unexported ones are copied as they are).
func deepCopy(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		for it := v.MapRange(); it.Next(); {
			out.SetMapIndex(deepCopy(it.Key()), deepCopy(it.Value()))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			out.Index(i).Set(deepCopy(v.Index(i)))
		}
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(deepCopy(v.Elem()))
		return out
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(deepCopy(v.Elem()))
		return out
	case reflect.Struct, reflect.Array:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		if v.Kind() == reflect.Array {
			for i := range v.Len() {
				out.Index(i).Set(deepCopy(v.Index(i)))
			}
			return out
		}
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				out.Field(i).Set(deepCopy(v.Field(i)))
			}
		}
		return out
	}
	return v
}

// mergeSettings copies into dst every setting cur changed since base (all
// exported fields but Connections), by reflection like settingsSnapshot.
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

// MaxSavedConnections caps saved recent connections. Named ones (Profile set)
// are not counted or trimmed.
const MaxSavedConnections = 30

// AddOrUpdate saves a successful connection. Name is set to
// conn.GeneratedName(), the dedup key: a match is replaced, and its Profile and
// MaxAllow carry over. The entry moves to the end (most recent) and the oldest
// unnamed entries are trimmed to MaxSavedConnections.
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
// The carry-over happens here so a replayed op takes the profile the file holds
// then, which another instance may have changed.
func addOrUpdate(list []Connection, conn Connection) []Connection {
	if conn.Profile == "" {
		if i := slices.IndexFunc(list, func(e Connection) bool {
			return e.Profile != "" && (e.Name == conn.Name || e.GeneratedName() == conn.Name)
		}); i >= 0 {
			conn.Profile, conn.MaxAllow = list[i].Profile, list[i].MaxAllow
		}
	}
	list, _ = removeConnection(list, conn.Name)
	list = append(list, conn)
	return trimUnnamed(list)
}

// trimUnnamed drops the oldest unnamed entries past MaxSavedConnections;
// named ones stay wherever they are.
func trimUnnamed(list []Connection) []Connection {
	excess := -MaxSavedConnections
	for _, e := range list {
		if e.Profile == "" {
			excess++
		}
	}
	if excess <= 0 {
		return list
	}
	return slices.DeleteFunc(list, func(e Connection) bool {
		if e.Profile != "" || excess == 0 {
			return false
		}
		excess--
		return true
	})
}

// RemoveConnection deletes the saved connection named name (and its sealed
// password), reporting whether one was found. The generated name is matched too,
// as older builds stored a different Name.
func (c *Config) RemoveConnection(name string) bool {
	var found bool
	c.Connections, found = removeConnection(c.Connections, name)
	if found {
		c.ops = append(c.ops, connOp{remove: name})
	}
	return found
}

// removeConnection is RemoveConnection on a list. Every match goes: older
// configs can hold distinct names generating the same name.
func removeConnection(list []Connection, name string) ([]Connection, bool) {
	n := len(list)
	list = slices.DeleteFunc(list, func(existing Connection) bool {
		return existing.Name == name || existing.GeneratedName() == name
	})
	return list, len(list) != n
}

// MatchByServer returns saved connections whose Server has the given
// case-insensitive prefix, most recent first (empty prefix: all).
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
