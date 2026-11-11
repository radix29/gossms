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
// SSMS's three starting points: empty, a copy of the system stoplist, or a copy
// of an existing one in this database or another (new_fulltext_copy_source.go).

// NewFullTextStoplistDialog is the New Full-Text Stoplist dialog.
type NewFullTextStoplistDialog struct {
	dbFolderDialog[fullTextCopyPrefetch]
}

// NewNewFullTextStoplistDialog creates the dialog and wires its callbacks.
func NewNewFullTextStoplistDialog(app *App) *NewFullTextStoplistDialog {
	d := &NewFullTextStoplistDialog{}
	d.initInFolder(app, newObjectConfig[fullTextCopyPrefetch]{
		title: "New Full-Text Stoplist",
		noun:  "Full-text stoplist",
		pages: []string{"General"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*fullTextCopyPrefetch, error) {
			return fetchFullTextCopyPrefetch(ctx, sc, d.dbName, func(ctx context.Context, dbh *gosmo.Database) ([]string, error) {
				lists, err := dbh.FullTextStoplists(ctx)
				names := make([]string, len(lists))
				for i, l := range lists {
					names[i] = l.Name
				}
				return names, err
			})
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

func (d *NewFullTextStoplistDialog) buildPages(pf *fullTextCopyPrefetch) {
	sc := d.sc
	dbName := d.dbName

	nameField := propsheet.Text("Stoplist name", "", 30)
	owner := propsheet.Select("Owner", pf.owners, 0)
	owner.SetFitItems(true)
	source := propsheet.Radio("Start from", []string{
		"An empty stoplist", "The system stoplist", "An existing stoplist",
	}, stoplistSourceSystem)
	from := newFullTextCopySource(d.app, d.ctx, sc, dbName, pf.databases, pf.names,
		"stoplist", "Existing stoplist", fullTextStoplistNames)

	rows := []propsheet.Row{
		propsheet.Section("Stoplist"),
		nameField,
		propsheet.Static("Database", dbName),
		owner,
		propsheet.Section("Stopwords"),
		source,
	}
	rows = append(rows, from.rows()...)
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
			var err error
			if req.From, req.FromDatabase, err = from.source("start from the system stoplist or an empty one"); err != nil {
				return err
			}
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		_, err := sc.Server.DatabaseRef(dbName).CreateFullTextStoplist(ctx, req)
		return err
	}
}

// fullTextStoplistNames reads d's stoplist names, for the copy-from picker.
func fullTextStoplistNames(ctx context.Context, d *gosmo.Database) ([]string, error) {
	lists, err := d.FullTextStoplists(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(lists))
	for i, l := range lists {
		names[i] = l.Name
	}
	return names, nil
}
