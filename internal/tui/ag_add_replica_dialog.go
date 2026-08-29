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
// Replicas folder — SSMS's Add Replica to Availability Group wizard. It is the
// only path to the verb once a group exists: New Availability Group's own Add
// Replica button just edits the CREATE it is about to issue.
//
// # Adding a replica is three statements on two instances
//
// ALTER AVAILABILITY GROUP ... ADD REPLICA runs on the primary and, on its own,
// leaves the new replica disconnected. The replica then has to run JOIN against
// itself — the primary cannot join anything on its behalf — and, for automatic
// seeding, GRANT CREATE ANY DATABASE, without which SEEDING_MODE = AUTOMATIC
// seeds nothing and reports no error for it. Exactly the shape
// NewAGDialog.createGroup has for the replicas named in a CREATE, which is why
// the two read alike.
//
// A replica is reached through db.ServerConn.Peer, which uses that instance's
// own saved connection when there is one and this connection's credentials
// otherwise — or when that saved connection will not connect, which is what
// keeps a stale saved login from making a replica unreachable. So a replica
// wanting a different login or port is reached by connecting to it once via
// File > Connect first.

// agAddReplicaPrefetch is what the dialog reads from the group's primary before
// any page is built: what the new replica has to be compatible with, and what
// its settings should default to.
type agAddReplicaPrefetch struct {
	// primary names the replica everything here was read from.
	primary string

	// clusterType fixes the legal failover modes; see agFailoverModesFor.
	clusterType string

	// existing is every replica already in the group, lower-cased, so a second
	// attempt at one is refused here rather than by the server.
	existing map[string]bool

	// defaults are the primary replica's own settings. A second replica of the
	// same group nearly always wants them, and matching the primary is the only
	// defensible guess for a mode the user has not chosen yet.
	defaults newAGReplica
}

// AGAddReplicaDialog is the Add Replica to Availability Group dialog.
type AGAddReplicaDialog struct {
	newObjectDialog[agAddReplicaPrefetch]

	// agName and node are set by show, before the shell's own show runs.
	agName string
	node   *explorerNode

	// resolved is the instance Connect reached, with the endpoint URL read off
	// it. Empty until Connect succeeds — which is what preflight tests, since
	// ADD REPLICA cannot be written without an ENDPOINT_URL and guessing one
	// produces a replica that never connects.
	resolved newAGReplica

	// commit copies the form's rows into resolved. Assigned by buildPages.
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
		refresh: func(*db.ServerConn) { refreshExplorerNode(d.app, d.node) },
	})
	// The shell would script all three statements as though they ran here; two
	// of them belong to the instance being added. See runScript.
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

// fetchPrefetch reads the group from its primary: ADD REPLICA is rejected on a
// secondary, and the cluster type and the existing replicas both have to come
// from the instance that will run the statement.
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

// agDefaultFailoverMode is the failover mode a cluster type permits, when it
// permits only one. WSFC allows two, so the first is offered and the user can
// change it.
func agDefaultFailoverMode(clusterType string) string {
	allowed, _ := agFailoverModesFor(clusterType)
	return allowed[0]
}

func (d *AGAddReplicaDialog) buildPages(pf *agAddReplicaPrefetch) {
	nameRow := propsheet.Text("Server instance", "", 30)
	// Editable, not Static. Connect fills it from the instance's own endpoint,
	// whose host comes from that instance's @@SERVERNAME — so an instance whose
	// short name the other replicas cannot resolve produces a URL that parses,
	// is accepted, and then never connects. Typing the FQDN is the only repair,
	// and there was none.
	endpointRow := propsheet.Text("Endpoint URL", "", 40)

	modeRow := propsheet.Select("Availability mode", agAvailabilityModeItems, indexOf(agAvailabilityModeItems, pf.defaults.availabilityMode))
	failoverRow := propsheet.Select("Failover mode", agFailoverModeItems, indexOf(agFailoverModeItems, pf.defaults.failoverMode))
	seedingRow := propsheet.Select("Seeding mode", agSeedingModeItems, indexOf(agSeedingModeItems, pf.defaults.seedingMode))
	primaryRoleRow := propsheet.Select("Connections in primary role", agPrimaryRoleItems, indexOf(agPrimaryRoleItems, pf.defaults.primaryRole))
	secondaryRoleRow := propsheet.Select("Readable secondary", agSecondaryRoleItems, indexOf(agSecondaryRoleItems, pf.defaults.secondaryRole))
	timeoutRow := propsheet.Int("Session timeout", int64(pf.defaults.sessionTimeout), 5, 3600, "s")
	priorityRow := propsheet.Int("Backup priority", int64(pf.defaults.backupPriority), 0, 100, "")

	d.commit = func() {
		// The row wins over what Connect read, so an edited host is what ADD
		// REPLICA gets. Clearing it clears resolved too, which validation then
		// refuses — the same answer as never having connected.
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

	// Typing over a name that was already connected to invalidates the endpoint
	// with it. Without this the dialog would show the new name and write ADD
	// REPLICA for the old one, which is the worst of both.
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

// validateEndpointURL rejects an endpoint URL that ADD REPLICA would store and
// then fail to connect over. The shape is tcp://host:port — the only one
// database mirroring endpoints use, and the one gosmo's endpointURL builds.
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

// agAllowedFailoverModes is agFailoverModesFor's list alone, for the note.
func agAllowedFailoverModes(clusterType string) []string {
	allowed, _ := agFailoverModesFor(clusterType)
	return allowed
}

// validateAddReplica rejects what the server would, naming the reason.
func validateAddReplica(r newAGReplica, pf *agAddReplicaPrefetch) error {
	if r.name == "" || r.endpointURL == "" {
		return fmt.Errorf("type the instance to add and press Connect — its endpoint URL has to be read from the instance itself")
	}
	// The URL is editable, so it is now the one field a typo reaches the server
	// through. ADD REPLICA takes a malformed one without complaint and the
	// replica simply never connects, which is diagnosed hours later.
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

// connect resolves the typed instance name: reach it with this connection's
// credentials and read its endpoint off the instance itself.
//
// Done before OK rather than inside the apply, so the endpoint URL that is
// about to be written into ADD REPLICA is on screen — and so an unreachable
// instance is a message in the dialog rather than a half-run pipeline.
func (d *AGAddReplicaDialog) connect(pf *agAddReplicaPrefetch, name string, done func(newAGReplica)) {
	if name == "" {
		d.SetMessage("Type the instance to add first.", true)
		return
	}
	if pf.existing[strings.ToLower(name)] {
		d.SetMessage(fmt.Sprintf("%s is already a replica of this availability group.", name), true)
		return
	}

	sc := d.sc
	sessionCtx := d.ctx
	d.SetMessage("Connecting to "+name+"...", false)
	d.app.safego("connecting to an availability replica", func() {
		ctx, cancel := context.WithTimeout(sessionCtx, propFetchTimeout)
		defer cancel()

		peer, err := sc.Peer(ctx, name)
		var ep *gosmo.DatabaseMirroringEndpoint
		if err == nil {
			ep, err = replicaEndpoint(ctx, peer)
		}
		d.app.postAndWake(func() {
			if d.ctx != sessionCtx {
				return
			}
			if err != nil {
				d.resolved.name, d.resolved.endpointURL = "", ""
				d.SetMessage(err.Error(), true)
				return
			}
			// The instance's own @@SERVERNAME, not what was typed: ADD REPLICA
			// addresses the replica by the name the catalog will report, and an
			// alias or an address here makes JOIN find no matching replica.
			d.resolved.name = peer.Server.Name()
			d.resolved.endpointURL = ep.URL()
			if pf.existing[strings.ToLower(d.resolved.name)] {
				d.resolved.name, d.resolved.endpointURL = "", ""
				d.SetMessage(fmt.Sprintf("%s answers as %s, which is already a replica of this group.", name, peer.Server.Name()), true)
				return
			}
			d.SetMessage(fmt.Sprintf("Connected to %s (%s).", d.resolved.name, d.resolved.endpointURL), false)
			done(d.resolved)
		})
	})
}

// addReplica is the whole pipeline: ADD REPLICA on the primary, then JOIN and
// (for automatic seeding) GRANT CREATE ANY DATABASE on the replica itself.
//
// A replica that fails to join leaves a real, added replica behind in a
// disconnected state — which is why the error says so and names the instance,
// rather than removing a replica the user asked for on the strength of one
// failed statement. Same choice createGroup makes for the same reason.
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

// runScript replaces the shell's, which would emit all three statements with
// nothing saying that only the first runs here. Run whole against the primary,
// the JOIN either errors or joins the primary to its own group.
func (d *AGAddReplicaDialog) runScript() {
	scriptCtx, script := gosmo.WithScript(d.ctx)
	sc := d.sc
	d.runPipeline(scriptCtx, func() {
		d.app.openQueryWithText(sc, "", d.annotateScript(script.Statements))
	})
}

// annotateScript labels each statement with the instance it has to run on.
func (d *AGAddReplicaDialog) annotateScript(statements []string) string {
	var b strings.Builder
	b.WriteString("-- Add Replica: these statements do NOT all run on the same instance.\n")

	// Statement 0 is the ADD REPLICA, on the primary; the JOIN and the GRANT
	// below it belong to the replica being added.
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

// showAGAddReplicaDialog opens Add Replica — the Object Explorer context menu's
// entry point on a group's Availability Replicas folder. node is the tree node
// reloaded once the replica is added.
func (a *App) showAGAddReplicaDialog(sc *db.ServerConn, agName string, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.agAddReplicaDialog.show(sc, agName, node)
}
