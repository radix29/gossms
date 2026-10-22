//go:build livedb

// Live check of registered peer credentials against a real Always On pair:
// a failed peer login must not retry with the parent's credentials.
//
//	go test -tags livedb ./internal/db/ -run TestLivePeer -v \
//	  -live-primary ubusql1.fritz.box -live-replica ubusql2 \
//	  -live-user sa -live-password PASS
//
// -live-replica is spelled as sys.availability_replicas.replica_server_name, as
// Peer receives it. Skipped without the flags.
//
// The resolver's registered address, port and login are authoritative.
package db

import (
	"context"
	"database/sql"
	"flag"
	"net/url"
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
	sc, err := Connect(context.Background(), liveOpts(*livePrimary, *liveUser, *livePassword), RoleExplorer)
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
	// A named instance is the URL's path ("HOST\INSTANCE" pasted into the host
	// is "invalid URL format"); a port stays on the host.
	host, instance, _ := strings.Cut(*liveReplica, `\`)
	if inst, port, ok := strings.Cut(instance, ","); ok {
		instance, host = inst, host+":"+port
	} else if h, port, ok := strings.Cut(host, ","); ok {
		host = h + ":" + port
	}
	dsn := url.URL{Scheme: "sqlserver", User: url.UserPassword(*liveUser, *livePassword),
		Host: host, Path: instance, RawQuery: "TrustServerCertificate=true"}
	db, err := sql.Open("sqlserver", dsn.String())
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

func TestLivePeerDoesNotFallBackToTheParentCredentials(t *testing.T) {
	parent := liveParent(t)
	createLivePeerLogin(t)

	broken := liveOpts(*liveReplica, livePeerLogin, "wrong-password")
	parent.SetPeerCredentials(func(server string) (config.Connection, bool) {
		if config.InstanceKey(server) == config.InstanceKey(*liveReplica) {
			return broken, true
		}
		return config.Connection{}, false
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	peer, err := parent.Peer(ctx, *liveReplica)
	if err == nil || peer != nil {
		t.Fatalf("Peer(%q) = %v, %v; want the registered login's failure, not a fallback", *liveReplica, peer, err)
	}
	again, err := parent.Peer(ctx, *liveReplica)
	if err == nil || again != nil {
		t.Errorf("the second Peer(%q) = %v, %v; want the cached failure", *liveReplica, again, err)
	}
}

// A registered peer with a working login remains reachable.
func TestLivePeerPrefersAWorkingResolverHit(t *testing.T) {
	parent := liveParent(t)
	createLivePeerLogin(t)

	parent.SetPeerCredentials(func(server string) (config.Connection, bool) {
		if config.InstanceKey(server) == config.InstanceKey(*liveReplica) {
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
	if !strings.EqualFold(peer.Server.Info().Login, livePeerLogin) {
		t.Errorf("the peer is authenticated as %q, want the resolver's %q", peer.Server.Info().Login, livePeerLogin)
	}
}
