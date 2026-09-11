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
// its Availability Databases folder): SSMS's Add Database to Availability Group
// wizard reduced to its one choice.
//
// Built on the newObjectDialog shell. It overrides the success verb because it
// adds membership; the database isn't "created".

// agAddDBPrefetch is which primary databases can be added, and why the rest
// can't.
type agAddDBPrefetch struct {
	// primary names the replica everything was read from.
	primary string

	// eligible are the addable database names, in server order.
	eligible []string

	// excluded explains each left-out database (e.g. "HealthClinic — recovery
	// model is SIMPLE"), so absences aren't a mystery.
	excluded []string
}

// agDBCandidate is a database reduced to the four facts eligibility depends on.
// gosmo.Database hides its metadata behind methods, so the rule is written
// against this, testable without a server.
type agDBCandidate struct {
	Name          string
	RecoveryModel string
	State         string
	IsSystem      bool

	// LogChainStarted is DatabaseRecoveryStatus.LogBackupChainStarted: a full
	// backup since entering FULL recovery. Read separately; gosmo.Database
	// carries no backup state.
	LogChainStarted bool
}

// agCandidatesFrom pairs each database with its log chain state, keyed by
// lowercased name.
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

// agEligibleDatabases splits the primary's databases into joinable and not,
// with a reason per exclusion.
//
// SQL Server requires FULL recovery and a started log backup chain. The chain
// is sys.database_recovery_status.last_log_backup_lsn (gosmo's
// LogBackupChainStarted), not msdb backup history, which is wrong both ways
// (verified live): purged history still joins, and a backup before a SIMPLE
// round trip fails with Msg 1475.
func agEligibleDatabases(dbs []agDBCandidate, inGroup map[string]bool) (eligible, excluded []string) {
	for _, d := range dbs {
		switch {
		case d.IsSystem:
			// Silent: nobody expects master here.
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

	// agName and node are set by show before the shell's show.
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
		refresh: func(*db.ServerConn) { d.app.explorer.Reload(d.node) },
	})
	return d
}

func (d *AGAddDatabaseDialog) show(sc *db.ServerConn, agName string, node *explorerNode) {
	d.agName = agName
	d.node = node
	d.newObjectDialog.show(sc)
	d.SetHeader("Availability group: "+agName, "Server: "+sc.Opts.Server)
}

// fetchPrefetch reads candidates from the primary, where ADD DATABASE runs; a
// secondary's copies aren't addable.
func (d *AGAddDatabaseDialog) fetchPrefetch(ctx context.Context, sc *db.ServerConn) (*agAddDBPrefetch, error) {
	ag, err := agOnPrimary(ctx, sc, d.agName)
	if err != nil {
		return nil, err
	}
	primary := ag.Server()

	// Every group on the instance: a database in another group is just as
	// unaddable.
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

	// One server-wide read of every database's log chain state.
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

// showAGAddDatabaseDialog opens Add Database for a group, from Object
// Explorer's menu on the group or its Availability Databases folder. node is
// reloaded after the add.
func (a *App) showAGAddDatabaseDialog(sc *db.ServerConn, agName string, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.agAddDatabaseDialog.show(sc, agName, node)
}
