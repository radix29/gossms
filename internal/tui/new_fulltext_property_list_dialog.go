package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_fulltext_property_list_dialog.go is the New Search Property List dialog
// (a database's Storage > Search Property Lists folder), built on
// newObjectDialog: empty, or a copy of one of this database's lists.
// Properties are registered afterwards, on the list's Properties page.

// nftPropertyListPrefetch is what the dialog reads before it opens.
type nftPropertyListPrefetch struct {
	existingNames *nameSet
	lists         []string
	owners        []string
}

func fetchNewSearchPropertyListPrefetch(ctx context.Context, sc *db.ServerConn, dbName string) (*nftPropertyListPrefetch, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	lists, err := d.SearchPropertyLists(ctx)
	if err != nil {
		return nil, err
	}
	pf := &nftPropertyListPrefetch{existingNames: newNameSet(databaseCollation(d))}
	for _, l := range lists {
		pf.existingNames.Add(l.Name)
		pf.lists = append(pf.lists, l.Name)
	}
	if pf.owners, err = fullTextOwnerItems(ctx, d); err != nil {
		return nil, err
	}
	return pf, nil
}

// NewSearchPropertyListDialog is the New Search Property List dialog.
type NewSearchPropertyListDialog struct {
	dbFolderDialog[nftPropertyListPrefetch]
}

// NewNewSearchPropertyListDialog creates the dialog and wires its callbacks.
func NewNewSearchPropertyListDialog(app *App) *NewSearchPropertyListDialog {
	d := &NewSearchPropertyListDialog{}
	d.initInFolder(app, newObjectConfig[nftPropertyListPrefetch]{
		title: "New Search Property List",
		noun:  "Search property list",
		pages: []string{"General"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*nftPropertyListPrefetch, error) {
			return fetchNewSearchPropertyListPrefetch(ctx, sc, d.dbName)
		},
		build: d.buildPages,
	})
	return d
}

func (d *NewSearchPropertyListDialog) buildPages(pf *nftPropertyListPrefetch) {
	sc := d.sc
	dbName := d.dbName

	nameField := propsheet.Text("List name", "", 30)
	owner := propsheet.Select("Owner", pf.owners, 0)
	owner.SetFitItems(true)
	source := propsheet.Radio("Start from", []string{"An empty list", "An existing list"}, 0)
	from := propsheet.Select("Existing list", pf.lists, 0)
	from.SetFitItems(true)

	rows := []propsheet.Row{
		propsheet.Section("Search property list"),
		nameField,
		propsheet.Static("Database", dbName),
		owner,
		propsheet.Section("Properties"),
		source,
		from,
	}
	if len(pf.lists) == 0 {
		rows = append(rows, propsheet.Note("This database has no search property list to copy yet."))
	}
	rows = append(rows, propsheet.Note("Register properties on the new list's Properties page; "+
		"a full-text index searches them once the list is set on it."))
	d.forms[0] = propsheet.NewForm(rows...)

	var req gosmo.CreateSearchPropertyListRequest
	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("list name is required")
		}
		if pf.existingNames.Has(name) {
			return fmt.Errorf("a search property list named %q already exists in %s", name, dbName)
		}
		req = gosmo.CreateSearchPropertyListRequest{Name: name, Owner: fullTextOwnerValue(owner)}
		if source.Selected() == 1 {
			if len(pf.lists) == 0 {
				return fmt.Errorf("there is no existing list to copy — start from an empty one")
			}
			req.From = from.Value()
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		_, err := sc.Server.DatabaseRef(dbName).CreateSearchPropertyList(ctx, req)
		return err
	}
}
