package tui

import (
	"context"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// agent_alert_props.go builds the Alert Properties dialog — General
// (identity, trigger, response scope, notification) and Response
// (operators to e-mail, response job). Field shapes mirror
// new_alert_dialog.go's creation form; the Response page's operator-toggle
// diffing mirrors agent_job_props_alerts.go's pageJobAlerts. Uses the same
// shared *string name-cell pattern as agent_schedule_props.go — see that
// file's doc comment and sql-agent-job-props-review-2026-07 (Bug 2).

// findAgentAlert is a thin wrapper over gosmo.Server.AlertByNameContext,
// mirroring findAgentJob/findAgentSchedule.
func findAgentAlert(ctx context.Context, sc *db.ServerConn, name string) (*gosmo.Alert, error) {
	return sc.Server.AlertByNameContext(ctx, name)
}

// alertPropPages builds the page set for Alert Properties.
func alertPropPages(sc *db.ServerConn, alertName string) []propPage {
	name := &alertName
	return []propPage{
		pageAlertGeneral(sc, name),
		pageAlertResponse(sc, name),
	}
}

// showAlertProperties opens Alert Properties for a known connection and
// alert name — the Object Explorer context menu's entry point.
func (a *App) showAlertProperties(sc *db.ServerConn, alertName string) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.propDialog.show(sc, "msdb", "Alert Properties", "Alert: "+alertName, "Server: "+sc.Opts.Server,
		alertPropPages(sc, alertName))
}

func pageAlertGeneral(sc *db.ServerConn, alertName *string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			al, err := findAgentAlert(ctx, sc, *alertName)
			if err != nil {
				return nil, nil, err
			}
			dbs, err := sc.Server.DatabasesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			dbNames := make([]string, len(dbs))
			for i, d := range dbs {
				dbNames[i] = d.Name()
			}
			cats, err := sc.Server.CategoriesContext(ctx, gosmo.CategoryClassAlert)
			if err != nil {
				return nil, nil, err
			}
			catNames := make([]string, len(cats))
			for i, c := range cats {
				catNames[i] = c.Name
			}

			nameField := propsheet.Text("Name", al.Name, 30)
			enabledCheck := propsheet.Check("Enabled", al.Enabled)
			triggerIdx := 0
			if al.Severity != 0 {
				triggerIdx = 1
			}
			triggerRow := propsheet.Radio("Trigger", []string{"SQL Server error number", "Severity level"}, triggerIdx)
			errorField := propsheet.Int("Error number", int64(al.ErrorNumber), 0, 2147483647, "")
			severityField := propsheet.Int("Severity", int64(al.Severity), 0, 25, "")
			dbItems := append([]string{allDatabasesItem}, dbNames...)
			dbIdx := 0
			if al.DatabaseName != "" {
				dbIdx = 1 + indexOf(dbNames, al.DatabaseName)
			}
			dbRow := propsheet.Select("Database", dbItems, dbIdx)
			delayField := propsheet.Int("Delay between responses", int64(al.DelayBetweenResponses/time.Second), 0, 86400, "sec")
			messageField := propsheet.Text("Notification message", al.NotificationMessage, 50)
			catItems := append([]string{noneItem}, catNames...)
			catIdx := 0
			if al.Category != "" {
				catIdx = 1 + indexOf(catNames, al.Category)
			}
			categoryRow := propsheet.Select("Category", catItems, catIdx)

			f := propsheet.NewForm(
				propsheet.Section("Alert identity"),
				nameField, enabledCheck,
				propsheet.Section("Trigger"),
				triggerRow, errorField, severityField,
				propsheet.Note("Only the field matching Trigger above is used — the other is ignored."),
				propsheet.Section("Response scope"),
				dbRow, delayField,
				propsheet.Section("Notification"),
				messageField, categoryRow,
			)

			apply := func(ctx context.Context) error {
				al, err := findAgentAlert(ctx, sc, *alertName)
				if err != nil {
					return err
				}
				if nameField.Dirty() {
					if err := al.RenameContext(ctx, nameField.Value()); err != nil {
						return err
					}
					*alertName = nameField.Value()
				}
				if enabledCheck.Dirty() {
					if enabledCheck.Checked() {
						err = al.EnableContext(ctx)
					} else {
						err = al.DisableContext(ctx)
					}
					if err != nil {
						return err
					}
				}
				if triggerRow.Dirty() || errorField.Dirty() || severityField.Dirty() {
					var errNum, sev int
					if triggerRow.Selected() == 0 {
						errNum = intRowValue0(errorField.IntValue())
					} else {
						sev = intRowValue0(severityField.IntValue())
					}
					if err := al.SetTriggerContext(ctx, errNum, sev); err != nil {
						return err
					}
				}
				if dbRow.Dirty() {
					target := ""
					if dbRow.Selected() != 0 {
						target = dbRow.Value()
					}
					if err := al.SetDatabaseContext(ctx, target); err != nil {
						return err
					}
				}
				if delayField.Dirty() {
					n := intRowValue0(delayField.IntValue())
					if err := al.SetDelayContext(ctx, time.Duration(n)*time.Second); err != nil {
						return err
					}
				}
				if messageField.Dirty() {
					if err := al.SetNotificationMessageContext(ctx, messageField.Value()); err != nil {
						return err
					}
				}
				if categoryRow.Dirty() {
					target := ""
					if categoryRow.Selected() != 0 {
						target = categoryRow.Value()
					}
					if err := al.SetCategoryContext(ctx, target); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

// pageAlertResponse manages which operators this alert e-mails and which
// job (if any) it runs in response, mirroring new_alert_dialog.go's
// Response page and agent_job_props_alerts.go's pageJobAlerts toggle-diff.
func pageAlertResponse(sc *db.ServerConn, alertName *string) propPage {
	return propPage{
		title: "Response",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			al, err := findAgentAlert(ctx, sc, *alertName)
			if err != nil {
				return nil, nil, err
			}
			operators, err := sc.Server.OperatorsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			notifs, err := al.NotificationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			notified := make(map[string]bool, len(notifs))
			for _, n := range notifs {
				notified[n.OperatorName] = true
			}

			opNames := make([]string, len(operators))
			opText := make([][]string, len(operators))
			opVals := make([][]bool, len(operators))
			for i, o := range operators {
				opNames[i] = o.Name
				opText[i] = []string{o.Name}
				opVals[i] = []bool{notified[o.Name]}
			}
			notifyGrid := propsheet.NewToggleGrid([]string{"Notify", "Operator"}, []int{0}, 10)
			notifyGrid.SetRows(opText, opVals)

			jobs, err := sc.Server.JobsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			jobNames := make([]string, len(jobs))
			for i, j := range jobs {
				jobNames[i] = j.Name
			}
			jobItems := append([]string{noneItem}, jobNames...)
			jobIdx := 0
			if al.JobName != "" {
				jobIdx = 1 + indexOf(jobNames, al.JobName)
			}
			responseJobSelect := propsheet.Select("Response job", jobItems, jobIdx)

			f := propsheet.NewForm(
				propsheet.Section("Operators to e-mail on this alert"),
				notifyGrid,
				propsheet.Section("Response job"),
				responseJobSelect,
				propsheet.Note("Pager and Net Send notification aren't offered — SQL-only scope. A response job can also be set from that job's own Properties > Alerts page."),
			)

			apply := func(ctx context.Context) error {
				al, err := findAgentAlert(ctx, sc, *alertName)
				if err != nil {
					return err
				}
				for i, v := range notifyGrid.Values() {
					if v[0] == notified[opNames[i]] {
						continue
					}
					if v[0] {
						if err := al.NotifyContext(ctx, opNames[i], gosmo.NotifyMethodEmail); err != nil {
							return err
						}
					} else if err := al.RemoveNotifyContext(ctx, opNames[i]); err != nil {
						return err
					}
				}
				if responseJobSelect.Dirty() {
					target := ""
					if responseJobSelect.Selected() != 0 {
						target = responseJobSelect.Value()
					}
					if err := al.SetJobResponseContext(ctx, target); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}
