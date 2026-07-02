// Package config holds persistent application state (saved connections, settings).
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// AuthMethod is gossms's own authentication-method enum, used for the UI
// dropdown and JSON config serialisation. It is intentionally NOT required
// to share numeric values with gosmo.AuthMethod — internal/db/connection.go
// translates between the two via an explicit mapping (toGosmoAuth), so this
// enum's values can be added to or reordered freely without silently
// selecting the wrong auth strategy at connect time.
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

// Connection stores one saved server connection.
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
	Connections []Connection `json:"connections"`
}

// configPath returns the path to the config file.
func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "gossms", "config.json")
}

// Load reads the config from disk, returning an empty config on error.
func Load() *Config {
	path := configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return new(Config) // Go 1.26: new(expr) — zero-value Config
	}
	cfg := new(Config)
	if err := json.Unmarshal(data, cfg); err != nil {
		return new(Config)
	}
	return cfg
}

// Save writes the config to disk.
func (c *Config) Save() error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// AddOrUpdate adds a connection or updates an existing one by name.
func (c *Config) AddOrUpdate(conn Connection) {
	for i, existing := range c.Connections {
		if existing.Name == conn.Name {
			c.Connections[i] = conn
			return
		}
	}
	c.Connections = append(c.Connections, conn)
}
