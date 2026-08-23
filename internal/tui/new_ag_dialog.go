package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// new_ag_dialog.go is New Availability Group — the Object Explorer's Always On
// High Availability > Availability Groups folder, "New Availability Group...".
// The pages are in new_ag_pages.go; this file holds the prefetch, the shared
// state both pages edit, and the create pipeline.
//
// # Creating a group is four statements on three instances
//
// CREATE AVAILABILITY GROUP is only the first. Each secondary then has to run
// ALTER AVAILABILITY GROUP ... JOIN against itself — the primary cannot join
// anything on its behalf — and, if it is to seed automatically, ALTER ... GRANT
// CREATE ANY DATABASE, without which SEEDING_MODE = AUTOMATIC seeds nothing and
// reports no error for it. The connections come from db.ServerConn.Peer, which
// reuses this connection's credentials; a replica that wants different ones is
// out of scope and surfaces as a connect error naming the instance.
//
// What this dialog deliberately does not do is create the database mirroring
// endpoints. An instance can have only one, and creating one where there is
// none means a certificate exchange across every participating instance —
// enough of a flow to have its own dialog, new_endpoint_dialog.go. A missing
// endpoint is reported as a blocking problem naming the instance and pointing
// at that dialog, rather than guessed at here, which would produce a group that
// looks created and never connects.

// newAGReplica is one replica of the group being defined. Unlike the
// Properties pages' agReplicaEdit there are no "orig" fields: nothing exists
// yet, so there is nothing to diff against — every value here is written.
type newAGReplica struct {
	name        string
	endpointURL string

	availabilityMode string
	failoverMode     string
	seedingMode      string
	primaryRole      string
	secondaryRole    string
	backupPriority   int
	sessionTimeout   int

	// isPrimary marks the instance the dialog is connected to. CREATE runs
	// there, so it is the group's primary by definition and cannot be removed.
	isPrimary bool
}

func (r *newAGReplica) spec() gosmo.AvailabilityReplicaSpec {
	return gosmo.AvailabilityReplicaSpec{
		ServerName:                    r.name,
		EndpointURL:                   r.endpointURL,
		AvailabilityMode:              r.availabilityMode,
		FailoverMode:                  r.failoverMode,
		SeedingMode:                   r.seedingMode,
		BackupPriority:                r.backupPriority,
		SessionTimeout:                r.sessionTimeout,
		PrimaryRoleAllowConnections:   r.primaryRole,
		SecondaryRoleAllowConnections: r.secondaryRole,
	}
}

// cloneAGReplicas copies the list and every replica in it, so a snapshot taken
// for a page's RevertFn is not aliased by the edits it exists to undo — the
// slice holds pointers, and a plain slices.Clone would share every element.
func cloneAGReplicas(replicas []*newAGReplica) []*newAGReplica {
	out := make([]*newAGReplica, len(replicas))
	for i, r := range replicas {
		copied := *r
		out[i] = &copied
	}
	return out
}

// newAGDatabase is one candidate database and whether it is to be included.
type newAGDatabase struct {
	name     string
	included bool
}

// newAGPrefetch is what the dialog reads once, before any page is built.
type newAGPrefetch struct {
	primaryName     string
	primaryEndpoint string

	// blocker is why a group cannot be created from this instance at all —
	// Always On disabled, or no usable database mirroring endpoint. Empty when
	// there is no such problem. Reported on the page rather than as a load
	// error, because the reason is the useful part.
	blocker string

	existingGroups map[string]bool

	databases []newAGDatabase
	excluded  []string
}

// NewAGDialog is the New Availability Group dialog.
type NewAGDialog struct {
	newObjectDialog[newAGPrefetch]

	node *explorerNode

	// State shared by both pages, built by buildPages from the prefetch. The
	// Backup Preferences page writes backupPriority on these same replicas,
	// which is why they are one CREATE rather than a create plus an ALTER.
	replicas  []*newAGReplica
	databases []newAGDatabase

	// Group-level values the General page owns; read here when the request is
	// assembled. Backup preference is the Backup Preferences page's.
	groupName         string
	clusterType       string
	requiredSync      int
	dbFailover        bool
	dtcSupport        bool
	contained         bool
	backupPreference  string
	commitGeneralPage func()
	commitBackupPage  func()
}

// NewNewAGDialog creates the dialog and wires its callbacks.
func NewNewAGDialog(app *App) *NewAGDialog {
	d := &NewAGDialog{}
	d.init(app, newObjectConfig[newAGPrefetch]{
		title:   "New Availability Group",
		noun:    "Availability group",
		pages:   []string{"General", "Backup Preferences"},
		fetch:   d.fetchPrefetch,
		build:   d.buildPages,
		refresh: func(*db.ServerConn) { refreshExplorerNode(d.app, d.node) },
	})
	// The shell scripts exactly what it would have run, which here is
	// statements for three different instances with nothing saying so. Replaced
	// with a variant that labels them; see runScript.
	d.OnScript = d.runScript
	return d
}

func (d *NewAGDialog) show(sc *db.ServerConn, node *explorerNode) {
	d.node = node
	d.replicas = nil
	d.databases = nil
	d.commitGeneralPage = nil
	d.commitBackupPage = nil
	d.newObjectDialog.show(sc)
	d.SetHeader("New availability group", "Server: "+sc.Opts.Server)
}

func (d *NewAGDialog) fetchPrefetch(ctx context.Context, sc *db.ServerConn) (*newAGPrefetch, error) {
	pf := &newAGPrefetch{primaryName: sc.Server.Name(), existingGroups: map[string]bool{}}

	if info := sc.Server.Info(); info != nil && !info.IsHADREnabled {
		pf.blocker = fmt.Sprintf("Always On availability groups are not enabled on %s. Enable the feature and restart the instance first — on Linux, `mssql-conf set hadr.hadrenabled 1`.", sc.Opts.Server)
		return pf, nil
	}

	ep, err := sc.Server.DatabaseMirroringEndpointContext(ctx)
	if err != nil {
		return nil, err
	}
	switch {
	case ep == nil:
		pf.blocker = fmt.Sprintf("%s has no database mirroring endpoint. Every replica needs one before a group can name it. Use \"New Database Mirroring Endpoint...\" on the Always On High Availability node to create one on this instance and each of its peers, exchanging certificates between them, then reopen this dialog.", sc.Opts.Server)
		return pf, nil
	case !strings.EqualFold(ep.State, "STARTED"):
		pf.blocker = fmt.Sprintf("%s's database mirroring endpoint %q is %s, not STARTED. A replica behind a stopped endpoint never connects.", sc.Opts.Server, ep.Name, ep.State)
		return pf, nil
	}
	pf.primaryEndpoint = ep.URL()

	groups, err := sc.Server.AvailabilityGroupsContext(ctx)
	if err != nil {
		return nil, err
	}
	inGroup := map[string]bool{}
	for _, g := range groups {
		pf.existingGroups[strings.ToLower(g.Name)] = true
		dbs, err := g.DatabasesContext(ctx)
		if err != nil {
			return nil, err
		}
		for _, adb := range dbs {
			inGroup[strings.ToLower(adb.DatabaseName)] = true
		}
	}

	// The log backup chain state of every database in one read — CREATE
	// AVAILABILITY GROUP ... FOR DATABASE enforces the same prerequisite as
	// ADD DATABASE, so this page applies the same rule.
	statuses, err := sc.Server.DatabaseRecoveryStatusesContext(ctx)
	if err != nil {
		return nil, err
	}
	logChain := map[string]bool{}
	for _, st := range statuses {
		logChain[strings.ToLower(st.DatabaseName)] = st.LogBackupChainStarted
	}

	dbs, err := sc.Server.DatabasesContext(ctx)
	if err != nil {
		return nil, err
	}
	eligible, excluded := agEligibleDatabases(agCandidatesFrom(dbs, logChain), inGroup)
	for _, name := range eligible {
		pf.databases = append(pf.databases, newAGDatabase{name: name})
	}
	pf.excluded = excluded
	return pf, nil
}

// request assembles the CREATE from both pages' state.
func (d *NewAGDialog) request() (gosmo.CreateAvailabilityGroupRequest, error) {
	if d.commitGeneralPage != nil {
		d.commitGeneralPage()
	}
	if d.commitBackupPage != nil {
		d.commitBackupPage()
	}
	req := gosmo.CreateAvailabilityGroupRequest{
		Name:                      strings.TrimSpace(d.groupName),
		ClusterType:               d.clusterType,
		AutomatedBackupPreference: d.backupPreference,
		DBFailover:                d.dbFailover,
		DTCSupport:                d.dtcSupport,
		Contained:                 d.contained,
		// Zero is a legitimate value, so the omit sentinel is negative; the
		// dialog always has a number, so it is always written.
		RequiredSynchronizedSecondariesToCommit: d.requiredSync,
	}
	for _, db := range d.databases {
		if db.included {
			req.Databases = append(req.Databases, db.name)
		}
	}
	for _, r := range d.replicas {
		req.Replicas = append(req.Replicas, r.spec())
	}
	if len(req.Replicas) == 0 {
		return req, fmt.Errorf("the group has no replicas")
	}
	// CREATE makes the instance it runs on the primary, and gosmo writes the
	// replicas in order, so the local one has to be first.
	if !d.replicas[0].isPrimary {
		return req, fmt.Errorf("the first replica must be %s, the instance the group is created on", d.replicas[0].name)
	}
	return req, nil
}

// createGroup is the whole pipeline: CREATE here, then JOIN and (for automatic
// seeding) GRANT CREATE ANY DATABASE on each secondary in turn.
//
// A secondary that fails to join leaves a real, created group behind with that
// replica disconnected — which is why the error names the instance and says the
// group exists. Rolling back would mean dropping a group the user asked for on
// the strength of one unreachable peer.
func (d *NewAGDialog) createGroup(ctx context.Context) error {
	sc := d.sc
	req, err := d.request()
	if err != nil {
		return err
	}
	if _, err := sc.Server.CreateAvailabilityGroupContext(ctx, req); err != nil {
		return err
	}
	for _, r := range d.replicas {
		if r.isPrimary {
			continue
		}
		target := sc.Server
		if !gosmo.Scripting(ctx) {
			peer, err := sc.Peer(ctx, r.name)
			if err != nil {
				return fmt.Errorf("availability group %q was created, but connecting to %s to join it failed: %w", req.Name, r.name, err)
			}
			target = peer.Server
		}
		ag := target.AvailabilityGroup(req.Name)
		if err := ag.JoinContext(ctx, req.ClusterType); err != nil {
			return fmt.Errorf("availability group %q was created, but %s could not join it: %w", req.Name, r.name, err)
		}
		if strings.EqualFold(r.seedingMode, "AUTOMATIC") {
			if err := ag.GrantCreateAnyDatabaseContext(ctx); err != nil {
				return fmt.Errorf("availability group %q was created and %s joined it, but granting it CREATE ANY DATABASE failed — automatic seeding will silently seed nothing until that is granted: %w", req.Name, r.name, err)
			}
		}
	}
	return nil
}

// runScript replaces the shell's, which would emit the statements with nothing
// saying that only the first runs here. Every JOIN and GRANT below it belongs
// to a different instance, and a script that does not say so is a trap: run
// whole against the primary it either errors or, worse, joins the primary to
// its own group.
func (d *NewAGDialog) runScript() {
	scriptCtx, script := gosmo.WithScript(d.ctx)
	sc := d.sc
	d.runPipeline(scriptCtx, func() {
		d.app.openQueryWithText(sc, "", d.annotateScript(script.Statements))
	})
}

// annotateScript labels each statement with the instance it has to run on.
func (d *NewAGDialog) annotateScript(statements []string) string {
	var b strings.Builder
	b.WriteString("-- New Availability Group: these statements do NOT all run on the same instance.\n")

	// Statement 0 is the CREATE, on the primary. Everything after it is one or
	// two statements per secondary, in the order createGroup issues them —
	// which is why a MANUAL-seeding replica, whose GRANT is skipped, shifts
	// every label after it.
	var targets []string
	for _, r := range d.replicas {
		if r.isPrimary {
			targets = append(targets, r.name)
			continue
		}
		targets = append(targets, r.name)
		if strings.EqualFold(r.seedingMode, "AUTOMATIC") {
			targets = append(targets, r.name)
		}
	}
	for i, stmt := range statements {
		target := "(unknown instance)"
		if i < len(targets) {
			target = targets[i]
		}
		fmt.Fprintf(&b, "\n-- on %s\n%s\nGO\n", target, stmt)
	}
	return b.String()
}

// showNewAGDialog opens New Availability Group — the Object Explorer context
// menu's entry point on the Availability Groups folder.
func (a *App) showNewAGDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newAGDialog.show(sc, node)
}
