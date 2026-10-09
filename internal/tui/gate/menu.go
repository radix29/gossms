package gate

import (
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// menu.go wraps a menu item in the rule: its Enabled predicate ANDed with the
// gate's answer, and a Note saying what is missing when withheld.

// Item returns item with its Enabled predicate extended to consult the
// capability set, ANDed with any it had: either reason withholds.
func Item(item controls.MenuItem, sc *db.ServerConn, dbName string, rights ...Right) controls.MenuItem {
	return ItemOn(item, sc, dbName, "", "", rights...)
}

// ItemOn is Item for an action aimed at one object in a schema — see
// AllowsOn.
func ItemOn(item controls.MenuItem, sc *db.ServerConn, dbName, schema, object string, rights ...Right) controls.MenuItem {
	return ItemOnAll(item, sc, dbName, schema, object, rights)
}

// AllowsAllOn is AllowsOn for an action needing a right from *each* of groups:
// every group is any-of and every group must pass. For statements refused with
// either permission missing: DROP SECURITY POLICY needs ALTER ANY SECURITY
// POLICY and ALTER on the policy's schema. Flattened into one any-of set,
// either half alone offered a drop the server refuses.
//
// An empty group, like an empty set, withholds nothing.
func AllowsAllOn(sc *db.ServerConn, dbName, schema, object string, groups ...[]Right) bool {
	for _, g := range groups {
		if !AllowsOn(sc, dbName, schema, object, g...) {
			return false
		}
	}
	return true
}

// Missing is the right a withheld item's note names: the first right of the
// first group that fails, or of the first group when none does. Failing, not
// merely first: the first group may be held, and naming ALTER ANY SECURITY
// POLICY to a principal who holds it and lacks the schema half sends them after
// the wrong grant.
func Missing(sc *db.ServerConn, dbName, schema, object string, groups ...[]Right) (Right, bool) {
	var first Right
	found := false
	for _, g := range groups {
		if len(g) == 0 {
			continue
		}
		if !AllowsOn(sc, dbName, schema, object, g...) {
			return g[0], true
		}
		if !found {
			first, found = g[0], true
		}
	}
	return first, found
}

// NoteName is how a withheld item's note names one right: bare for database-
// and server-wide rights; scoped for a right on one schema, securable or
// object, where "needs CONTROL" would read as the far wider database-wide
// right. Matters most for Move to Schema, the only note an object-scoped right
// leads: "needs CONTROL" sent readers after CONTROL on the database when
// CONTROL on the one object is the grant.
func NoteName(r Right) string {
	if r.Securable != "" || r.Schema || r.Object {
		return r.nameOnly()
	}
	return r.Name
}

// ItemOnAll is ItemOn for an action needing a right from each of groups — see
// AllowsAllOn. ItemOn is its one-group case, so the note, DENY reading and
// predicate ANDing are shared: nesting two gateOns keeps only the outer note,
// which may name a right the principal already holds.
func ItemOnAll(item controls.MenuItem, sc *db.ServerConn, dbName, schema, object string, groups ...[]Right) controls.MenuItem {
	prev := item.Enabled
	allowed := func() bool { return AllowsAllOn(sc, dbName, schema, object, groups...) }
	// Every right of every group, for the DENY question: a DENY on a right any
	// group needs withholds the action, whichever group it sits in.
	var rights []Right
	for _, g := range groups {
		rights = append(rights, g...)
	}
	if len(rights) > 0 {
		// Shown only while disabled, and only one right: the whole "Requires
		// X (role) or Y or Z." sentence would double every context menu's width.
		r, _ := Missing(sc, dbName, schema, object, groups...)
		item.Note = "needs " + NoteName(r)
		// Unless a DENY on the object withheld it: naming a right would send
		// the user after one they may hold, since the denial beats it. Read
		// once here, not in the predicate: the menu is rebuilt on each open and
		// Note is a string, not a callback.
		if r, at, denied := DeniedOn(sc, dbName, schema, object, rights...); denied {
			right, where := deniedPhrase(r, at)
			item.Note = right + " denied on " + where
		}
		// And only when the rights are why it is disabled: an item its own
		// predicate withheld (a failover offered on secondaries only) is grey
		// for a reason this note doesn't describe.
		//
		// prev alone decides: NoteWhen is consulted only while disabled
		// (MenuItem.showsNote), and with prev passing that means allowed() said
		// no. Asking again re-ran the whole gate on every draw of every
		// withheld item.
		item.NoteWhen = func() bool { return prev == nil || prev() }
	}
	item.Enabled = func() bool {
		if prev != nil && !prev() {
			return false
		}
		return allowed()
	}
	return item
}
