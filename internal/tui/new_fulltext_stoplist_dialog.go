package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_fulltext_stoplist_dialog.go is the New Full-Text Stoplist dialog (a
// database's Storage > Full Text Stoplists folder), built on newObjectDialog.
// SSMS's three starting points: empty, a copy of the system stoplist, or a
// copy of an existing stoplist. The copy is offered from this database's
// stoplists only; gosmo's FromDatabase (a cross-database copy) is left to a
// query window — docs/open-threads.md N1.

// nftStoplistPrefetch is what the dialog reads before it opens.
type nftStoplistPrefetch struct {
	existingNames *nameSet
	stoplists     []string
	owners        []string
}

func fetchNewFullTextStoplistPrefetch(ctx context.Context, sc *db.ServerConn, dbName string) (*nftStoplistPrefetch, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	lists, err := d.FullTextStoplists(ctx)
	if err != nil {
		return nil, err
	}
	pf := &nftStoplistPrefetch{existingNames: newNameSet(databaseCollation(d))}
	for _, l := range lists {
		pf.existingNames.Add(l.Name)
		pf.stoplists = append(pf.stoplists, l.Name)
	}
	if pf.owners, err = fullTextOwnerItems(ctx, d); err != nil {
		return nil, err
	}
	return pf, nil
}

// NewFullTextStoplistDialog is the New Full-Text Stoplist dialog.
type NewFullTextStoplistDialog struct {
	dbFolderDialog[nftStoplistPrefetch]
}

// NewNewFullTextStoplistDialog creates the dialog and wires its callbacks.
func NewNewFullTextStoplistDialog(app *App) *NewFullTextStoplistDialog {
	d := &NewFullTextStoplistDialog{}
	d.initInFolder(app, newObjectConfig[nftStoplistPrefetch]{
		title: "New Full-Text Stoplist",
		noun:  "Full-text stoplist",
		pages: []string{"General"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*nftStoplistPrefetch, error) {
			return fetchNewFullTextStoplistPrefetch(ctx, sc, d.dbName)
		},
		build: d.buildPages,
	})
	return d
}

// The starting points, in the radio's order.
const (
	stoplistSourceEmpty = iota
	stoplistSourceSystem
	stoplistSourceExisting
)

func (d *NewFullTextStoplistDialog) buildPages(pf *nftStoplistPrefetch) {
	sc := d.sc
	dbName := d.dbName

	nameField := propsheet.Text("Stoplist name", "", 30)
	owner := propsheet.Select("Owner", pf.owners, 0)
	owner.SetFitItems(true)
	source := propsheet.Radio("Start from", []string{
		"An empty stoplist", "The system stoplist", "An existing stoplist",
	}, stoplistSourceSystem)
	from := propsheet.Select("Existing stoplist", pf.stoplists, 0)
	from.SetFitItems(true)

	rows := []propsheet.Row{
		propsheet.Section("Stoplist"),
		nameField,
		propsheet.Static("Database", dbName),
		owner,
		propsheet.Section("Stopwords"),
		source,
		from,
	}
	if len(pf.stoplists) == 0 {
		rows = append(rows, propsheet.Note("This database has no stoplist to copy yet."))
	}
	rows = append(rows, propsheet.Note("A stoplist leaves its words out of every full-text index that uses it. "+
		"Copying one copies its words now; the two are independent afterwards."))
	d.forms[0] = propsheet.NewForm(rows...)

	var req gosmo.CreateFullTextStoplistRequest
	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("stoplist name is required")
		}
		if pf.existingNames.Has(name) {
			return fmt.Errorf("a full-text stoplist named %q already exists in %s", name, dbName)
		}
		req = gosmo.CreateFullTextStoplistRequest{Name: name, Owner: fullTextOwnerValue(owner)}
		switch source.Selected() {
		case stoplistSourceSystem:
			req.FromSystem = true
		case stoplistSourceExisting:
			if len(pf.stoplists) == 0 {
				return fmt.Errorf("there is no existing stoplist to copy — start from the system stoplist or an empty one")
			}
			req.From = from.Value()
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		_, err := sc.Server.DatabaseRef(dbName).CreateFullTextStoplist(ctx, req)
		return err
	}
}
