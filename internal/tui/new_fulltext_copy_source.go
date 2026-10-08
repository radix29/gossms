package tui

import (
	"context"
	"fmt"
	"slices"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_fulltext_copy_source.go is the "copy an existing one" pair the New
// Full-Text Stoplist and New Search Property List dialogs share: a database
// picker and the list picker it fills (CREATE … FROM [db].[list]). The
// dialog's own database is answered from its prefetch; any other is read when
// first chosen and kept for the showing, so each database costs one read.
// Latest-only: running the picker through several databases ends on the last
// one's lists, and the reads it moved past are cancelled.

// fullTextCopySource is one dialog's copy-from rows and what they have read.
type fullTextCopySource struct {
	dbRow   *propsheet.SelectRow
	listRow *propsheet.SelectRow
	hint    *propsheet.HintRow

	app *App
	ctx context.Context // the dialog showing's, so closing it stops a read
	sc  *db.ServerConn

	home string // the dialog's database
	noun string // "stoplist", "search property list"
	read func(context.Context, *gosmo.Database) ([]string, error)

	// lists holds each database's lists once read; failed its read's error,
	// until the database is chosen again and retried.
	lists  map[string][]string
	failed map[string]error
	run    latest
}

// newFullTextCopySource builds the rows. databases is what the database
// picker offers (home is added if the list lacks it), homeLists home's own
// lists from the prefetch.
func newFullTextCopySource(app *App, ctx context.Context, sc *db.ServerConn, home string,
	databases, homeLists []string, noun, listLabel string,
	read func(context.Context, *gosmo.Database) ([]string, error)) *fullTextCopySource {

	if !slices.Contains(databases, home) {
		databases = append([]string{home}, databases...)
	}
	c := &fullTextCopySource{
		app: app, ctx: ctx, sc: sc, home: home, noun: noun, read: read,
		lists:  map[string][]string{home: homeLists},
		failed: map[string]error{},
	}
	c.dbRow = propsheet.Select("Copy from database", databases, slices.Index(databases, home))
	c.dbRow.SetFitItems(true)
	c.listRow = propsheet.Select(listLabel, nil, 0)
	c.listRow.SetFitItems(true)
	c.hint = propsheet.Hint()
	c.show(home, homeLists)
	c.dbRow.SetOnChange(c.choose)
	return c
}

// rows are the form rows, in order.
func (c *fullTextCopySource) rows() []propsheet.Row {
	return []propsheet.Row{c.dbRow, c.listRow, c.hint}
}

// choose fills the list picker with dbName's lists, reading them first if
// this showing has not.
func (c *fullTextCopySource) choose(dbName string) {
	if lists, ok := c.lists[dbName]; ok {
		c.run.Abandon()
		c.show(dbName, lists)
		return
	}
	delete(c.failed, dbName)
	c.listRow.SetItems(nil)
	c.hint.Set(fmt.Sprintf("Reading %s's %ss...", dbName, c.noun))
	ctx, token := c.run.BeginTimeout(c.ctx, propFetchTimeout)
	c.app.safego("reading full-text lists to copy", func() {
		lists, err := c.fetch(ctx, dbName)
		c.app.postAndWake(func() {
			if !c.run.Done(token) {
				return
			}
			if err != nil {
				c.failed[dbName] = err
				c.hint.SetError(err.Error())
				return
			}
			c.lists[dbName] = lists
			c.show(dbName, lists)
		})
	})
}

// fetch reads dbName's lists. A database the login cannot open says so in one
// sentence rather than the server's Msg 916; a probe that could not run
// answers Accessible and lets the read report what it finds.
func (c *fullTextCopySource) fetch(ctx context.Context, dbName string) ([]string, error) {
	if !c.sc.DatabaseCapabilities(ctx, dbName).Accessible {
		return nil, fmt.Errorf("%sCONNECT permission on %s is required to copy from it.", accessDeniedLabel, dbName)
	}
	lists, err := c.read(ctx, c.sc.Server.DatabaseRef(dbName))
	if err != nil {
		return nil, displayError(err)
	}
	return lists, nil
}

// show fills the list picker. The catalog views list only what the login
// holds a permission on, so an empty list is worded as "none visible", never
// "none" (db-rules), and names the right — REFERENCES, which CREATE … FROM
// needs on the source anyway.
func (c *fullTextCopySource) show(dbName string, lists []string) {
	c.listRow.SetItems(lists)
	switch {
	case len(lists) > 0:
		c.hint.Clear()
	case dbName == c.home:
		c.hint.Set(fmt.Sprintf("No %s in this database is visible to this login — copying one needs REFERENCES on it.", c.noun))
	default:
		c.hint.Set(fmt.Sprintf("No %s in %s is visible to this login — copying one needs REFERENCES on it.", c.noun, dbName))
	}
}

// source is the chosen list and, for another database, that database — the
// request's From and FromDatabase. orElse ends the refusal when there is
// nothing to copy ("start from an empty one").
func (c *fullTextCopySource) source(orElse string) (from, fromDB string, err error) {
	dbName := c.dbRow.Value()
	lists, ok := c.lists[dbName]
	switch {
	case !ok && c.failed[dbName] != nil:
		return "", "", c.failed[dbName]
	case !ok:
		return "", "", fmt.Errorf("still reading %s's %ss — try again in a moment", dbName, c.noun)
	case len(lists) == 0 && dbName == c.home:
		return "", "", fmt.Errorf("no %s in this database is visible to this login — %s, or ask for REFERENCES on one", c.noun, orElse)
	case len(lists) == 0:
		return "", "", fmt.Errorf("no %s in %s is visible to this login — choose another database, %s, or ask for REFERENCES on one", c.noun, dbName, orElse)
	}
	if dbName != c.home {
		fromDB = dbName
	}
	return c.listRow.Value(), fromDB, nil
}
