package db

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
)

// peer.go lets one connection reach a *different* instance, which Always On
// needs: sys.availability_groups and sys.availability_replicas are cluster-wide,
// but the sys.dm_hadr_* DMVs describe only what the connected instance can see,
// so a secondary reports empty roles, empty health and no per-database queue
// detail for every replica but itself. Only the primary has the whole picture,
// and it is usually not the instance the user registered.
//
// Peers are cached for the parent connection's lifetime and closed with it.

// Peer returns a connection to another instance in the same topology,
// authenticated as sc is and cached on sc, so repeated Object Explorer
// expansions of one availability group reuse a connection instead of paying a
// TCP+TLS+login round trip each time.
//
// server is a SQL Server instance name as the catalog reports it — typically
// sys.availability_replicas.replica_server_name, which may carry a
// "HOST\INSTANCE" suffix. peerOptions decides which credentials, port and
// transport settings reach it.
//
// Returns sc itself when server names sc's own instance, so callers can route
// through Peer unconditionally.
func (sc *ServerConn) Peer(ctx context.Context, server string) (*ServerConn, error) {
	if sc.isSelf(server) {
		return sc, nil
	}

	// InstanceKey, not a plain lowercase: "UBUSQL2", "ubusql2,1433" and
	// "ubusql2" are one instance, and the catalog and the user rarely spell it
	// the same way. The credential resolver is keyed by the same normalizer, so
	// a hit there and here agree on what "that instance" means.
	key := InstanceKey(server)

	// peerLive, not IsOpen: Peer runs on background loader goroutines while
	// closePeers writes the closed flag on the UI goroutine outside peerMu, so
	// reading it here would race. The context Close cancels first answers the
	// same question race-free.
	sc.peerMu.Lock()
	if p, ok := sc.peers[key]; ok && peerLive(p) {
		sc.peerMu.Unlock()
		return p, nil
	}
	if f, ok := sc.peerFails[key]; ok && time.Since(f.at) < peerFailureTTL {
		sc.peerMu.Unlock()
		return nil, f.err
	}
	sc.peerMu.Unlock()

	// Connect outside the lock: it does network I/O, and holding the mutex across
	// it would serialise every replica of a group behind the slowest one.
	opts := sc.peerOptions(server)
	peer, err := Connect(opts)
	if err != nil {
		// A resolver hit that cannot connect must never leave the instance less
		// reachable than before one was installed: the saved connection may
		// carry a password the config key can no longer decrypt, or a login
		// since dropped, where the parent connection's credentials would have
		// worked. So the pre-resolver derivation is tried once more. The cost is
		// one extra connect attempt against an instance that is simply down.
		//
		// The first error is reported when both fail: it names the credentials
		// the user deliberately registered for that instance.
		fallback := sc.parentPeerOptions(server)
		if fallback == opts {
			return nil, sc.recordPeerFailure(key, err)
		}
		var ferr error
		if peer, ferr = Connect(fallback); ferr != nil {
			return nil, sc.recordPeerFailure(key, err)
		}
	}
	// A peer's own peers resolve through the same table: Object Explorer follows
	// a group to its primary and reads on from there, and a resolver stopping at
	// the first hop would leave the second reaching a third instance with the
	// primary's login.
	peer.SetPeerCredentials(sc.peerCredentials())

	sc.peerMu.Lock()
	defer sc.peerMu.Unlock()
	// sc was closed, or another goroutine won the race, while we connected.
	// Checked via Context rather than the closed flag, which Close writes on the
	// UI goroutine while Peer runs on background loader ones.
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
	delete(sc.peerFails, key)
	sc.peers[key] = peer
	return peer, nil
}

// peerFailureTTL is how long a failed connect answers for the instance that
// failed. It is short because the entry is only there to collapse a burst: the
// user expands three folders of one group and each one asks for the same
// primary. Long enough and a primary that came back stays unreachable in the UI
// for no reason, which is what ForgetPeerFailure exists to cut short.
const peerFailureTTL = 30 * time.Second

// ForgetPeerFailure drops the cached connect failure for server, so the next
// Peer for it dials again instead of replaying the error for the rest of
// peerFailureTTL. Called when something proves the cached answer stale — a
// successful direct connect to that instance.
func (sc *ServerConn) ForgetPeerFailure(server string) {
	sc.forgetPeerFailures(InstanceKey(server), map[*ServerConn]bool{})
}

// ForgetPeerFailures drops every cached connect failure — the answer to an
// explicit Refresh, where the user is asking for the reads to be retried and
// has no way to say which instance came back.
func (sc *ServerConn) ForgetPeerFailures() {
	sc.forgetPeerFailures("", map[*ServerConn]bool{})
}

// forgetPeerFailures clears one instance's cached failure, or every one when
// key is "".
//
// It recurses into the cached peers because a peer read chains: Object Explorer
// follows a group to its primary and reads on from there, so the failure to
// reach a third instance is recorded on the primary's connection, not on sc.
// seen guards the cycle two instances that have each opened the other make.
func (sc *ServerConn) forgetPeerFailures(key string, seen map[*ServerConn]bool) {
	if sc == nil || seen[sc] {
		return
	}
	seen[sc] = true

	sc.peerMu.Lock()
	if key == "" {
		clear(sc.peerFails)
	} else {
		delete(sc.peerFails, key)
	}
	peers := slices.Collect(maps.Values(sc.peers))
	sc.peerMu.Unlock()

	for _, p := range peers {
		p.forgetPeerFailures(key, seen)
	}
}

// recordPeerFailure caches err as the answer for key and returns it unchanged,
// so a caller reads `return nil, sc.recordPeerFailure(...)`.
//
// Without it a primary whose packets are dropped rather than refused costs the
// driver's full connect timeout — 15s measured, 30s when a saved replica
// credential makes the fallback try a second time — on *every* call, and
// nothing caches a peer that was never opened: expanding an availability
// group's three folders stalled 45s, then Properties another 15s.
func (sc *ServerConn) recordPeerFailure(key string, err error) error {
	sc.peerMu.Lock()
	defer sc.peerMu.Unlock()
	if sc.peerFails == nil {
		sc.peerFails = map[string]peerFailure{}
	}
	sc.peerFails[key] = peerFailure{err: err, at: time.Now()}
	return err
}

// peerOptions is the connection options for reaching server: the ones a resolver
// installed with SetPeerCredentials holds for that instance, or sc's own
// retargeted at it — and either way deliberately **no database**.
//
// A database named in the connection string must be openable or the connect
// fails at ping time ("Cannot open database %q that was requested by the
// login"), and the database the user connected through is exactly the one a peer
// may not be able to open: a secondary that is not readable, or has not joined
// that database yet. Carrying it over turns an ordinary Always On read into a
// connect error.
//
// Nothing is lost by dropping it: everything Peer reaches is server-scoped — the
// availability catalog, the sys.dm_hadr_* DMVs, an endpoint, a certificate — and
// anything database-scoped goes through gosmo's Database handles, which set
// their context per query.
//
// A saved connection is taken whole rather than field by field: port, auth
// method, Entra tenant and client, TLS and extra properties are all part of how
// the user reaches that instance, and a hand-copied subset silently stops
// carrying whatever field config.Connection gains next. Only Server and Database
// are overridden.
//
// This is the preferred derivation, not the only one Peer tries — a hit here
// that fails to connect falls back to parentPeerOptions.
func (sc *ServerConn) peerOptions(server string) config.Connection {
	if creds := sc.peerCredentials(); creds != nil {
		if saved, ok := creds(server); ok {
			return retargetAt(saved, server)
		}
	}
	return sc.parentPeerOptions(server)
}

// parentPeerOptions is sc's own connection options retargeted at server — what
// Peer falls back to when a resolver's answer will not connect.
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

// InstanceKey normalizes a SQL Server instance name to the one spelling used to
// key peers and their credentials: lowercased host, plus "\instance" for a named
// instance, with any port dropped.
//
// The port goes because it is not part of the instance's identity — "ubusql2"
// and "ubusql2,1433" are the same server, and a credential saved under one
// spelling has to be found under the other. It lives here rather than in config
// because it needs gosmo's address parser.
func InstanceKey(server string) string {
	host, instance, _ := gosmo.ParseServerAddress(server)
	key := strings.ToLower(strings.TrimSpace(host))
	if instance != "" {
		key += "\\" + strings.ToLower(instance)
	}
	return key
}

// PeerCredentials answers, for one instance name, the saved connection to reach
// it with. False means nothing is saved for that instance, and peerOptions falls
// back to the parent connection's settings.
type PeerCredentials func(server string) (config.Connection, bool)

// SetPeerCredentials installs the resolver peerOptions consults before falling
// back to sc's own settings — what lets a replica needing a different login or
// port be reached at all. Peer installs it on each peer it opens, so one call on
// the user's connection reaches the whole topology.
func (sc *ServerConn) SetPeerCredentials(fn PeerCredentials) {
	sc.peerMu.Lock()
	defer sc.peerMu.Unlock()
	sc.creds = fn
}

// peerCredentials reads the resolver under peerMu: Peer runs on background
// loader goroutines while SetPeerCredentials is called from the UI one.
func (sc *ServerConn) peerCredentials() PeerCredentials {
	sc.peerMu.Lock()
	defer sc.peerMu.Unlock()
	return sc.creds
}

// isSelf reports whether server names the instance sc is already connected to,
// compared against @@SERVERNAME rather than the address the user typed: the
// catalog reports instance names, and a host, its FQDN and its IP are one
// instance but only one matches Opts.Server.
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

// peerLive reports whether a cached peer is still usable without reading the
// closed flag, which is racy between Peer's background loader goroutines and
// Close on the UI one; the context Close cancels first is not. Not a method on
// ServerConn — IsOpen is the exported answer for UI-goroutine callers.
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
	// peerFails holds the last connect failure per instance, so a peer that
	// cannot be opened is not re-dialled on every read for peerFailureTTL.
	// Guarded by peerMu.
	peerFails map[string]peerFailure
	// creds resolves an instance to its own saved connection; nil means every
	// peer is reached with this connection's settings. Guarded by peerMu.
	creds PeerCredentials
}

// peerFailure is one instance's last failed connect, and when it happened.
type peerFailure struct {
	err error
	at  time.Time
}
