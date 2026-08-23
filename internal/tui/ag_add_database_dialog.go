package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// ag_add_database_dialog.go is "Add Database..." on an availability group (and
// on its Availability Databases folder) — the counterpart of SSMS's Add
// Database to Availability Group wizard, reduced to the one choice that
// wizard's pages come down to.
//
// It is built on the newObjectDialog shell, so OK/Apply/Script Changes behave
// like every other create dialog. What it creates is group membership rather
// than an object, which is why it overrides the shell's success verb — the
// database already existed, and saying it was "created" would be a lie.

// agAddDBPrefetch is the one fetch this dialog needs: which databases on the
// primary can be added, and why the rest cannot.
type agAddDBPrefetch struct {
	// primary names the replica everything here was read from, for the header.
	primary string

	// eligible are the addable database names, in server order.
	eligible []string

	// excluded explains each database that was left out, e.g.
	// "HealthClinic — recovery model is SIMPLE". Shown so a database's absence
	// from the dropdown is never a mystery.
	excluded []string
}

// agDBCandidate is one database being considered for membership, reduced to
// the four facts the decision turns on. gosmo.Database keeps its metadata
// behind methods and its fields unexported, so the eligibility rule is written
// against this instead — which also lets it be tested without a server.
type agDBCandidate struct {
	Name          string
	RecoveryModel string
	State         string
	IsSystem      bool

	// LogChainStarted is gosmo's DatabaseRecoveryStatus.LogBackupChainStarted:
	// the database has had a full backup since it entered the FULL recovery
	// model. It comes from a separate server-wide read rather than from
	// gosmo.Database, which carries no backup state.
	LogChainStarted bool
}

// agCandidatesFrom pairs each database with its log backup chain state, keyed
// by lower-cased name.
func agCandidatesFrom(dbs []*gosmo.Database, logChain map[string]bool) []agDBCandidate {
	out := make([]agDBCandidate, 0, len(dbs))
	for _, d := range dbs {
		out = append(out, agDBCandidate{
			Name:            d.Name(),
			RecoveryModel:   string(d.RecoveryModel()),
			State:           d.State(),
			IsSystem:        d.IsSystem(),
			LogChainStarted: logChain[strings.ToLower(d.Name())],
		})
	}
	return out
}

// agEligibleDatabases splits the primary's databases into the ones that can
// join the group and the ones that cannot, with a reason for each exclusion.
//
// The rule SQL Server enforces is full recovery plus a started log backup
// chain, and both are checked. The second is not "has a row in
// msdb.dbo.backupset": verified live against the AG cluster on 2026-08-23, the
// backup history is wrong in both directions — a database whose history has
// been purged still joins, and one backed up before a SIMPLE round trip does
// not, failing with Msg 1475. What decides it is
// sys.database_recovery_status.last_log_backup_lsn, which gosmo reports as
// DatabaseRecoveryStatus.LogBackupChainStarted.
func agEligibleDatabases(dbs []agDBCandidate, inGroup map[string]bool) (eligible, excluded []string) {
	for _, d := range dbs {
		switch {
		case d.IsSystem:
			// Silent: nobody expects master in this list.
		case inGroup[strings.ToLower(d.Name)]:
			excluded = append(excluded, d.Name+" — already in an availability group")
		case !strings.EqualFold(d.RecoveryModel, string(gosmo.RecoveryModelFull)):
			excluded = append(excluded, fmt.Sprintf("%s — recovery model is %s, must be FULL", d.Name, d.RecoveryModel))
		case d.State != "" && !strings.EqualFold(d.State, "ONLINE"):
			excluded = append(excluded, fmt.Sprintf("%s — database is %s", d.Name, strings.ToLower(d.State)))
		case !d.LogChainStarted:
			excluded = append(excluded, d.Name+" — no full backup since it entered the FULL recovery model; back it up first")
		default:
			eligible = append(eligible, d.Name)
		}
	}
	return eligible, excluded
}

// AGAddDatabaseDialog is the Add Database to Availability Group dialog.
type AGAddDatabaseDialog struct {
	newObjectDialog[agAddDBPrefetch]

	// agName and node are set by show, before the shell's own show runs.
	agName string
	node   *explorerNode
}

// NewAGAddDatabaseDialog creates the dialog and wires its callbacks.
func NewAGAddDatabaseDialog(app *App) *AGAddDatabaseDialog {
	d := &AGAddDatabaseDialog{}
	d.init(app, newObjectConfig[agAddDBPrefetch]{
		title:   "Add Database to Availability Group",
		noun:    "Database",
		verb:    "added to the availability group",
		pages:   []string{"General"},
		fetch:   d.fetchPrefetch,
		build:   d.buildPages,
		refresh: func(*db.ServerConn) { refreshExplorerNode(d.app, d.node) },
	})
	return d
}

func (d *AGAddDatabaseDialog) show(sc *db.ServerConn, agName string, node *explorerNode) {
	d.agName = agName
	d.node = node
	d.newObjectDialog.show(sc)
	d.SetHeader("Availability group: "+agName, "Server: "+sc.Opts.Server)
}

// fetchPrefetch reads the candidate list from the primary, not from whichever
// replica the tree is on: ADD DATABASE runs there, and a secondary's copies are
// not addable in the first place.
func (d *AGAddDatabaseDialog) fetchPrefetch(ctx context.Context, sc *db.ServerConn) (*agAddDBPrefetch, error) {
	ag, err := agOnPrimary(ctx, sc, d.agName)
	if err != nil {
		return nil, err
	}
	primary := ag.Server()

	// Every group on the instance, not just this one — a database can only
	// belong to one, so one already in a different group is just as unaddable.
	groups, err := primary.AvailabilityGroupsContext(ctx)
	if err != nil {
		return nil, err
	}
	inGroup := map[string]bool{}
	for _, g := range groups {
		dbs, err := g.DatabasesContext(ctx)
		if err != nil {
			return nil, err
		}
		for _, adb := range dbs {
			inGroup[strings.ToLower(adb.DatabaseName)] = true
		}
	}

	// One server-wide read for the log backup chain state of every database,
	// rather than a backup-history query per candidate.
	statuses, err := primary.DatabaseRecoveryStatusesContext(ctx)
	if err != nil {
		return nil, err
	}
	logChain := map[string]bool{}
	for _, st := range statuses {
		logChain[strings.ToLower(st.DatabaseName)] = st.LogBackupChainStarted
	}

	dbs, err := primary.DatabasesContext(ctx)
	if err != nil {
		return nil, err
	}
	eligible, excluded := agEligibleDatabases(agCandidatesFrom(dbs, logChain), inGroup)
	return &agAddDBPrefetch{primary: ag.PrimaryReplicaServerName, eligible: eligible, excluded: excluded}, nil
}

func (d *AGAddDatabaseDialog) buildPages(pf *agAddDBPrefetch) {
	sc := d.sc
	agName := d.agName

	rows := []propsheet.Row{propsheet.Section("Database to add")}
	var dbRow *propsheet.SelectRow
	if len(pf.eligible) == 0 {
		rows = append(rows, propsheet.Note("No database on "+pf.primary+" can be added to this group."))
	} else {
		dbRow = propsheet.Select("Database", pf.eligible, 0)
		rows = append(rows, dbRow)
	}
	if len(pf.excluded) > 0 {
		rows = append(rows, propsheet.Section("Not offered"))
		for _, line := range pf.excluded {
			rows = append(rows, propsheet.Note(line))
		}
	}
	rows = append(rows,
		propsheet.Section("Prerequisites"),
		propsheet.Note("The database must be in the FULL recovery model and have a full backup taken since — both are checked, and a database missing either is listed above rather than offered."),
		propsheet.Note("Back one up from the database's Tasks > Back Up, then reopen this dialog."),
		propsheet.Note("A secondary set to AUTOMATIC seeding seeds itself. One set to MANUAL needs the database restored there WITH NORECOVERY and then joined."),
	)

	d.forms[0] = propsheet.NewForm(rows...)
	d.objectName = func() string {
		if dbRow == nil {
			return ""
		}
		return dbRow.Value()
	}
	d.preflight = func() error {
		if d.objectName() == "" {
			return fmt.Errorf("there is no database available to add to %q", agName)
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		ag, err := agOnPrimary(ctx, sc, agName)
		if err != nil {
			return err
		}
		return ag.AddDatabaseContext(ctx, d.objectName())
	}
}

// showAGAddDatabaseDialog opens Add Database for a group — the Object Explorer
// context menu's entry point on an availability group and on its Availability
// Databases folder. node is the tree node reloaded once the database is added.
func (a *App) showAGAddDatabaseDialog(sc *db.ServerConn, agName string, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.agAddDatabaseDialog.show(sc, agName, node)
}
