package tui

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// The Object Explorer wiring for server ▸ Replication. Statement text is
// gosmo's to test; these pin what the tree does with the answers — where the
// folder goes and on which edition, and the rows Local Publications adds to
// explain what it could not list.

// Match text for gosmo's replication reads. The two sys.databases reads differ
// by the parentheses LocalPublications puts around its flags.
const (
	replPublishedDBsRead = "WHERE (is_published = 1 OR is_merge_published = 1)"
	replTablesRead       = "OBJECT_ID(@p1, N'U')"
	replPublicationsRead = "FROM   dbo.syspublications"
	replDistributorRead  = "FROM sys.servers WHERE is_distributor = 1"
	replDatabasesRead    = "WHERE  is_published = 1 OR is_merge_published = 1 OR is_distributor = 1"
)

// replPublishedDB answers LocalPublications' database list with one online
// database.
func replPublishedDB(name string) fakeResponse {
	return fakeResponse{match: replPublishedDBsRead, cols: 10, rows: [][]driver.Value{{
		name, int64(5), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS",
		"SQL_Latin1_General_CP1_CI_AS", false, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), int64(0),
	}}}
}

// replTransactionalPub answers dbName's publication tables with one
// transactional publication and no merge table.
func replTransactionalPub(dbName, pub string) []fakeResponse {
	return []fakeResponse{
		{match: replTablesRead, db: dbName, cols: 2, rows: [][]driver.Value{{true, false}}},
		{match: replPublicationsRead, db: dbName, cols: 23, rows: [][]driver.Value{{
			int64(1), pub, "", int64(0), int64(0), int64(1), int64(336), int64(3),
			true, true, false, false, int64(1),
			true, "", false, "", "",
			false, true, false, false, false,
		}}},
	}
}

// replInfo answers ReplicationInfo: the distributor's name (nil for none), not
// a distributor itself, and the given sys.databases flag rows (name,
// published, merge published, distribution, readable).
func replInfo(distributor any, dbs ...[]driver.Value) []fakeResponse {
	return []fakeResponse{
		{match: replDistributorRead, cols: 4, rows: [][]driver.Value{{distributor, false, false, false}}},
		{match: replDatabasesRead, cols: 5, rows: dbs},
	}
}

func loadLabels(t *testing.T, sc *db.ServerConn, load childLoader) ([]string, []*explorerNode) {
	t.Helper()
	children, err := load(loaderCtx{ctx: context.Background(), sc: sc}, &explorerNode{})
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, c := range children {
		labels = append(labels, c.label)
	}
	return labels, children
}

func TestLocalPublicationsListsAndExplains(t *testing.T) {
	for _, tc := range []struct {
		name      string
		responses []fakeResponse
		want      []string
	}{
		{"a publication, and a published database the login cannot read",
			slices.Concat([]fakeResponse{replPublishedDB("SalesDB")}, replTransactionalPub("SalesDB", "pubA"),
				replInfo("SRV",
					[]driver.Value{"HiddenDB", false, true, false, false},
					[]driver.Value{"SalesDB", true, false, false, true})),
			[]string{"[SalesDB]: pubA", "[HiddenDB]: not visible (offline, or no access)"}},
		{"no distributor and nothing published",
			slices.Concat([]fakeResponse{{match: replPublishedDBsRead, cols: 10}}, replInfo(nil)),
			[]string{replicationNotConfiguredLabel}},
		// A distributor and nothing published is an ordinary empty folder:
		// the instance can publish, it just does not.
		{"a distributor and nothing published",
			slices.Concat([]fakeResponse{{match: replPublishedDBsRead, cols: 10}}, replInfo("SRV")),
			nil},
		// The explanation is all ReplicationInfo is read for; its failure
		// must not cost the list.
		{"configuration read refused",
			slices.Concat([]fakeResponse{replPublishedDB("SalesDB")}, replTransactionalPub("SalesDB", "pubA"),
				[]fakeResponse{{match: replDistributorRead, err: errors.New("refused")}}),
			[]string{"[SalesDB]: pubA"}},
	} {
		sc, _ := newFakeConn(t, tc.responses...)
		got, children := loadLabels(t, sc, loadLocalPublicationsChildren)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: children = %q, want %q", tc.name, got, tc.want)
			continue
		}
		if len(children) > 0 && children[0].data.Type == NodePublication {
			d := children[0].data
			if d.DBName != "SalesDB" || d.Name != "pubA" || d.ReplPubType != gosmo.PublicationTransactional {
				t.Errorf("%s: publication node = %+v, want SalesDB/pubA transactional", tc.name, d)
			}
		}
		for _, c := range children[min(1, len(children)):] {
			if c.data.Type != NodeError {
				t.Errorf("%s: explanation row %q is %v, want a NodeError row", tc.name, c.label, c.data.Type)
			}
		}
	}
}

func TestReplicationFollowsServerObjectsExceptOnAzureSQLDatabase(t *testing.T) {
	folders := func(sc *db.ServerConn) []string {
		got, _ := loadLabels(t, sc, loadServerChildren)
		return got
	}
	for _, tc := range []struct {
		name    string
		edition int64
		version string
		shown   bool
	}{
		{"Developer", 3, "16.0.4085.2", true},
		{"Managed Instance", 8, "12.0.2000.8", true},
		{"Azure SQL Database", 5, "12.0.2000.8", false},
	} {
		got := folders(newFakeConnEdition(t, tc.edition, tc.version))
		i := slices.Index(got, "Replication")
		switch {
		case tc.shown && (i < 1 || got[i-1] != "Server Objects"):
			t.Errorf("%s: server folders = %q, want Replication right after Server Objects", tc.name, got)
		case !tc.shown && i >= 0:
			t.Errorf("%s: server folders = %q, want no Replication — no replication catalog there", tc.name, got)
		}
	}
	// No connection info: shown, per the fail-open rule.
	if got := folders(nil); !slices.Contains(got, "Replication") {
		t.Errorf("no connection: server folders = %q, want Replication", got)
	}
}

func TestReplicationLabels(t *testing.T) {
	if got, want := publicationLabel("SalesDB", "pubA"), "[SalesDB]: pubA"; got != want {
		t.Errorf("publicationLabel = %q, want %q", got, want)
	}
	if got, want := localSubscriptionLabel("SubDB", "WIN10CLI", "SalesDB", "pubA"),
		"[SubDB] - [WIN10CLI].[SalesDB]: pubA"; got != want {
		t.Errorf("localSubscriptionLabel = %q, want %q", got, want)
	}
}

// One database can subscribe to two publications of one name on different
// publishers; their nodes must not be taken for each other on a reload.
func TestLocalSubscriptionsOfOneNameAreDifferentNodes(t *testing.T) {
	a := nodeData{Type: NodeLocalSubscription, DBName: "SubDB", Name: "pubA", ReplPublisher: "SRV1", ReplPublisherDB: "SalesDB"}
	b := a
	b.ReplPublisher = "SRV2"
	if a.key() == b.key() {
		t.Error("subscriptions to SRV1's and SRV2's pubA share a nodeKey")
	}
	b = a
	b.ReplPublisherDB = "OtherDB"
	if a.key() == b.key() {
		t.Error("subscriptions to two publisher databases' pubA share a nodeKey")
	}
}

func TestPublicationIconShowsItsKind(t *testing.T) {
	kinds := []gosmo.PublicationType{gosmo.PublicationTransactional, gosmo.PublicationSnapshot,
		gosmo.PublicationPeerToPeer, gosmo.PublicationMerge}
	for _, style := range []config.IconStyle{config.IconStyleEmoji, config.IconStyleSymbols, config.IconStylePortable} {
		seen := map[rune]gosmo.PublicationType{}
		for _, k := range kinds {
			g := nodeIcon(nodeData{Type: NodePublication, ReplPubType: k}, style, false)
			if prev, dup := seen[g]; dup {
				t.Errorf("style %v: %v and %v publications share the glyph %q", style, prev, k, g)
			}
			seen[g] = k
		}
	}
}

// TestReplicationMenusOpenPropertiesAndWithholdTheMonitor. The two leaves'
// Properties open their dialogs (W12); Launch Replication Monitor stays
// withheld, with its note, until W13 builds it.
func TestReplicationMenusOpenPropertiesAndWithholdTheMonitor(t *testing.T) {
	var newQuery, refresh controls.MenuItem
	find := func(items []controls.MenuItem, label string) *controls.MenuItem {
		for i := range items {
			if items[i].Label == label {
				return &items[i]
			}
		}
		return nil
	}
	pub := publicationMenuItems(nil, nil, &explorerNode{}, newQuery, refresh)
	sub := localSubscriptionMenuItems(nil, nil, &explorerNode{}, newQuery, refresh)
	for name, items := range map[string][]controls.MenuItem{"publication": pub, "subscription": sub} {
		it := find(items, "Properties...")
		if it == nil || it.Action == nil || (it.Enabled != nil && !it.Enabled()) || it.Note != "" {
			t.Errorf("%s: Properties... missing or withheld: %+v", name, it)
		}
	}
	it := find(pub, "Launch Replication Monitor")
	if it == nil || it.Enabled == nil || it.Enabled() || it.Note == "" {
		t.Errorf("Launch Replication Monitor enabled or unexplained — it has nothing to open yet")
	}
}
