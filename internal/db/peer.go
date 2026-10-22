package db

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/radix29/gossms/internal/config"
)

// peer.go lets a connection reach a different instance, which Always On needs:
// sys.dm_hadr_* only describes what the connected instance sees, so a secondary
// reports empty roles, health and queue detail for other replicas. Only the
// primary has the whole picture, and it is usually not the registered instance.
// Peers are cached for the parent's lifetime and closed with it.

// ErrServerConnClosed is Peer's answer once its parent is closed: no peer,
// cached or new, outlives the parent. It wraps context.Canceled, which a
// closed parent's statements already fail with, so a caller treating
// cancellation as "superseded, say nothing" still does.
var ErrServerConnClosed = fmt.Errorf("connection closed: %w", context.Canceled)

// Peer returns a connection to another instance in the same topology,
// using that instance's registered connection, cached on sc so repeated
// expansions reuse it. Unregistered or ambiguous destinations are refused.
//
// server is an instance name as the catalog reports it
// (sys.availability_replicas.replica_server_name, possibly "HOST\INSTANCE");
// peerOptions picks credentials, port and transport.
//
// Returns sc itself when server is sc's own instance, so callers can always
// route through Peer. Once sc is closed it returns ErrServerConnClosed instead,
// whatever the cache holds.
//
// One dial per instance at a time: three folders asking for the same primary dial
// it once and the others wait, each bounded by its own ctx. Cancelling ctx
// abandons the caller's wait or, for the dialling caller, the dial itself.
func (sc *ServerConn) Peer(ctx context.Context, server string) (*ServerConn, error) {
	if !sc.IsOpen() {
		return nil, ErrServerConnClosed
	}
	if sc.isSelf(server) {
		return sc, nil
	}
	opts, err := sc.peerOptions(server)
	if err != nil {
		return nil, err
	}

	// Cache the registered destination, not the metadata alias. A direct
	// connection can then invalidate its failures under the same key.
	key := config.InstanceKey(config.ConnectionAddress(opts))

	for {
		sc.peerMu.Lock()
		// Under peerMu: Close marks sc closed before closePeers takes the
		// lock, so a lookup after that never hands out a peer closing with it.
		if !sc.IsOpen() {
			sc.peerMu.Unlock()
			return nil, ErrServerConnClosed
		}
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
			p, err := sc.dialPeer(ctx, opts, key, d)
			if err != nil && !sc.IsOpen() {
				err = ErrServerConnClosed
			}
			return p, err
		}
		sc.peerMu.Unlock()

		if !d.wait(ctx) {
			return nil, ctx.Err()
		}
		if !sc.IsOpen() {
			return nil, ErrServerConnClosed
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
func (sc *ServerConn) dialPeer(ctx context.Context, opts config.Connection, key string, d *peerDial) (peer *ServerConn, err error) {
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

	peer, err = connectPeer(dctx, opts, sc.role)
	if err != nil {
		return nil, err
	}
	// A peer's peers resolve through the same table: Object Explorer follows a
	// group to its primary and reads on, and stopping at the first hop would reach
	// a third instance with the primary's login.
	peer.SetPeerCredentials(sc.peerCredentials())
	return peer, nil
}

// connectPeer is Peer's dial; a variable so tests can count and hold dials
// without a server.
var connectPeer = Connect

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
// every call (expanding an AG's three folders stalled 45s).
func (sc *ServerConn) recordPeerFailureLocked(key string, err error) error {
	if sc.peerFails == nil {
		sc.peerFails = map[string]peerFailure{}
	}
	sc.peerFails[key] = peerFailure{err: err, at: time.Now()}
	return err
}

// peerOptions returns the registered connection for server with no database.
//
// A named database must be openable or connect fails at ping ("Cannot open
// database %q that was requested by the login"), and the user's database is
// exactly what a secondary may not open (not readable, or not joined). Nothing
// is lost: Peer's reads are server-scoped, and database-scoped work uses gosmo
// Database handles that set context per query.
//
// The destination must remain the one the user registered, even when catalog
// metadata uses a short alias. Retargeting or retrying with the parent's login
// can disclose credentials to a different server.
func (sc *ServerConn) peerOptions(server string) (config.Connection, error) {
	if creds := sc.peerCredentials(); creds != nil {
		if saved, ok := creds(server); ok && strings.TrimSpace(saved.Server) != "" {
			saved.Database = ""
			return saved, nil
		}
	}
	return config.Connection{}, &PeerNotRegisteredError{Server: server}
}

// PeerNotRegisteredError is Peer's refusal of an instance with no registered
// connection (or only an ambiguous short alias). Server is the name as the
// catalog reported it. The UI recognises it to offer a Connect prompt for that
// instance instead of only reporting it.
type PeerNotRegisteredError struct {
	Server string
}

func (e *PeerNotRegisteredError) Error() string {
	return fmt.Sprintf("peer %q is unregistered or ambiguous; connect to it explicitly with File > Connect first", e.Server)
}

// PeerCredentials returns the saved connection for an instance name; false
// means none or ambiguous, and Peer refuses to dial it. The saved destination
// is authoritative; server may be an alias supplied by untrusted metadata.
type PeerCredentials func(server string) (config.Connection, bool)

// SetPeerCredentials installs the resolver peerOptions consults, so
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
