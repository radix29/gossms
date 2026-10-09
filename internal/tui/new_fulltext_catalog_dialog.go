package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_fulltext_catalog_dialog.go is the New Full-Text Catalog dialog (a
// database's Storage > Full Text Catalogs folder), built on newObjectDialog.
// SSMS's General page: name, owner, default catalog, accent sensitivity.

// nftCatalogPrefetch is what the dialog reads before it opens.
type nftCatalogPrefetch struct {
	existingNames *nameSet
	owners        []string
}

func fetchNewFullTextCatalogPrefetch(ctx context.Context, sc *db.ServerConn, dbName string) (*nftCatalogPrefetch, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	cats, err := d.FullTextCatalogs(ctx)
	if err != nil {
		return nil, err
	}
	existing := newNameSet(databaseCollation(d))
	for _, c := range cats {
		existing.Add(c.Name)
	}
	owners, err := fullTextOwnerItems(ctx, d)
	if err != nil {
		return nil, err
	}
	return &nftCatalogPrefetch{existingNames: existing, owners: owners}, nil
}

// NewFullTextCatalogDialog is the New Full-Text Catalog dialog.
type NewFullTextCatalogDialog struct {
	dbFolderDialog[nftCatalogPrefetch]
}

// NewNewFullTextCatalogDialog creates the dialog and wires its callbacks.
func NewNewFullTextCatalogDialog(app *App) *NewFullTextCatalogDialog {
	d := &NewFullTextCatalogDialog{}
	d.initInFolder(app, newObjectConfig[nftCatalogPrefetch]{
		title: "New Full-Text Catalog",
		noun:  "Full-text catalog",
		pages: []string{"General"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*nftCatalogPrefetch, error) {
			return fetchNewFullTextCatalogPrefetch(ctx, sc, d.dbName)
		},
		build: d.buildPages,
	})
	return d
}

// fullTextAccentItems are the New dialog's accent-sensitivity choices, in the
// order fullTextAccentChoice reads them; the first omits the clause, taking the
// database's collation.
var fullTextAccentItems = []string{"Database default", "Sensitive", "Insensitive"}

// fullTextAccentChoice is WITH ACCENT_SENSITIVITY for item i of
// fullTextAccentItems: nil for the database default.
func fullTextAccentChoice(i int) *bool {
	switch i {
	case 1:
		return new(true)
	case 2:
		return new(false)
	}
	return nil
}

func (d *NewFullTextCatalogDialog) buildPages(pf *nftCatalogPrefetch) {
	sc := d.sc
	dbName := d.dbName

	nameField := propsheet.Text("Catalog name", "", 30)
	owner := propsheet.Select("Owner", pf.owners, 0)
	owner.SetFitItems(true)
	isDefault := propsheet.Check("Set as default catalog", false)
	accent := propsheet.Select("Accent sensitivity", fullTextAccentItems, 0)

	d.forms[0] = propsheet.NewForm(
		propsheet.Section("Catalog"),
		nameField,
		propsheet.Static("Database", dbName),
		owner,
		isDefault,
		accent,
		propsheet.Note("The default catalog is the one a full-text index goes into when it names none. "+
			"Accent sensitivity applies to every index in the catalog; changing it later means a rebuild."),
	)

	var req gosmo.CreateFullTextCatalogRequest
	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("catalog name is required")
		}
		if pf.existingNames.Has(name) {
			return fmt.Errorf("a full-text catalog named %q already exists in %s", name, dbName)
		}
		req = gosmo.CreateFullTextCatalogRequest{
			Name:            name,
			AccentSensitive: fullTextAccentChoice(accent.Selected()),
			IsDefault:       isDefault.Checked(),
			Owner:           fullTextOwnerValue(owner),
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		// DatabaseRef: the CREATE addresses the database by name, and a by-name read
		// would not work under Script Changes.
		_, err := sc.Server.DatabaseRef(dbName).CreateFullTextCatalog(ctx, req)
		return err
	}
}
