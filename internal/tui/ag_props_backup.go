package tui

import (
	"context"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// ag_props_backup.go is Availability Group Properties' Backup Preferences
// page: where automated backups of the group's databases should run, and each
// replica's priority within that.

// agBackupPreferenceItems are the radio options in SSMS's order, paired with
// the AUTOMATED_BACKUP_PREFERENCE keyword each one means. The wording is
// SSMS's; the keyword is what gosmo writes.
var agBackupPreferenceItems = []struct {
	label   string
	keyword string
}{
	{"Prefer Secondary", "SECONDARY"},
	{"Secondary only", "SECONDARY_ONLY"},
	{"Primary", "PRIMARY"},
	{"Any Replica", "NONE"},
}

func agBackupPreferenceLabels() []string {
	out := make([]string, len(agBackupPreferenceItems))
	for i, p := range agBackupPreferenceItems {
		out[i] = p.label
	}
	return out
}

// agBackupPreferenceIndex maps a group's AUTOMATED_BACKUP_PREFERENCE onto its
// radio index, defaulting to "Prefer Secondary" — which is also SQL Server's
// own default, so an unrecognized or empty value shows the same thing a
// freshly created group would.
func agBackupPreferenceIndex(pref string) int {
	for i, p := range agBackupPreferenceItems {
		if strings.EqualFold(pref, p.keyword) {
			return i
		}
	}
	return 0
}

// agBackupPriorityEdit is one replica's pending backup priority. Priority and
// "excluded" are the same server value — see the page's note — so there is
// only one number here.
type agBackupPriorityEdit struct {
	name     string
	priority int
	orig     int
}

func pageAGBackupPreferences(sc *db.ServerConn, agName string) propPage {
	return propPage{
		title: "Backup Preferences",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			ag, err := agOnPrimary(ctx, sc, agName)
			if err != nil {
				return nil, nil, err
			}
			replicas, err := ag.ReplicasContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			edits := make([]*agBackupPriorityEdit, len(replicas))
			for i, r := range replicas {
				edits[i] = &agBackupPriorityEdit{name: r.ReplicaServerName, priority: r.BackupPriority, orig: r.BackupPriority}
			}

			prefRow := propsheet.Radio("Where should backups occur?", agBackupPreferenceLabels(),
				agBackupPreferenceIndex(ag.AutomatedBackupPreference))

			headers := []string{"Server instance", "Backup priority", "Excluded"}
			gridRows := func() [][]string {
				rows := make([][]string, len(edits))
				for i, e := range edits {
					rows[i] = []string{e.name, strconv.Itoa(e.priority), boolStr(e.priority == 0)}
				}
				return rows
			}
			grid := controls.NewDataGrid()
			grid.SetData(headers, gridRows())
			grid.SetCellCursor(true)

			priorityRow := propsheet.Int("Backup priority", 50, 0, 100, "")

			selected := func() *agBackupPriorityEdit {
				i := grid.SelectedRow()
				if i < 0 || i >= len(edits) {
					return nil
				}
				return edits[i]
			}
			var current *agBackupPriorityEdit
			commitCurrent := func() {
				if current == nil {
					return
				}
				if n, err := priorityRow.IntValue(); err == nil {
					current.priority = int(n)
				}
			}
			syncFromSelection := func() {
				current = selected()
				if current == nil {
					return
				}
				priorityRow.SetValue(strconv.Itoa(current.priority))
			}
			grid.OnSelectRow = func(int) {
				commitCurrent()
				syncFromSelection()
				grid.SetData(headers, gridRows())
			}
			syncFromSelection()

			gridRow := propsheet.NewGridRow(grid, 9)
			gridRow.DirtyFn = func() bool {
				for _, e := range edits {
					if e.priority != e.orig {
						return true
					}
				}
				return false
			}
			gridRow.RevertFn = func() {
				for _, e := range edits {
					e.priority = e.orig
				}
				current = nil
				grid.SetData(headers, gridRows())
				syncFromSelection()
			}

			f := propsheet.NewForm(
				propsheet.Section("Automated backups"),
				prefRow,
				propsheet.Note("The preference is advisory: SQL Server does not enforce it. A backup job honours it by testing sys.fn_hadr_backup_is_preferred_replica before running."),
				propsheet.Section("Replica backup priorities"),
				gridRow,
				propsheet.Section("Selected replica"),
				priorityRow,
				propsheet.Note("Priority runs 1 (lowest) to 100 (highest). 0 excludes the replica from automated backups entirely — it is the single value behind SSMS's separate Exclude Replica checkbox, and the Excluded column above just reports it."),
			)

			apply := func(ctx context.Context) error {
				commitCurrent()
				ag, err := agOnPrimary(ctx, sc, agName)
				if err != nil {
					return err
				}
				if prefRow.Dirty() {
					pref := agBackupPreferenceItems[prefRow.Selected()].keyword
					if err := ag.SetAutomatedBackupPreferenceContext(ctx, pref); err != nil {
						return err
					}
				}
				return applyAGBackupPriorities(ctx, ag, edits)
			}
			return f, apply, nil
		},
	}
}

func applyAGBackupPriorities(ctx context.Context, ag *gosmo.AvailabilityGroup, edits []*agBackupPriorityEdit) error {
	changed := false
	for _, e := range edits {
		if e.priority != e.orig {
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	replicas, err := ag.ReplicasContext(ctx)
	if err != nil {
		return err
	}
	byName := agReplicasByName(replicas)
	for _, e := range edits {
		if e.priority == e.orig {
			continue
		}
		r := byName[strings.ToLower(e.name)]
		if r == nil {
			return agMissingReplicaErr(e.name)
		}
		if err := r.SetBackupPriorityContext(ctx, e.priority); err != nil {
			return err
		}
	}
	return nil
}
