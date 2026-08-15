package db

import (
	"context"
	"strings"
	"sync"

	"github.com/radix29/gossms/internal/config"
)

// peer.go lets one connection reach a *different* instance using the same
// credentials. Always On is the reason it exists: sys.availability_groups and
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
// "HOST\INSTANCE" suffix. sc's own port and auth settings are reused, but not
// its database — see peerOptions. A topology whose replicas listen on
// different ports or want different credentials is out of scope here and will
// surface as a connect error.
//
// Returns sc itself when server names sc's own instance, so callers can route
// through Peer unconditionally without special-casing the local case.
func (sc *ServerConn) Peer(ctx context.Context, server string) (*ServerConn, error) {
	if sc.isSelf(server) {
		return sc, nil
	}

	key := strings.ToLower(server)

	sc.peerMu.Lock()
	if p, ok := sc.peers[key]; ok && p.IsOpen() {
		sc.peerMu.Unlock()
		return p, nil
	}
	sc.peerMu.Unlock()

	// Connect outside the lock: it does network I/O, and holding the mutex
	// across it would serialise every replica of a multi-replica group behind
	// the slowest one.
	peer, err := Connect(sc.peerOptions(server))
	if err != nil {
		return nil, err
	}

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
	if existing, ok := sc.peers[key]; ok && existing.IsOpen() {
		peer.Close()
		return existing, nil
	}
	if sc.peers == nil {
		sc.peers = map[string]*ServerConn{}
	}
	sc.peers[key] = peer
	return peer, nil
}

// peerOptions is sc's own connection options retargeted at server: same port,
// same auth, same credentials, and deliberately **no database**.
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
func (sc *ServerConn) peerOptions(server string) config.Connection {
	opts := sc.Opts
	opts.Server = server
	opts.Database = ""
	return opts
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
}
