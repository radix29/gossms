package tui

import (
	"strings"
	"sync"

	"github.com/radix29/gossms/internal/config"
)

// app_peer_creds.go holds App's answer to db.PeerCredentials: which saved
// connection to reach a given instance with.
//
// Always On is why it matters. Everything the Object Explorer, the AG dialogs and
// the endpoint wizard read off a second instance goes through
// db.ServerConn.Peer, which without this uses the login the tree was registered
// with. A topology whose replicas want different credentials or ports would
// surface as a connect error naming the instance, and on the follow-the-primary
// path as a silent "(partial - primary X unreachable)".
//
// The answer is the connections the user has already made: connect to a replica
// once through File > Connect and every later peer read reaches it the same way.

// peerCredStore is how to reach each instance the user has connected to.
// byInstance is keyed by config.InstanceKey; byShortHost by short host name,
// consulted only when byInstance misses (see shortHostKey). mu guards both, as
// App.filterMu guards savedFilters: background loaders read them through Peer.
type peerCredStore struct {
	mu          sync.Mutex
	byInstance  map[string]config.Connection
	byShortHost map[string]config.Connection
}

// peerCredentialsFor resolves an instance name to its own saved connection; the
// db.PeerCredentials installed on every connection App opens.
//
// Read from background loader goroutines (chiefly the Object Explorer's Always
// On loader, through Peer), so the maps are behind peerCreds.mu.
func (a *App) peerCredentialsFor(server string) (config.Connection, bool) {
	key := config.InstanceKey(server)
	a.peerCreds.mu.Lock()
	defer a.peerCreds.mu.Unlock()
	if c, ok := a.peerCreds.byInstance[key]; ok {
		return c, true
	}
	c, ok := a.peerCreds.byShortHost[key]
	return c, ok
}

// rememberPeerCredentials records conn as the way to reach its instance,
// replacing what was held. Called for each saved connection at startup and on
// every successful connect, so the most recent way the user reached an instance
// is the one a peer read uses.
func (a *App) rememberPeerCredentials(conn config.Connection) {
	if conn.Server == "" {
		return
	}
	// ConnectionAddress, not conn.Server: the Connect dialog saves a port in the
	// separate Port field, and keyed without it "win10cli" and its SQL2017 instance
	// on 55253 became one instance with the later login.
	key := config.InstanceKey(config.ConnectionAddress(conn))
	a.peerCreds.mu.Lock()
	defer a.peerCreds.mu.Unlock()
	if a.peerCreds.byInstance == nil {
		a.peerCreds.byInstance = map[string]config.Connection{}
	}
	a.peerCreds.byInstance[key] = conn
	if alias := shortHostKey(key); alias != "" {
		if a.peerCreds.byShortHost == nil {
			a.peerCreds.byShortHost = map[string]config.Connection{}
		}
		a.peerCreds.byShortHost[alias] = conn
	}
}

// shortHostKey is key with the host's domain suffix dropped, or "" when the host
// has none.
//
// The two names for an instance rarely agree: the catalog reports @@SERVERNAME
// (the short machine name), while on a domain network the user usually types the
// FQDN into Connect. Keyed only by exact host, a saved "ubusql2.fritz.box" would
// never answer a peer read for the "ubusql2" sys.availability_replicas reports.
//
// A separate, lower-priority tier rather than a collapsed key: two instances
// really can be "sql.a.example" and "sql.b.example", and folding them onto one key
// would hand one the other's login. Consulted only when the exact host misses, the
// worst case is the connect error a miss gives anyway.
//
// The "\instance" or ",port" InstanceKey appended is kept: the alias drops the
// domain, not what tells two instances on one host apart.
func shortHostKey(key string) string {
	host, suffix := key, ""
	if i := strings.IndexAny(key, "\\,"); i >= 0 {
		host, suffix = key[:i], key[i:]
	}
	short, _, dotted := strings.Cut(host, ".")
	if !dotted || short == "" {
		return ""
	}
	return short + suffix
}

// loadPeerCredentials seeds the map from the saved connections in stored order
// (oldest first, so the most recently used entry for an instance is the one left).
// Same precedence config.MatchByServer offers the Connect dialog.
//
// An entry whose password could not be decrypted is not seeded. Nothing on disk
// has been proven to work for it, and preferring one certain to fail over the
// parent connection's own credentials makes a reachable instance unreachable. A
// replaced config key blanks every saved password at once (config.Load), so this
// is a whole-file state, not a rare entry.
//
// Nothing else is judged here: an entry that passes and still fails is Peer's
// fallback to deal with.
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

// forgetPeerFailure tells every open connection that server is reachable
// again, dropping the cached connect failure that would otherwise answer for
// it until it expires. See db.ServerConn.ForgetPeerFailure.
func (a *App) forgetPeerFailure(server string) {
	for _, sc := range a.connections {
		sc.ForgetPeerFailure(server)
	}
}
