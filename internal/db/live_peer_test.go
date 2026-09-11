//go:build livedb

// Live check of Peer's fallback to parentPeerOptions against a real Always On
// pair: a resolver hit that won't connect (undecryptable saved password,
// dropped login) must never leave an instance less reachable than without saved
// credentials. peer_test.go pins the option derivation; only two real instances
// prove the second Connect happens and works.
//
//	go test -tags livedb ./internal/db/ -run TestLivePeer -v \
//	  -live-primary ubusql1.fritz.box -live-replica ubusql2 \
//	  -live-user sa -live-password PASS
//
// -live-replica is spelled as sys.availability_replicas.replica_server_name, as
// Peer receives it. Skipped without the flags.
package db

import (
	"context"
	"database/sql"
	"flag"
	"strings"
	"testing"
	"time"

	_ "github.com/microsoft/go-mssqldb"
	"github.com/radix29/gossms/internal/config"
)

var (
	livePrimary  = flag.String("live-primary", "", "instance to connect to as the parent")
	liveReplica  = flag.String("live-replica", "", "peer instance name, as the catalog reports it")
	liveUser     = flag.String("live-user", "", "login for both instances")
	livePassword = flag.String("live-password", "", "password for -live-user")
)

// livePeerLogin is the throwaway login the resolver's saved connection names.
const livePeerLogin = "gossms_peer_probe"

// liveParent opens the parent connection each test starts from.
func liveParent(t *testing.T) *ServerConn {
	t.Helper()
	if *livePrimary == "" || *liveReplica == "" || *liveUser == "" {
		t.Skip("no -live-primary/-live-replica/-live-user given")
	}
	sc, err := Connect(liveOpts(*livePrimary, *liveUser, *livePassword))
	if err != nil {
		t.Fatalf("connect to the parent %s: %v", *livePrimary, err)
	}
	t.Cleanup(sc.Close)
	return sc
}

func liveOpts(server, user, password string) config.Connection {
	return config.Connection{
		Server:                 server,
		User:                   user,
		Password:               password,
		TrustServerCertificate: true,
	}
}

// liveReplicaExec runs one statement on the replica as -live-user. Not routed
// through Peer, which is what's under test.
func liveReplicaExec(t *testing.T, stmt string) {
	t.Helper()
	db, err := sql.Open("sqlserver", "sqlserver://"+*liveUser+":"+*livePassword+"@"+*liveReplica+"?TrustServerCertificate=true")
	if err != nil {
		t.Fatalf("open %s: %v", *liveReplica, err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		t.Fatalf("exec %q on %s: %v", stmt, *liveReplica, err)
	}
}

// createLivePeerLogin creates the throwaway login and drops it after the test.
func createLivePeerLogin(t *testing.T) {
	t.Helper()
	liveReplicaExec(t, "IF SUSER_ID('"+livePeerLogin+"') IS NOT NULL DROP LOGIN ["+livePeerLogin+"]")
	liveReplicaExec(t, "CREATE LOGIN ["+livePeerLogin+"] WITH PASSWORD = 'g0ssms-Peer-Probe!', CHECK_POLICY = OFF")
	t.Cleanup(func() {
		liveReplicaExec(t, "IF SUSER_ID('"+livePeerLogin+"') IS NOT NULL DROP LOGIN ["+livePeerLogin+"]")
	})
}

// A resolver answer with a wrong password must still yield a working peer via
// the parent's credentials (Login proves which).
func TestLivePeerFallsBackToTheParentCredentials(t *testing.T) {
	createLivePeerLogin(t)
	parent := liveParent(t)

	broken := liveOpts(*liveReplica, livePeerLogin, "wrong-password")
	parent.SetPeerCredentials(func(server string) (config.Connection, bool) {
		if InstanceKey(server) == InstanceKey(*liveReplica) {
			return broken, true
		}
		return config.Connection{}, false
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	peer, err := parent.Peer(ctx, *liveReplica)
	if err != nil {
		t.Fatalf("Peer(%q) = %v, want the parentPeerOptions retry to connect", *liveReplica, err)
	}
	if peer == parent {
		t.Fatal("Peer returned the parent connection; the replica name was read as self")
	}
	if !strings.EqualFold(peer.Login, *liveUser) {
		t.Errorf("the peer is authenticated as %q, want the parent's %q — the retry did not use parentPeerOptions",
			peer.Login, *liveUser)
	}
	if name := peer.Server.Name(); !strings.EqualFold(name, InstanceKey(*liveReplica)) &&
		!strings.EqualFold(name, *liveReplica) {
		t.Errorf("the peer is connected to %q, want %q", name, *liveReplica)
	}
	// The fallback peer is cached and credentialled like any other, or the next
	// expansion repeats the failed attempt.
	again, err := parent.Peer(ctx, *liveReplica)
	if err != nil || again != peer {
		t.Errorf("the second Peer(%q) = %p, %v; want the cached %p", *liveReplica, again, err, peer)
	}
	if parent.peerCredentials() == nil || peer.peerCredentials() == nil {
		t.Error("the resolver did not reach the peer opened by the fallback")
	}
}

// A working resolver hit is used as given, so the fallback isn't the only path;
// otherwise a Peer ignoring the resolver would pass the test above.
func TestLivePeerPrefersAWorkingResolverHit(t *testing.T) {
	createLivePeerLogin(t)
	parent := liveParent(t)

	parent.SetPeerCredentials(func(server string) (config.Connection, bool) {
		if InstanceKey(server) == InstanceKey(*liveReplica) {
			return liveOpts(*liveReplica, livePeerLogin, "g0ssms-Peer-Probe!"), true
		}
		return config.Connection{}, false
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	peer, err := parent.Peer(ctx, *liveReplica)
	if err != nil {
		t.Fatalf("Peer(%q) = %v, want the resolver's saved credentials to connect", *liveReplica, err)
	}
	if !strings.EqualFold(peer.Login, livePeerLogin) {
		t.Errorf("the peer is authenticated as %q, want the resolver's %q", peer.Login, livePeerLogin)
	}
}
