package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Profiles
//
// A profile is a saved connection the user has named in the Connect dialog's
// Profile dialog. The command line addresses it by that name (`gossms
// profile test dev`) and reads its MaxAllow ceiling; the TUI shows the name in History.
// Naming is the only way a profile is made: there is no separate list.

// AllowClass is an operation class, as the command line's safety model spells
// it: each one permits everything below it.
type AllowClass string

const (
	AllowRead  AllowClass = "read"
	AllowWrite AllowClass = "write"
	AllowDDL   AllowClass = "ddl"
	AllowAdmin AllowClass = "admin"
)

// AllAllowClasses returns the classes lowest first, the Profile dialog's order.
func AllAllowClasses() []AllowClass {
	return []AllowClass{AllowRead, AllowWrite, AllowDDL, AllowAdmin}
}

// AllowClassLabel is the Profile dialog's label for a.
func AllowClassLabel(a AllowClass) string {
	switch a {
	case AllowRead:
		return "Read (queries, metadata)"
	case AllowWrite:
		return "Write (+ INSERT/UPDATE/DELETE)"
	case AllowDDL:
		return "DDL (+ CREATE/ALTER/DROP)"
	case AllowAdmin:
		return "Admin (everything)"
	default:
		return string(a)
	}
}

// Ceiling is the highest class the command line may run through c: MaxAllow,
// with "" meaning AllowAdmin (no ceiling beyond --allow). A value this build
// does not know (a hand-edited config.json, or a newer gossms's) is AllowRead:
// a ceiling that cannot be read must not lift.
func (c Connection) Ceiling() AllowClass {
	switch c.MaxAllow {
	case "":
		return AllowAdmin
	case AllowRead, AllowWrite, AllowDDL, AllowAdmin:
		return c.MaxAllow
	default:
		return AllowRead
	}
}

// MaxProfileNameLength bounds a profile name; it is typed on command lines.
const MaxProfileNameLength = 64

// ValidateProfileName reports why name cannot be a profile name: letters,
// digits, '-', '_' and '.', starting with a letter or digit — a leading '-'
// would read as an option on the command line — and at most
// MaxProfileNameLength long. The empty name is valid: it clears the profile.
func ValidateProfileName(name string) error {
	if name == "" {
		return nil
	}
	if len(name) > MaxProfileNameLength {
		return fmt.Errorf("a profile name is at most %d characters", MaxProfileNameLength)
	}
	for i, r := range name {
		alnum := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if i == 0 && !alnum {
			return errors.New("a profile name starts with a letter or digit")
		}
		if !alnum && r != '-' && r != '_' && r != '.' {
			return fmt.Errorf("a profile name holds only letters, digits, '-', '_' and '.', not %q", r)
		}
	}
	return nil
}

// ErrProfileNotFound is ProfileByName's answer for a name nothing matches.
var ErrProfileNotFound = errors.New("no saved connection by that name")

// profileOp is one recorded SetProfile: the entry's dedup key, and what it is
// named now.
type profileOp struct {
	key      string
	name     string
	maxAllow AllowClass
}

// keyMatches reports whether e is the entry key names, as removeConnection
// matches.
func keyMatches(e Connection, key string) bool {
	return e.Name == key || e.GeneratedName() == key
}

// applyTo replays the op on a list. An entry another instance removed since
// is gone, so there is nothing to name. A name another instance gave a
// different entry meanwhile moves to this one: names stay unique, and the
// later save is the one the user just made.
func (op *profileOp) applyTo(list []Connection) []Connection {
	i := slices.IndexFunc(list, func(e Connection) bool { return keyMatches(e, op.key) })
	if i < 0 {
		return list
	}
	if op.name != "" {
		for j := range list {
			if j != i && strings.EqualFold(list[j].Profile, op.name) {
				list[j].Profile, list[j].MaxAllow = "", ""
			}
		}
	}
	list[i].Profile, list[i].MaxAllow = op.name, op.maxAllow
	if op.name == "" {
		list[i].MaxAllow = ""
	}
	return list
}

// SetProfile names the saved connection whose dedup key is key (its
// GeneratedName, as RemoveConnection takes) and sets its ceiling. An empty name
// turns it back into a plain recent connection, clearing the ceiling too. The
// name must pass ValidateProfileName and be unique, ignoring case, among the
// other saved connections.
func (c *Config) SetProfile(key, name string, maxAllow AllowClass) error {
	if err := ValidateProfileName(name); err != nil {
		return err
	}
	if name != "" && !slices.Contains(AllAllowClasses(), maxAllow) {
		return fmt.Errorf("unknown operation class %q", maxAllow)
	}
	i := slices.IndexFunc(c.Connections, func(e Connection) bool { return keyMatches(e, key) })
	if i < 0 {
		return fmt.Errorf("%s: %w", key, ErrProfileNotFound)
	}
	if name != "" {
		for j, e := range c.Connections {
			if j != i && strings.EqualFold(e.Profile, name) {
				return fmt.Errorf("the profile name %q is already used by %s", e.Profile, e.GeneratedName())
			}
		}
	}
	op := &profileOp{key: key, name: name, maxAllow: maxAllow}
	c.Connections = op.applyTo(c.Connections)
	c.ops = append(c.ops, connOp{profile: op})
	return nil
}

// Profiles returns the named connections, sorted by name ignoring case.
func (c *Config) Profiles() []Connection {
	var out []Connection
	for _, e := range c.Connections {
		if e.Profile != "" {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b Connection) int {
		return strings.Compare(strings.ToLower(a.Profile), strings.ToLower(b.Profile))
	})
	return out
}

// ProfileByName finds the saved connection the command line's name means: the
// profile of that name, ignoring case, else the connection whose generated
// name (`srv,1433,db,user`) it is, so a connection can be used before it is
// named. A name matching nothing wraps ErrProfileNotFound.
//
// A hand-edited config.json can hold one profile name twice; the most recently
// saved entry wins, as History lists it first.
func (c *Config) ProfileByName(name string) (Connection, error) {
	for _, e := range slices.Backward(c.Connections) {
		if e.Profile != "" && strings.EqualFold(e.Profile, name) {
			return e, nil
		}
	}
	for _, e := range slices.Backward(c.Connections) {
		if keyMatches(e, name) {
			return e, nil
		}
	}
	return Connection{}, fmt.Errorf("%s: %w", name, ErrProfileNotFound)
}

// LoadReadOnly reads the config for the command line, which may run beside a
// TUI on the same files and must not disturb it (docs/cli.md § Profiles):
//
//   - Nothing is written: no key file is created, no stale temp file swept, a
//     corrupt config.json is not copied aside, and Save on the result refuses.
//   - Passwords open with an existing key only. No key, or one that cannot be
//     read, leaves every password sealed (Connection.PasswordUnreadable).
//
// A config.json that is missing is an empty config; one that exists but
// cannot be read or parsed is an error, since an empty answer would read as
// "no profiles".
func LoadReadOnly() (*Config, error) {
	path := configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		cfg := defaultConfig()
		cfg.readOnly = true
		if !errors.Is(err, fs.ErrNotExist) {
			return cfg, fmt.Errorf("config: %s could not be read: %w", path, err)
		}
		return cfg, nil
	}
	cfg, err := decodeConfig(data)
	cfg.readOnly = true
	if err != nil {
		return cfg, fmt.Errorf("config: %s did not parse: %w", path, err)
	}
	key, err := readKey(filepath.Join(filepath.Dir(path), keyFileName))
	if err != nil {
		cfg.keepSealed()
		return cfg, nil
	}
	cfg.openPasswords(key, path)
	return cfg, nil
}

// ConfigDir is the directory config.json lives in, for messages naming it.
func ConfigDir() string { return filepath.Dir(configPath()) }
