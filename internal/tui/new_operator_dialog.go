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
// single-page entity, but still built on propsheet.PropertySheet (one page,
// "General") rather than a bespoke shell, so OK/Cancel/Apply/Script Changes
// behave identically to every other dialog in the app.

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
	newObjectDialog[noperatorPrefetch]
}

// NewNewOperatorDialog creates the dialog and wires its callbacks.
func NewNewOperatorDialog(app *App) *NewOperatorDialog {
	d := &NewOperatorDialog{}
	d.init(app, newObjectConfig[noperatorPrefetch]{
		title:          "New Operator",
		noun:           "Operator",
		pages:          []string{"General"},
		scriptDatabase: "msdb",
		fetch:          fetchNewOperatorPrefetch,
		build:          d.buildPages,
		refresh:        func(sc *db.ServerConn) { d.app.explorer.RefreshFolderByType(sc, NodeAgentOperators) },
	})
	return d
}

func (d *NewOperatorDialog) buildPages(pf *noperatorPrefetch) {
	sc := d.sc

	nameField := propsheet.Text("Name", "", 30)
	enabledRow := propsheet.Check("Enabled", true)
	emailField := propsheet.Text("E-mail address", "", 40)
	catItems := append([]string{noneItem}, pf.categories...)
	categoryRow := propsheet.Select("Category", catItems, 0)

	d.forms[0] = propsheet.NewForm(
		propsheet.Section("Operator identity"),
		nameField, enabledRow,
		propsheet.Section("Notifications"),
		emailField, categoryRow,
		propsheet.Section("Pager operator"),
		propsheet.Note("<excluded — SQL-only scope>"),
		propsheet.Section("Net send operator"),
		propsheet.Note("<excluded — SQL-only scope>"),
	)
	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("operator name is required")
		}
		if pf.existingNames[strings.ToLower(name)] {
			return fmt.Errorf("an operator named %q already exists", name)
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		req := gosmo.CreateOperatorRequest{
			Name: d.objectName(), Enabled: enabledRow.Checked(), EmailAddress: emailField.Value(),
		}
		if categoryRow.Selected() != 0 {
			req.Category = categoryRow.Value()
		}
		_, err := sc.Server.CreateOperatorContext(ctx, req)
		return err
	}
}
