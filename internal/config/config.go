// Package config holds persistent application state (saved connections, settings).
package config

import (
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

// AuthMethod is gossms's own authentication-method enum, for the UI dropdown
// and JSON serialisation. Its numeric values are independent of
// gosmo.AuthMethod; internal/db/connection.go maps between the two.
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

// IconStyle selects the glyph set the Object Explorer tree uses for its node
// icons. Its zero value, IconStyleEmoji, is the default, so a config.json
// missing the field still loads as Emoji.
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
// Password is always plaintext in memory: Load and Save handle AES-256-GCM
// encryption at the JSON boundary (see secret.go), so nothing else in the app
// needs to know encryption is involved.
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

	// sealed is the on-disk ciphertext Load could not open for this entry — a
	// replaced key file, a hand-edited server/user (which the AAD binds to, see
	// secret.go), a truncated write. Save writes it back verbatim instead of
	// re-encrypting the "" Password came back as, so a failed decrypt stays
	// recoverable rather than being overwritten by the next unrelated write.
	//
	// Unexported, so encoding/json neither reads nor writes it: it lives only
	// between one Load and the Saves that follow. An entry the user reconnects
	// with goes through AddOrUpdate as a fresh Connection with sealed empty, so
	// re-entering the password replaces the unreadable ciphertext for good.
	sealed string
}

// ConnectionName builds the identifier auto-generated for every saved
// connection: "server,port,database,user". It is both the label in the Connect
// dialog's autocomplete list and the dedup key in AddOrUpdate.
//
// It doesn't fold in AuthMethod, so Windows Auth and Entra Default to the same
// server/port/database — both with an empty User — generate the same name and
// overwrite each other in the saved list.
//
// An unspecified port (0) is spelled as the default 1433 it dials, so an entry
// saved before the Connect dialog stopped pre-filling "1433" still dedups
// against the same server connected to today rather than doubling in the list.
func ConnectionName(server string, port int, database, user string) string {
	if port == 0 {
		port = 1433
	}
	return server + "," + strconv.Itoa(port) + "," + database + "," + user
}

// PasswordUnreadable reports whether this entry had a stored password Load could
// not decrypt: the sealed ciphertext is still held and written back untouched,
// but the password is unavailable this session.
//
// It separates "no password saved" from "a password is saved and unusable",
// which Password alone cannot express since Load blanks both — connecting with
// the "" is a login failure, not an attempt worth making.
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
	// IntelliSenseDisabled turns off the SQL editor's autocomplete. Stored
	// inverted so Go's bool zero value keeps the feature on by default, for a
	// fresh install and for a config.json missing the field.
	IntelliSenseDisabled bool `json:"intellisense_disabled"`

	// unreadable is the error Load hit reading an existing config.json, if any.
	// It makes this Config write-protected (see Save): everything the file held
	// is missing from it, so writing it back would destroy a file that is very
	// likely still intact. Unexported, so it neither serialises nor survives a
	// copy into a new Config.
	unreadable error
}

// DefaultMaxCellLength is how many characters a result-grid cell displays before
// truncating, absent an Options dialog override; a column dragged wider by its
// separator shows more. Load applies it to a zero MaxCellLength, so every reader
// of *Config sees a usable value.
const DefaultMaxCellLength = 24

// configPath returns the path to the config file.
func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "gossms", "config.json")
}

// LogFilePath returns the path cmd/gossms's main should open its log file at —
// next to the config file, not the working directory, so where gossms is
// launched from doesn't decide where its log ends up. The directory is created
// if missing, matching Config.Save's MkdirAll.
func LogFilePath() (string, error) {
	dir := filepath.Dir(configPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "gossms.log"), nil
}

// Load reads the config from disk, returning an empty config if there isn't one
// yet. Saved passwords are decrypted back to plaintext (see secret.go); if the
// key can't be read or created, or one password doesn't decrypt, that
// connection's Password comes back "" and every other field still loads.
//
// A config file that exists but can't be read (a permission change, EIO, too
// many open files) is not the same as not having one: returning an empty config
// for both brings the app up with no saved connections, and the next Save of
// some unrelated setting writes that emptiness over a file that was still good.
// Such a config comes back unreadable instead, and Save refuses to touch it.
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
		// The file exists but doesn't parse — a hand-edit, or an interrupted
		// write. Falling back to an empty config discards every saved
		// connection, so keep the bytes under a .corrupt name first: a password
		// can't be recovered by hand, but the server/user/database fields are
		// readable. Best-effort, and written atomically — the sidecar is the
		// only remaining copy, so a partial one is the loss it exists to
		// prevent.
		_ = fileutil.WriteAtomic(path+".corrupt", data, 0o600)
		cfg = new(Config)
	}
	if cfg.MaxCellLength <= 0 {
		cfg.MaxCellLength = DefaultMaxCellLength
	}

	key, err := loadOrCreateKey(filepath.Dir(path))
	if err != nil {
		// No key means nothing can be opened. Every ciphertext is stashed in
		// sealed so a later Save writes it back rather than replacing it with an
		// encryption of "".
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
// base64-encoded (see secret.go) in the on-disk copy only; c itself keeps its
// plaintext passwords.
//
// An entry whose stored password Load could not open keeps its original
// ciphertext (Connection.sealed) rather than being re-encrypted from the "" Load
// handed back — otherwise saving any unrelated setting destroys the only
// recoverable copy of passwords a restored key file could still open.
func (c *Config) Save() error {
	path := configPath()
	if c.unreadable != nil {
		// Load never saw this file's contents, so c is missing everything it
		// held. Writing c out would replace a readable-again config with that
		// emptiness.
		return fmt.Errorf("config: not saving over %s — it could not be read at startup: %w", path, c.unreadable)
	}
	dir := filepath.Dir(path)
	// 0700, matching loadOrCreateKey's MkdirAll (secret.go). Save runs before
	// that call and MkdirAll never chmods an existing directory, so whichever
	// runs first decides the real permissions — both must ask for the owner-only
	// posture the key's directory is documented to have.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	key, err := loadOrCreateKey(dir)
	if err != nil {
		return err
	}
	// Copy c wholesale, then replace Connections with an encrypted copy: listing
	// the carried-over fields one by one would silently drop any field added to
	// Config later.
	onDisk := *c
	onDisk.Connections = make([]Connection, len(c.Connections))
	for i, conn := range c.Connections {
		if conn.Password == "" && conn.sealed != "" {
			// Load couldn't open this one. Write the bytes back as found rather
			// than sealing the "" standing in for them in memory.
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

// MaxSavedConnections caps how many recent connections Config keeps. The
// Connect dialog persists a successful connection here automatically.
const MaxSavedConnections = 30

// AddOrUpdate saves a successful connection. Its Name is overwritten with the
// auto-generated ConnectionName(Server, Port, Database, User), which doubles as
// the dedup key: an entry with the same generated name is replaced in place,
// otherwise a new one is added. Either way the entry moves to the end of
// Connections as most recently used, and the list is trimmed to
// MaxSavedConnections from the front.
//
// conn is taken by value, so the caller's copy is never mutated — only the
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

// MatchByServer returns saved connections whose Server starts with the given
// (case-insensitive) prefix, most-recently-used first — the data source for the
// Connect dialog's autocomplete list.
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
