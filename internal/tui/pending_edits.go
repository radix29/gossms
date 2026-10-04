package tui

import (
	"fmt"

	"github.com/radix29/gosmo"
)

// pendingNameIndex is a master-detail page's "Add" duplicate check: the index
// in edits of the row whose name is name under collation, or -1. The
// collation is the scope's, as for nameSet — the database's for files,
// filegroups, extended properties and database-role members; the server's for
// server-role members, msdb objects (job steps, mail) and Resource Governor.
//
// A byte-wise `==` here accepted filegroup `fg1` beside `FG1` on the default
// case-insensitive collation, and Apply then failed with the server's
// "already exists" — after any earlier phase of the same Apply had run, since
// filegroup and file DDL cannot run in a transaction. On a case-sensitive
// collation the two are distinct, so the check is the collation's call, not
// strings.EqualFold.
//
// The pages each still keep their own edits slice and call this in place; it
// is the seed of the shared pending-edit list docs/open-threads.md describes.
func pendingNameIndex[E any](collation string, edits []E, nameOf func(E) string, name string) int {
	for i, e := range edits {
		if gosmo.SameName(collation, nameOf(e), name) {
			return i
		}
	}
	return -1
}

// pendingNameTaken reports whether a row of edits is named name under
// collation — pendingNameIndex for a page that has no row to select.
func pendingNameTaken[E any](collation string, edits []E, nameOf func(E) string, name string) bool {
	return pendingNameIndex(collation, edits, nameOf, name) >= 0
}

// pendingName is one row of a master-detail page as pendingNamesRefusal sees
// it: the name the server has ("" for a row not yet created), the name the
// page gives it, and whether the page removes it.
type pendingName struct {
	stored, name string
	removing     bool
}

// pendingNamesRefusal is the check a page with Add, Remove and rename runs
// over its rows before Apply writes anything: every name that will exist is
// non-empty and unique under collation, and no row is created or renamed onto
// the stored name of another row that is being removed or renamed. noun names
// a row ("profile", "step").
//
// The second rule is the Apply's order: Database Mail creates and renames
// before it drops, and Job Properties updates steps before it deletes them, so
// the name is still taken when the create or rename runs and the server
// refuses it — the whole Apply in one transaction, or in New Job, a job left
// half-built. Reordering the phases fixes a removal but not a swap of two
// names, so the page refuses and the user applies the removal first.
//
// Pages that check only the Add (pendingNameTaken) let a rename through: an
// Apply-time check with slices.Contains compared names byte-wise, so `Ops`
// and `ops` passed it on a case-insensitive server and failed there.
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
