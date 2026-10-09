package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
)

// new_ag_dialog.go is New Availability Group (Object Explorer's Always On High
// Availability > Availability Groups folder). The pages are in new_ag_pages.go;
// this file holds the prefetch, the state both pages edit, and the create
// pipeline.
//
// # Creating a group is one statement here and two on every secondary
//
// CREATE AVAILABILITY GROUP is only the first. Each secondary must run ALTER
// AVAILABILITY GROUP ... JOIN against itself (the primary cannot join anything
// on its behalf) and, to seed automatically, ALTER ... GRANT CREATE ANY
// DATABASE, without which SEEDING_MODE = AUTOMATIC seeds nothing and reports no
// error. The connections come from db.ServerConn.Peer, which reuses this
// connection's credentials; a replica wanting different ones is out of scope
// and surfaces as a connect error naming the instance.
//
// This dialog deliberately does not create the database mirroring endpoints. An
// instance can have only one, and creating one means a certificate exchange
// across every participating instance, enough to have its own dialog
// (new_endpoint_dialog.go). A missing endpoint is a blocking problem naming the
// instance and pointing at that dialog, not guessed at here, which would give a
// group that looks created and never connects.

// newAGReplica is one replica of the group being defined. Unlike the
// Properties pages' agReplicaEdit there are no "orig" fields: nothing exists
// yet, so every value is written.
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

	// isPrimary marks the instance the dialog is connected to: CREATE runs there,
	// so it is the primary by definition and cannot be removed.
	isPrimary bool
}

func (r *newAGReplica) spec() gosmo.AvailabilityReplicaSpec {
	return gosmo.AvailabilityReplicaSpec{
		ServerName:                    r.name,
		EndpointURL:                   r.endpointURL,
		AvailabilityMode:              gosmo.AvailabilityMode(r.availabilityMode),
		FailoverMode:                  gosmo.FailoverMode(r.failoverMode),
		SeedingMode:                   gosmo.SeedingMode(r.seedingMode),
		BackupPriority:                r.backupPriority,
		SessionTimeout:                r.sessionTimeout,
		PrimaryRoleAllowConnections:   gosmo.AllowConnections(r.primaryRole),
		SecondaryRoleAllowConnections: gosmo.AllowConnections(r.secondaryRole),
	}
}

// cloneAGReplicas copies the list and every replica, so a snapshot taken for a
// page's RevertFn is not aliased by the edits it undoes (slices.Clone would
// share the pointers).
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

	// blocker is why a group cannot be created from this instance at all (Always On
	// disabled, or no usable database mirroring endpoint); empty if none. Reported
	// on the page rather than as a load error because the reason is the useful part.
	blocker string

	existingGroups *nameSet

	databases []newAGDatabase
	excluded  []string
}

// NewAGDialog is the New Availability Group dialog.
type NewAGDialog struct {
	newObjectDialog[newAGPrefetch]

	node *explorerNode

	// State shared by both pages, built by buildPages from the prefetch. The Backup
	// Preferences page writes backupPriority on these same replicas, which is why
	// they are one CREATE rather than a create plus an ALTER.
	replicas  []*newAGReplica
	databases []newAGDatabase

	// Group-level values the General page owns, read when the request is
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

	// peerFor resolves a replica's connection, defaulting to db.ServerConn.Peer. A
	// test seam (tests cannot open a second connection), like
	// new_endpoint_dialog.go's peerServerFor.
	peerFor func(ctx context.Context, name string) (*db.ServerConn, error)
}

// peer resolves a replica's connection through peerFor, or Peer when nothing
// has replaced it.
func (d *NewAGDialog) peer(ctx context.Context, name string) (*db.ServerConn, error) {
	if d.peerFor != nil {
		return d.peerFor(ctx, name)
	}
	return d.sc.Peer(ctx, name)
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
		refresh: func(sc *db.ServerConn) { d.app.explorer.ReloadFolders(sc, sameNodeAs(d.node)) },
	})
	// The shell scripts exactly what it would have run, which here is statements
	// for three different instances with nothing saying so. Replaced with a variant
	// that labels them; see runScript.
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
	pf := &newAGPrefetch{primaryName: sc.Server.Name(), existingGroups: newNameSet(serverCollation(sc))}

	if info := sc.Server.Info(); info != nil && !info.IsHADREnabled {
		pf.blocker = fmt.Sprintf("Always On availability groups are not enabled on %s. Enable the feature and restart the instance first — on Linux, `mssql-conf set hadr.hadrenabled 1`.", sc.Opts.Server)
		return pf, nil
	}

	ep, err := sc.Server.DatabaseMirroringEndpoint(ctx)
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

	groups, err := sc.Server.AvailabilityGroups(ctx)
	if err != nil {
		return nil, err
	}
	inGroup := newNameSet(serverCollation(sc))
	for _, g := range groups {
		pf.existingGroups.Add(g.Name)
		dbs, err := g.Databases(ctx)
		if err != nil {
			return nil, err
		}
		for _, adb := range dbs {
			inGroup.Add(adb.DatabaseName)
		}
	}

	// Log backup chain state of every database in one read: CREATE AVAILABILITY
	// GROUP ... FOR DATABASE enforces the same prerequisite as ADD DATABASE.
	statuses, err := sc.Server.DatabaseRecoveryStatuses(ctx)
	if err != nil {
		return nil, err
	}
	logChain := newNameMap[bool](serverCollation(sc))
	for _, st := range statuses {
		logChain.Set(st.DatabaseName, st.LogBackupChainStarted)
	}

	dbs, err := sc.Server.Databases(ctx)
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
		ClusterType:               gosmo.ClusterType(d.clusterType),
		AutomatedBackupPreference: gosmo.BackupPreference(d.backupPreference),
		DBFailover:                d.dbFailover,
		DTCSupport:                d.dtcSupport,
		Contained:                 d.contained,
		// Zero is legitimate, so the omit sentinel is negative; the dialog always has
		// a number, so it is always written.
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
	// CREATE makes the instance it runs on the primary and gosmo writes replicas
	// in order, so the local one has to be first.
	if !d.replicas[0].isPrimary {
		return req, fmt.Errorf("the first replica must be %s, the instance the group is created on", d.replicas[0].name)
	}
	return req, nil
}

// replicaJoinProblem reports why r could not join the group being created, or
// "" if nothing is in its way. r is the replica as the request carries it. It
// asks only about states that make the JOIN fail *after* the CREATE succeeded;
// see preflightReplicas.
func (d *NewAGDialog) replicaJoinProblem(ctx context.Context, r gosmo.AvailabilityReplicaSpec) string {
	peer, err := d.peer(ctx, r.ServerName)
	if err != nil {
		return fmt.Sprintf("%s cannot be reached: %v", r.ServerName, err)
	}
	if info := peer.Server.Info(); info != nil && !info.IsHADREnabled {
		return fmt.Sprintf("Always On is not enabled on %s, so it cannot host a replica", r.ServerName)
	}
	ep, err := replicaEndpoint(ctx, peer)
	if err != nil {
		return err.Error()
	}
	// The URL the CREATE is about to write was read when the replica was added,
	// perhaps minutes ago on an instance whose endpoint has since been recreated on
	// another port. A group naming the old one is created, looks right, and never
	// connects.
	if !strings.EqualFold(ep.URL(), r.EndpointURL) {
		return fmt.Sprintf("%s's endpoint is now %s, not %s — remove the replica and add it again", r.ServerName, ep.URL(), r.EndpointURL)
	}
	// gate.Allows, not Has: the fail-open rule. A peer whose probe could not run is
	// let through to try the JOIN; sysadmin passes by role.
	if !gate.Allows(peer, "", gate.AlterAnyAG) {
		return fmt.Sprintf("%s's login may not join an availability group — it needs ALTER ANY AVAILABILITY GROUP there", r.ServerName)
	}
	return ""
}

// preflightReplicas asks every secondary whether it could join, before
// anything is written.
//
// This keeps a half-built group from existing. CREATE is one statement on the
// primary, but the JOIN runs on each secondary, and every ordinary reason it
// fails (peer down, Always On off, endpoint missing or stopped, no rights) is
// knowable first. Checked here, the run is refused with nothing created; only
// attempted, the user is left with a real group missing a replica.
//
// The primary is not re-checked: one unable to host the group fails the CREATE
// itself and leaves nothing behind.
//
// Every replica is asked though only the first problem is shown, so the count is
// honest: being sent back three times is worse than being told there are three.
//
// The secondaries are req's replicas after the first (request makes the primary).
func (d *NewAGDialog) preflightReplicas(ctx context.Context, req gosmo.CreateAvailabilityGroupRequest) error {
	if gosmo.Scripting(ctx) {
		// Script Changes must work with no peer reachable: the script is what the user
		// takes to those instances.
		return nil
	}
	var problems []string
	for _, r := range agSecondaries(req) {
		if p := d.replicaJoinProblem(ctx, r); p != "" {
			problems = append(problems, p)
		}
	}
	switch len(problems) {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("%s. Nothing was created", problems[0])
	default:
		return fmt.Errorf("%s (and %d more problem(s) with other replicas). Nothing was created", problems[0], len(problems)-1)
	}
}

// createGroup is the whole pipeline: preflight every secondary, CREATE here,
// then JOIN and (for automatic seeding) GRANT CREATE ANY DATABASE on each
// secondary in turn.
//
// The preflight makes a partly created group rare, not impossible: a peer can
// die between the check and the JOIN, leaving a real group with that replica
// disconnected, which is why the errors name the instance and say the group
// exists. Nothing is rolled back: dropping the group would destroy what the
// user asked for on one unreachable peer's account, and a DROP on the primary
// cannot reach a secondary that already joined and is now unreachable, whose
// copy of the metadata would survive and need a local DROP anyway.
//
// req is built by preflight on the UI goroutine (see request); this runs on the
// pipeline's and reads nothing else of the dialog's pages.
func (d *NewAGDialog) createGroup(ctx context.Context, req gosmo.CreateAvailabilityGroupRequest) error {
	sc := d.sc
	if err := d.preflightReplicas(ctx, req); err != nil {
		return err
	}
	if _, err := sc.Server.CreateAvailabilityGroup(ctx, req); err != nil {
		return err
	}
	for _, r := range agSecondaries(req) {
		// Under Script Changes nothing connects to the secondary: its JOIN is scripted
		// through the primary's handle, labelled with the secondary it belongs to.
		target, joinCtx := sc.Server, gosmo.WithScriptServer(ctx, r.ServerName)
		if !gosmo.Scripting(ctx) {
			peer, err := d.peer(ctx, r.ServerName)
			if err != nil {
				return fmt.Errorf("availability group %q was created, but connecting to %s to join it failed: %w", req.Name, r.ServerName, err)
			}
			target, joinCtx = peer.Server, ctx
		}
		ag := target.AvailabilityGroupRef(req.Name)
		if err := ag.Join(joinCtx, req.ClusterType); err != nil {
			return fmt.Errorf("availability group %q was created, but %s could not join it: %w", req.Name, r.ServerName, err)
		}
		if strings.EqualFold(string(r.SeedingMode), string(gosmo.SeedingAutomatic)) {
			if err := ag.GrantCreateAnyDatabase(joinCtx); err != nil {
				return fmt.Errorf("availability group %q was created and %s joined it, but granting it CREATE ANY DATABASE failed — automatic seeding will silently seed nothing until that is granted: %w", req.Name, r.ServerName, err)
			}
		}
	}
	return nil
}

// agSecondaries is every replica of req but the first (the instance the CREATE
// runs on).
func agSecondaries(req gosmo.CreateAvailabilityGroupRequest) []gosmo.AvailabilityReplicaSpec {
	if len(req.Replicas) == 0 {
		return nil
	}
	return req.Replicas[1:]
}

// runScript replaces the shell's, which would emit the statements with nothing
// saying only the first runs here. Every JOIN and GRANT belongs to a different
// instance; run whole against the primary the script either errors or joins the
// primary to its own group.
func (d *NewAGDialog) runScript() {
	scriptCtx, script := gosmo.WithScript(d.ctx)
	sc := d.sc
	d.runPipeline(scriptCtx, func() {
		d.app.openQueryWithText(sc, "", multiInstanceScript("New Availability Group", script))
	})
}

// multiInstanceScript renders a script whose statements run on more than one
// instance under a warning saying so. The collector labels each statement
// ("-- on <server>") from where it was captured (a secondary's JOIN and GRANT
// are captured under gosmo.WithScriptServer), so a label cannot drift from its
// statement as one chosen by position did.
func multiInstanceScript(title string, script *gosmo.ScriptCollector) string {
	return "-- " + title + ": these statements do NOT all run on the same instance.\n\n" + script.String()
}

// showNewAGDialog opens New Availability Group — the Object Explorer context
// menu's entry point on the Availability Groups folder.
func (a *App) showNewAGDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newAGDialog.show(sc, node)
}
