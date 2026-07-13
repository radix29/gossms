// Package config holds persistent application state (saved connections, settings).
package config

import (
	"encoding/json"
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
		cfg = new(Config)
	}
	if cfg.MaxCellLength <= 0 {
		cfg.MaxCellLength = DefaultMaxCellLength
	}

	key, err := loadOrCreateKey(filepath.Dir(path))
	if err != nil {
		for i := range cfg.Connections {
			cfg.Connections[i].Password = ""
		}
		return cfg
	}
	for i := range cfg.Connections {
		cfg.Connections[i].Password = decryptPassword(key, cfg.Connections[i].Password)
	}
	return cfg
}

// Save writes the config to disk. Passwords are AES-256-GCM encrypted and
// base64-encoded (see secret.go) in the on-disk copy only — c itself (the
// live in-memory config the rest of the app reads from) is left with
// plaintext passwords untouched.
func (c *Config) Save() error {
	path := configPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	key, err := loadOrCreateKey(dir)
	if err != nil {
		return err
	}
	onDisk := Config{
		Connections:   make([]Connection, len(c.Connections)),
		IconStyle:     c.IconStyle,
		MaxCellLength: c.MaxCellLength,
	}
	for i, conn := range c.Connections {
		enc, err := encryptPassword(key, conn.Password)
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
	return os.WriteFile(path, data, 0600)
}

// MaxSavedConnections caps how many recent connections Config keeps. The
// Connect dialog persists a successful connection here automatically.
const MaxSavedConnections = 15

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
