package tui

import "github.com/radix29/gosmo"

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
