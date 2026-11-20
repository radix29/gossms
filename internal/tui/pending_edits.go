package tui

import (
	"fmt"

	"github.com/radix29/gosmo"
	"github.com/radix29/tuikit/propsheet"
)

// newPendingEdits is a master-detail page's pending-edit list
// (propsheet.PendingEdits), its names compared under collation. Every page with
// a grid of rows, an Add and a Remove keeps one (Resource Governor pools and
// groups, Database Mail profiles and accounts, database files and filegroups,
// New Database filegroups, extended properties, role members, job steps, key
// signatures, symmetric-key encryptions, full-text index columns, stoplist
// words, search property list properties); each row type embeds
// propsheet.PendingState.
//
// The collation is the scope's, as for nameSet: the database's for files,
// filegroups, extended properties and database-role members; the server's for
// server-role members, msdb objects (job steps, mail) and Resource Governor.
//
// A byte-wise `==` accepted filegroup `fg1` beside `FG1` on the default
// case-insensitive collation, and Apply then failed with "already exists" after
// any earlier phase of the same Apply had run (filegroup and file DDL cannot run
// in a transaction). On a case-sensitive collation the two are distinct, so the
// check is the collation's call, not strings.EqualFold.
func newPendingEdits[E propsheet.PendingRow](collation string, loaded []E, nameOf func(E) string, changed func(E) bool, reset func(E)) *propsheet.PendingEdits[E] {
	same := func(a, b string) bool { return gosmo.SameName(collation, a, b) }
	return propsheet.NewPendingEdits(loaded, same, nameOf, changed, reset)
}

// pendingRefusal is pendingNamesRefusal over edits' rows, stored being a loaded
// row's name on the server.
func pendingRefusal[E propsheet.PendingRow](collation string, edits *propsheet.PendingEdits[E], noun string, stored func(E) string) error {
	rows := edits.All()
	names := make([]pendingName, len(rows))
	for i, e := range rows {
		s := e.Pending()
		names[i] = pendingName{name: edits.Name(e), removing: s.Removing}
		if !s.IsNew {
			names[i].stored = stored(e)
		}
	}
	return pendingNamesRefusal(collation, noun, names)
}

// pendingName is one row of a master-detail page as pendingNamesRefusal sees it:
// the name the server has ("" for a row not yet created), the name the page gives
// it, and whether the page removes it.
type pendingName struct {
	stored, name string
	removing     bool
}

// pendingNamesRefusal is the check a page with Add, Remove and rename runs over
// its rows before Apply writes anything: every name that will exist is non-empty
// and unique under collation, and no row is created or renamed onto the stored
// name of another row being removed or renamed. noun names a row ("profile",
// "step").
//
// The second rule is the Apply's order: Database Mail creates and renames before
// it drops, and Job Properties updates steps before it deletes them, so the name
// is still taken when the create or rename runs and the server refuses it (the
// whole Apply in one transaction, or in New Job a job left half-built).
// Reordering the phases fixes a removal but not a swap of two names, so the page
// refuses and the user applies the removal first.
//
// Pages that check only the Add (PendingEdits.Taken) let a rename through: an
// Apply-time check with slices.Contains compared names byte-wise, so `Ops` and
// `ops` passed it on a case-insensitive server and failed there.
func pendingNamesRefusal(collation, noun string, rows []pendingName) error {
	for i, r := range rows {
		if r.removing {
			continue
		}
		if r.name == "" {
			return fmt.Errorf("every %s needs a name", noun)
		}
		for _, o := range rows[:i] {
			if !o.removing && gosmo.SameName(collation, o.name, r.name) {
				return fmt.Errorf("two %ss are named %s", noun, r.name)
			}
		}
	}
	for i, r := range rows {
		if r.removing || r.name == r.stored {
			continue
		}
		for j, o := range rows {
			if j == i || o.stored == "" || !gosmo.SameName(collation, o.stored, r.name) {
				continue
			}
			switch {
			case o.removing:
				return fmt.Errorf("%s %s is still in use until its removal is applied — Apply that first", noun, o.stored)
			case o.name != o.stored:
				return fmt.Errorf("%s %s is still in use until its rename to %s is applied — Apply that first", noun, o.stored, o.name)
			}
		}
	}
	return nil
}
