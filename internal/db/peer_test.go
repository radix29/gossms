package db

import (
	"context"
	"sync"
	"testing"

	"github.com/radix29/gossms/internal/config"
)

// newTestConn builds a ServerConn with the lifetime plumbing Connect sets up
// but no gosmo.Server behind it — Close is nil-safe, so the close path can be
// exercised without a live instance.
func newTestConn(server string) *ServerConn {
	ctx, cancel := context.WithCancel(context.Background())
	return &ServerConn{Opts: config.Connection{Server: server}, ctx: ctx, cancel: cancel}
}

// Peer runs on background loader goroutines while Close runs on the UI one,
// and closePeers closes each cached peer — writing ServerConn.closed — after
// releasing peerMu, which looks like a race against the cache lookup's
// IsOpen. It is not, and this pins why: every IsOpen on a cached peer happens
// under peerMu, and closePeers takes that same mutex before any peer is
// closed, so the write is always ordered after the read. Once closePeers has
// run, sc.peers is nil and there is no cached peer left to test.
//
// Run under -race; without it this proves nothing.
func TestPeerLookupRacesDisconnect(t *testing.T) {
	for range 50 {
		sc := newTestConn("primary")
		peer := newTestConn("secondary")
		sc.peers = map[string]*ServerConn{"secondary": peer}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			sc.Peer(context.Background(), "secondary")
		}()
		go func() {
			defer wg.Done()
			sc.Close()
		}()
		wg.Wait()
	}
}

// A database named in the connection string has to be openable or the connect
// itself fails, at ping time — "Cannot open database %q that was requested by
// the login", verified live against win10cli 2026-08-14. The database the user
// connected through is exactly the one a peer may not be able to open (an
// unreadable secondary, or one that has not joined that database), so Peer
// must not carry it over.
//
// Peer's own connect can't run without a real second instance, so this pins
// the decision at the only seam a unit test has: the options it builds from.
func TestPeerOptionsDropTheDatabase(t *testing.T) {
	sc := &ServerConn{Opts: config.Connection{
		Server: "primary", Port: 1433, User: "sa", Password: "pw",
		Database: "SalesDB", TrustServerCertificate: true,
	}}

	opts := sc.peerOptions("secondary")

	if opts.Database != "" {
		t.Errorf("peer options carry Database %q — a secondary that cannot open "+
			"it fails the connect outright", opts.Database)
	}
	if opts.Server != "secondary" {
		t.Errorf("peer options name server %q, want %q", opts.Server, "secondary")
	}
	// Everything else has to survive, or the peer authenticates differently
	// from the connection it was derived from.
	if opts.User != "sa" || opts.Password != "pw" || opts.Port != 1433 || !opts.TrustServerCertificate {
		t.Errorf("peer options lost credentials or transport settings: %+v", opts)
	}
}
