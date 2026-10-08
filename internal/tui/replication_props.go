package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// replication_props.go is the read-only Properties for a publication and a
// local subscription: the pages SSMS's Publication
// and Subscription Properties have, every one showing and none writing.
// Replication is browsed here, never configured — no create, alter or drop,
// no reinitialize, no agent start/stop (docs/decisions.md § Replication) — so
// every page is in prop_page_requires_test.go's pagesThatOnlyRead.
//
// Both finders are the ones the Details pane uses, so the pane and the dialog
// cannot disagree about what a publication or a subscription is.

// findPublication resolves a publication by database and name, of either kind.
func findPublication(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.Publication, error) {
	return inDB(ctx, sc, dbName, name, (*gosmo.Database).PublicationByName)
}

// findLocalSubscription resolves the subscription dbName holds to publisher's
// publication pub in publisherDB. A database can subscribe to same-named
// publications of two publishers, so all three name it — the same three the
// tree node's key carries. Server and database names compare without regard
// to case: the fixture's subscriber stores its publisher as WIN10CLI in one
// table and win10cli in another.
func findLocalSubscription(ctx context.Context, sc *db.ServerConn, dbName, publisher, publisherDB, pub string) (*gosmo.LocalSubscription, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	subs, err := d.LocalSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range subs {
		if strings.EqualFold(s.Publisher, publisher) && strings.EqualFold(s.PublisherDB, publisherDB) &&
			strings.EqualFold(s.Publication, pub) {
			return s, nil
		}
	}
	return nil, fmt.Errorf("no subscription in database %q to [%s].[%s]: %s", dbName, publisher, publisherDB, pub)
}

// -- Publication ----------------------------------------------------------------

func publicationPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	load := func(ctx context.Context) (*gosmo.Publication, error) { return findPublication(ctx, sc, dbName, name) }
	return []propPage{
		pagePublicationGeneral(load),
		pagePublicationArticles(load),
		pagePublicationFilterRows(load),
		pagePublicationSnapshot(load),
		pagePublicationSubscriptionOptions(load),
		pagePublicationSubscriptions(load),
	}
}

func pagePublicationGeneral(find func(context.Context) (*gosmo.Publication, error)) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			p, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			status := "Inactive"
			if p.Active {
				status = "Active"
			}
			f := propsheet.NewForm(
				propsheet.Section("Publication"),
				propsheet.Static("Name", p.Name),
				propsheet.Static("Database", p.Database().Name),
				propsheet.Static("Type", p.Type.String()),
				propsheet.Static("Status", status),
				propsheet.Static("Description", p.Description),
				propsheet.Section("Subscription expiration"),
				propsheet.Static("Subscriptions expire", publicationRetention(p)),
			)
			// A merge publication names the oldest subscriber it supports;
			// a transactional one has no such setting.
			if p.Type == gosmo.PublicationMerge {
				f.Add(propsheet.Section("Compatibility"))
				f.Add(propsheet.Static("Compatibility level", mergeCompatLevel(p.CompatibilityLevel)))
			}
			f.Add(propsheet.Note("Replication is read-only here: publications are browsed, not created or altered."))
			return f, nil, nil
		},
	}
}

// publicationRetention renders a publication's retention as SSMS's General
// page words it: never, or after so many units.
func publicationRetention(p *gosmo.Publication) string {
	if p.Retention == 0 {
		return "Never"
	}
	unit := p.RetentionUnit
	if unit == "" {
		unit = "hour"
	}
	if p.Retention != 1 {
		unit += "s"
	}
	return "After " + strconv.Itoa(p.Retention) + " " + unit + " without synchronizing"
}

// mergeCompatLevel names the SQL Server release a merge publication's
// backward_comp_level stands for.
func mergeCompatLevel(level int) string {
	names := map[int]string{
		90: "SQL Server 2005", 100: "SQL Server 2008", 110: "SQL Server 2012",
		120: "SQL Server 2014", 130: "SQL Server 2016", 140: "SQL Server 2017",
		150: "SQL Server 2019", 160: "SQL Server 2022", 170: "SQL Server 2025",
	}
	if n, ok := names[level]; ok {
		return n + " (" + strconv.Itoa(level) + ")"
	}
	return strconv.Itoa(level)
}

// articleObject is "schema.object", or the bare name when the object no
// longer resolves (gosmo leaves SourceSchema empty then).
func articleObject(schema, object string) string {
	if schema == "" {
		return object
	}
	return schema + "." + object
}

func pagePublicationArticles(find func(context.Context) (*gosmo.Publication, error)) propPage {
	return propPage{
		title: "Articles",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			p, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			arts, err := p.Articles(ctx)
			if err != nil {
				return nil, nil, err
			}
			rows := make([][]string, len(arts))
			for i, a := range arts {
				rows[i] = []string{a.Name, a.Type, articleObject(a.SourceSchema, a.SourceObject),
					articleObject(a.DestinationOwner, a.DestinationObject)}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Article", "Type", "Published object", "Destination"}, rows)

			descStatic := propsheet.Static("Description", "")
			filterStatic := propsheet.Static("Row filter", "")
			preStatic := propsheet.Static("Existing destination object", "")
			optsHeaders := []string{"Schema option"}
			opts := controls.NewDataGrid()
			// The options are sentences; the default cap cut them mid-word.
			opts.SetMaxCellWidth(100)
			opts.SetData(optsHeaders, nil)
			// The detail rows follow the selection only: a read-only page's
			// grid browses, and nothing here writes (docs/ui-rules.md).
			syncFromSelection := func(row int) {
				if row < 0 || row >= len(arts) {
					descStatic.SetValue("")
					filterStatic.SetValue("")
					preStatic.SetValue("")
					resetGrid(opts, optsHeaders, nil, 0)
					return
				}
				a := arts[row]
				descStatic.SetValue(a.Description)
				filterStatic.SetValue(orNone(a.Filter))
				preStatic.SetValue(a.PreCreationCommand)
				var optRows [][]string
				for _, o := range a.SchemaOption.Options() {
					optRows = append(optRows, []string{o})
				}
				resetGrid(opts, optsHeaders, optRows, 0)
			}
			grid.OnSelectRow = syncFromSelection
			if len(arts) > 0 {
				syncFromSelection(0)
			}
			return propsheet.NewForm(
				propsheet.Section("Published articles"),
				propsheet.NewGridRow(grid, 8),
				propsheet.Section("Selected article"),
				descStatic, filterStatic, preStatic,
				propsheet.NewGridRow(opts, 8),
			), nil, nil
		},
	}
}

// orNone is s, or "(none)" for an empty s that would otherwise read as "not
// loaded yet".
func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func pagePublicationFilterRows(find func(context.Context) (*gosmo.Publication, error)) propPage {
	return propPage{
		title: "Filter Rows",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			p, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			arts, err := p.Articles(ctx)
			if err != nil {
				return nil, nil, err
			}
			var rows [][]string
			for _, a := range arts {
				if a.Filter != "" {
					rows = append(rows, []string{a.Name, articleObject(a.SourceSchema, a.SourceObject), a.Filter})
				}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Article", "Table", "Filter"}, rows)
			f := propsheet.NewForm(
				propsheet.Section("Filtered tables"),
				propsheet.NewGridRow(grid, 10),
			)
			if len(rows) == 0 {
				f.Add(propsheet.Note("No article of this publication is filtered: every row of every published table is replicated."))
			}
			return f, nil, nil
		},
	}
}

func pagePublicationSnapshot(find func(context.Context) (*gosmo.Publication, error)) propPage {
	return propPage{
		title: "Snapshot",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			p, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("Snapshot format"),
				propsheet.Static("Format", p.SyncMethod),
				propsheet.Section("Location of snapshot files"),
				propsheet.Static("In the default folder", boolStr(p.SnapshotInDefaultFolder)),
				propsheet.Static("Alternate folder", orNone(p.AltSnapshotFolder)),
				propsheet.Static("Compress snapshot files", boolStr(p.CompressSnapshot)),
				propsheet.Section("Run additional scripts"),
				propsheet.Static("Before applying the snapshot", orNone(p.PreSnapshotScript)),
				propsheet.Static("After applying the snapshot", orNone(p.PostSnapshotScript)),
			)
			// immediate_sync is syspublications' alone.
			if p.Type != gosmo.PublicationMerge {
				f.Add(propsheet.Section("Snapshot availability"))
				f.Add(propsheet.Static("Keep snapshot available", boolStr(p.ImmediateSync)))
			}
			return f, nil, nil
		},
	}
}

func pagePublicationSubscriptionOptions(find func(context.Context) (*gosmo.Publication, error)) propPage {
	return propPage{
		title: "Subscription Options",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			p, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("Creation and synchronization"),
				propsheet.Static("Allow push subscriptions", boolStr(p.AllowPush)),
				propsheet.Static("Allow pull subscriptions", boolStr(p.AllowPull)),
				propsheet.Static("Allow anonymous subscriptions", boolStr(p.AllowAnonymous)),
				propsheet.Static("Allow subscription copying", boolStr(p.AllowSubscriptionCopy)),
				propsheet.Section("Schema replication"),
				propsheet.Static("Replicate schema changes", boolStr(p.ReplicateDDL)),
			)
			if p.Type == gosmo.PublicationMerge {
				f.Add(propsheet.Section("Merge"))
				f.Add(propsheet.Static("Allow web synchronization", boolStr(p.AllowWebSync)))
				f.Add(propsheet.Static("Precompute partitions", boolStr(p.UsePartitionGroups)))
				f.Add(propsheet.Static("Conflicts kept at publisher", boolStr(p.CentralizedConflicts)))
				f.Add(propsheet.Static("Conflict retention (days)", strconv.Itoa(p.ConflictRetention)))
				return f, nil, nil
			}
			f.Add(propsheet.Section("Agents and initialization"))
			f.Add(propsheet.Static("Independent Distribution Agent", boolStr(p.IndependentAgent)))
			f.Add(propsheet.Static("Allow init from backup files", boolStr(p.AllowInitializeFromBackup)))
			f.Add(propsheet.Section("Updatable subscriptions"))
			f.Add(propsheet.Static("Allow immediate updating", boolStr(p.AllowImmediateUpdates)))
			f.Add(propsheet.Static("Allow queued updating", boolStr(p.AllowQueuedUpdates)))
			return f, nil, nil
		},
	}
}

func pagePublicationSubscriptions(find func(context.Context) (*gosmo.Publication, error)) propPage {
	return propPage{
		title: "Subscriptions",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			p, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			subs, err := p.Subscriptions(ctx)
			if err != nil {
				return nil, nil, err
			}
			grid := controls.NewDataGrid()
			grid.SetData(publicationSubscriptionColumns, publicationSubscriptionRows(subs))
			f := propsheet.NewForm(
				propsheet.Section("Subscriptions to this publication"),
				propsheet.NewGridRow(grid, 10),
			)
			if p.Type != gosmo.PublicationMerge {
				f.Add(propsheet.Note("The publisher records a transactional subscription's last synchronization at the distributor, not here — Replication Monitor shows it."))
			}
			return f, nil, nil
		},
	}
}

// publicationSubscriptionColumns and publicationSubscriptionRows are the
// publisher's list of a publication's subscriptions, shared by the
// Subscriptions page and the publication's Details view.
var publicationSubscriptionColumns = []string{"Subscriber", "Subscription Database", "Type", "Status", "Sync Type", "Last Sync", "Last Result"}

func publicationSubscriptionRows(subs []*gosmo.Subscription) [][]string {
	rows := make([][]string, len(subs))
	for i, s := range subs {
		rows[i] = []string{s.Subscriber, s.SubscriberDB, s.Type.String(), s.Status, s.SyncType,
			formatSQLDate(s.LastSyncTime), replSyncStatus(s.LastSyncStatus)}
	}
	return rows
}

// replSyncStatus names a merge agent's last run status as the subscription
// tables record it (sysmergesubscriptions.last_sync_status); 0 is "never ran"
// and renders empty, like a missing date.
func replSyncStatus(status int) string {
	switch status {
	case 0:
		return ""
	case 1:
		return "Started"
	case 2:
		return "Succeeded"
	case 3:
		return "In progress"
	case 4:
		return "Idle"
	case 5:
		return "Retrying"
	case 6:
		return "Failed"
	}
	return strconv.Itoa(status)
}

// -- Subscription ---------------------------------------------------------------

func localSubscriptionPropPages(sc *db.ServerConn, dbName, publisher, publisherDB, pub string) []propPage {
	find := func(ctx context.Context) (*gosmo.LocalSubscription, error) {
		return findLocalSubscription(ctx, sc, dbName, publisher, publisherDB, pub)
	}
	return []propPage{
		pageLocalSubscriptionGeneral(find),
		pageLocalSubscriptionAgent(find),
		pageLocalSubscriptionSynchronization(find),
	}
}

func pageLocalSubscriptionGeneral(find func(context.Context) (*gosmo.LocalSubscription, error)) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			s, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			return propsheet.NewForm(
				propsheet.Section("Subscription"),
				propsheet.Static("Subscription database", s.Database().Name),
				propsheet.Static("Subscription type", s.Type.String()),
				propsheet.Static("Description", s.Description),
				propsheet.Section("Publication"),
				propsheet.Static("Publisher", s.Publisher),
				propsheet.Static("Publication database", s.PublisherDB),
				propsheet.Static("Publication", s.Publication),
				propsheet.Static("Publication type", s.PublicationType.String()),
			), nil, nil
		},
	}
}

func pageLocalSubscriptionAgent(find func(context.Context) (*gosmo.LocalSubscription, error)) propPage {
	return propPage{
		title: "Agent",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			s, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			// Where the agent runs is the subscription type: a pull agent
			// is a job here, a push agent one at the distributor.
			runsAt := "Distributor (push subscription)"
			if s.Type == gosmo.SubscriptionPull {
				runsAt = "Subscriber (pull subscription)"
			}
			distributor := s.Distributor
			if distributor == "" {
				distributor = "(not recorded at the subscriber)"
			}
			f := propsheet.NewForm(
				propsheet.Section("Synchronization agent"),
				propsheet.Static("Agent runs at", runsAt),
				propsheet.Static("Agent job", orNone(s.AgentJob)),
				propsheet.Static("Distributor", distributor),
			)
			if s.PublicationType != gosmo.PublicationMerge {
				f.Add(propsheet.Static("Independent agent", boolStr(s.IndependentAgent)))
			}
			return f, nil, nil
		},
	}
}

func pageLocalSubscriptionSynchronization(find func(context.Context) (*gosmo.LocalSubscription, error)) propPage {
	return propPage{
		title: "Synchronization",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			s, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			last := formatSQLDate(s.LastSyncTime)
			if last == "" {
				last = "Never"
			}
			f := propsheet.NewForm(
				propsheet.Section("Last synchronization"),
				propsheet.Static("Last synchronized", last),
			)
			if s.PublicationType == gosmo.PublicationMerge {
				f.Add(propsheet.Static("Last result", replSyncStatus(s.LastSyncStatus)))
				f.Add(propsheet.Static("Summary", s.LastSyncSummary))
				f.Add(propsheet.Section("Merge"))
				f.Add(propsheet.Static("Subscriber type", s.SubscriberType))
				return f, nil, nil
			}
			f.Add(propsheet.Section("Updates at the subscriber"))
			f.Add(propsheet.Static("Update mode", replUpdateMode(s.UpdateMode)))
			return f, nil, nil
		},
	}
}

// replUpdateMode names MSreplication_subscriptions.update_mode.
func replUpdateMode(mode int) string {
	switch mode {
	case 0:
		return "Read-only"
	case 1:
		return "Immediate updating"
	case 2:
		return "Queued updating"
	case 3:
		return "Immediate, queued as failover"
	case 4:
		return "Queued, immediate as failover"
	}
	return strconv.Itoa(mode)
}
