package config

import (
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
)

// address.go folds server addresses into keys. It lives in config, below db,
// so the tracked-query sets and the per-identity caches key by the same rule
// db's peer resolver does.

// DialPort is the gosmo.ConnectionOptions.Port a saved Port dials with: 1433
// is 0, unspecified. The driver defaults to 1433 anyway, and a port on
// "host\instance" suppresses the SQL Browser lookup for the instance's dynamic
// port (win10cli\sql2017 listens on 55253; a pinned 1433 reaches the default
// instance). A port written in Server wins over it, in gosmo.
func DialPort(port int) int {
	if port == 1433 {
		return 0
	}
	return port
}

// ResolveServer folds the dialog's Server and Port into one address string,
// for keys (ConnectionAddress) and the Connect dialog's display; dialling
// passes them to gosmo separately (db.toGosmoOptions). Server may already be
// any gosmo.ParseServerAddress form; a port it carries wins, as in gosmo.
//
// Port 0 or 1433 is omitted (DialPort). With "\instance", a non-default port
// is appended with a comma; gosmo reads a colon there as part of the instance
// name.
func ResolveServer(server string, dialogPort int) string {
	host, _, embeddedPort := gosmo.ParseServerAddress(server)
	if embeddedPort != 0 {
		return server
	}
	port := DialPort(dialogPort)
	if port == 0 {
		return server
	}
	// Comma for a bare IPv6 literal too: in "fe80::1:1500" the ":1500" is
	// another address group.
	sep := ":"
	if strings.ContainsRune(server, '\\') ||
		(strings.ContainsRune(host, ':') && !strings.HasPrefix(host, "[")) {
		sep = ","
	}
	return fmt.Sprintf("%s%s%d", server, sep, port)
}

// InstanceKey normalizes an instance name for keying peers, credentials and
// the tracked-query sets:
// lowercased host, then "\instance" for a named instance, or ",port" when
// there's no instance name and the port isn't 1433.
//
// A named instance is identified by name, so its port is dropped
// ("host\inst,1500" = "host\inst"). Without a name the port distinguishes
// instances on one host (win10cli vs "win10cli,55253" for SQL2017); a shared
// key would try the other instance's login, counting toward a CHECK_POLICY
// lockout. 1433 equals no port, as the driver dials it by default.
//
// The catalog reports names without ports, so a default instance saved as
// "host,1500" doesn't answer a peer read for "HOST"; Peer then falls back to
// the parent's settings.
func InstanceKey(server string) string {
	host, instance, port := gosmo.ParseServerAddress(strings.TrimSpace(server))
	key := strings.ToLower(host)
	switch {
	case instance != "":
		key += "\\" + strings.ToLower(instance)
	case port != 0 && port != 1433:
		key += "," + strconv.Itoa(port)
	}
	return key
}

// ConnectionAddress is the address a saved connection dials: Server with the
// dialog's Port folded in, as Connect does. Key a Connection by
// InstanceKey(ConnectionAddress(c)), never c.Server alone, which drops a
// Port-field port.
func ConnectionAddress(c Connection) string {
	return ResolveServer(c.Server, c.Port)
}

// IdentityKey identifies the instance and the identity c signs in as: its
// InstanceKey, then the GeneratedName identity and auth tag, NUL-separated
// (a login name or an address may hold a comma; neither can hold a NUL).
//
// Key anything that depends on who is looking by it — the IntelliSense caches
// (the catalog is filtered by metadata visibility) and the saved OE filters.
// User alone is empty for Windows, Entra Default/MSI and service principals,
// so two identities on one server would share one catalog, and disconnecting
// either would purge the other's. The database is left out; a per-database
// cache appends its own.
func (c Connection) IdentityKey() string {
	identity, tag := c.signInIdentity()
	return InstanceKey(ConnectionAddress(c)) + "\x00" + identity + "\x00" + tag
}
