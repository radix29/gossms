package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// jobAlertLink tracks whether one SQL Server event alert responds to this job,
// toggled in a grid cell; sysalerts.job_id is a single relationship, not a
// list.
type jobAlertLink struct {
	alert      *gosmo.Alert
	origLinked bool
	linked     bool
}

// pageJobAlerts is the Alerts page: every SQL Server event alert (WMI excluded,
// as in the Alerts folder), toggled linked or not to this job. Alert authoring
// is Alert Properties' job (agent_alert_props.go).
func pageJobAlerts(sc *db.ServerConn, jobName *string) propPage {
	return propPage{
		title: "Alerts",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			alerts, err := sc.Server.EventAlertsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			edits := make([]*jobAlertLink, len(alerts))
			for i, al := range alerts {
				linked := al.JobName == *jobName
				edits[i] = &jobAlertLink{alert: al, origLinked: linked, linked: linked}
			}

			cols := []string{"Linked", "Name", "Event Source", "Database"}
			rowsFor := func() [][]string {
				rows := make([][]string, len(edits))
				for i, e := range edits {
					dbScope := e.alert.DatabaseName
					if dbScope == "" {
						dbScope = "<all databases>"
					}
					rows[i] = []string{mapCell(e.linked), e.alert.Name, e.alert.EventSource, dbScope}
				}
				return rows
			}

			grid := controls.NewDataGrid()
			grid.SetData(cols, rowsFor())
			grid.SetCellCursor(true)
			grid.OnActivateCell = func(row, col int) {
				if col != 0 || row < 0 || row >= len(edits) {
					return
				}
				edits[row].linked = !edits[row].linked
				redrawGrid(grid, cols, rowsFor())
			}

			nameStatic := propsheet.Static("Name", "")
			enabledStatic := propsheet.Static("Enabled", "")
			sourceStatic := propsheet.Static("Event source", "")
			dbStatic := propsheet.Static("Database", "")
			errorStatic := propsheet.Static("Error number", "")
			severityStatic := propsheet.Static("Severity", "")
			responseStatic := propsheet.Static("Response", "")
			syncFromSelection := func(row int) {
				if row < 0 || row >= len(edits) {
					nameStatic.SetValue("")
					enabledStatic.SetValue("")
					sourceStatic.SetValue("")
					dbStatic.SetValue("")
					errorStatic.SetValue("")
					severityStatic.SetValue("")
					responseStatic.SetValue("")
					return
				}
				e := edits[row]
				al := e.alert
				nameStatic.SetValue(al.Name)
				enabledStatic.SetValue(boolStr(al.Enabled))
				sourceStatic.SetValue(al.EventSource)
				dbScope := al.DatabaseName
				if dbScope == "" {
					dbScope = "<all databases>"
				}
				dbStatic.SetValue(dbScope)
				errorNum := "<not used>"
				if al.ErrorNumber != 0 {
					errorNum = strconv.Itoa(al.ErrorNumber)
				}
				errorStatic.SetValue(errorNum)
				severity := "<not used>"
				if al.Severity != 0 {
					severity = strconv.Itoa(al.Severity)
				}
				severityStatic.SetValue(severity)
				response := "(none)"
				if e.linked {
					response = *jobName + " (this job)"
				} else if al.JobName != "" {
					response = al.JobName
				}
				responseStatic.SetValue(response)
			}
			grid.OnSelectRow = syncFromSelection
			if len(edits) > 0 {
				syncFromSelection(0)
			}

			linkRow := propsheet.NewGridRow(grid, 10)
			linkRow.DirtyFn = func() bool {
				for _, e := range edits {
					if e.linked != e.origLinked {
						return true
					}
				}
				return false
			}
			linkRow.RevertFn = func() {
				for _, e := range edits {
					e.linked = e.origLinked
				}
				redrawGrid(grid, cols, rowsFor())
			}

			f := propsheet.NewForm(
				propsheet.Section("Alerts that respond to this job"),
				linkRow,
				propsheet.Note("Space/Enter (or click) on Linked toggles whether the alert runs this job. Linking an alert that already responds to a different job reassigns it here — SQL Server only lets an alert respond to one job at a time."),
				propsheet.Section("Selected alert"),
				nameStatic, enabledStatic, sourceStatic, dbStatic, errorStatic, severityStatic, responseStatic,
				propsheet.Section("SQL-only scope"),
				propsheet.Note("SQL Server event alerts are included. WMI event alerts are excluded."),
			)

			apply := func(ctx context.Context) error {
				for _, e := range edits {
					if e.linked == e.origLinked {
						continue
					}
					target := ""
					if e.linked {
						target = *jobName
					}
					if err := e.alert.SetJobResponseContext(ctx, target); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

var notifyConditionItems = []string{"When the job succeeds", "When the job fails", "When the job completes"}
var notifyConditionLevels = []gosmo.NotifyLevel{gosmo.NotifyOnSuccess, gosmo.NotifyOnFailure, gosmo.NotifyOnComplete}

func notifyConditionIndex(level gosmo.NotifyLevel) int {
	for i, l := range notifyConditionLevels {
		if l == level {
			return i
		}
	}
	return 1 // "When the job fails"
}

// pageJobNotifications is the Notifications page: e-mail operator/condition and
// auto-delete are editable msdb state; net send, pager and event log keep their
// sections with an "excluded" note (outside SQL-only scope).
func pageJobNotifications(sc *db.ServerConn, jobName *string) propPage {
	return propPage{
		title: "Notifications",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			j, err := findAgentJob(ctx, sc, *jobName)
			if err != nil {
				return nil, nil, err
			}
			operators, err := sc.Server.OperatorsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			opNames := make([]string, len(operators))
			for i, o := range operators {
				opNames[i] = o.Name
			}

			emailCheck := propsheet.Check("E-mail", j.NotifyLevelEmail != gosmo.NotifyNever)
			// noneItem is always offered as a real choice. selectPreserving
			// keeps a since-dropped operator visible instead of collapsing to
			// opNames[0].
			opItems := append([]string{noneItem}, opNames...)
			operatorSelect := selectPreserving("Operator", opItems, j.NotifyEmailOperatorName, noneItem)
			conditionSelect := propsheet.Select("When to e-mail", notifyConditionItems, notifyConditionIndex(j.NotifyLevelEmail))
			deleteCheck := propsheet.Check("Delete job", j.DeleteLevel != gosmo.NotifyNever)
			deleteConditionSelect := propsheet.Select("When to delete", notifyConditionItems, notifyConditionIndex(j.DeleteLevel))

			f := propsheet.NewForm(
				propsheet.Section("E-mail operator"),
				emailCheck, operatorSelect, conditionSelect,
				propsheet.Note("E-mail notification is metadata configuration only — actual mail delivery depends on Database Mail being configured, outside this dialog."),
				propsheet.Section("Net send operator"),
				propsheet.Note("<excluded — SQL-only scope>"),
				propsheet.Section("Pager operator"),
				propsheet.Note("<excluded — SQL-only scope>"),
				propsheet.Section("Write to Windows application event log"),
				propsheet.Note("<excluded — SQL-only scope>"),
				propsheet.Section("Automatically delete job"),
				deleteCheck, deleteConditionSelect,
			)

			apply := func(ctx context.Context) error {
				j, err := findAgentJob(ctx, sc, *jobName)
				if err != nil {
					return err
				}
				// Each write is gated on its own rows being dirty (DirtyPages
				// is page-level only), so editing Delete Job alone doesn't
				// rewrite the e-mail operator.
				if emailCheck.Dirty() || operatorSelect.Dirty() || conditionSelect.Dirty() {
					emailLevel := gosmo.NotifyNever
					if emailCheck.Checked() {
						emailLevel = notifyConditionLevels[conditionSelect.Selected()]
					}
					// preservedValue maps noneItem to "", which SetEmailNotify
					// treats as "leave the operator unchanged" (sp_update_job
					// can't clear one). Otherwise ticking E-mail with no
					// operator would send to the first operator.
					if err := j.SetEmailNotifyContext(ctx, preservedValue(operatorSelect, noneItem), emailLevel); err != nil {
						return err
					}
				}
				if deleteCheck.Dirty() || deleteConditionSelect.Dirty() {
					deleteLevel := gosmo.NotifyNever
					if deleteCheck.Checked() {
						deleteLevel = notifyConditionLevels[deleteConditionSelect.Selected()]
					}
					if err := j.SetDeleteLevelContext(ctx, deleteLevel); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}
