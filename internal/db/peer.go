package db

import (
	"context"
	"strings"
	"sync"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
)

// peer.go lets one connection reach a *different* instance. Always On is the reason it exists: sys.availability_groups and
// sys.availability_replicas are cluster-wide and agree on every replica, but
// the sys.dm_hadr_* DMVs only describe what the connected instance can see, so
// a secondary reports empty roles, empty health and no per-database queue
// detail for every replica but itself. Reading the group from its primary is
// the only way to get the whole picture, and the primary is usually not the
// instance the user registered.
//
// Peers are cached for the parent connection's lifetime and closed with it.

// Peer returns a connection to another instance in the same topology,
// authenticated exactly as sc is and cached on sc, so repeated Object Explorer
// expansions of the same availability group reuse one connection instead of
// paying a full TCP+TLS+login round trip each time.
//
// server is a SQL Server instance name as the catalog reports it — typically
// sys.availability_replicas.replica_server_name, which may carry a
// "HOST\INSTANCE" suffix. Which credentials, port and transport settings reach
// it is peerOptions' answer: sc's own, unless a resolver installed with
// SetPeerCredentials knows that instance.
//
// Returns sc itself when server names sc's own instance, so callers can route
// through Peer unconditionally without special-casing the local case.
func (sc *ServerConn) Peer(ctx context.Context, server string) (*ServerConn, error) {
	if sc.isSelf(server) {
		return sc, nil
	}

	// InstanceKey, not a plain lowercase: "UBUSQL2", "ubusql2,1433" and
	// "ubusql2" are one instance, and the catalog and the user rarely spell it
	// the same way. It is the same normalizer the credential resolver is keyed
	// by, so a hit there and a hit here always agree about what "that instance"
	// means.
	key := InstanceKey(server)

	// peerLive, not IsOpen: Peer runs on background loader goroutines, and a
	// peer's closed flag is written by closePeers on the UI goroutine outside
	// peerMu. Reading it here would be a data race; the context Close cancels
	// first answers the same question race-free.
	sc.peerMu.Lock()
	if p, ok := sc.peers[key]; ok && peerLive(p) {
		sc.peerMu.Unlock()
		return p, nil
	}
	sc.peerMu.Unlock()

	// Connect outside the lock: it does network I/O, and holding the mutex
	// across it would serialise every replica of a multi-replica group behind
	// the slowest one.
	opts := sc.peerOptions(server)
	peer, err := Connect(opts)
	if err != nil {
		// A resolver hit that cannot connect must never leave the instance
		// less reachable than it was before one was installed: the saved
		// connection may carry a password the config key can no longer
		// decrypt (config.Load blanks those), or a login that has since been
		// dropped, and the parent connection's own credentials would have
		// worked. So the pre-resolver derivation is tried once more before
		// giving up. The cost is one extra connect attempt against an
		// instance that is simply down, and only when a resolver answered
		// for it.
		//
		// The first error is the one reported when both fail: it names the
		// credentials the user deliberately registered for that instance,
		// which is the more useful of the two.
		fallback := sc.parentPeerOptions(server)
		if fallback == opts {
			return nil, err
		}
		var ferr error
		if peer, ferr = Connect(fallback); ferr != nil {
			return nil, err
		}
	}
	// A peer's own peers resolve through the same table: the Object Explorer
	// follows a group to its primary and reads on from there, and a resolver
	// that stopped at the first hop would leave the second reaching a third
	// instance with the primary's login rather than its own.
	peer.SetPeerCredentials(sc.peerCredentials())

	sc.peerMu.Lock()
	defer sc.peerMu.Unlock()
	// sc was closed, or another goroutine won the race, while we connected.
	// Checked via Context rather than the closed flag: Peer runs on background
	// loader goroutines and closed is written by Close on the UI goroutine,
	// where the context cancellation Close already performs is race-free.
	if err := sc.Context().Err(); err != nil {
		peer.Close()
		return nil, err
	}
	if existing, ok := sc.peers[key]; ok && peerLive(existing) {
		peer.Close()
		return existing, nil
	}
	if sc.peers == nil {
		sc.peers = map[string]*ServerConn{}
	}
	sc.peers[key] = peer
	return peer, nil
}

// peerOptions is the connection options for reaching server: the ones a
// resolver installed with SetPeerCredentials holds for that instance, or sc's
// own retargeted at it — and, either way, deliberately **no database**.
//
// A database named in the connection string has to be openable or the connect
// itself fails — "Cannot open database %q that was requested by the login",
// raised at ping time, verified live against win10cli 2026-08-14. The database
// the user happened to connect through is exactly the one a peer may not be
// able to open: a secondary replica that is not readable, or one that has not
// joined that database yet. Carrying it over turns an ordinary Always On read
// into a connect error before any of it runs.
//
// Nothing is lost by dropping it. Everything Peer exists to reach is
// server-scoped — the availability catalog, the sys.dm_hadr_* DMVs, an
// endpoint, a certificate — and anything database-scoped goes through gosmo's
// own Database handles, which set their context per query.
//
// A saved connection is taken whole rather than field by field. Every setting
// on it — port, auth method, Entra tenant and client, TLS, extra properties —
// is part of how the user actually reaches that instance, and a hand-copied
// subset silently stops carrying whatever field config.Connection gains next.
// Only Server and Database are overridden, and Server only so the catalog's
// spelling of the instance is what the peer is opened against.
//
// This is the preferred derivation, not the only one Peer will try — a hit
// here that fails to connect falls back to parentPeerOptions.
func (sc *ServerConn) peerOptions(server string) config.Connection {
	if creds := sc.peerCredentials(); creds != nil {
		if saved, ok := creds(server); ok {
			return retargetAt(saved, server)
		}
	}
	return sc.parentPeerOptions(server)
}

// parentPeerOptions is sc's own connection options retargeted at server — what
// every Peer call used before SetPeerCredentials existed, and what Peer falls
// back to when a resolver's answer will not connect.
func (sc *ServerConn) parentPeerOptions(server string) config.Connection {
	return retargetAt(sc.Opts, server)
}

// retargetAt points one saved connection at server, with no database — see
// peerOptions for why both.
func retargetAt(opts config.Connection, server string) config.Connection {
	opts.Server = server
	opts.Database = ""
	return opts
}

// InstanceKey normalizes a SQL Server instance name to the one spelling used
// to key peers and their credentials: lowercased host, plus "\instance" for a
// named instance, with any port dropped.
//
// The port goes because it is not part of the instance's identity — "ubusql2"
// and "ubusql2,1433" are the same server, and a credential saved under one
// spelling has to be found under the other. It lives here rather than in
// config because it needs gosmo's address parser, which config does not
// import.
func InstanceKey(server string) string {
	host, instance, _ := gosmo.ParseServerAddress(server)
	key := strings.ToLower(strings.TrimSpace(host))
	if instance != "" {
		key += "\\" + strings.ToLower(instance)
	}
	return key
}

// PeerCredentials answers, for one instance name, the saved connection to
// reach it with. Reporting false means "nothing saved for that instance", and
// peerOptions falls back to the parent connection's own settings.
type PeerCredentials func(server string) (config.Connection, bool)

// SetPeerCredentials installs the resolver peerOptions consults before falling
// back to sc's own settings — the seam that lets a replica needing a different
// login, or listening on a different port, be reached at all.
//
// Peer installs it on each peer it opens, so it reaches the whole topology
// from one call on the connection the user actually made.
func (sc *ServerConn) SetPeerCredentials(fn PeerCredentials) {
	sc.peerMu.Lock()
	defer sc.peerMu.Unlock()
	sc.creds = fn
}

// peerCredentials reads the resolver under peerMu. Peer runs on background
// loader goroutines while SetPeerCredentials is called from the UI one, so the
// field cannot be read bare.
func (sc *ServerConn) peerCredentials() PeerCredentials {
	sc.peerMu.Lock()
	defer sc.peerMu.Unlock()
	return sc.creds
}

// isSelf reports whether server names the instance sc is already connected to.
// Compared against the server's own @@SERVERNAME rather than the address the
// user typed: the catalog reports instance names, and "ubusql1",
// "ubusql1.fritz.box" and "192.168.178.97" are all the same instance but only
// one of them matches Opts.Server.
func (sc *ServerConn) isSelf(server string) bool {
	if server == "" {
		return true
	}
	if sc.Server != nil {
		if name := sc.Server.Name(); name != "" && strings.EqualFold(name, server) {
			return true
		}
	}
	return strings.EqualFold(sc.Opts.Server, server)
}

// peerLive reports whether a cached peer is still usable, without reading the
// closed flag. Peer's callers are background loader goroutines and Close runs
// on the UI goroutine, so closed is racy across them; the context Close cancels
// before anything else is not, which makes this the only safe form of the
// question here. Not a method on ServerConn: IsOpen is the exported answer for
// UI-goroutine callers, and this one exists purely because that answer can't be
// read from here.
func peerLive(p *ServerConn) bool { return p != nil && p.Context().Err() == nil }

// closePeers closes and drops every cached peer. Called by Close.
func (sc *ServerConn) closePeers() {
	sc.peerMu.Lock()
	peers := sc.peers
	sc.peers = nil
	sc.peerMu.Unlock()

	for _, p := range peers {
		p.Close()
	}
}

// peerFields is embedded in ServerConn; kept here so the peer cache's state
// lives with the code that owns it.
type peerFields struct {
	peerMu sync.Mutex
	peers  map[string]*ServerConn
	// creds resolves an instance to its own saved connection; nil means every
	// peer is reached with this connection's settings. Guarded by peerMu — see
	// peerCredentials.
	creds PeerCredentials
}
