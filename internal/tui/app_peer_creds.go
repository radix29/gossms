package tui

import (
	"strings"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// app_peer_creds.go holds App's answer to db.PeerCredentials: which saved
// connection to reach a given instance with.
//
// Always On is the reason it matters. Everything the Object Explorer, the AG
// dialogs and the endpoint wizard read off a second instance goes through
// db.ServerConn.Peer, which without this reaches every one of them with the
// login the user happened to register the tree with. A topology whose replicas
// want different credentials — or listen on a different port — surfaced as a
// connect error naming the instance, and on the follow-the-primary path as a
// silent "(partial — primary X unreachable)".
//
// The answer is the connections the user has already made: connect to a
// replica once through File > Connect and every later peer read reaches it the
// same way.

// peerCredentialsFor resolves an instance name to its own saved connection.
// This is the db.PeerCredentials installed on every connection App opens.
//
// Read from background loader goroutines — the Object Explorer's Always On
// loader is the main caller, through Peer — so the map is behind peerCredMu.
func (a *App) peerCredentialsFor(server string) (config.Connection, bool) {
	key := db.InstanceKey(server)
	a.peerCredMu.Lock()
	defer a.peerCredMu.Unlock()
	if c, ok := a.peerCreds[key]; ok {
		return c, true
	}
	c, ok := a.peerCredAliases[key]
	return c, ok
}

// rememberPeerCredentials records conn as the way to reach its instance,
// replacing whatever was held for it. Called for each saved connection at
// startup and again on every successful connect, so the most recent way the
// user reached an instance is the one a peer read uses.
func (a *App) rememberPeerCredentials(conn config.Connection) {
	if conn.Server == "" {
		return
	}
	key := db.InstanceKey(conn.Server)
	a.peerCredMu.Lock()
	defer a.peerCredMu.Unlock()
	if a.peerCreds == nil {
		a.peerCreds = map[string]config.Connection{}
	}
	a.peerCreds[key] = conn
	if alias := shortHostKey(key); alias != "" {
		if a.peerCredAliases == nil {
			a.peerCredAliases = map[string]config.Connection{}
		}
		a.peerCredAliases[alias] = conn
	}
}

// shortHostKey is key with the host's domain suffix dropped, or "" when the
// host has none.
//
// It exists because the two names for an instance rarely agree: the catalog
// reports @@SERVERNAME, which is the short machine name, while what the user
// types into Connect on a domain network is usually the FQDN. Keyed only by
// the exact host, a saved "ubusql2.fritz.box" would never answer a peer read
// for the "ubusql2" sys.availability_replicas reports, which is most of the
// cases this whole resolver exists for.
//
// Deliberately a separate, lower-priority tier rather than a collapsed key:
// two instances really can be "sql.a.example" and "sql.b.example", and folding
// them onto one key would hand one of them the other's login. As a fallback
// consulted only when the exact host misses, the worst case is the connect
// error that was the behaviour before any of this.
func shortHostKey(key string) string {
	host, instance, hasInstance := strings.Cut(key, "\\")
	short, _, dotted := strings.Cut(host, ".")
	if !dotted || short == "" {
		return ""
	}
	if hasInstance {
		return short + "\\" + instance
	}
	return short
}

// loadPeerCredentials seeds the map from the saved connections, in the order
// they are stored — oldest first, so the most recently used entry for an
// instance is the one left in the map. Same precedence config.MatchByServer
// offers the Connect dialog.
//
// An entry whose password could not be decrypted is not seeded. Unlike a
// connection the user just made, nothing on disk has been proven to work, and
// preferring one that is certain to fail over the parent connection's own
// credentials makes an instance that was reachable unreachable. A replaced
// config key blanks every saved password at once (config.Load), so this is a
// whole-file state rather than a rare entry.
//
// Nothing else is judged here — an entry that gets past this and still fails
// is Peer's fallback to deal with, which is the general answer and does not
// need this one to be exhaustive.
func (a *App) loadPeerCredentials() {
	if a.cfg == nil {
		return
	}
	for _, c := range a.cfg.Connections {
		if c.PasswordUnreadable() {
			continue
		}
		a.rememberPeerCredentials(c)
	}
}
