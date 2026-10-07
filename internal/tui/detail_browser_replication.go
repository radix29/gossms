package tui

import (
	"context"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
)

// detail_browser_replication.go is the Detail Browser's view of server ›
// Replication: the distributor configuration on the folder itself, one row
// per publication or subscription on Local Publications and Local
// Subscriptions, and a publication's articles and subscriptions on its leaf.
//
// Like every other detail view here, each leaf reuses the finder its
// Properties page uses (replication_props.go). No arm fills objs: nothing
// replicated has a Delete (docs/decisions.md § Replication).

// replicationDetail is the Replication folder: whether a distributor is
// configured and where, the distribution databases and publishers a local
// distributor keeps, and each database's replication role.
func replicationDetail(ctx context.Context, sc *dbconn.ServerConn) ([]string, [][]string, error) {
	info, err := sc.Server.ReplicationInfo(ctx)
	if err != nil {
		return nil, nil, err
	}
	distributor := info.Distributor
	if !info.DistributorConfigured {
		distributor = "(not configured)"
	}
	rows := [][]string{
		{"Distributor", distributor},
		{"Is its own distributor", boolStr(info.IsDistributor)},
		{"Is a publisher", boolStr(info.IsPublisher)},
	}
	for _, d := range info.DistributionDatabases {
		rows = append(rows, []string{"Distribution database",
			d.Name + " — transactions kept " + strconv.Itoa(d.MinRetentionHours) + "–" + strconv.Itoa(d.MaxRetentionHours) +
				" h, history " + strconv.Itoa(d.HistoryRetentionHours) + " h"})
	}
	for _, p := range info.Publishers {
		auth := "SQL Server login"
		if p.WindowsAuth {
			auth = "Windows authentication"
		}
		rows = append(rows, []string{"Publisher",
			p.Name + " — uses " + p.DistributionDatabase + ", " + auth + ", snapshots in " + orNone(p.WorkingDirectory)})
	}
	for _, d := range info.Databases {
		rows = append(rows, []string{"Database", d.Name + " — " + replicationRoles(d)})
	}
	return propertyValueColumns, rows, nil
}

// replicationRoles is a database's replication flags as words, with the one
// thing a published database can additionally be: unreadable to this login.
func replicationRoles(d gosmo.ReplicationDatabase) string {
	var roles []string
	if d.Published {
		roles = append(roles, "published (transactional/snapshot)")
	}
	if d.MergePublished {
		roles = append(roles, "published (merge)")
	}
	if d.Distribution {
		roles = append(roles, "distribution database")
	}
	s := strings.Join(roles, ", ")
	if !d.Readable {
		s += " — not visible (offline, or no access)"
	}
	return s
}

// localPublicationsDetail lists every publication on the instance. A
// published database the login cannot read gets a row saying so, as the tree
// does, rather than vanishing from the list.
func localPublicationsDetail(ctx context.Context, sc *dbconn.ServerConn) ([]string, [][]string, error) {
	pubs, err := sc.Server.LocalPublications(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0, len(pubs))
	for _, p := range pubs {
		status := "Inactive"
		if p.Active {
			status = "Active"
		}
		rows = append(rows, []string{p.Name, p.Database().Name, p.Type.String(), status,
			publicationRetention(p), p.Description})
	}
	// As the tree's loader (replicationExplanation): ReplicationInfo only
	// explains the list, so its failure costs the explanation, not the list.
	if info, err := sc.Server.ReplicationInfo(ctx); err == nil {
		for _, d := range info.Databases {
			if (d.Published || d.MergePublished) && !d.Readable {
				rows = append(rows, []string{"", d.Name, "", "not visible (offline, or no access)", "", ""})
			}
		}
		if len(rows) == 0 && !info.DistributorConfigured {
			rows = append(rows, []string{replicationNotConfiguredLabel, "", "", "", "", ""})
		}
	}
	return []string{"Publication", "Database", "Type", "Status", "Subscriptions Expire", "Description"}, rows, nil
}

// localSubscriptionsDetail lists every subscription a database here holds.
func localSubscriptionsDetail(ctx context.Context, sc *dbconn.ServerConn) ([]string, [][]string, error) {
	subs, err := sc.Server.LocalSubscriptions(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0, len(subs))
	for _, s := range subs {
		rows = append(rows, []string{s.Database().Name, s.Publisher, s.PublisherDB, s.Publication,
			s.PublicationType.String(), s.Type.String(), formatSQLDate(s.LastSyncTime)})
	}
	return []string{"Subscription Database", "Publisher", "Publication Database", "Publication",
		"Publication Type", "Subscription Type", "Last Sync"}, rows, nil
}

// publicationDetail is one publication: its articles, then the publisher's
// record of its subscriptions, in one grid with a Kind column — the pane has
// one grid, and the two lists are what a publication is. A failed
// subscription read keeps the articles and says so in a row of its own.
func publicationDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	p, err := findPublication(ctx, sc, node.data.DBName, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	arts, err := p.Articles(ctx)
	if err != nil {
		return nil, nil, err
	}
	var rows [][]string
	for _, a := range arts {
		rows = append(rows, []string{"Article", a.Name, a.Type,
			articleObject(a.SourceSchema, a.SourceObject), a.Filter})
	}
	subs, err := p.Subscriptions(ctx)
	if err != nil {
		rows = append(rows, []string{"Subscription", "N/A", "", "", err.Error()})
	}
	for _, s := range subs {
		rows = append(rows, []string{"Subscription", s.Subscriber + "." + s.SubscriberDB, s.Type.String(),
			s.Status, s.SyncType})
	}
	return []string{"Kind", "Name", "Type", "Object / Status", "Filter / Sync Type"}, rows, nil
}

// localSubscriptionDetail is one subscription's Property/Value view.
func localSubscriptionDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	n := node.data
	s, err := findLocalSubscription(ctx, sc, n.DBName, n.ReplPublisher, n.ReplPublisherDB, n.Name)
	if err != nil {
		return nil, nil, err
	}
	last := formatSQLDate(s.LastSyncTime)
	if last == "" {
		last = "Never"
	}
	return propertyRows(
		"Subscription database", s.Database().Name,
		"Publisher", s.Publisher,
		"Publication database", s.PublisherDB,
		"Publication", s.Publication,
		"Publication type", s.PublicationType.String(),
		"Subscription type", s.Type.String(),
		"Distributor", s.Distributor,
		"Agent job", s.AgentJob,
		"Last synchronized", last,
	)
}
