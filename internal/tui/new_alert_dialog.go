package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_alert_dialog.go is the New Alert creation dialog (Object Explorer's
// SQL Server Agent > Alerts > SQL Server Event Alerts, "New Alert...").
// Two pages: General (the alert's own definition) and Response (which
// operators get e-mailed). Linking a response job is left out —
// alert-to-job linking lives on Job Properties' own Alerts page (see
// agent_job_props_alerts.go), which has the job list to pick from.

type nalertPrefetch struct {
	existingNames map[string]bool
	dbNames       []string
	categories    []string
	operatorNames []string
}

func fetchNewAlertPrefetch(ctx context.Context, sc *db.ServerConn) (*nalertPrefetch, error) {
	alerts, err := sc.Server.AlertsContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(alerts))
	for _, a := range alerts {
		existing[strings.ToLower(a.Name)] = true
	}
	dbs, err := sc.Server.DatabasesContext(ctx)
	if err != nil {
		return nil, err
	}
	dbNames := make([]string, len(dbs))
	for i, d := range dbs {
		dbNames[i] = d.Name()
	}
	cats, err := sc.Server.CategoriesContext(ctx, gosmo.CategoryClassAlert)
	if err != nil {
		return nil, err
	}
	catNames := make([]string, len(cats))
	for i, c := range cats {
		catNames[i] = c.Name
	}
	ops, err := sc.Server.OperatorsContext(ctx)
	if err != nil {
		return nil, err
	}
	opNames := make([]string, len(ops))
	for i, o := range ops {
		opNames[i] = o.Name
	}
	return &nalertPrefetch{existingNames: existing, dbNames: dbNames, categories: catNames, operatorNames: opNames}, nil
}

const allDatabasesItem = "<All databases>"

// NewAlertDialog is the New Alert creation dialog.
type NewAlertDialog struct {
	newObjectDialog[nalertPrefetch]
}

// NewNewAlertDialog creates the dialog and wires its callbacks.
func NewNewAlertDialog(app *App) *NewAlertDialog {
	d := &NewAlertDialog{}
	d.init(app, newObjectConfig[nalertPrefetch]{
		title:          "New Alert",
		noun:           "Alert",
		pages:          []string{"General", "Response"},
		scriptDatabase: "msdb",
		fetch:          fetchNewAlertPrefetch,
		build:          d.buildPages,
		refresh:        func(sc *db.ServerConn) { d.app.explorer.RefreshFolderByType(sc, NodeAgentEventAlerts) },
	})
	return d
}

func (d *NewAlertDialog) buildPages(pf *nalertPrefetch) {
	sc := d.sc

	nameField := propsheet.Text("Name", "", 30)
	enabledRow := propsheet.Check("Enabled", true)
	triggerRow := propsheet.Radio("Trigger", []string{"SQL Server error number", "Severity level"}, 0)
	errorField := propsheet.Int("Error number", 0, 0, 2147483647, "")
	severityField := propsheet.Int("Severity", 0, 0, 25, "")
	dbItems := append([]string{allDatabasesItem}, pf.dbNames...)
	dbRow := propsheet.Select("Database", dbItems, 0)
	delayField := propsheet.Int("Delay between responses", 0, 0, 86400, "sec")
	messageField := propsheet.Text("Notification message", "", 50)
	catItems := append([]string{noneItem}, pf.categories...)
	categoryRow := propsheet.Select("Category", catItems, 0)

	generalForm := propsheet.NewForm(
		propsheet.Section("Alert identity"),
		nameField, enabledRow,
		propsheet.Section("Trigger"),
		triggerRow, errorField, severityField,
		propsheet.Note("Only the field matching Trigger above is used — the other is ignored."),
		propsheet.Section("Response scope"),
		dbRow, delayField,
		propsheet.Section("Notification"),
		messageField, categoryRow,
	)

	alertName := func() string { return strings.TrimSpace(nameField.Value()) }
	generalApply := func(ctx context.Context) error {
		req := gosmo.CreateAlertRequest{
			Name: alertName(), Enabled: enabledRow.Checked(),
			DelayBetweenResponses: time.Duration(intRowValue0(delayField.IntValue())) * time.Second,
			NotificationMessage:   messageField.Value(),
		}
		if triggerRow.Selected() == 0 {
			req.ErrorNumber = intRowValue0(errorField.IntValue())
		} else {
			req.Severity = intRowValue0(severityField.IntValue())
		}
		if dbRow.Selected() != 0 {
			req.DatabaseName = dbRow.Value()
		}
		if categoryRow.Selected() != 0 {
			req.Category = categoryRow.Value()
		}
		_, err := sc.Server.CreateAlertContext(ctx, req)
		return err
	}

	notifyGrid := propsheet.NewToggleGrid([]string{"Notify", "Operator"}, []int{0}, 10)
	opText := make([][]string, len(pf.operatorNames))
	opVals := make([][]bool, len(pf.operatorNames))
	for i, name := range pf.operatorNames {
		opText[i] = []string{name}
		opVals[i] = []bool{false}
	}
	notifyGrid.SetRows(opText, opVals)

	responseForm := propsheet.NewForm(
		propsheet.Section("Operators to e-mail on this alert"),
		notifyGrid,
		propsheet.Note("Pager and Net Send notification aren't offered — SQL-only scope. Response job execution can be set up afterward from Job Properties' Alerts page."),
	)
	responseApply := func(ctx context.Context) error {
		al, err := scriptSafeAlert(ctx, sc, alertName())
		if err != nil {
			return err
		}
		for i, v := range notifyGrid.Values() {
			if !v[0] {
				continue
			}
			if err := al.NotifyContext(ctx, pf.operatorNames[i], gosmo.NotifyMethodEmail); err != nil {
				return err
			}
		}
		return nil
	}

	d.forms = []*propsheet.Form{generalForm, responseForm}
	d.applyFns = []propApply{generalApply, responseApply}
	d.objectName = alertName
	d.preflight = func() error {
		name := alertName()
		if name == "" {
			return fmt.Errorf("alert name is required")
		}
		if pf.existingNames[strings.ToLower(name)] {
			return fmt.Errorf("an alert named %q already exists", name)
		}
		if triggerRow.Selected() == 0 && intRowValue0(errorField.IntValue()) == 0 {
			return fmt.Errorf("error number is required when Trigger is set to error number")
		}
		if triggerRow.Selected() == 1 && intRowValue0(severityField.IntValue()) == 0 {
			return fmt.Errorf("severity is required when Trigger is set to severity level")
		}
		return nil
	}
}

// intRowValue0 adapts an Int row's (int64, error) IntValue() to a plain
// int, falling back to 0 on a parse error. Int rows carry their own range
// validator, so callers have already rejected a malformed field before
// Apply runs.
func intRowValue0(v int64, err error) int {
	if err != nil {
		return 0
	}
	return int(v)
}
