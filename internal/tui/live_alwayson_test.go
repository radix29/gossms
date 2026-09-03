//go:build livedb

// Live verification of the two opposite answers gossms gives when an
// availability group's primary replica cannot be reached from the client:
// resolveAGView degrades to the local secondary's partial view and keeps the
// Object Explorer branch expandable, while agOnPrimary — every Properties page,
// dialog and menu action — treats the same condition as a hard error rather
// than offering rows it could not save.
//
// Neither is reachable from a unit test: both sit behind db.ServerConn.Peer,
// which connects. The condition is produced the way it actually happens in
// production — a login that exists on the instance the user connected to and
// not on the replica holding the primary role — so Peer's resolver hit *and*
// its parentPeerOptions retry both fail against a live, healthy group.
//
//	go test -tags livedb ./internal/tui/ -run TestLiveAG -v \
//	  -live-secondary ubusql1.fritz.box -live-sa sa -live-sa-password PASS \
//	  -live-ag AAG1
//
// -live-secondary must name a replica that is *not* currently the primary; the
// tests skip if it is. Creates and drops one throwaway login on that instance
// and touches nothing else. Skipped entirely without the flags.
package tui

import (
	"context"
	"database/sql"
	"flag"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"

	_ "github.com/microsoft/go-mssqldb"
)

var (
	liveSecondary = flag.String("live-secondary", "", "a replica that is currently a secondary")
	liveSA        = flag.String("live-sa", "", "sysadmin login on -live-secondary")
	liveSAPass    = flag.String("live-sa-password", "", "password for -live-sa")
	liveAG        = flag.String("live-ag", "", "availability group name")

	livePrimaryBlackholed = flag.Bool("live-primary-blackholed", false,
		"the primary's SQL port is firewalled off from this host (see TestLiveAGHandlesABlackholedPrimary)")
)

// liveAGProbeLogin is created on the secondary only. Its password never leaves
// this file: it exists for the length of one test.
const (
	liveAGProbeLogin = "gossms_ag_probe"
	liveAGProbePass  = "g0ssms-AG-Probe!"
)

// liveAGSA opens a plain driver connection to the secondary as the sysadmin,
// for the fixture DDL. gosmo has no general Exec and the point here is the
// two resolvers, not the DDL.
func liveAGSA(t *testing.T) *sql.DB {
	t.Helper()
	if *liveSecondary == "" || *liveSA == "" || *liveAG == "" {
		t.Skip("no -live-secondary/-live-sa/-live-ag given")
	}
	raw, err := sql.Open("sqlserver", "sqlserver://"+*liveSA+":"+*liveSAPass+"@"+*liveSecondary+"?TrustServerCertificate=true")
	if err != nil {
		t.Fatalf("open %s: %v", *liveSecondary, err)
	}
	t.Cleanup(func() { raw.Close() })
	return raw
}

// liveAGConn connects as user and returns the connection plus a bounded
// context, the way a loader runs.
func liveAGConn(t *testing.T, user, password string) (*db.ServerConn, context.Context) {
	t.Helper()
	sc, err := db.Connect(config.Connection{
		Server:                 *liveSecondary,
		AuthMethod:             config.AuthSQLServer,
		User:                   user,
		Password:               password,
		TrustServerCertificate: true,
	})
	if err != nil {
		t.Fatalf("connect %s as %s: %v", *liveSecondary, user, err)
	}
	t.Cleanup(sc.Close)
	ctx, cancel := context.WithTimeout(sc.Context(), 90*time.Second)
	t.Cleanup(cancel)
	return sc, ctx
}

// liveAGUnreachablePrimary sets the fixture up and hands back a connection to
// the secondary whose credentials cannot reach the primary, plus the primary's
// name. It skips rather than fails when -live-secondary turns out to hold the
// primary role: which node is primary is the cluster's choice, not the test's.
func liveAGUnreachablePrimary(t *testing.T) (*db.ServerConn, context.Context, string) {
	t.Helper()
	raw := liveAGSA(t)

	sa, saCtx := liveAGConn(t, *liveSA, *liveSAPass)
	ag, err := sa.Server.AvailabilityGroupByNameContext(saCtx, *liveAG)
	if err != nil {
		t.Fatalf("read availability group %q from %s: %v", *liveAG, *liveSecondary, err)
	}
	if ag.IsLocalPrimary() {
		t.Skipf("%s currently holds the primary role for %q; point -live-secondary at the other replica", *liveSecondary, *liveAG)
	}
	primary := ag.PrimaryReplicaServerName
	if primary == "" {
		t.Fatalf("%s reports no primary replica for %q — the group is not healthy", *liveSecondary, *liveAG)
	}

	exec := func(q string) {
		t.Helper()
		if _, err := raw.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("exec %.60q: %v", q, err)
		}
	}
	exec("IF SUSER_ID('" + liveAGProbeLogin + "') IS NOT NULL DROP LOGIN [" + liveAGProbeLogin + "]")
	exec("CREATE LOGIN [" + liveAGProbeLogin + "] WITH PASSWORD = '" + liveAGProbePass + "', CHECK_POLICY = OFF")
	t.Cleanup(func() {
		if _, err := raw.ExecContext(context.Background(), "IF SUSER_ID('"+liveAGProbeLogin+"') IS NOT NULL DROP LOGIN ["+liveAGProbeLogin+"]"); err != nil {
			t.Errorf("drop login %s: %v", liveAGProbeLogin, err)
		}
	})
	// What the app's own reads need, and nothing that would let this login
	// change anything: the point is an unreachable primary, not a rights test.
	exec("GRANT VIEW SERVER STATE, VIEW ANY DEFINITION, VIEW ANY DATABASE TO [" + liveAGProbeLogin + "]")

	sc, ctx := liveAGConn(t, liveAGProbeLogin, liveAGProbePass)
	// The fixture is only a fixture if the primary really is out of reach.
	if _, err := sc.Peer(ctx, primary); err == nil {
		t.Fatalf("the probe login reached the primary %s; it exists there too, so nothing is being tested", primary)
	}
	return sc, ctx, primary
}

// TestLiveAGViewDegradesWhenThePrimaryIsUnreachable is the Object Explorer
// half: the branch still expands, off the secondary's own partial catalog, and
// says so in a trailing note.
func TestLiveAGViewDegradesWhenThePrimaryIsUnreachable(t *testing.T) {
	sc, ctx, primary := liveAGUnreachablePrimary(t)
	l := loaderCtx{ctx: ctx, sc: sc}

	view, err := resolveAGView(l, *liveAG)
	if err != nil {
		t.Fatalf("resolveAGView = %v, want the local view rather than an error", err)
	}
	if view.followed {
		t.Error("resolveAGView reports it read from the primary, which is unreachable")
	}
	if view.unreachable != primary {
		t.Errorf("view.unreachable = %q, want the primary %q — the caller has nothing to show the user", view.unreachable, primary)
	}
	if view.ag == nil {
		t.Fatal("resolveAGView returned no group; the local view was dropped along with the peer")
	}

	node := l.node("Availability Replicas", NodeAvailabilityReplicas, "", *liveAG, "")
	node.data.AGName = *liveAG
	children, err := loadAvailabilityReplicasChildren(l, node)
	if err != nil {
		t.Fatalf("loadAvailabilityReplicasChildren = %v, want the branch to stay expandable", err)
	}
	var note *explorerNode
	replicas := 0
	for _, c := range children {
		if c.data.Type == NodeAvailabilityReplica {
			replicas++
			continue
		}
		note = c
	}
	if replicas < 2 {
		t.Errorf("the folder listed %d replicas, want the group's whole membership — sys.availability_replicas is cluster-wide and readable on a secondary", replicas)
	}
	if note == nil {
		t.Fatal("no trailing note; a half-filled replica list reads as a fault")
	}
	t.Logf("note: %q", note.label)
	if note.data.Type != NodeError || !strings.Contains(note.label, primary) || !strings.Contains(note.label, "partial") {
		t.Errorf("note = %q (type %v), want a NodeError naming %q as unreachable", note.label, note.data.Type, primary)
	}
}

// TestLiveAGOnPrimaryFailsWhenThePrimaryIsUnreachable is the opposite half:
// a Properties page must not open on a secondary's blanks, so the same
// condition is an error naming the replica that could not be reached.
func TestLiveAGOnPrimaryFailsWhenThePrimaryIsUnreachable(t *testing.T) {
	sc, ctx, primary := liveAGUnreachablePrimary(t)

	ag, err := agOnPrimary(ctx, sc, *liveAG)
	if err == nil {
		t.Fatalf("agOnPrimary returned a group (%v) read from a secondary; its rows would be blanks Apply could not save", ag)
	}
	t.Logf("agOnPrimary error: %v", err)
	if !strings.Contains(err.Error(), primary) {
		t.Errorf("agOnPrimary error = %q, want it to name the primary %q the user has to reach", err, primary)
	}
}

// TestLiveAGHandlesABlackholedPrimary is the network flavour of the same
// condition, and the one the credential fixture above cannot reach: packets to
// the primary's SQL port are dropped, so Peer does not fail fast — it hangs to
// the driver's connect timeout. Both callers must still reach their own answer
// rather than the loader goroutine's context deadline, which is what would
// leave an Object Explorer branch stuck on "Loading...".
//
// Needs an out-of-band firewall rule on the primary blocking this host, e.g.
// on the primary: iptables -I INPUT -s <client> -p tcp --dport 1433 -j DROP.
// Run with -live-primary-blackholed once it is in place; the flag exists so
// the test never silently passes on an unblocked cluster.
func TestLiveAGHandlesABlackholedPrimary(t *testing.T) {
	if !*livePrimaryBlackholed {
		t.Skip("no -live-primary-blackholed; the primary is reachable")
	}
	sc, ctx := liveAGConn(t, *liveSA, *liveSAPass)
	l := loaderCtx{ctx: ctx, sc: sc}

	start := time.Now()
	view, err := resolveAGView(l, *liveAG)
	if err != nil {
		t.Fatalf("resolveAGView = %v after %v, want the local view rather than an error", err, time.Since(start))
	}
	t.Logf("resolveAGView degraded after %v", time.Since(start))
	if view.unreachable == "" || view.followed {
		t.Errorf("view = unreachable %q followed %v, want the primary flagged unreachable", view.unreachable, view.followed)
	}
	if ctx.Err() != nil {
		t.Fatal("the loader context expired first; the branch would show a timeout, not a partial view")
	}

	// The second call is the one the user actually waits on: a group has three
	// folders and a Properties dialog behind it, all asking for the same
	// primary. Peer's failure cache is what keeps that from costing another
	// full connect timeout each — the stall this test was written after.
	start = time.Now()
	_, err = agOnPrimary(ctx, sc, *liveAG)
	second := time.Since(start)
	if err == nil {
		t.Fatal("agOnPrimary succeeded with the primary blackholed")
	}
	t.Logf("agOnPrimary failed after %v: %v", second, err)
	if !strings.Contains(err.Error(), view.unreachable) {
		t.Errorf("error = %q, want it to name the primary %q", err, view.unreachable)
	}
	if second > time.Second {
		t.Errorf("the second call took %v; a cached failure answers at once, a re-dial pays the connect timeout", second)
	}
}

// The control both tests need: with credentials that *do* reach the primary,
// the same two calls follow it. Without this a resolver that never reached a
// primary at all would pass everything above.
func TestLiveAGFollowsAReachablePrimary(t *testing.T) {
	if *liveSecondary == "" || *liveSA == "" || *liveAG == "" {
		t.Skip("no -live-secondary/-live-sa/-live-ag given")
	}
	sc, ctx := liveAGConn(t, *liveSA, *liveSAPass)
	l := loaderCtx{ctx: ctx, sc: sc}

	view, err := resolveAGView(l, *liveAG)
	if err != nil {
		t.Fatalf("resolveAGView = %v", err)
	}
	if view.unreachable != "" {
		t.Fatalf("view.unreachable = %q with working credentials", view.unreachable)
	}
	local, err := sc.Server.AvailabilityGroupByNameContext(ctx, *liveAG)
	if err != nil {
		t.Fatalf("AvailabilityGroupByNameContext: %v", err)
	}
	if !local.IsLocalPrimary() && !view.followed {
		t.Error("view.followed is false on a secondary; the primary was never followed")
	}
	if _, err := agOnPrimary(ctx, sc, *liveAG); err != nil {
		t.Errorf("agOnPrimary = %v, want the group read through its primary", err)
	}
}
