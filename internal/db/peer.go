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

// peer.go lets a connection reach a different instance, which Always On needs:
// sys.dm_hadr_* only describes what the connected instance sees, so a secondary
// reports empty roles, health and queue detail for other replicas. Only the
// primary has the whole picture, and it is usually not the registered instance.
//
// Peers are cached for the parent's lifetime and closed with it.

// Peer returns a connection to another instance in the same topology,
// authenticated as sc is and cached on sc so repeated expansions reuse it.
//
// server is an instance name as the catalog reports it
// (sys.availability_replicas.replica_server_name, possibly "HOST\INSTANCE");
// peerOptions picks credentials, port and transport.
//
// Returns sc itself when server is sc's own instance, so callers can always
// route through Peer.
//
// One dial per instance at a time: three folders asking for the same primary
// dial it once and the other callers wait, each bounded by its own ctx.
// Cancelling ctx abandons the caller's wait or, for the dialling caller, the
// dial itself, so a superseded load no longer waits out two 30s connects.
func (sc *ServerConn) Peer(ctx context.Context, server string) (*ServerConn, error) {
	if sc.isSelf(server) {
		return sc, nil
	}

	// InstanceKey, not lowercase: "UBUSQL2", "ubusql2,1433" and "ubusql2" are
	// one instance ("ubusql2,1500" another). The credential resolver uses the
	// same key.
	key := config.InstanceKey(server)

	for {
		sc.peerMu.Lock()
		if p, ok := sc.peers[key]; ok && p.IsOpen() {
			sc.peerMu.Unlock()
			return p, nil
		}
		if f, ok := sc.peerFails[key]; ok && time.Since(f.at) < peerFailureTTL {
			sc.peerMu.Unlock()
			return nil, f.err
		}
		d, leader := sc.peerDials.join(key)
		if leader {
			sc.peerMu.Unlock()
			return sc.dialPeer(ctx, server, key, d)
		}
		sc.peerMu.Unlock()

		if !d.wait(ctx) {
			return nil, ctx.Err()
		}
		if d.val != nil {
			return d.val, nil
		}
		// The dialling caller gave up, which says nothing about the instance;
		// a waiter still wanting an answer dials again as first in line.
		if d.abandoned && ctx.Err() == nil {
			continue
		}
		return nil, d.err
	}
}

// dialPeer runs d's dial, caches the outcome, and releases d's waiters —
// deferred, so a panic still releases them.
//
// Connects outside peerMu; holding it across network I/O serialises every
// replica behind the slowest.
func (sc *ServerConn) dialPeer(ctx context.Context, server, key string, d *peerDial) (peer *ServerConn, err error) {
	// The dial ends with ctx or with sc: a peer of a closed connection would
	// only be closed again.
	dctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(sc.Server.Context(), cancel)
	defer stop()

	defer func() {
		sc.peerMu.Lock()
		sc.peerDials.forget(key, d)
		switch {
		case err == nil && sc.Server.Context().Err() != nil:
			// sc closed while connecting; closePeers has already run.
			peer.Close()
			peer, err = nil, sc.Server.Context().Err()
		case err == nil:
			if sc.peers == nil {
				sc.peers = map[string]*ServerConn{}
			}
			delete(sc.peerFails, key)
			sc.peers[key] = peer
		case ctx.Err() != nil || sc.Server.Context().Err() != nil:
			// A cancelled dial learnt nothing about the instance; caching
			// it would refuse the next caller for peerFailureTTL.
			d.abandoned = true
		default:
			sc.recordPeerFailureLocked(key, err)
		}
		d.val, d.err = peer, err
		sc.peerMu.Unlock()
		d.land()
	}()

	opts := sc.peerOptions(server)
	peer, err = connectPeer(dctx, opts, sc.role)
	if err != nil {
		// A resolver hit that cannot connect must not leave the instance less
		// reachable than the parent's credentials would, so retry with the
		// pre-resolver derivation. Costs one extra attempt against a really
		// down instance. When both fail, report the first error: it names the
		// credentials the user registered.
		fallback := sc.parentPeerOptions(server)
		if fallback == opts || dctx.Err() != nil {
			return nil, err
		}
		var ferr error
		if peer, ferr = connectPeer(dctx, fallback, sc.role); ferr != nil {
			return nil, err
		}
	}
	// A peer's peers resolve through the same table: Object Explorer follows a
	// group to its primary and reads on, and stopping at the first hop would
	// reach a third instance with the primary's login.
	peer.SetPeerCredentials(sc.peerCredentials())
	return peer, nil
}

// connectPeer is Peer's dial; a variable so tests can count and hold dials
// without a server.
var connectPeer = ConnectContext

// peerFailureTTL is how long a failed connect answers for its instance. Short:
// it only collapses bursts (three folders of one group asking for the same
// primary); longer would keep a recovered primary unreachable (see
// ForgetPeerFailure).
const peerFailureTTL = 30 * time.Second

// ForgetPeerFailure drops server's cached connect failure so the next Peer
// dials again. Called when the entry is proven stale, e.g. by a successful
// direct connect.
func (sc *ServerConn) ForgetPeerFailure(server string) {
	sc.forgetPeerFailures(config.InstanceKey(server), map[*ServerConn]bool{})
}

// ForgetPeerFailures drops every cached connect failure, for an explicit
// Refresh.
func (sc *ServerConn) ForgetPeerFailures() {
	sc.forgetPeerFailures("", map[*ServerConn]bool{})
}

// forgetPeerFailures clears one instance's cached failure, or all when key is
// "".
//
// Recurses into cached peers because reads chain (a group → its primary → on),
// so a third instance's failure is recorded on the primary's connection. seen
// guards against two instances that opened each other.
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

// recordPeerFailureLocked caches err for key and returns it, for `return nil,
// sc.recordPeerFailureLocked(...)`. peerMu must be held.
//
// Without it, a primary that drops packets costs the full connect timeout on
// every call: expanding an AG's three folders stalled 45s.
func (sc *ServerConn) recordPeerFailureLocked(key string, err error) error {
	if sc.peerFails == nil {
		sc.peerFails = map[string]peerFailure{}
	}
	sc.peerFails[key] = peerFailure{err: err, at: time.Now()}
	return err
}

// peerOptions returns the options for reaching server: the resolver's saved
// connection for that instance, or sc's own retargeted, either way with no
// database.
//
// A named database must be openable or connect fails at ping ("Cannot open
// database %q that was requested by the login"), and the user's database is
// exactly what a secondary may not open (not readable, or not joined). Nothing
// is lost: Peer's reads are server-scoped, and database-scoped work uses gosmo
// Database handles that set context per query.
//
// A saved connection is taken whole (port, auth, Entra, TLS, extra properties)
// so future fields are not dropped; only Server and Database are overridden.
// If a resolver hit fails to connect, Peer falls back to parentPeerOptions.
func (sc *ServerConn) peerOptions(server string) config.Connection {
	if creds := sc.peerCredentials(); creds != nil {
		if saved, ok := creds(server); ok {
			return retargetAt(saved, server)
		}
	}
	return sc.parentPeerOptions(server)
}

// parentPeerOptions is sc's own options retargeted at server — Peer's fallback
// when a resolver answer won't connect.
func (sc *ServerConn) parentPeerOptions(server string) config.Connection {
	return retargetAt(sc.Opts, server)
}

// retargetAt points a saved connection at server with no database; see
// peerOptions.
//
// A port written in the saved Server moves to Port first: the catalog names an
// instance without one, and "host\SQL2017,55253" retargeted to "HOST\SQL2017"
// would need SQL Browser, which that host may not run. A port in server itself
// still wins, in gosmo.
func retargetAt(opts config.Connection, server string) config.Connection {
	if _, _, port := gosmo.ParseServerAddress(opts.Server); port != 0 {
		opts.Port = port
	}
	opts.Server = server
	opts.Database = ""
	return opts
}

// PeerCredentials returns the saved connection for an instance name; false
// means none, and peerOptions falls back to the parent's settings.
type PeerCredentials func(server string) (config.Connection, bool)

// SetPeerCredentials installs the resolver peerOptions consults first, so
// replicas needing a different login or port are reachable. Peer installs it on
// each peer it opens, so one call covers the topology.
func (sc *ServerConn) SetPeerCredentials(fn PeerCredentials) {
	sc.peerMu.Lock()
	defer sc.peerMu.Unlock()
	sc.creds = fn
}

// peerCredentials reads the resolver under peerMu (Peer runs on loader
// goroutines, SetPeerCredentials on the UI one).
func (sc *ServerConn) peerCredentials() PeerCredentials {
	sc.peerMu.Lock()
	defer sc.peerMu.Unlock()
	return sc.creds
}

// isSelf reports whether server is sc's instance, compared against
// @@SERVERNAME: host, FQDN and IP are one instance but only one matches
// Opts.Server.
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

// peerFields is embedded in ServerConn; kept here with the code that owns it.
type peerFields struct {
	peerMu sync.Mutex
	peers  map[string]*ServerConn
	// peerDials is the in-flight dial per instance; later callers wait for
	// it. Guarded by peerMu.
	peerDials flights[string, *ServerConn]
	// peerFails holds each instance's last connect failure so it isn't
	// re-dialled for peerFailureTTL. Guarded by peerMu.
	peerFails map[string]peerFailure
	// creds resolves an instance to its saved connection; nil means every peer
	// uses this connection's settings. Guarded by peerMu.
	creds PeerCredentials
}

// peerFailure is an instance's last failed connect and its time.
type peerFailure struct {
	err error
	at  time.Time
}

// peerDial is one in-flight Peer dial; val is nil if it failed.
type peerDial = flight[*ServerConn]
