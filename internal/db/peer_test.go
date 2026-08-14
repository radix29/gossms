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
