package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// agent_operator_props.go builds the Operator Properties dialog — General
// (identity, e-mail, category) and a read-only Notifications page listing
// which alerts/jobs notify this operator. Pager/Net Send are excluded:
// SQL-only scope, no gosmo setters. Every page closes over a shared
// *string name cell, so a rename on General is visible to later pages in
// the same Apply run.

// findAgentOperator is a thin wrapper over gosmo.Server.OperatorByNameContext,
// mirroring findAgentJob/findAgentSchedule/findAgentAlert.
func findAgentOperator(ctx context.Context, sc *db.ServerConn, name string) (*gosmo.Operator, error) {
	return sc.Server.OperatorByNameContext(ctx, name)
}

// operatorPropPages builds the page set for Operator Properties.
func operatorPropPages(sc *db.ServerConn, operatorName string) []propPage {
	name := &operatorName
	return []propPage{
		withRequires(pageOperatorGeneral(sc, name), "", agentWriteRights()...),
		pageOperatorNotifications(sc, name),
	}
}

// showOperatorProperties opens Operator Properties for a known connection
// and operator name — the Object Explorer context menu's entry point.
func (a *App) showOperatorProperties(sc *db.ServerConn, operatorName string) {
	a.propDialog.show(sc, "msdb", "Operator Properties", "Operator: "+operatorName, "Server: "+sc.Opts.Server,
		func() []propPage { return operatorPropPages(sc, operatorName) })
}

func pageOperatorGeneral(sc *db.ServerConn, operatorName *string) propPage {
	return propPage{
		title:   "General",
		renames: true,
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			o, err := findAgentOperator(ctx, sc, *operatorName)
			if err != nil {
				return nil, nil, err
			}
			cats, err := sc.Server.CategoriesContext(ctx, gosmo.CategoryClassOperator)
			if err != nil {
				return nil, nil, err
			}
			catNames := make([]string, len(cats))
			for i, c := range cats {
				catNames[i] = c.Name
			}

			nameField := propsheet.Text("Name", o.Name, 30)
			enabledCheck := propsheet.Check("Enabled", o.Enabled)
			emailField := propsheet.Text("E-mail address", o.EmailAddress, 40)
			catItems := append([]string{noneItem}, catNames...)
			categoryRow := selectPreserving("Category", catItems, o.Category, noneItem)

			f := propsheet.NewForm(
				propsheet.Section("Operator identity"),
				nameField, enabledCheck,
				propsheet.Section("Notifications"),
				emailField, categoryRow,
				propsheet.Section("Pager operator"),
				propsheet.Note("<excluded — SQL-only scope>"),
				propsheet.Section("Net send operator"),
				propsheet.Note("<excluded — SQL-only scope>"),
			)

			apply := func(ctx context.Context) error {
				o, err := findAgentOperator(ctx, sc, *operatorName)
				if err != nil {
					return err
				}
				if enabledCheck.Dirty() {
					if enabledCheck.Checked() {
						err = o.EnableContext(ctx)
					} else {
						err = o.DisableContext(ctx)
					}
					if err != nil {
						return err
					}
				}
				if emailField.Dirty() {
					if err := o.SetEmailAddressContext(ctx, emailField.Value()); err != nil {
						return err
					}
				}
				if categoryRow.Dirty() {
					if err := o.SetCategoryContext(ctx, preservedValue(categoryRow, noneItem)); err != nil {
						return err
					}
				}
				// Renaming last keeps every write above addressed by the
				// name the server still has — see propPage.renames.
				if nameField.Dirty() {
					if err := o.RenameContext(ctx, nameField.Value()); err != nil {
						return err
					}
					commitRename(ctx, operatorName, nameField.Value())
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

// pageOperatorNotifications is a read-only page listing every alert and job
// configured to notify this operator — there's nothing page-local to
// apply; editing a link is done from the alert's or job's own Properties.
func pageOperatorNotifications(sc *db.ServerConn, operatorName *string) propPage {
	return propPage{
		title: "Notifications",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			o, err := findAgentOperator(ctx, sc, *operatorName)
			if err != nil {
				return nil, nil, err
			}
			alerts, err := o.NotifyingAlertsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			jobs, err := o.NotifyingJobsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			alertCols := []string{"Alert", "Method"}
			alertRows := make([][]string, len(alerts))
			for i, n := range alerts {
				alertRows[i] = []string{n.AlertName, n.Method.String()}
			}
			alertGrid := controls.NewDataGrid()
			alertGrid.SetData(alertCols, alertRows)

			jobCols := []string{"Job", "Notify condition"}
			jobRows := make([][]string, len(jobs))
			for i, n := range jobs {
				jobRows[i] = []string{n.JobName, formatNotifyLevel(n.Level)}
			}
			jobGrid := controls.NewDataGrid()
			jobGrid.SetData(jobCols, jobRows)

			f := propsheet.NewForm(
				propsheet.Section("Alerts that notify this operator"),
				propsheet.NewGridRow(alertGrid, 8),
				propsheet.Section("Jobs that notify this operator"),
				propsheet.NewGridRow(jobGrid, 8),
				propsheet.Note("Edit a link from the alert's or job's own Properties dialog (Alert Properties > Response, Job Properties > Notifications)."),
			)
			return f, nil, nil
		},
	}
}
