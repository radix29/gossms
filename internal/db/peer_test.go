package db

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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

// TestInstanceKeyNormalizesSpellings pins the one normalizer both the peer
// cache and the credential lookup are keyed by. The catalog reports
// "HOST\INSTANCE", the user types "host,1433", and a credential saved under
// one spelling has to be found under the other — a key that kept the port, or
// the case, would miss on exactly the connection the user just made.
func TestInstanceKeyNormalizesSpellings(t *testing.T) {
	groups := [][]string{
		{"UBUSQL2", "ubusql2", "ubusql2,1433", "  ubusql2  ", "UbuSQL2:1433"},
		{"HOST\\INST", "host\\inst", "HOST\\inst,1433"},
	}
	for _, g := range groups {
		want := InstanceKey(g[0])
		if want == "" {
			t.Fatalf("InstanceKey(%q) is empty", g[0])
		}
		for _, spelling := range g[1:] {
			if got := InstanceKey(spelling); got != want {
				t.Errorf("InstanceKey(%q) = %q, want %q (same instance as %q)", spelling, got, want, g[0])
			}
		}
	}
	// A named instance is a different instance from the bare host, so the two
	// must not collapse together — sharing a key would hand HOST\INST the
	// default instance's login.
	if InstanceKey("host") == InstanceKey("host\\inst") {
		t.Error("InstanceKey collapses a named instance onto its host")
	}
}

// TestPeerOptionsUseTheInstancesOwnCredentials is the point of the resolver: a
// replica needing a different login was unreachable, because peerOptions
// copied the parent connection's wholesale.
//
// The instance asked for is deliberately not the parent's and is spelled
// differently from the saved entry — the catalog reports "UBUSQL2\PROD" while
// the user connected as "ubusql2\prod,1433" — so a lookup that skipped
// InstanceKey would miss and fall back without failing anything else here.
func TestPeerOptionsUseTheInstancesOwnCredentials(t *testing.T) {
	parent := &ServerConn{Opts: config.Connection{
		Server: "ubusql1", Port: 1433, User: "sa", Password: "parent-pw",
		Database: "SalesDB", TrustServerCertificate: true,
	}}
	parent.SetPeerCredentials(func(server string) (config.Connection, bool) {
		if InstanceKey(server) == InstanceKey("ubusql2\\prod,1433") {
			return config.Connection{
				Server: "ubusql2.fritz.box\\prod", Port: 14330,
				User: "replica_login", Password: "replica-pw",
				Database: "OtherDB", Encrypt: true,
			}, true
		}
		return config.Connection{}, false
	})

	opts := parent.peerOptions("UBUSQL2\\PROD")

	if opts.User != "replica_login" || opts.Password != "replica-pw" {
		t.Errorf("peer options authenticate as %q/%q, want the instance's own saved login",
			opts.User, opts.Password)
	}
	if opts.Port != 14330 {
		t.Errorf("peer options use port %d, want the instance's own 14330 — the port half of the same limitation", opts.Port)
	}
	if !opts.Encrypt {
		t.Error("peer options dropped the saved connection's Encrypt setting")
	}
	// Server is overridden with the name the catalog gave, not the spelling the
	// saved connection happens to carry.
	if opts.Server != "UBUSQL2\\PROD" {
		t.Errorf("peer options name server %q, want the catalog's %q", opts.Server, "UBUSQL2\\PROD")
	}
	// The database is blanked on this path too — the saved connection names one
	// of its own, and it is exactly as unopenable on a secondary.
	if opts.Database != "" {
		t.Errorf("peer options carry Database %q from the saved connection", opts.Database)
	}
}

// TestPeerOptionsFallBackToTheParent pins the miss: an instance nobody has
// saved a connection for is still reached the way it always was.
func TestPeerOptionsFallBackToTheParent(t *testing.T) {
	parent := &ServerConn{Opts: config.Connection{
		Server: "ubusql1", Port: 1433, User: "sa", Password: "parent-pw",
		Database: "SalesDB", TrustServerCertificate: true,
	}}
	parent.SetPeerCredentials(func(string) (config.Connection, bool) {
		return config.Connection{}, false
	})

	opts := parent.peerOptions("ubusql3")

	if opts.User != "sa" || opts.Password != "parent-pw" || opts.Port != 1433 || !opts.TrustServerCertificate {
		t.Errorf("a resolver miss did not fall back to the parent's settings: %+v", opts)
	}
	if opts.Server != "ubusql3" || opts.Database != "" {
		t.Errorf("peer options = server %q database %q, want %q and no database", opts.Server, opts.Database, "ubusql3")
	}
}

// TestPeerCredentialsSurviveANilResolver keeps the pre-resolver behaviour
// available: a connection nobody installed a resolver on reaches every peer
// with its own settings rather than panicking on a nil call.
func TestPeerCredentialsSurviveANilResolver(t *testing.T) {
	parent := &ServerConn{Opts: config.Connection{Server: "ubusql1", User: "sa", Password: "pw"}}
	if opts := parent.peerOptions("ubusql2"); opts.User != "sa" || opts.Password != "pw" {
		t.Errorf("peer options without a resolver = %+v, want the parent's credentials", opts)
	}
}

// TestPeerCredentialsAreInheritedByAPeer pins the two halves Peer composes to
// hand a peer the parent's resolver — peerCredentials() reading it back, and
// SetPeerCredentials installing it — so a peer's own peers resolve through the
// same table.
//
// It matters on the follow-the-primary path: the Object Explorer reaches a
// group's primary through Peer and reads on from there, and a resolver that
// stopped at the first hop would leave the second reaching a third instance
// with the primary's login rather than its own. Peer's own call cannot be
// driven from a unit test — it is behind Connect, which needs a real
// instance — so the wiring itself is live-verified.
func TestPeerCredentialsAreInheritedByAPeer(t *testing.T) {
	parent := &ServerConn{Opts: config.Connection{Server: "ubusql1", User: "sa", Password: "parent-pw"}}
	parent.SetPeerCredentials(func(server string) (config.Connection, bool) {
		if InstanceKey(server) == InstanceKey("ubusql3") {
			return config.Connection{Server: "ubusql3", User: "third_login", Password: "third-pw"}, true
		}
		return config.Connection{}, false
	})

	// Exactly what Peer does to the connection it just opened.
	peer := &ServerConn{Opts: config.Connection{Server: "ubusql2", User: "sa", Password: "parent-pw"}}
	peer.SetPeerCredentials(parent.peerCredentials())

	if got := peer.peerOptions("ubusql3"); got.User != "third_login" {
		t.Errorf("a peer resolved ubusql3 as %q, want the resolver's %q — the table did not reach the second hop",
			got.User, "third_login")
	}
}

// TestParentPeerOptionsIgnoreTheResolver pins the fallback Peer reaches for
// when a resolver's answer will not connect: the pre-resolver derivation,
// unchanged. A saved connection can carry a password the config key can no
// longer decrypt or a login that has since been dropped, and the parent
// connection's own credentials are live and working — so a resolver must
// never leave an instance less reachable than it was before one existed.
func TestParentPeerOptionsIgnoreTheResolver(t *testing.T) {
	parent := &ServerConn{Opts: config.Connection{
		Server: "ubusql1", Port: 1433, User: "sa", Password: "parent-pw",
		Database: "SalesDB", TrustServerCertificate: true,
	}}
	parent.SetPeerCredentials(func(string) (config.Connection, bool) {
		return config.Connection{Server: "ubusql2", User: "replica_login", Password: ""}, true
	})

	opts := parent.peerOptions("ubusql2")
	if opts.User != "replica_login" {
		t.Fatalf("peerOptions returned %q, want the resolver's answer as the preferred set", opts.User)
	}

	fallback := parent.parentPeerOptions("ubusql2")
	if fallback.User != "sa" || fallback.Password != "parent-pw" || !fallback.TrustServerCertificate {
		t.Errorf("fallback = %+v, want the parent's own credentials and transport settings", fallback)
	}
	if fallback.Server != "ubusql2" || fallback.Database != "" {
		t.Errorf("fallback = server %q database %q, want %q and no database", fallback.Server, fallback.Database, "ubusql2")
	}
	// Peer decides whether a second attempt is worth making by comparing the
	// two, so they have to differ here — and config.Connection has to stay
	// comparable for that test to compile at all.
	if fallback == opts {
		t.Error("the fallback is identical to the resolver's answer; the retry would be skipped")
	}
}

// The other side of that comparison: a resolver that answers with what the
// parent would have produced anyway costs no second connect attempt.
func TestParentPeerOptionsEqualTheResolverWhenItAgrees(t *testing.T) {
	opts := config.Connection{Server: "ubusql1", Port: 1433, User: "sa", Password: "pw"}
	parent := &ServerConn{Opts: opts}
	parent.SetPeerCredentials(func(string) (config.Connection, bool) {
		return opts, true
	})

	if parent.peerOptions("ubusql2") != parent.parentPeerOptions("ubusql2") {
		t.Error("an agreeing resolver produced a different option set, so Peer would retry for nothing")
	}
}

// TestPeerReturnsACachedFailureWithoutDialling pins the negative half of the
// peer cache. A blackholed instance costs the driver's whole connect timeout,
// so re-dialling it on every read is what made expanding an availability
// group's folders stall for a multiple of it.
func TestPeerReturnsACachedFailureWithoutDialling(t *testing.T) {
	sc := newTestConn("ubusql1")
	defer sc.Close()

	want := errors.New("dial tcp 192.168.178.98:1433: i/o timeout")
	sc.recordPeerFailure(InstanceKey("ubusql2"), want)

	// Called with a different spelling than the one recorded, on purpose: the
	// two have to agree through InstanceKey or the cache misses for the caller
	// that has the catalog's spelling.
	start := time.Now()
	peer, err := sc.Peer(context.Background(), "UBUSQL2,1433")
	if !errors.Is(err, want) {
		t.Fatalf("Peer = %v, %v; want the cached failure %v", peer, err, want)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the cached failure took %v to answer; that is a dial, not a cache hit", elapsed)
	}
}

// The entry has to expire, or a primary that came back stays unreachable in the
// UI until the connection is closed. Asserted through Peer: an aged entry means
// a real connect attempt, whose failure is the dial's, not the cached one.
func TestPeerRetriesOnceACachedFailureHasExpired(t *testing.T) {
	// Port 1 on loopback refuses immediately, so the retry this test forces
	// costs no timeout and reaches no other host.
	sc := newTestConn("ubusql1")
	defer sc.Close()

	key := InstanceKey("127.0.0.1:1")
	cached := errors.New("dial tcp 192.168.178.98:1433: i/o timeout")
	sc.recordPeerFailure(key, cached)
	sc.peerMu.Lock()
	f := sc.peerFails[key]
	f.at = time.Now().Add(-peerFailureTTL - time.Second)
	sc.peerFails[key] = f
	sc.peerMu.Unlock()

	_, err := sc.Peer(context.Background(), "127.0.0.1:1")
	if err == nil {
		t.Fatal("something answered on 127.0.0.1:1; the test cannot tell a retry from a cache hit")
	}
	if errors.Is(err, cached) {
		t.Errorf("Peer answered with the expired failure %v; the entry outlived peerFailureTTL", cached)
	}
	// And the fresh failure replaces it, so the next burst is collapsed too.
	sc.peerMu.Lock()
	fresh, ok := sc.peerFails[key]
	sc.peerMu.Unlock()
	if !ok || errors.Is(fresh.err, cached) {
		t.Error("the retry did not record its own failure; the expired entry is still what answers")
	}
}

// A cached failure that outlives the outage is what ForgetPeerFailure exists
// to cut short: the user fixes the network, hits Refresh, and is told the peer
// is down for the rest of peerFailureTTL. Asserted through Peer, since the
// point is that the next call dials rather than replaying the entry.
func TestForgetPeerFailureDropsTheEntry(t *testing.T) {
	// Port 1 on loopback refuses immediately, so the dial this forces costs no
	// timeout and reaches no other host.
	sc := newTestConn("ubusql1")
	defer sc.Close()

	cached := errors.New("dial tcp 192.168.178.98:1433: i/o timeout")
	sc.recordPeerFailure(InstanceKey("127.0.0.1:1"), cached)

	// Forgotten under a different spelling than the one recorded, on purpose:
	// File > Connect holds what the user typed, the cache what the catalog
	// reported, and only InstanceKey makes the two agree.
	sc.ForgetPeerFailure("127.0.0.1,1")

	_, err := sc.Peer(context.Background(), "127.0.0.1:1")
	if err == nil {
		t.Fatal("something answered on 127.0.0.1:1; the test cannot tell a dial from a cache hit")
	}
	if errors.Is(err, cached) {
		t.Errorf("Peer answered with the forgotten failure %v; ForgetPeerFailure did not drop it", cached)
	}
}

// Forgetting one instance must not clear the others: the burst the cache exists
// to collapse is several folders asking for the same unreachable primary, and a
// connect to a different replica is no evidence about that one.
func TestForgetPeerFailureLeavesOtherInstances(t *testing.T) {
	sc := newTestConn("ubusql1")
	defer sc.Close()

	want := errors.New("dial tcp 192.168.178.98:1433: i/o timeout")
	sc.recordPeerFailure(InstanceKey("ubusql2"), want)
	sc.recordPeerFailure(InstanceKey("ubusql3"), errors.New("other"))

	sc.ForgetPeerFailure("ubusql3")

	if _, err := sc.Peer(context.Background(), "ubusql2"); !errors.Is(err, want) {
		t.Errorf("Peer(ubusql2) = %v; want the still-cached failure %v", err, want)
	}
}

// A peer read chains — Object Explorer follows a group to its primary and reads
// on from there — so the failure to reach a third instance is recorded on the
// primary's connection, not on the one the user is refreshing. Two instances
// that have each opened the other are a cycle; without the seen set this test
// does not fail, it hangs.
func TestForgetPeerFailuresReachCachedPeers(t *testing.T) {
	sc := newTestConn("ubusql1")
	defer sc.Close()
	primary := newTestConn("ubusql2")
	defer primary.Close()

	sc.peers = map[string]*ServerConn{"ubusql2": primary}
	primary.peers = map[string]*ServerConn{"ubusql1": sc}
	primary.recordPeerFailure(InstanceKey("ubusql3"), errors.New("dial tcp: i/o timeout"))

	sc.ForgetPeerFailures()

	primary.peerMu.Lock()
	n := len(primary.peerFails)
	primary.peerMu.Unlock()
	if n != 0 {
		t.Errorf("the peer still holds %d cached failure(s); a chained read stays blackholed", n)
	}
}
