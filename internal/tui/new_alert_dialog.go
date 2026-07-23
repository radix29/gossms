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
// operators get e-mailed). Linking a response job is deliberately left
// out — see agent_job_props_alerts.go's pageJobAlerts doc comment: full
// alert-to-job linking lives on Job Properties' own Alerts page, which
// also has the job list to pick from; wiring it here too would duplicate
// that UI for no real benefit, and a freshly created job may not even be
// enlisted yet (see CreateJobContext's doc comment in gosmo).

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
	*propsheet.PropertySheet

	app *App
	sc  *db.ServerConn

	ctx    context.Context
	cancel context.CancelFunc

	prefetch  *nalertPrefetch
	forms     [2]*propsheet.Form
	applyFns  [2]propApply
	alertName func() string
	preflight func() error
}

// NewNewAlertDialog creates the dialog and wires its callbacks.
func NewNewAlertDialog(app *App) *NewAlertDialog {
	d := &NewAlertDialog{
		app:           app,
		PropertySheet: propsheet.NewPropertySheet(app.screen, "New Alert"),
	}
	d.OnLoadPage = d.onLoadPage
	d.OnApply = func() { d.runApply(false) }
	d.OnOK = func() { d.runApply(true) }
	d.OnClose = d.onClose
	d.ConfirmDiscard = d.onConfirmDiscard
	d.OnScript = d.runScript
	return d
}

func (d *NewAlertDialog) show(sc *db.ServerConn) {
	if d.cancel != nil {
		d.cancel()
	}
	d.ctx, d.cancel = context.WithCancel(context.Background())
	d.sc = sc
	d.prefetch = nil
	d.forms = [2]*propsheet.Form{}
	d.applyFns = [2]propApply{}
	d.alertName = nil
	d.preflight = nil
	d.SetHeader("Instance: "+sc.Opts.Server, "Connected: yes")
	d.SetPages([]string{"General", "Response"})
	d.Show()
}

func (d *NewAlertDialog) onClose() {
	if d.cancel != nil {
		d.cancel()
	}
}

func (d *NewAlertDialog) post(fn func()) {
	d.app.postEvent(fn)
	d.app.wakeEventLoop()
}

func (d *NewAlertDialog) onLoadPage(page, seq int) {
	if d.prefetch != nil {
		d.SetPageForm(page, seq, d.forms[page])
		return
	}
	sc := d.sc
	sessionCtx := d.ctx
	go func() {
		ctx, cancel := context.WithTimeout(sessionCtx, propFetchTimeout)
		defer cancel()
		pf, err := fetchNewAlertPrefetch(ctx, sc)
		d.post(func() {
			if err != nil {
				d.SetPageError(page, seq, err)
				return
			}
			d.buildPages(pf)
			d.SetPageForm(page, seq, d.forms[page])
		})
	}()
}

func (d *NewAlertDialog) buildPages(pf *nalertPrefetch) {
	d.prefetch = pf
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
		al, err := sc.Server.AlertByNameContext(ctx, alertName())
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

	d.forms = [2]*propsheet.Form{generalForm, responseForm}
	d.applyFns = [2]propApply{generalApply, responseApply}
	d.alertName = alertName
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

func (d *NewAlertDialog) onConfirmDiscard(page int, proceed func()) {
	d.app.confirmDialog.ShowConfirm("Discard Changes",
		"This page has unsaved changes. Discard them and refresh from the server?",
		func(confirmed bool) {
			if confirmed {
				proceed()
			}
		})
}

func (d *NewAlertDialog) runPipeline(runCtx context.Context, onSuccess func()) {
	if d.prefetch == nil {
		d.SetMessage("Still loading — try again in a moment.", true)
		return
	}
	if err := d.preflight(); err != nil {
		d.SelectPage(0)
		d.SetMessage(err.Error(), true)
		return
	}
	if page, err := d.Validate(); err != nil {
		d.SelectPage(page)
		d.SetMessage(err.Error(), true)
		return
	}

	fns := d.applyFns
	d.SetApplying(true)
	d.SetMessage("", false)

	go func() {
		var runErr error
		for _, fn := range fns {
			if runErr = fn(runCtx); runErr != nil {
				break
			}
		}
		d.post(func() {
			d.SetApplying(false)
			if runErr != nil {
				d.SetMessage(runErr.Error(), true)
				return
			}
			onSuccess()
		})
	}()
}

func (d *NewAlertDialog) runApply(hideOnSuccess bool) {
	d.runPipeline(d.ctx, func() {
		d.app.setStatus(fmt.Sprintf("Alert %q created", d.alertName()))
		d.app.explorer.RefreshFolderByType(d.sc, NodeAgentEventAlerts)
		if hideOnSuccess {
			d.Hide()
		}
	})
}

func (d *NewAlertDialog) runScript() {
	scriptCtx, script := gosmo.WithScript(d.ctx)
	sc := d.sc
	d.runPipeline(scriptCtx, func() {
		d.app.openQueryWithText(sc, "msdb", strings.Join(script.Statements, "\n\n"))
	})
}

// intRowValue0 adapts an Int row's (int64, error) IntValue() to a plain
// int, falling back to 0 on a parse error — every caller above already
// validates the field is well-formed before Apply runs (Int rows carry
// their own range validator), so this is just a terser accessor than
// repeating the (n, err) shape at every call site.
func intRowValue0(v int64, err error) int {
	if err != nil {
		return 0
	}
	return int(v)
}
