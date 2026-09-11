package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// ag_add_replica_dialog.go is "Add Replica..." on a group's Availability
// Replicas folder (SSMS's Add Replica wizard), the only way to add a replica to
// an existing group.
//
// # Adding a replica is three statements on two instances
//
// ADD REPLICA runs on the primary and leaves the replica disconnected. The
// replica must then JOIN itself and, for automatic seeding, GRANT CREATE ANY
// DATABASE, without which AUTOMATIC seeding silently seeds nothing. Same shape
// as NewAGDialog.createGroup.
//
// Replicas are reached through db.ServerConn.Peer: the instance's own saved
// connection if any (and working), else this connection's credentials. A
// replica needing a different login or port is reached by connecting to it once
// via File > Connect.

// agAddReplicaPrefetch is read from the primary before building pages: what the
// new replica must be compatible with, and its defaults.
type agAddReplicaPrefetch struct {
	// primary names the replica everything here was read from.
	primary string

	// clusterType fixes the legal failover modes; see agFailoverModesFor.
	clusterType string

	// existing is every current replica, lowercased, so a duplicate is refused
	// here.
	existing map[string]bool

	// defaults are the primary's own settings, the best guess for a new
	// replica.
	defaults newAGReplica
}

// AGAddReplicaDialog is the Add Replica to Availability Group dialog.
type AGAddReplicaDialog struct {
	newObjectDialog[agAddReplicaPrefetch]

	// agName and node are set by show before the shell's show.
	agName string
	node   *explorerNode

	// resolved is the instance Connect reached, with its endpoint URL. Empty
	// until Connect succeeds, which preflight checks: ADD REPLICA needs an
	// ENDPOINT_URL, and a guessed one never connects.
	resolved newAGReplica

	// commit copies the form into resolved; assigned by buildPages.
	commit func()
}

// NewAGAddReplicaDialog creates the dialog and wires its callbacks.
func NewAGAddReplicaDialog(app *App) *AGAddReplicaDialog {
	d := &AGAddReplicaDialog{}
	d.init(app, newObjectConfig[agAddReplicaPrefetch]{
		title:   "Add Replica to Availability Group",
		noun:    "Replica",
		verb:    "added to the availability group",
		pages:   []string{"General"},
		fetch:   d.fetchPrefetch,
		build:   d.buildPages,
		refresh: func(*db.ServerConn) { d.app.explorer.Reload(d.node) },
	})
	// The shell would script all three statements as if run here; two belong to
	// the new instance. See runScript.
	d.OnScript = d.runScript
	return d
}

func (d *AGAddReplicaDialog) show(sc *db.ServerConn, agName string, node *explorerNode) {
	d.agName = agName
	d.node = node
	d.resolved = newAGReplica{}
	d.commit = nil
	d.newObjectDialog.show(sc)
	d.SetHeader("Availability group: "+agName, "Server: "+sc.Opts.Server)
}

// fetchPrefetch reads from the primary: ADD REPLICA is rejected on a secondary,
// and cluster type and replicas must come from the instance running it.
func (d *AGAddReplicaDialog) fetchPrefetch(ctx context.Context, sc *db.ServerConn) (*agAddReplicaPrefetch, error) {
	ag, err := agOnPrimary(ctx, sc, d.agName)
	if err != nil {
		return nil, err
	}
	replicas, err := ag.ReplicasContext(ctx)
	if err != nil {
		return nil, err
	}

	pf := &agAddReplicaPrefetch{
		primary:     ag.PrimaryReplicaServerName,
		clusterType: ag.ClusterType,
		existing:    map[string]bool{},
		defaults: newAGReplica{
			availabilityMode: "SYNCHRONOUS_COMMIT",
			failoverMode:     agDefaultFailoverMode(ag.ClusterType),
			seedingMode:      "AUTOMATIC",
			primaryRole:      "ALL",
			secondaryRole:    "NO",
			backupPriority:   50,
			sessionTimeout:   10,
		},
	}
	for _, r := range replicas {
		pf.existing[strings.ToLower(r.ReplicaServerName)] = true
		if !strings.EqualFold(r.ReplicaServerName, ag.PrimaryReplicaServerName) {
			continue
		}
		pf.defaults = newAGReplica{
			availabilityMode: orDefault(r.AvailabilityMode, "SYNCHRONOUS_COMMIT"),
			failoverMode:     orDefault(r.FailoverMode, agDefaultFailoverMode(ag.ClusterType)),
			seedingMode:      orDefault(r.SeedingMode, "AUTOMATIC"),
			primaryRole:      orDefault(r.PrimaryRoleAllowConnections, "ALL"),
			secondaryRole:    orDefault(r.SecondaryRoleAllowConnections, "NO"),
			backupPriority:   r.BackupPriority,
			sessionTimeout:   r.SessionTimeout,
		}
	}
	return pf, nil
}

// agDefaultFailoverMode is the cluster type's failover mode when it permits
// only one. WSFC allows two; the first is offered.
func agDefaultFailoverMode(clusterType string) string {
	allowed, _ := agFailoverModesFor(clusterType)
	return allowed[0]
}

func (d *AGAddReplicaDialog) buildPages(pf *agAddReplicaPrefetch) {
	nameRow := propsheet.Text("Server instance", "", 30)
	// Editable: Connect fills it from the instance's endpoint, whose host is
	// its @@SERVERNAME, which other replicas may not resolve. Typing the FQDN
	// is the fix.
	endpointRow := propsheet.Text("Endpoint URL", "", 40)

	modeRow := propsheet.Select("Availability mode", agAvailabilityModeItems, indexOf(agAvailabilityModeItems, pf.defaults.availabilityMode))
	failoverRow := propsheet.Select("Failover mode", agFailoverModeItems, indexOf(agFailoverModeItems, pf.defaults.failoverMode))
	seedingRow := propsheet.Select("Seeding mode", agSeedingModeItems, indexOf(agSeedingModeItems, pf.defaults.seedingMode))
	primaryRoleRow := propsheet.Select("Connections in primary role", agPrimaryRoleItems, indexOf(agPrimaryRoleItems, pf.defaults.primaryRole))
	secondaryRoleRow := propsheet.Select("Readable secondary", agSecondaryRoleItems, indexOf(agSecondaryRoleItems, pf.defaults.secondaryRole))
	timeoutRow := propsheet.Int("Session timeout", int64(pf.defaults.sessionTimeout), 5, 3600, "s")
	priorityRow := propsheet.Int("Backup priority", int64(pf.defaults.backupPriority), 0, 100, "")

	d.commit = func() {
		// The row wins over what Connect read. Clearing it clears resolved,
		// which validation refuses.
		d.resolved.endpointURL = strings.TrimSpace(endpointRow.Value())
		d.resolved.availabilityMode = modeRow.Value()
		d.resolved.failoverMode = failoverRow.Value()
		d.resolved.seedingMode = seedingRow.Value()
		d.resolved.primaryRole = primaryRoleRow.Value()
		d.resolved.secondaryRole = secondaryRoleRow.Value()
		if n, err := timeoutRow.IntValue(); err == nil {
			d.resolved.sessionTimeout = int(n)
		}
		if n, err := priorityRow.IntValue(); err == nil {
			d.resolved.backupPriority = int(n)
		}
	}

	// Retyping a connected name invalidates its endpoint, or the dialog would
	// show the new name and write ADD REPLICA for the old.
	nameRow.SetOnChange(func(v string) {
		if !strings.EqualFold(strings.TrimSpace(v), d.resolved.name) {
			d.resolved.name, d.resolved.endpointURL = "", ""
			endpointRow.SetValue("")
		}
	})

	connectBtn := widgets.NewButton("Connect", func() {
		d.connect(pf, strings.TrimSpace(nameRow.Value()), func(r newAGReplica) {
			nameRow.SetValue(r.name)
			endpointRow.SetValue(r.endpointURL)
		})
	})

	d.forms[0] = propsheet.NewForm(
		propsheet.Section("Replica to add"),
		nameRow,
		propsheet.Buttons(connectBtn),
		endpointRow,
		propsheet.Note("Connect reads the instance's database mirroring endpoint, which ADD REPLICA needs and which cannot be guessed — an endpoint that is missing or not STARTED is refused here rather than producing a replica that never connects."),
		propsheet.Note("The URL it fills in names the instance the way that instance names itself. Edit the host if the other replicas cannot resolve it — the port is the endpoint's and should be left alone."),
		propsheet.Section("Replica settings"),
		modeRow, failoverRow, seedingRow, primaryRoleRow, secondaryRoleRow, timeoutRow, priorityRow,
		propsheet.Note(fmt.Sprintf("This group's cluster type is %s, so the failover mode must be %s.",
			strings.ToUpper(orDefault(pf.clusterType, "WSFC")),
			strings.Join(agAllowedFailoverModes(pf.clusterType), " or "))),
		propsheet.Note("AUTOMATIC seeding copies every database in the group over the endpoint once the replica joins; the replica is granted CREATE ANY DATABASE for it. MANUAL means restoring each one there WITH NORECOVERY and joining it afterwards."),
		propsheet.Note("Priority runs 1 (lowest) to 100 (highest) for automated backups. 0 excludes the replica entirely."),
	)

	d.objectName = func() string { return d.resolved.name }
	d.preflight = func() error {
		d.commit()
		return validateAddReplica(d.resolved, pf)
	}
	d.applyFns[0] = d.addReplica
}

// validateEndpointURL rejects URLs ADD REPLICA would store and then fail to
// connect over. Shape is tcp://host:port, as mirroring endpoints use and
// gosmo's endpointURL builds.
func validateEndpointURL(u string) error {
	const scheme = "tcp://"
	rest, ok := strings.CutPrefix(strings.ToLower(u), scheme)
	if !ok {
		return fmt.Errorf("endpoint URL %q has to start with tcp:// — the form is tcp://host:port", u)
	}
	host, port, ok := strings.Cut(rest, ":")
	if !ok || host == "" {
		return fmt.Errorf("endpoint URL %q needs a host and a port — the form is tcp://host:port", u)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("endpoint URL %q has no usable port — the form is tcp://host:port", u)
	}
	return nil
}

// agAllowedFailoverModes is agFailoverModesFor's list, for the note.
func agAllowedFailoverModes(clusterType string) []string {
	allowed, _ := agFailoverModesFor(clusterType)
	return allowed
}

// validateAddReplica rejects what the server would, with the reason.
func validateAddReplica(r newAGReplica, pf *agAddReplicaPrefetch) error {
	if r.name == "" || r.endpointURL == "" {
		return fmt.Errorf("type the instance to add and press Connect — its endpoint URL has to be read from the instance itself")
	}
	// ADD REPLICA accepts a malformed URL and the replica silently never
	// connects.
	if err := validateEndpointURL(r.endpointURL); err != nil {
		return err
	}
	if pf.existing[strings.ToLower(r.name)] {
		return fmt.Errorf("%s is already a replica of this availability group", r.name)
	}
	allowed, why := agFailoverModesFor(pf.clusterType)
	if !slices.ContainsFunc(allowed, func(m string) bool { return strings.EqualFold(m, r.failoverMode) }) {
		return fmt.Errorf("cluster type %s %s, so %s cannot use failover mode %s — set it to %s",
			strings.ToUpper(orDefault(pf.clusterType, "WSFC")), why, r.name, r.failoverMode,
			strings.Join(allowed, " or "))
	}
	return nil
}

// connect resolves the typed name: reach the instance with this connection's
// credentials and read its endpoint.
//
// Done before OK so the endpoint URL is on screen, and an unreachable instance
// is a dialog message rather than a half-run pipeline.
func (d *AGAddReplicaDialog) connect(pf *agAddReplicaPrefetch, name string, done func(newAGReplica)) {
	if name == "" {
		d.SetMessage("Type the instance to add first.", true)
		return
	}
	if pf.existing[strings.ToLower(name)] {
		d.SetMessage(fmt.Sprintf("%s is already a replica of this availability group.", name), true)
		return
	}

	d.probeReplicaEndpoint("connecting to an availability replica", name,
		func(peer *db.ServerConn, ep *gosmo.DatabaseMirroringEndpoint) {
			// The instance's @@SERVERNAME, not what was typed: JOIN matches the
			// catalog name, and an alias or address finds no replica.
			d.resolved.name = peer.Server.Name()
			d.resolved.endpointURL = ep.URL()
			if pf.existing[strings.ToLower(d.resolved.name)] {
				d.resolved.name, d.resolved.endpointURL = "", ""
				d.SetMessage(fmt.Sprintf("%s answers as %s, which is already a replica of this group.", name, peer.Server.Name()), true)
				return
			}
			d.SetMessage(fmt.Sprintf("Connected to %s (%s).", d.resolved.name, d.resolved.endpointURL), false)
			done(d.resolved)
		},
		func(err error) {
			// Cleared so a stale pair from an earlier Connect can't pass
			// preflight.
			d.resolved.name, d.resolved.endpointURL = "", ""
			d.SetMessage(err.Error(), true)
		})
}

// addReplica is the pipeline: ADD REPLICA on the primary, then JOIN and
// (automatic seeding) GRANT CREATE ANY DATABASE on the replica.
//
// A failed JOIN leaves an added, disconnected replica; the error says so and
// names the instance rather than removing it (as createGroup does).
func (d *AGAddReplicaDialog) addReplica(ctx context.Context) error {
	sc, agName, r := d.sc, d.agName, d.resolved

	ag, err := agOnPrimary(ctx, sc, agName)
	if err != nil {
		return err
	}
	spec := r.spec()
	if err := ag.AddReplicaContext(ctx, spec); err != nil {
		return err
	}

	target := sc.Server
	if !gosmo.Scripting(ctx) {
		peer, err := sc.Peer(ctx, r.name)
		if err != nil {
			return fmt.Errorf("replica %s was added to %q, but connecting to it to join failed: %w", r.name, agName, err)
		}
		target = peer.Server
	}
	joined := target.AvailabilityGroup(agName)
	if err := joined.JoinContext(ctx, ag.ClusterType); err != nil {
		return fmt.Errorf("replica %s was added to %q, but could not join it: %w", r.name, agName, err)
	}
	if strings.EqualFold(r.seedingMode, "AUTOMATIC") {
		if err := joined.GrantCreateAnyDatabaseContext(ctx); err != nil {
			return fmt.Errorf("replica %s was added to %q and joined it, but granting it CREATE ANY DATABASE failed — automatic seeding will silently seed nothing until that is granted: %w", r.name, agName, err)
		}
	}
	return nil
}

// runScript replaces the shell's, which would present all three statements as
// runnable here; run on the primary, JOIN errors or joins the primary to its
// own group.
func (d *AGAddReplicaDialog) runScript() {
	scriptCtx, script := gosmo.WithScript(d.ctx)
	sc := d.sc
	d.runPipeline(scriptCtx, func() {
		d.app.openQueryWithText(sc, "", d.annotateScript(script.Statements))
	})
}

// annotateScript labels each statement with the instance it must run on.
func (d *AGAddReplicaDialog) annotateScript(statements []string) string {
	var b strings.Builder
	b.WriteString("-- Add Replica: these statements do NOT all run on the same instance.\n")

	// Statement 0 (ADD REPLICA) is the primary's; JOIN and GRANT are the new
	// replica's.
	primary := "(the primary replica)"
	switch {
	case d.prefetch != nil && d.prefetch.primary != "":
		primary = d.prefetch.primary
	case d.sc != nil:
		primary = d.sc.Opts.Server
	}
	targets := []string{primary, d.resolved.name, d.resolved.name}
	for i, stmt := range statements {
		target := "(unknown instance)"
		if i < len(targets) {
			target = targets[i]
		}
		fmt.Fprintf(&b, "\n-- on %s\n%s\nGO\n", target, stmt)
	}
	return b.String()
}

// showAGAddReplicaDialog opens Add Replica from a group's Availability Replicas
// folder. node is reloaded after the add.
func (a *App) showAGAddReplicaDialog(sc *db.ServerConn, agName string, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.agAddReplicaDialog.show(sc, agName, node)
}
