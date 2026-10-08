package tui

import (
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// explorer_replication.go is the server's Replication folder, as SSMS hangs it
// after Server Objects: Local Publications, one leaf per publication of every
// published database here, and Local Subscriptions, one leaf per subscription
// a database here holds to a publication anywhere. Read-only
// (docs/decisions.md § Replication): nothing here creates, alters or drops
// replication objects.
//
// Both folders are listed on every instance but Azure SQL Database, which has
// no replication catalog (replicationHidden) — a subscriber-only instance has
// no distributor and still has Local Subscriptions to show. What an empty
// Local Publications means is said in it rather than left to an empty
// folder: no distributor ("not configured"), or published databases this
// login cannot read ("not visible"), which gosmo's LocalPublications skips.

// loadReplicationChildren returns the Replication folder's two folders.
func loadReplicationChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Local Publications", NodeLocalPublications, "", "", ""),
		l.node("Local Subscriptions", NodeLocalSubscriptions, "", "", ""),
	}, nil
}

// replicationNotConfiguredLabel is Local Publications' one row on an instance
// with no distributor: it cannot publish until sp_adddistributor names one.
const replicationNotConfiguredLabel = "Replication is not configured — no distributor"

// loadLocalPublicationsChildren lists every publication on the instance,
// labelled "[db]: name" as SSMS labels them.
//
// ReplicationInfo is read after the list, only to explain it: a published
// database the login cannot read (offline, or no HAS_DBACCESS) gets a "not
// visible" row of its own, since LocalPublications skips it silently, and an
// instance with nothing published and no distributor says so. Its failure
// costs only those rows — the list itself is what the folder is for.
func loadLocalPublicationsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	pubs, err := l.sc.Server.LocalPublications(l.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*explorerNode, 0, len(pubs))
	for _, p := range pubs {
		dbName := p.Database().Name
		n := l.node(publicationLabel(dbName, p.Name), NodePublication, "", p.Name, dbName)
		n.data.ReplPubType = p.Type
		out = append(out, n)
	}
	info, err := l.sc.Server.ReplicationInfo(l.ctx)
	if err != nil {
		return out, nil
	}
	return append(out, replicationExplanation(l, info, len(pubs))...), nil
}

// replicationExplanation is the rows Local Publications adds below its
// publications from ReplicationInfo: one per published database the login
// cannot read, and, when there is nothing at all to list, the
// not-configured row.
func replicationExplanation(l loaderCtx, info *gosmo.ReplicationInfo, listed int) []*explorerNode {
	var out []*explorerNode
	for _, d := range info.Databases {
		if (d.Published || d.MergePublished) && !d.Readable {
			out = append(out, l.node(publicationLabel(d.Name, "not visible (offline, or no access)"), NodeError, "", "", ""))
		}
	}
	if listed == 0 && len(out) == 0 && !info.DistributorConfigured {
		out = append(out, l.node(replicationNotConfiguredLabel, NodeError, "", "", ""))
	}
	return out
}

// publicationLabel is SSMS's "[db]: publication".
func publicationLabel(dbName, pub string) string {
	return "[" + dbName + "]: " + pub
}

// loadLocalSubscriptionsChildren lists the subscriptions every database here
// holds, labelled "[db] - [publisher].[pubdb]: publication" as SSMS labels
// them. An empty folder is the ordinary answer and gets no row: unlike a
// published database, a subscribing one the login cannot read leaves no trace
// to report (gosmo finds subscribers by their tables, which OBJECT_ID cannot
// see in such a database).
func loadLocalSubscriptionsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(
		func() ([]*gosmo.LocalSubscription, error) { return l.sc.Server.LocalSubscriptions(l.ctx) },
		func(s *gosmo.LocalSubscription) *explorerNode {
			dbName := s.Database().Name
			n := l.node(localSubscriptionLabel(dbName, s.Publisher, s.PublisherDB, s.Publication),
				NodeLocalSubscription, "", s.Publication, dbName)
			n.data.ReplPubType = s.PublicationType
			n.data.ReplPublisher = s.Publisher
			n.data.ReplPublisherDB = s.PublisherDB
			return n
		})
}

// localSubscriptionLabel is SSMS's "[db] - [publisher].[pubdb]: publication".
func localSubscriptionLabel(dbName, publisher, publisherDB, pub string) string {
	return "[" + dbName + "] - [" + publisher + "].[" + publisherDB + "]: " + pub
}

// launchReplicationMonitor is the folders' and a publication's Launch
// Replication Monitor (replication_monitor_panel.go). pubDB and pub select a
// publication; empty for a folder. Not permission-gated: the rights it needs
// (replmonitor in the distribution database) are not probed at connect, so
// the panel itself measures them and says what is missing.
func launchReplicationMonitor(a *App, sc *db.ServerConn, pubDB, pub string) controls.MenuItem {
	return controls.MenuItem{Label: "Launch Replication Monitor", Action: func() {
		a.showReplicationMonitorFor(sc, pubDB, pub)
	}}
}

// The context menus for this family, looked up through nodeMenus
// (explorer_loaders.go). Nothing here writes, so nothing is permission-gated.

func replicationMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return folderMenu(newQuery, refresh, launchReplicationMonitor(a, sc, "", ""))
}

func localPublicationsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return folderMenu(newQuery, refresh, launchReplicationMonitor(a, sc, "", ""))
}

func publicationMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		launchReplicationMonitor(a, sc, node.data.DBName, node.data.Name),
		{Divider: true},
		newQuery,
		{Divider: true},
		refresh,
		{Label: "Properties...", Action: func() {
			a.showPublicationPropertiesFor(sc, node.data.DBName, node.data.Name)
		}},
	}
}

func localSubscriptionMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		refresh,
		{Label: "Properties...", Action: func() {
			a.showLocalSubscriptionPropertiesFor(sc, node.data.DBName, node.data.ReplPublisher,
				node.data.ReplPublisherDB, node.data.Name)
		}},
	}
}
