package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// new_ag_pages.go builds New Availability Group's two pages. Both edit the same
// NewAGDialog state — the replica list in particular — because everything they
// collect goes into one CREATE AVAILABILITY GROUP rather than a create followed
// by ALTERs.

// agClusterTypeItems are the CLUSTER_TYPE values, in the order they are worth
// offering: EXTERNAL first because a Linux instance is the case where the value
// has to be chosen at all. WSFC is the server's own default and the only one
// that works with no cluster type stated.
var agClusterTypeItems = []string{"EXTERNAL", "WSFC", "NONE"}

func (d *NewAGDialog) buildPages(pf *newAGPrefetch) {
	d.databases = append([]newAGDatabase(nil), pf.databases...)
	d.replicas = nil
	if pf.blocker == "" {
		d.replicas = []*newAGReplica{{
			name:             pf.primaryName,
			endpointURL:      pf.primaryEndpoint,
			availabilityMode: "SYNCHRONOUS_COMMIT",
			failoverMode:     "EXTERNAL",
			seedingMode:      "AUTOMATIC",
			primaryRole:      "ALL",
			secondaryRole:    "NO",
			backupPriority:   50,
			sessionTimeout:   10,
			isPrimary:        true,
		}}
	}

	d.buildGeneralPage(pf)
	d.buildBackupPage(pf)

	d.objectName = func() string { return strings.TrimSpace(d.groupName) }
	d.preflight = func() error {
		if pf.blocker != "" {
			return fmt.Errorf("%s", pf.blocker)
		}
		if d.commitGeneralPage != nil {
			d.commitGeneralPage()
		}
		return validateNewAG(d.objectName(), d.clusterType, d.replicas, pf.existingGroups)
	}
	d.applyFns[0] = d.createGroup
}

func (d *NewAGDialog) buildGeneralPage(pf *newAGPrefetch) {
	if pf.blocker != "" {
		d.forms[0] = propsheet.NewForm(
			propsheet.Section("Availability group"),
			propsheet.Note(pf.blocker),
		)
		return
	}

	nameRow := propsheet.Text("Availability group name", "", 30)
	clusterRow := propsheet.Select("Cluster type", agClusterTypeItems, 0)
	requiredSyncRow := propsheet.Int("Required synchronized secondaries to commit", 0, 0, 2, "")
	dbFailoverRow := propsheet.Check("Database level health detection", false)
	dtcRow := propsheet.Check("Per database DTC support", false)
	containedRow := propsheet.Check("Contained", false)

	dbGridRow, includeRow, commitDatabases := d.databaseRows()
	replicaRows, commitReplicas := d.replicaRows()

	d.commitGeneralPage = func() {
		commitDatabases()
		commitReplicas()
		d.groupName = nameRow.Value()
		d.clusterType = clusterRow.Value()
		d.dbFailover = dbFailoverRow.Checked()
		d.dtcSupport = dtcRow.Checked()
		d.contained = containedRow.Checked()
		if n, err := requiredSyncRow.IntValue(); err == nil {
			d.requiredSync = int(n)
		}
	}
	rows := []propsheet.Row{
		propsheet.Section("Availability group"),
		nameRow, clusterRow, requiredSyncRow,
		dbFailoverRow, dtcRow, containedRow,
		propsheet.Note("Cluster type EXTERNAL is an externally managed cluster — Pacemaker on Linux, which then owns failover. NONE is a read-scale group with no cluster manager and only forced failover. WSFC needs a Windows Server Failover Cluster under the instance."),
		propsheet.Note("The cluster type fixes every replica's failover mode: EXTERNAL requires EXTERNAL, NONE requires MANUAL, WSFC takes MANUAL or AUTOMATIC. Set each replica's below to match."),
		propsheet.Section("Availability databases"),
		dbGridRow, includeRow,
		propsheet.Note("Each database must be in the FULL recovery model and have a full backup. Databases can also be added after the group exists."),
	}
	if len(pf.excluded) > 0 {
		rows = append(rows, propsheet.Section("Databases not offered"))
		for _, line := range pf.excluded {
			rows = append(rows, propsheet.Note(line))
		}
	}
	rows = append(rows, replicaRows...)
	d.forms[0] = propsheet.NewForm(rows...)
}

// validateNewAG rejects what the server would, but with an explanation of why.
//
// The two cluster-type rules are the ones worth catching here rather than
// letting CREATE fail: both are consequences of what the cluster type *means*,
// and the server's own errors name neither the replica nor the reason.
func validateNewAG(name, clusterType string, replicas []*newAGReplica, existing map[string]bool) error {
	if name == "" {
		return fmt.Errorf("availability group name is required")
	}
	if existing[strings.ToLower(name)] {
		return fmt.Errorf("an availability group named %q already exists on this instance", name)
	}
	if len(replicas) < 2 {
		return fmt.Errorf("an availability group needs at least one secondary replica — add one on the General page")
	}
	for _, r := range replicas {
		if r.endpointURL == "" {
			return fmt.Errorf("replica %s has no endpoint URL", r.name)
		}
		allowed, why := agFailoverModesFor(clusterType)
		if !slices.ContainsFunc(allowed, func(m string) bool { return strings.EqualFold(m, r.failoverMode) }) {
			return fmt.Errorf("cluster type %s %s, so %s cannot use failover mode %s — set it to %s",
				strings.ToUpper(orDefault(clusterType, "WSFC")), why, r.name, r.failoverMode,
				strings.Join(allowed, " or "))
		}
	}
	return nil
}

// agFailoverModesFor is which failover modes a cluster type permits, and the
// clause explaining why, for the error message.
//
// Not a preference in any of the three cases, and the server's own errors do
// not name the replica: CLUSTER_TYPE = NONE is rejected with Msg 47101 ("only
// supports MANUAL failover mode"), and EXTERNAL requires the mode of the same
// name because the external cluster manager owns failover outright. Both
// verified against SQL Server 2025.
func agFailoverModesFor(clusterType string) (allowed []string, why string) {
	switch strings.ToUpper(clusterType) {
	case "EXTERNAL":
		return []string{"EXTERNAL"}, "hands failover to the external cluster manager"
	case "NONE":
		return []string{"MANUAL"}, "has no cluster manager to arbitrate a failover"
	default:
		return []string{"MANUAL", "AUTOMATIC"}, "is a Windows Server Failover Cluster, which arbitrates failover itself"
	}
}

// databaseRows builds the include-a-database grid and the checkbox that edits
// the selected row — the same grid-plus-detail-row idiom the Properties pages
// use, rather than a multi-select list, which propsheet has no row type for.
func (d *NewAGDialog) databaseRows() (propsheet.Row, propsheet.Row, func()) {
	headers := []string{"Database name", "In the group"}
	rowsFor := func() [][]string {
		rows := make([][]string, len(d.databases))
		for i, db := range d.databases {
			rows[i] = []string{db.name, boolStr(db.included)}
		}
		return rows
	}
	grid := controls.NewDataGrid()
	grid.SetData(headers, rowsFor())
	grid.SetCellCursor(true)

	includeRow := propsheet.Check("Include the selected database", false)

	current := -1
	commit := func() {
		if current >= 0 && current < len(d.databases) {
			d.databases[current].included = includeRow.Checked()
		}
	}
	sync := func() {
		current = grid.SelectedRow()
		if current >= 0 && current < len(d.databases) {
			includeRow.SetChecked(d.databases[current].included)
		}
	}
	reload := wireGridEditor(grid, headers, rowsFor, commit, sync)

	// The inclusions the page opened with — nothing, since the group does not
	// exist yet. RevertFn needs a baseline of its own because the state it
	// restores lives on the dialog, not in the grid: Ctrl+Z otherwise emptied
	// the name field, said "Reverted to the loaded values", and left every
	// database the user had ticked still queued for the CREATE.
	baseline := slices.Clone(d.databases)

	gridRow := propsheet.NewGridRow(grid, 7)
	// The grid mirrors state the checkbox owns, so it has to be redrawn from
	// that state whenever the checkbox is read back — the row itself is never
	// edited in place.
	gridRow.DirtyFn = func() bool {
		commit()
		reload()
		for _, db := range d.databases {
			if db.included {
				return true
			}
		}
		return false
	}
	gridRow.RevertFn = func() {
		d.databases = slices.Clone(baseline)
		reload()
	}
	return gridRow, includeRow, commit
}

// replicaRows builds the replica grid, its per-replica detail rows, and the
// add/remove controls.
func (d *NewAGDialog) replicaRows() ([]propsheet.Row, func()) {
	headers := []string{"Server instance", "Role", "Availability mode", "Failover mode",
		"Seeding mode", "Connections in primary role", "Readable secondary", "Endpoint URL"}
	rowsFor := func() [][]string {
		rows := make([][]string, len(d.replicas))
		for i, r := range d.replicas {
			role := "Secondary"
			if r.isPrimary {
				role = "Primary"
			}
			rows[i] = []string{r.name, role, r.availabilityMode, r.failoverMode,
				r.seedingMode, r.primaryRole, r.secondaryRole, r.endpointURL}
		}
		return rows
	}
	grid := controls.NewDataGrid()
	grid.SetData(headers, rowsFor())
	grid.SetCellCursor(true)

	modeRow := propsheet.Select("Availability mode", agAvailabilityModeItems, 0)
	failoverRow := propsheet.Select("Failover mode", agFailoverModeItems, 0)
	seedingRow := propsheet.Select("Seeding mode", agSeedingModeItems, 0)
	primaryRoleRow := propsheet.Select("Connections in primary role", agPrimaryRoleItems, 0)
	secondaryRoleRow := propsheet.Select("Readable secondary", agSecondaryRoleItems, 0)
	timeoutRow := propsheet.Int("Session timeout", 10, 5, 3600, "s")
	addNameRow := propsheet.Text("Instance to add", "", 30)

	current := -1
	commit := func() {
		if current < 0 || current >= len(d.replicas) {
			return
		}
		r := d.replicas[current]
		r.availabilityMode = modeRow.Value()
		r.failoverMode = failoverRow.Value()
		r.seedingMode = seedingRow.Value()
		r.primaryRole = primaryRoleRow.Value()
		r.secondaryRole = secondaryRoleRow.Value()
		if n, err := timeoutRow.IntValue(); err == nil {
			r.sessionTimeout = int(n)
		}
	}
	sync := func() {
		current = grid.SelectedRow()
		if current < 0 || current >= len(d.replicas) {
			return
		}
		r := d.replicas[current]
		agSetSelect(modeRow, agAvailabilityModeItems, r.availabilityMode)
		agSetSelect(failoverRow, agFailoverModeItems, r.failoverMode)
		agSetSelect(seedingRow, agSeedingModeItems, r.seedingMode)
		agSetSelect(primaryRoleRow, agPrimaryRoleItems, r.primaryRole)
		agSetSelect(secondaryRoleRow, agSecondaryRoleItems, r.secondaryRole)
		timeoutRow.SetValue(strconv.Itoa(r.sessionTimeout))
	}
	reload := wireGridEditor(grid, headers, rowsFor, commit, sync)

	addBtn := widgets.NewButton("Add Replica", func() {
		commit()
		d.addReplica(strings.TrimSpace(addNameRow.Value()), func() {
			addNameRow.SetValue("")
			reload()
		})
	})
	removeBtn := widgets.NewButton("Remove Replica", func() {
		commit()
		if current < 0 || current >= len(d.replicas) {
			return
		}
		if d.replicas[current].isPrimary {
			d.SetMessage("The primary replica is the instance the group is created on and cannot be removed.", true)
			return
		}
		d.replicas = append(d.replicas[:current], d.replicas[current+1:]...)
		current = -1
		reload()
	})

	// The replica list the page opened with. Same reason databaseRows keeps
	// one: without it Ctrl+Z reverted the visible rows and left every added
	// replica, and every per-replica mode already committed, in the request.
	// It restores the whole list, backup priorities included — those are set
	// on the Backup Preferences page but stored on these same replicas, and a
	// dialog that has not been applied has no other baseline to keep them
	// against.
	baseline := cloneAGReplicas(d.replicas)

	gridRow := propsheet.NewGridRow(grid, 8)
	gridRow.DirtyFn = func() bool {
		commit()
		reload()
		return len(d.replicas) > 1
	}
	gridRow.RevertFn = func() {
		d.replicas = cloneAGReplicas(baseline)
		current = -1
		reload()
	}

	return []propsheet.Row{
		propsheet.Section("Availability replicas"),
		gridRow,
		propsheet.Section("Selected replica"),
		modeRow, failoverRow, seedingRow, primaryRoleRow, secondaryRoleRow, timeoutRow,
		propsheet.Section("Add a replica"),
		addNameRow,
		propsheet.Buttons(addBtn, removeBtn),
		propsheet.Note("A replica is reached with this connection's credentials, and must already have a started database mirroring endpoint that this instance can connect to."),
		propsheet.Note("AUTOMATIC seeding copies the databases over the endpoint; the secondary is granted CREATE ANY DATABASE for it. MANUAL means restoring each database there WITH NORECOVERY and joining it afterwards."),
	}, commit
}

// addReplica resolves an instance name to a replica: connect with this
// connection's credentials, read its endpoint, and append it.
//
// The connect is what makes this worth doing rather than just taking the name
// on trust — the endpoint URL has to come from the instance itself, and an
// instance that cannot be reached now certainly cannot join later.
func (d *NewAGDialog) addReplica(name string, done func()) {
	if name == "" {
		d.SetMessage("Type the instance name to add first.", true)
		return
	}
	for _, r := range d.replicas {
		if strings.EqualFold(r.name, name) {
			d.SetMessage(fmt.Sprintf("%s is already a replica of this group.", name), true)
			return
		}
	}

	sc := d.sc
	sessionCtx := d.ctx
	d.SetMessage("Connecting to "+name+"...", false)
	d.app.safego("adding an availability replica", func() {
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
				d.SetMessage(err.Error(), true)
				return
			}
			// Seeding and failover default to the primary's, which is nearly
			// always what a second replica of the same group wants.
			primary := d.replicas[0]
			d.replicas = append(d.replicas, &newAGReplica{
				name:             peer.Server.Name(),
				endpointURL:      ep.URL(),
				availabilityMode: primary.availabilityMode,
				failoverMode:     primary.failoverMode,
				seedingMode:      primary.seedingMode,
				primaryRole:      primary.primaryRole,
				secondaryRole:    primary.secondaryRole,
				backupPriority:   50,
				sessionTimeout:   10,
			})
			d.SetMessage(fmt.Sprintf("Added %s (%s).", peer.Server.Name(), ep.URL()), false)
			done()
		})
	})
}

func (d *NewAGDialog) buildBackupPage(pf *newAGPrefetch) {
	if pf.blocker != "" {
		d.forms[1] = propsheet.NewForm(propsheet.Note(pf.blocker))
		return
	}

	prefRow := propsheet.Radio("Where should backups occur?", agBackupPreferenceLabels(), 0)

	headers := []string{"Server instance", "Backup priority", "Excluded"}
	rowsFor := func() [][]string {
		rows := make([][]string, len(d.replicas))
		for i, r := range d.replicas {
			rows[i] = []string{r.name, strconv.Itoa(r.backupPriority), boolStr(r.backupPriority == 0)}
		}
		return rows
	}
	grid := controls.NewDataGrid()
	grid.SetData(headers, rowsFor())
	grid.SetCellCursor(true)

	priorityRow := propsheet.Int("Backup priority", 50, 0, 100, "")

	current := -1
	commit := func() {
		if current < 0 || current >= len(d.replicas) {
			return
		}
		if n, err := priorityRow.IntValue(); err == nil {
			d.replicas[current].backupPriority = int(n)
		}
	}
	sync := func() {
		current = grid.SelectedRow()
		if current >= 0 && current < len(d.replicas) {
			priorityRow.SetValue(strconv.Itoa(d.replicas[current].backupPriority))
		}
	}
	reload := wireGridEditor(grid, headers, rowsFor, commit, sync)

	// Priorities are keyed by replica name rather than snapshotted as a slice,
	// unlike the General page's baseline: the replica list belongs to that
	// page, and a replica added there after this page was built must survive a
	// revert here. A name missing from the map is such a replica, and keeps
	// whatever priority it was added with.
	basePriority := map[string]int{}
	for _, r := range d.replicas {
		basePriority[r.name] = r.backupPriority
	}

	gridRow := propsheet.NewGridRow(grid, 8)
	// The replica list is the General page's, and a replica added there after
	// this page was built has to show up here. Rebuilding on every dirty check
	// is what keeps the two in step without a change notification.
	gridRow.DirtyFn = func() bool {
		commit()
		reload()
		return false
	}
	gridRow.RevertFn = func() {
		for _, r := range d.replicas {
			if p, ok := basePriority[r.name]; ok {
				r.backupPriority = p
			}
		}
		current = -1
		reload()
	}

	d.commitBackupPage = func() {
		commit()
		d.backupPreference = agBackupPreferenceItems[prefRow.Selected()].keyword
	}

	d.forms[1] = propsheet.NewForm(
		propsheet.Section("Automated backups"),
		prefRow,
		propsheet.Note("The preference is advisory: SQL Server does not enforce it. A backup job honours it by testing sys.fn_hadr_backup_is_preferred_replica before running."),
		propsheet.Section("Replica backup priorities"),
		gridRow,
		propsheet.Section("Selected replica"),
		priorityRow,
		propsheet.Note("Priority runs 1 (lowest) to 100 (highest). 0 excludes the replica from automated backups entirely."),
	)
}

// replicaEndpoint reads an instance's database mirroring endpoint and refuses
// the two states that would produce a group that looks created and never
// connects: no endpoint at all, and one that is not started.
func replicaEndpoint(ctx context.Context, sc *db.ServerConn) (*gosmo.DatabaseMirroringEndpoint, error) {
	ep, err := sc.Server.DatabaseMirroringEndpointContext(ctx)
	if err != nil {
		return nil, err
	}
	if ep == nil {
		return nil, fmt.Errorf("%s has no database mirroring endpoint — create one there first; a replica without one can never connect", sc.Server.Name())
	}
	if !strings.EqualFold(ep.State, "STARTED") {
		return nil, fmt.Errorf("%s's database mirroring endpoint %q is %s, not STARTED", sc.Server.Name(), ep.Name, ep.State)
	}
	return ep, nil
}
