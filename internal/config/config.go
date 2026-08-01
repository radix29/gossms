// Package config holds persistent application state (saved connections, settings).
package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// AuthMethod is gossms's own authentication-method enum, used for the UI
// dropdown and JSON config serialisation. Its numeric values are independent
// of gosmo.AuthMethod; internal/db/connection.go maps between the two.
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

// AuthMethodName returns a human-readable label for the auth method.
func AuthMethodName(m AuthMethod) string {
	switch m {
	case AuthSQLServer:
		return "SQL Server Authentication"
	case AuthWindows:
		return "Windows Authentication"
	case AuthEntraDefault:
		return "Azure Entra - Default"
	case AuthEntraPassword:
		return "Azure Entra - Password"
	case AuthEntraMSI:
		return "Azure Entra - Managed Identity"
	case AuthEntraServicePrincipal:
		return "Azure Entra - Service Principal"
	case AuthEntraInteractive:
		return "Azure Entra - Interactive"
	case AuthEntraDeviceCode:
		return "Azure Entra - Device Code"
	case AuthEntraAzCLI:
		return "Azure Entra - Azure CLI"
	default:
		return "Unknown"
	}
}

// AllAuthMethods returns all available auth methods for display.
func AllAuthMethods() []AuthMethod {
	return []AuthMethod{
		AuthSQLServer,
		AuthWindows,
		AuthEntraDefault,
		AuthEntraPassword,
		AuthEntraMSI,
		AuthEntraServicePrincipal,
		AuthEntraInteractive,
		AuthEntraDeviceCode,
		AuthEntraAzCLI,
	}
}

// IconStyle selects the glyph set the Object Explorer tree uses for its
// node icons. Its zero value, IconStyleEmoji, is the default so a
// config.json written before this option existed (or missing the field)
// still loads as Emoji.
type IconStyle int

const (
	IconStyleEmoji IconStyle = iota
	IconStyleSymbols
	IconStylePortable
	IconStyleNone
)

// IconStyleName returns a human-readable label for the icon style, used by
// the Options dialog's radio box.
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

// AllIconStyles returns all available icon styles, in the order the Options
// dialog lists them.
func AllIconStyles() []IconStyle {
	return []IconStyle{IconStyleEmoji, IconStyleSymbols, IconStylePortable, IconStyleNone}
}

// Connection stores one saved server connection.
//
// Password is always plaintext here in memory — Load/Save (below) handle
// AES-256-GCM encryption transparently at the JSON boundary (see
// secret.go), so every other part of the app (Connect dialog, autofill,
// BuildConnectionString...) never needs to know encryption is involved.
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
	Encrypt                bool       `json:"encrypt"`
	ExtraProperties        string     `json:"extra_properties"`

	// sealed is the on-disk ciphertext Load could not open for this entry —
	// a replaced key file, a hand-edited server/user (which the AAD binds
	// to, see secret.go), a truncated write. Save writes it back verbatim in
	// place of re-encrypting the "" that Password came back as, so a failed
	// decrypt stays recoverable instead of being overwritten by the next
	// unrelated config write.
	//
	// Unexported, so encoding/json neither reads nor writes it: it exists
	// only between one Load and the Saves that follow it. An entry the user
	// actually reconnects with goes through AddOrUpdate as a fresh
	// Connection value with sealed empty, so re-entering the password by
	// hand replaces the unreadable ciphertext for good.
	sealed string
}

// ConnectionName builds the identifier auto-generated for every saved
// connection: "server,port,database,user". It's used both as the label
// shown in the Connect dialog's server-field autocomplete list and as the
// dedup/lookup key in AddOrUpdate.
//
// It doesn't fold in AuthMethod, so e.g. Windows Auth and Entra Default to
// the same server/port/database (both typically with an empty User)
// generate the same name and will overwrite each other in the saved list.
func ConnectionName(server string, port int, database, user string) string {
	return server + "," + strconv.Itoa(port) + "," + database + "," + user
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
	// IntelliSenseDisabled turns off the SQL editor's autocomplete. Stored
	// inverted (rather than an "IntelliSenseEnabled" flag) so Go's bool
	// zero value keeps the feature on by default for both a fresh install
	// and a config.json written before this option existed.
	IntelliSenseDisabled bool `json:"intellisense_disabled"`
}

// DefaultMaxCellLength is how many characters a result-grid cell displays
// before truncating to an ellipsis, absent an Options dialog override —
// Load applies it to a zero (unset, or predating this field) MaxCellLength
// so every other reader of *Config always sees a usable value.
const DefaultMaxCellLength = 24

// configPath returns the path to the config file.
func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "gossms", "config.json")
}

// LogFilePath returns the path cmd/gossms's main should open its log file
// at — next to the config file, not the process's current working
// directory, so where gossms is launched from doesn't determine where (or
// whether writably) its log ends up. The directory is created if it
// doesn't exist yet, matching Config.Save's own MkdirAll.
func LogFilePath() (string, error) {
	dir := filepath.Dir(configPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "gossms.log"), nil
}

// Load reads the config from disk, returning an empty config on error.
// Saved passwords are decrypted back to plaintext in the returned Config
// (see secret.go) — if the key can't be read/created, or a given password
// doesn't decrypt cleanly, that connection's Password comes back ""; every
// other field still loads fine.
func Load() *Config {
	path := configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		cfg := new(Config) // Go 1.26: new(expr) — zero-value Config
		cfg.MaxCellLength = DefaultMaxCellLength
		return cfg
	}
	cfg := new(Config)
	if err := json.Unmarshal(data, cfg); err != nil {
		// The file exists but doesn't parse — a hand-edit, or a write
		// interrupted partway. Falling back to an empty config discards
		// every saved connection, so keep the bytes under a .corrupt name
		// first: recovering a password by hand is impossible, but the
		// server/user/database fields are readable. Best-effort — nothing
		// here should stop the app from starting.
		_ = os.WriteFile(path+".corrupt", data, 0o600)
		cfg = new(Config)
	}
	if cfg.MaxCellLength <= 0 {
		cfg.MaxCellLength = DefaultMaxCellLength
	}

	key, err := loadOrCreateKey(filepath.Dir(path))
	if err != nil {
		// No key means nothing can be opened. Every ciphertext is stashed in
		// sealed so a later Save (of some unrelated setting) writes it back
		// rather than replacing it with an encryption of "".
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

// Save writes the config to disk. Passwords are AES-256-GCM encrypted and
// base64-encoded (see secret.go) in the on-disk copy only — c itself (the
// live in-memory config the rest of the app reads from) is left with
// plaintext passwords untouched.
//
// An entry whose stored password Load could not open keeps its original
// ciphertext (Connection.sealed) rather than being re-encrypted from the ""
// that Load handed back. Without that, saving any unrelated setting
// destroyed the only recoverable copy of every password a restored key file
// or an undone hand-edit could still have opened.
func (c *Config) Save() error {
	path := configPath()
	dir := filepath.Dir(path)
	// 0700, matching loadOrCreateKey's own MkdirAll (secret.go). Save runs
	// before that call and MkdirAll never chmods an already-existing
	// directory, so whichever runs first decides the directory's real
	// permissions — both must ask for the owner-only posture the
	// encryption key's directory is documented to have.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	key, err := loadOrCreateKey(dir)
	if err != nil {
		return err
	}
	// Copy c wholesale, then replace Connections with an encrypted copy of
	// its own — listing the carried-over fields one by one instead would
	// silently drop any field added to Config later.
	onDisk := *c
	onDisk.Connections = make([]Connection, len(c.Connections))
	for i, conn := range c.Connections {
		if conn.Password == "" && conn.sealed != "" {
			// Load couldn't open this one. Write the bytes back exactly as
			// they were found rather than sealing the "" that stands in for
			// them in memory.
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
	return writeFileAtomic(path, data)
}

// writeFileAtomic writes data to path by way of a temp file in the same
// directory plus a rename, so path is only ever replaced whole. A plain
// os.WriteFile truncates in place: a crash, a full disk, or a power loss
// partway through leaves invalid JSON behind, which Load then discards
// entirely (see there) — silently taking every saved connection with it.
// The temp file has to share path's directory for the rename to be atomic,
// since a rename across filesystems isn't.
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename below has succeeded

	// CreateTemp makes the file 0600 already; set it explicitly so the
	// permissions don't depend on that staying true.
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	// Flush to disk before the rename, so a crash right after it can't
	// leave the new name pointing at an empty or partial file.
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// MaxSavedConnections caps how many recent connections Config keeps. The
// Connect dialog persists a successful connection here automatically.
const MaxSavedConnections = 30

// AddOrUpdate saves a successful connection. Its Name is overwritten with
// the auto-generated ConnectionName(Server, Port, Database, User), which
// also doubles as the dedup key: an existing entry with the same generated
// name is replaced in place, otherwise a new one is added. Either way the
// saved entry becomes the most recently used — AddOrUpdate moves it to
// the end of Connections — and the list is trimmed to
// MaxSavedConnections by evicting the oldest (front) entries first.
//
// conn is taken by value, so this never mutates the caller's copy (e.g.
// the live options App.connectServer just used to connect) — only the
// stored copy gets the generated Name.
func (c *Config) AddOrUpdate(conn Connection) {
	conn.Name = ConnectionName(conn.Server, conn.Port, conn.Database, conn.User)
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

// MatchByServer returns saved connections whose Server starts with the
// given (case-insensitive) prefix, most-recently-used first. It's the
// data source for the Connect dialog's server-field autocomplete list.
func (c *Config) MatchByServer(prefix string) []Connection {
	prefix = strings.ToLower(prefix)
	var out []Connection
	for i := len(c.Connections) - 1; i >= 0; i-- {
		conn := c.Connections[i]
		if strings.HasPrefix(strings.ToLower(conn.Server), prefix) {
			out = append(out, conn)
		}
	}
	return out
}
