package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_operator_dialog.go is the New Operator creation dialog (Object
// Explorer's SQL Server Agent > Operators folder, "New Operator..."). A
// single-page entity — unlike New Login/New Database, there's no natural
// multi-page split — but it still uses the propsheet.PropertySheet
// framework (one page, "General") rather than a bespoke shell, so OK/
// Cancel/Apply/Script Changes behave identically to every other dialog in
// the app.

// noperatorPrefetch holds the one fetch this dialog needs: existing
// operator names (name-uniqueness preflight) and operator categories
// (Category dropdown).
type noperatorPrefetch struct {
	existingNames map[string]bool
	categories    []string
}

func fetchNewOperatorPrefetch(ctx context.Context, sc *db.ServerConn) (*noperatorPrefetch, error) {
	ops, err := sc.Server.OperatorsContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(ops))
	for _, o := range ops {
		existing[strings.ToLower(o.Name)] = true
	}
	cats, err := sc.Server.CategoriesContext(ctx, gosmo.CategoryClassOperator)
	if err != nil {
		return nil, err
	}
	catNames := make([]string, len(cats))
	for i, c := range cats {
		catNames[i] = c.Name
	}
	return &noperatorPrefetch{existingNames: existing, categories: catNames}, nil
}

// NewOperatorDialog is the New Operator creation dialog.
type NewOperatorDialog struct {
	*propsheet.PropertySheet

	app *App
	sc  *db.ServerConn

	ctx    context.Context
	cancel context.CancelFunc

	prefetch  *noperatorPrefetch
	form      *propsheet.Form
	applyFn   propApply
	opName    func() string
	preflight func() error
}

// NewNewOperatorDialog creates the dialog and wires its callbacks.
func NewNewOperatorDialog(app *App) *NewOperatorDialog {
	d := &NewOperatorDialog{
		app:           app,
		PropertySheet: propsheet.NewPropertySheet(app.screen, "New Operator"),
	}
	d.OnLoadPage = d.onLoadPage
	d.OnApply = func() { d.runApply(false) }
	d.OnOK = func() { d.runApply(true) }
	d.OnClose = d.onClose
	d.ConfirmDiscard = d.onConfirmDiscard
	d.OnScript = d.runScript
	return d
}

func (d *NewOperatorDialog) show(sc *db.ServerConn) {
	if d.cancel != nil {
		d.cancel()
	}
	d.ctx, d.cancel = context.WithCancel(context.Background())
	d.sc = sc
	d.prefetch = nil
	d.form = nil
	d.applyFn = nil
	d.opName = nil
	d.preflight = nil
	d.SetHeader("Instance: "+sc.Opts.Server, "Connected: yes")
	d.SetPages([]string{"General"})
	d.Show()
}

func (d *NewOperatorDialog) onClose() {
	if d.cancel != nil {
		d.cancel()
	}
}

func (d *NewOperatorDialog) post(fn func()) {
	d.app.postEvent(fn)
	d.app.wakeEventLoop()
}

func (d *NewOperatorDialog) onLoadPage(page, seq int) {
	if d.prefetch != nil {
		d.SetPageForm(page, seq, d.form)
		return
	}
	sc := d.sc
	sessionCtx := d.ctx
	go func() {
		ctx, cancel := context.WithTimeout(sessionCtx, propFetchTimeout)
		defer cancel()
		pf, err := fetchNewOperatorPrefetch(ctx, sc)
		d.post(func() {
			if err != nil {
				d.SetPageError(page, seq, err)
				return
			}
			d.buildPage(pf)
			d.SetPageForm(page, seq, d.form)
		})
	}()
}

func (d *NewOperatorDialog) buildPage(pf *noperatorPrefetch) {
	d.prefetch = pf
	sc := d.sc

	nameField := propsheet.Text("Name", "", 30)
	enabledRow := propsheet.Check("Enabled", true)
	emailField := propsheet.Text("E-mail address", "", 40)
	catItems := append([]string{noneItem}, pf.categories...)
	categoryRow := propsheet.Select("Category", catItems, 0)

	d.form = propsheet.NewForm(
		propsheet.Section("Operator identity"),
		nameField, enabledRow,
		propsheet.Section("Notifications"),
		emailField, categoryRow,
		propsheet.Section("Pager operator"),
		propsheet.Note("<excluded — SQL-only scope>"),
		propsheet.Section("Net send operator"),
		propsheet.Note("<excluded — SQL-only scope>"),
	)
	d.opName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		name := d.opName()
		if name == "" {
			return fmt.Errorf("operator name is required")
		}
		if pf.existingNames[strings.ToLower(name)] {
			return fmt.Errorf("an operator named %q already exists", name)
		}
		return nil
	}
	d.applyFn = func(ctx context.Context) error {
		req := gosmo.CreateOperatorRequest{
			Name: d.opName(), Enabled: enabledRow.Checked(), EmailAddress: emailField.Value(),
		}
		if categoryRow.Selected() != 0 {
			req.Category = categoryRow.Value()
		}
		_, err := sc.Server.CreateOperatorContext(ctx, req)
		return err
	}
}

func (d *NewOperatorDialog) onConfirmDiscard(page int, proceed func()) {
	d.app.confirmDialog.ShowConfirm("Discard Changes",
		"This page has unsaved changes. Discard them and refresh from the server?",
		func(confirmed bool) {
			if confirmed {
				proceed()
			}
		})
}

func (d *NewOperatorDialog) runPipeline(runCtx context.Context, onSuccess func()) {
	if d.prefetch == nil {
		d.SetMessage("Still loading — try again in a moment.", true)
		return
	}
	if err := d.preflight(); err != nil {
		d.SetMessage(err.Error(), true)
		return
	}
	if _, err := d.Validate(); err != nil {
		d.SetMessage(err.Error(), true)
		return
	}

	d.SetApplying(true)
	d.SetMessage("", false)

	go func() {
		err := d.applyFn(runCtx)
		d.post(func() {
			d.SetApplying(false)
			if err != nil {
				d.SetMessage(err.Error(), true)
				return
			}
			onSuccess()
		})
	}()
}

func (d *NewOperatorDialog) runApply(hideOnSuccess bool) {
	d.runPipeline(d.ctx, func() {
		d.app.setStatus(fmt.Sprintf("Operator %q created", d.opName()))
		d.app.explorer.RefreshFolderByType(d.sc, NodeAgentOperators)
		if hideOnSuccess {
			d.Hide()
		}
	})
}

func (d *NewOperatorDialog) runScript() {
	scriptCtx, script := gosmo.WithScript(d.ctx)
	sc := d.sc
	d.runPipeline(scriptCtx, func() {
		d.app.openQueryWithText(sc, "msdb", strings.Join(script.Statements, "\n\n"))
	})
}
