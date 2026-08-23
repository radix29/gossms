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

// ag_props.go holds Availability Group Properties: the page set, the General
// page, and the pieces the Backup Preferences and Read-Only Routing pages share
// with it.
//
// Every page reads and writes through the group's *primary* replica: ALTER
// AVAILABILITY GROUP is rejected on a secondary, and the sys.dm_hadr_* columns a
// secondary sees are mostly empty, so a page loaded from one would show blanks
// it could not save. agOnPrimary is the chokepoint, and unlike resolveAGView it
// treats an unreachable primary as a hard error.

// agPropPages builds the page set for Availability Group Properties: the group's
// settings and its replicas' configuration (General), where automated backups
// run (Backup Preferences), and read-intent redirection (Read-Only Routing).
//
// Adding and removing databases, replicas and listeners is not here — those are
// cluster-wide operations with their own confirmation and seeding concerns, and
// live on the Object Explorer context menus.
func agPropPages(sc *db.ServerConn, agName string) []propPage {
	return []propPage{
		pageAGGeneral(sc, agName),
		pageAGBackupPreferences(sc, agName),
		pageAGReadOnlyRouting(sc, agName),
	}
}

// agOnPrimary resolves an availability group by name through a connection to its
// primary replica, opening one via db.ServerConn.Peer when the tree's connection
// is a secondary.
//
// Failure to reach the primary is an error here, unlike resolveAGView's
// degrade-to-partial-view: a Properties page loaded from a secondary would
// present editable rows whose Apply the server would reject.
func agOnPrimary(ctx context.Context, sc *db.ServerConn, name string) (*gosmo.AvailabilityGroup, error) {
	ag, _, err := agOnPrimaryFollowed(ctx, sc, name)
	return ag, err
}

// agOnPrimaryFollowed is agOnPrimary plus whether reaching the primary needed
// a peer connection — which the dashboard says out loud, since a reading taken
// from another instance is worth labelling as one.
func agOnPrimaryFollowed(ctx context.Context, sc *db.ServerConn, name string) (*gosmo.AvailabilityGroup, bool, error) {
	ag, err := sc.Server.AvailabilityGroupByNameContext(ctx, name)
	if err != nil {
		return nil, false, err
	}
	if ag.IsLocalPrimary() {
		return ag, false, nil
	}
	if ag.PrimaryReplicaServerName == "" {
		return nil, false, fmt.Errorf("availability group %q has no primary replica visible from %s — its settings can only be read and changed on the primary", name, sc.Opts.Server)
	}
	peer, err := sc.Peer(ctx, ag.PrimaryReplicaServerName)
	if err != nil {
		return nil, false, fmt.Errorf("connect to primary replica %s: %w", ag.PrimaryReplicaServerName, err)
	}
	ag, err = peer.Server.AvailabilityGroupByNameContext(ctx, name)
	return ag, true, err
}

// The keyword sets the replica dropdowns offer, spelled as both ALTER
// AVAILABILITY GROUP accepts them and the catalog's *_desc columns report them,
// so a value read off a replica always has a matching item and needs no
// translation. The Properties pages use the T-SQL spelling throughout, like
// Database Properties' recovery-model dropdown.
var (
	agAvailabilityModeItems = []string{"SYNCHRONOUS_COMMIT", "ASYNCHRONOUS_COMMIT", "CONFIGURATION_ONLY"}
	agFailoverModeItems     = []string{"MANUAL", "AUTOMATIC", "EXTERNAL"}
	agSeedingModeItems      = []string{"AUTOMATIC", "MANUAL"}
	agPrimaryRoleItems      = []string{"ALL", "READ_WRITE"}
	agSecondaryRoleItems    = []string{"NO", "READ_ONLY", "ALL"}
)

// agUnknownItem stands in for a replica setting the connected server version
// doesn't report — SeedingMode is empty before SQL Server 2016, and indexOf's
// not-found 0 would show "AUTOMATIC" as though the server had said so.
// agSelectValue maps it back to "", so a replica whose value was never known
// reads as unchanged rather than being set to a literal ALTER rejects.
const agUnknownItem = "(unknown)"

// agSetSelect points an existing row at value, widening the item list when value
// is not one of the known keywords, so the dropdown can never misreport what the
// server holds. selectPreserving's repoint-a-row form; the two share
// preservingItems and should stay that way.
func agSetSelect(row *propsheet.SelectRow, base []string, value string) {
	items, i := preservingItems(base, orDefault(value, agUnknownItem))
	row.SetItems(items)
	row.SetSelected(i)
}

// agSelectValue reads row back as a value to write, undoing agSetSelect's
// stand-in.
func agSelectValue(row *propsheet.SelectRow) string {
	return preservedValue(row, agUnknownItem)
}

// agFailureConditionItems describes FAILURE_CONDITION_LEVEL 1-5 in order, so the
// row's index maps to the level by +1. The numbers stay in the text because that
// is what sys.availability_groups reports and what T-SQL uses.
var agFailureConditionItems = []string{
	"1 - Server down",
	"2 - Server unresponsive",
	"3 - Critical server errors",
	"4 - Moderate server errors",
	"5 - Any qualifying failure condition",
}

// agReplicaEdit is one replica's pending General-page state alongside the values
// it loaded with. The originals are kept explicitly rather than read back off
// the gosmo replica at apply time: apply re-resolves the group from the primary,
// and diffing against those *fresh* values would write a stale setting back over
// someone else's change.
type agReplicaEdit struct {
	name string

	availabilityMode string
	failoverMode     string
	primaryRole      string
	secondaryRole    string
	seedingMode      string
	sessionTimeout   int

	origAvailabilityMode string
	origFailoverMode     string
	origPrimaryRole      string
	origSecondaryRole    string
	origSeedingMode      string
	origSessionTimeout   int
}

func agReplicaEditFrom(r *gosmo.AvailabilityReplica) *agReplicaEdit {
	return &agReplicaEdit{
		name:             r.ReplicaServerName,
		availabilityMode: r.AvailabilityMode, origAvailabilityMode: r.AvailabilityMode,
		failoverMode: r.FailoverMode, origFailoverMode: r.FailoverMode,
		primaryRole: r.PrimaryRoleAllowConnections, origPrimaryRole: r.PrimaryRoleAllowConnections,
		secondaryRole: r.SecondaryRoleAllowConnections, origSecondaryRole: r.SecondaryRoleAllowConnections,
		seedingMode: r.SeedingMode, origSeedingMode: r.SeedingMode,
		sessionTimeout: r.SessionTimeout, origSessionTimeout: r.SessionTimeout,
	}
}

func (e *agReplicaEdit) dirty() bool {
	return e.availabilityMode != e.origAvailabilityMode ||
		e.failoverMode != e.origFailoverMode ||
		e.primaryRole != e.origPrimaryRole ||
		e.secondaryRole != e.origSecondaryRole ||
		e.seedingMode != e.origSeedingMode ||
		e.sessionTimeout != e.origSessionTimeout
}

// agReplicasByName indexes a freshly read replica list for apply, which
// addresses replicas by the name the page loaded rather than by position: a
// replica added or removed between load and apply would shift the rows under the
// edits.
func agReplicasByName(replicas []*gosmo.AvailabilityReplica) map[string]*gosmo.AvailabilityReplica {
	byName := make(map[string]*gosmo.AvailabilityReplica, len(replicas))
	for _, r := range replicas {
		byName[strings.ToLower(r.ReplicaServerName)] = r
	}
	return byName
}

// agMissingReplicaErr is what every page's apply reports when a replica it
// holds an edit for is no longer in the group.
func agMissingReplicaErr(name string) error {
	return fmt.Errorf("replica %q is no longer part of this availability group — refresh the page and try again", name)
}

// wireGridEditor connects a per-row detail editor below a DataGrid to the grid's
// selection, the shape the Backup Preferences and Read-Only Routing pages share:
// moving off a row commits whatever was typed into the editor, loads the newly
// selected row into it, then redraws the grid. Returns the redraw a page's
// RevertFn needs after rewriting the edits behind the grid.
//
// The selected cell is saved and restored around SetData because SetData resets
// it to 0,0 and this redraw runs from inside OnSelectRow, after the grid has
// moved. Without the restore, every row but the first is unreachable: a click on
// another row selects the first instead, and since GridRow reports movement by
// comparing SelectedCell either side of the key, a grid that always came back to
// 0,0 answers "not handled" and the first arrow key throws focus out of it.
func wireGridEditor(grid *controls.DataGrid, headers []string, gridRows func() [][]string, commitCurrent, syncFromSelection func()) (reload func()) {
	redraw := func() { redrawGrid(grid, headers, gridRows()) }
	grid.OnSelectRow = func(int) {
		commitCurrent()
		syncFromSelection()
		redraw()
	}
	syncFromSelection()
	return func() {
		redraw()
		syncFromSelection()
	}
}

func pageAGGeneral(sc *db.ServerConn, agName string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			ag, err := agOnPrimary(ctx, sc, agName)
			if err != nil {
				return nil, nil, err
			}
			replicas, err := ag.ReplicasContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			dbs, err := ag.DatabasesContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			edits := make([]*agReplicaEdit, len(replicas))
			for i, r := range replicas {
				edits[i] = agReplicaEditFrom(r)
			}

			// -- group-level rows --

			// A synchronous secondary can only be required to commit if one
			// exists, so the ceiling is the replica count less the primary.
			maxRequiredSync := int64(len(replicas) - 1)
			if maxRequiredSync < 0 {
				maxRequiredSync = 0
			}
			requiredSyncRow := propsheet.Int("Required sync secondaries", int64(ag.RequiredSynchronizedSecondariesToCommit), 0, maxRequiredSync, "")
			dbFailoverRow := propsheet.Check("Database level health detection", ag.DBFailover)
			dtcRow := propsheet.Check("Per database DTC support", ag.DTCSupport)
			failureLevelRow := propsheet.Select("Failure condition level", agFailureConditionItems,
				agFailureConditionIndex(ag.FailureConditionLevel))
			healthTimeoutRow := propsheet.Int("Health check timeout", int64(ag.HealthCheckTimeout), 15000, 4294967295, "ms")

			// -- databases grid (read-only) --

			dbGrid := controls.NewDataGrid()
			dbGrid.SetData([]string{"Database name", "Synchronization state"}, agDatabaseRows(dbs))

			// -- replica grid and its detail rows --

			replicaHeaders := []string{"Server instance", "Role", "Availability mode", "Failover mode",
				"Connections in primary role", "Readable secondary", "Seeding mode", "Session timeout (s)"}
			roleByName := map[string]string{}
			for _, r := range replicas {
				roleByName[strings.ToLower(r.ReplicaServerName)] = r.Role
			}
			replicaRows := func() [][]string {
				rows := make([][]string, len(edits))
				for i, e := range edits {
					rows[i] = []string{
						e.name, roleByName[strings.ToLower(e.name)],
						e.availabilityMode, e.failoverMode,
						e.primaryRole, e.secondaryRole, e.seedingMode,
						strconv.Itoa(e.sessionTimeout),
					}
				}
				return rows
			}
			grid := controls.NewDataGrid()
			grid.SetData(replicaHeaders, replicaRows())
			grid.SetCellCursor(true)

			// Failover mode is the one replica setting the group's cluster type
			// constrains: EXTERNAL accepts only EXTERNAL and NONE only MANUAL,
			// both rejected with Msg 47101. Narrowing the dropdown is the whole
			// gate — agSetSelect widens it again for a replica whose stored value
			// is not in the list, so an already-illegal value still displays and
			// can be corrected.
			failoverModeItems, clusterWhy := agFailoverModesFor(ag.ClusterType)

			modeRow := propsheet.Select("Availability mode", agAvailabilityModeItems, 0)
			failoverRow := propsheet.Select("Failover mode", failoverModeItems, 0)
			primaryRoleRow := propsheet.Select("Connections in primary role", agPrimaryRoleItems, 0)
			secondaryRoleRow := propsheet.Select("Readable secondary", agSecondaryRoleItems, 0)
			seedingRow := propsheet.Select("Seeding mode", agSeedingModeItems, 0)
			timeoutRow := propsheet.Int("Session timeout", 10, 5, 3600, "s")

			selected := func() *agReplicaEdit {
				i := grid.SelectedRow()
				if i < 0 || i >= len(edits) {
					return nil
				}
				return edits[i]
			}
			var current *agReplicaEdit
			// commitCurrent folds the detail rows back into the replica they were
			// last synced from, before the selection moves off it.
			commitCurrent := func() {
				if current == nil {
					return
				}
				current.availabilityMode = agSelectValue(modeRow)
				current.failoverMode = agSelectValue(failoverRow)
				current.primaryRole = agSelectValue(primaryRoleRow)
				current.secondaryRole = agSelectValue(secondaryRoleRow)
				current.seedingMode = agSelectValue(seedingRow)
				if n, err := timeoutRow.IntValue(); err == nil {
					current.sessionTimeout = int(n)
				}
			}
			syncFromSelection := func() {
				current = selected()
				if current == nil {
					return
				}
				agSetSelect(modeRow, agAvailabilityModeItems, current.availabilityMode)
				agSetSelect(failoverRow, failoverModeItems, current.failoverMode)
				agSetSelect(primaryRoleRow, agPrimaryRoleItems, current.primaryRole)
				agSetSelect(secondaryRoleRow, agSecondaryRoleItems, current.secondaryRole)
				agSetSelect(seedingRow, agSeedingModeItems, current.seedingMode)
				timeoutRow.SetValue(strconv.Itoa(current.sessionTimeout))
			}
			reload := wireGridEditor(grid, replicaHeaders, replicaRows, commitCurrent, syncFromSelection)

			gridRow := propsheet.NewGridRow(grid, 9)
			gridRow.DirtyFn = func() bool {
				for _, e := range edits {
					if e.dirty() {
						return true
					}
				}
				return false
			}
			gridRow.RevertFn = func() {
				for i, r := range replicas {
					edits[i] = agReplicaEditFrom(r)
				}
				current = nil
				reload()
			}

			rows := []propsheet.Row{
				propsheet.Section("Availability group"),
				propsheet.Static("Name", ag.Name),
				propsheet.Static("Cluster type", orDefault(ag.ClusterType, "WSFC (not reported before SQL Server 2017)")),
				propsheet.Static("Primary replica", orDefault(ag.PrimaryReplicaServerName, "(not visible from here)")),
				propsheet.Static("Synchronization health", orDefault(ag.SynchronizationHealth, "(unknown)")),
				propsheet.Static("Basic availability group", boolStr(ag.BasicFeatures)),
				propsheet.Static("Contained", boolStr(ag.IsContained)),
				requiredSyncRow,
				dbFailoverRow,
				dtcRow,
				propsheet.Section("Health detection"),
				failureLevelRow,
				healthTimeoutRow,
			}
			if len(failoverModeItems) == 1 {
				rows = append(rows, propsheet.Note(fmt.Sprintf("Cluster type %s %s, so every replica's failover mode must be %s — the dropdown offers nothing else.",
					strings.ToUpper(orDefault(ag.ClusterType, "WSFC")), clusterWhy, failoverModeItems[0])))
			}
			if strings.EqualFold(ag.ClusterType, "EXTERNAL") {
				rows = append(rows, propsheet.Note("A resource agent may also maintain the required-synchronized-secondaries setting itself and reassert its own value."))
			}
			rows = append(rows,
				propsheet.Section("Availability databases"),
				propsheet.NewGridRow(dbGrid, 7),
				propsheet.Note("Databases are added to and removed from the group through the Availability Databases folder in Object Explorer."),
				propsheet.Section("Availability replicas"),
				gridRow,
				propsheet.Section("Selected replica"),
				modeRow, failoverRow, primaryRoleRow, secondaryRoleRow, seedingRow, timeoutRow,
			)
			f := propsheet.NewForm(rows...)

			apply := func(ctx context.Context) error {
				commitCurrent()
				ag, err := agOnPrimary(ctx, sc, agName)
				if err != nil {
					return err
				}
				if requiredSyncRow.Dirty() {
					n, err := requiredSyncRow.IntValue()
					if err != nil {
						return err
					}
					if err := ag.SetRequiredSynchronizedSecondariesToCommitContext(ctx, int(n)); err != nil {
						return err
					}
				}
				if dbFailoverRow.Dirty() {
					if err := ag.SetDBFailoverContext(ctx, dbFailoverRow.Checked()); err != nil {
						return err
					}
				}
				if dtcRow.Dirty() {
					if err := ag.SetDTCSupportContext(ctx, dtcRow.Checked()); err != nil {
						return err
					}
				}
				if failureLevelRow.Dirty() {
					if err := ag.SetFailureConditionLevelContext(ctx, failureLevelRow.Selected()+1); err != nil {
						return err
					}
				}
				if healthTimeoutRow.Dirty() {
					ms, err := healthTimeoutRow.IntValue()
					if err != nil {
						return err
					}
					if err := ag.SetHealthCheckTimeoutContext(ctx, int(ms)); err != nil {
						return err
					}
				}
				return applyAGReplicaEdits(ctx, ag, edits)
			}
			return f, apply, nil
		},
	}
}

// applyAGReplicaEdits writes every changed replica setting, one ALTER per
// setting, against replicas re-read from the primary.
func applyAGReplicaEdits(ctx context.Context, ag *gosmo.AvailabilityGroup, edits []*agReplicaEdit) error {
	if !agAnyReplicaDirty(edits) {
		return nil
	}
	replicas, err := ag.ReplicasContext(ctx)
	if err != nil {
		return err
	}
	byName := agReplicasByName(replicas)
	for _, e := range edits {
		if !e.dirty() {
			continue
		}
		r := byName[strings.ToLower(e.name)]
		if r == nil {
			return agMissingReplicaErr(e.name)
		}
		if e.availabilityMode != e.origAvailabilityMode {
			if err := r.SetAvailabilityModeContext(ctx, e.availabilityMode); err != nil {
				return err
			}
		}
		if e.failoverMode != e.origFailoverMode {
			if err := r.SetFailoverModeContext(ctx, e.failoverMode); err != nil {
				return err
			}
		}
		if e.primaryRole != e.origPrimaryRole {
			if err := r.SetPrimaryRoleAllowConnectionsContext(ctx, e.primaryRole); err != nil {
				return err
			}
		}
		if e.secondaryRole != e.origSecondaryRole {
			if err := r.SetSecondaryRoleAllowConnectionsContext(ctx, e.secondaryRole); err != nil {
				return err
			}
		}
		if e.seedingMode != e.origSeedingMode {
			if err := r.SetSeedingModeContext(ctx, e.seedingMode); err != nil {
				return err
			}
		}
		if e.sessionTimeout != e.origSessionTimeout {
			if err := r.SetSessionTimeoutContext(ctx, e.sessionTimeout); err != nil {
				return err
			}
		}
	}
	return nil
}

func agAnyReplicaDirty(edits []*agReplicaEdit) bool {
	for _, e := range edits {
		if e.dirty() {
			return true
		}
	}
	return false
}

// agFailureConditionIndex maps a FAILURE_CONDITION_LEVEL onto its row in
// agFailureConditionItems, clamping to the list rather than letting a negative
// index reach the widget.
func agFailureConditionIndex(level int) int {
	if level < 1 || level > len(agFailureConditionItems) {
		return 0
	}
	return level - 1
}

// agDatabaseRows renders the group's databases for the General page's read-only
// grid, one row per database with every distinct state its replicas report — as
// agDatabaseLabel does.
func agDatabaseRows(dbs []*gosmo.AvailabilityDatabase) [][]string {
	summaries := summarizeAGDatabases(dbs)
	rows := make([][]string, len(summaries))
	for i, s := range summaries {
		state := strings.Join(s.States, ", ")
		if state == "" {
			state = "(not seeded on any replica)"
		}
		if s.Suspended {
			state += " (suspended)"
		}
		rows[i] = []string{s.Name, state}
	}
	return rows
}
