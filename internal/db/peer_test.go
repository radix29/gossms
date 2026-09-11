package db

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/config"
)

// newTestConn builds a ServerConn with Connect's lifetime plumbing but no
// gosmo.Server; Close is nil-safe.
func newTestConn(server string) *ServerConn {
	ctx, cancel := context.WithCancel(context.Background())
	return &ServerConn{Opts: config.Connection{Server: server}, ctx: ctx, cancel: cancel}
}

// closePeers writes ServerConn.closed after releasing peerMu, which looks racy
// against the lookup's IsOpen. It isn't: every IsOpen on a cached peer is under
// peerMu, and closePeers takes it before closing any peer; afterwards sc.peers
// is nil.
//
// Meaningful only under -race.
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

// A named database must be openable or connect fails at ping ("Cannot open
// database %q that was requested by the login", verified live), and a secondary
// may not open the user's database, so Peer must not carry it. Peer's connect
// needs a real instance, so this tests the options it builds.
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
	// Everything else must survive, or the peer authenticates differently from
	// its parent.
	if opts.User != "sa" || opts.Password != "pw" || opts.Port != 1433 || !opts.TrustServerCertificate {
		t.Errorf("peer options lost credentials or transport settings: %+v", opts)
	}
}

// Peer cache and credential lookup share this normalizer. The catalog reports
// "HOST\INSTANCE", the user types "host,1433"; keeping case or the default port
// would miss.
func TestInstanceKeyNormalizesSpellings(t *testing.T) {
	groups := [][]string{
		{"UBUSQL2", "ubusql2", "ubusql2,1433", "  ubusql2  ", "UbuSQL2:1433"},
		{"HOST\\INST", "host\\inst", "HOST\\inst,1433", "host\\inst,1500"},
		{"win10cli,55253", "WIN10CLI:55253"},
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
	// Distinct instances must not share a key (that hands over a login). A
	// named instance isn't its host's default; without a name, the port
	// distinguishes them (win10cli's SQL2017 at "win10cli,55253").
	for _, pair := range [][2]string{
		{"host", "host\\inst"},
		{"host", "host,55253"},
		{"host,1500", "host,55253"},
	} {
		if InstanceKey(pair[0]) == InstanceKey(pair[1]) {
			t.Errorf("InstanceKey collapses %q and %q onto %q", pair[0], pair[1], InstanceKey(pair[0]))
		}
	}
}

// The dialog stores the port in Port, so keys must fold it in, or win10cli's
// two instances share an entry.
func TestConnectionAddressFoldsInTheDialogPort(t *testing.T) {
	for _, tc := range []struct {
		conn config.Connection
		want string
	}{
		{config.Connection{Server: "win10cli", Port: 55253}, InstanceKey("win10cli,55253")},
		{config.Connection{Server: "win10cli", Port: 1433}, InstanceKey("win10cli")},
		{config.Connection{Server: "win10cli"}, InstanceKey("win10cli")},
		// A port in the address wins over the dialog's, as in Connect.
		{config.Connection{Server: "win10cli,55253", Port: 1433}, InstanceKey("win10cli,55253")},
		{config.Connection{Server: "win10cli\\sql2017", Port: 55253}, InstanceKey("win10cli\\sql2017")},
	} {
		if got := InstanceKey(ConnectionAddress(tc.conn)); got != tc.want {
			t.Errorf("InstanceKey(ConnectionAddress(%q port %d)) = %q, want %q",
				tc.conn.Server, tc.conn.Port, got, tc.want)
		}
	}
}

// A replica needing a different login must use its own saved credentials. The
// requested name differs in spelling from the saved entry ("UBUSQL2\PROD" vs
// "ubusql2\prod,1433"), so skipping InstanceKey would miss.
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
				Database: "OtherDB", Encrypt: config.EncryptStrict,
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
	if opts.Encrypt != config.EncryptStrict {
		t.Error("peer options dropped the saved connection's Encrypt setting")
	}
	// Server is the catalog's name, not the saved spelling.
	if opts.Server != "UBUSQL2\\PROD" {
		t.Errorf("peer options name server %q, want the catalog's %q", opts.Server, "UBUSQL2\\PROD")
	}
	// The database is blanked here too; the saved one is just as unopenable on
	// a secondary.
	if opts.Database != "" {
		t.Errorf("peer options carry Database %q from the saved connection", opts.Database)
	}
}

// An instance with no saved connection uses the parent's settings.
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

// With no resolver installed, peers use sc's own settings rather than
// panicking.
func TestPeerCredentialsSurviveANilResolver(t *testing.T) {
	parent := &ServerConn{Opts: config.Connection{Server: "ubusql1", User: "sa", Password: "pw"}}
	if opts := parent.peerOptions("ubusql2"); opts.User != "sa" || opts.Password != "pw" {
		t.Errorf("peer options without a resolver = %+v, want the parent's credentials", opts)
	}
}

// Peer hands the parent's resolver to each peer (peerCredentials, then
// SetPeerCredentials) so the next hop resolves through the same table rather
// than using the primary's login. Peer's own call needs a real instance, so the
// wiring is verified live.
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

// The fallback is the pre-resolver derivation, unchanged: a resolver must never
// leave an instance less reachable than the parent's working credentials.
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
	// Peer compares the two to decide on a second attempt, so they must differ
	// here — and config.Connection must stay comparable.
	if fallback == opts {
		t.Error("the fallback is identical to the resolver's answer; the retry would be skipped")
	}
}

// A resolver answer equal to the parent derivation costs no second attempt.
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

// A blackholed instance costs the whole connect timeout, so a cached failure
// must be returned without dialling.
func TestPeerReturnsACachedFailureWithoutDialling(t *testing.T) {
	sc := newTestConn("ubusql1")
	defer sc.Close()

	want := errors.New("dial tcp 192.168.178.98:1433: i/o timeout")
	sc.recordPeerFailure(InstanceKey("ubusql2"), want)

	// A different spelling than recorded: both must agree through InstanceKey.
	start := time.Now()
	peer, err := sc.Peer(context.Background(), "UBUSQL2,1433")
	if !errors.Is(err, want) {
		t.Fatalf("Peer = %v, %v; want the cached failure %v", peer, err, want)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the cached failure took %v to answer; that is a dial, not a cache hit", elapsed)
	}
}

// Entries expire, or a recovered primary stays unreachable. An aged entry means
// a real dial, whose failure is the dial's own.
func TestPeerRetriesOnceACachedFailureHasExpired(t *testing.T) {
	// Loopback port 1 refuses immediately.
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
	// The fresh failure replaces it.
	sc.peerMu.Lock()
	fresh, ok := sc.peerFails[key]
	sc.peerMu.Unlock()
	if !ok || errors.Is(fresh.err, cached) {
		t.Error("the retry did not record its own failure; the expired entry is still what answers")
	}
}

// ForgetPeerFailure makes the next call dial rather than replay the entry until
// the TTL.
func TestForgetPeerFailureDropsTheEntry(t *testing.T) {
	// Loopback port 1 refuses immediately.
	sc := newTestConn("ubusql1")
	defer sc.Close()

	cached := errors.New("dial tcp 192.168.178.98:1433: i/o timeout")
	sc.recordPeerFailure(InstanceKey("127.0.0.1:1"), cached)

	// A different spelling on purpose: Connect holds the typed name, the cache
	// the catalog's; InstanceKey reconciles them.
	sc.ForgetPeerFailure("127.0.0.1,1")

	_, err := sc.Peer(context.Background(), "127.0.0.1:1")
	if err == nil {
		t.Fatal("something answered on 127.0.0.1:1; the test cannot tell a dial from a cache hit")
	}
	if errors.Is(err, cached) {
		t.Errorf("Peer answered with the forgotten failure %v; ForgetPeerFailure did not drop it", cached)
	}
}

// Forgetting one instance must not clear others; a connect to one replica says
// nothing about another.
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

// Reads chain, so a third instance's failure is recorded on the primary's
// connection. Two instances that opened each other form a cycle; without seen
// this hangs.
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
