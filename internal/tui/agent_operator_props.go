package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// agent_operator_props.go builds Operator Properties: General (identity,
// e-mail, category) and a read-only Notifications page of alerts/jobs notifying
// the operator. Pager/Net Send excluded (SQL-only scope). Pages share a *string
// name cell so a General rename is seen later in the same Apply.

// findAgentOperator wraps gosmo.Server.OperatorByNameContext, like
// findAgentJob.
func findAgentOperator(ctx context.Context, sc *db.ServerConn, name string) (*gosmo.Operator, error) {
	return sc.Server.OperatorByNameContext(ctx, name)
}

// operatorPropPages builds the page set for Operator Properties.
func operatorPropPages(sc *db.ServerConn, operatorName string) []propPage {
	name := &operatorName
	return []propPage{
		withRequires(pageOperatorGeneral(sc, name), "", gate.AgentWriteRights()...),
		pageOperatorNotifications(sc, name),
	}
}

// showOperatorProperties opens Operator Properties from Object Explorer's
// context menu.
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
				// One sp_update_operator for the whole page — see the same
				// batching in pageJobGeneral. The rename is addressed by
				// @name, the name the server still has, so it rides along
				// instead of having to go last (see propPage.renames).
				var ch gosmo.OperatorChanges
				if enabledCheck.Dirty() {
					ch.Enabled = gosmo.Ptr(enabledCheck.Checked())
				}
				if emailField.Dirty() {
					ch.EmailAddress = gosmo.Ptr(emailField.Value())
				}
				if categoryRow.Dirty() {
					ch.Category = gosmo.Ptr(preservedValue(categoryRow, noneItem))
				}
				if nameField.Dirty() {
					ch.Name = gosmo.Ptr(nameField.Value())
				}
				if err := o.AlterContext(ctx, ch); err != nil {
					return err
				}
				if nameField.Dirty() {
					commitRename(ctx, operatorName, nameField.Value())
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

// pageOperatorNotifications lists alerts and jobs that notify this operator,
// read-only; links are edited in the alert's or job's Properties.
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
