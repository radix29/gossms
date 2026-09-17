package gate

import (
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// menu.go wraps a menu item in the rule: the Enabled predicate it already had,
// ANDed with the gate's answer, and a Note that says what is missing when the
// item is withheld.

// Item returns item with its Enabled predicate extended to consult the
// capability set, keeping any predicate it already had. The two are ANDed:
// "no active query panel" and "no rights for this" are both reasons to
// withhold, and neither should cancel the other out.
func Item(item controls.MenuItem, sc *db.ServerConn, dbName string, rights ...Right) controls.MenuItem {
	return ItemOn(item, sc, dbName, "", "", rights...)
}

// ItemOn is Item for an action aimed at one object in a schema — see
// AllowsOn.
func ItemOn(item controls.MenuItem, sc *db.ServerConn, dbName, schema, object string, rights ...Right) controls.MenuItem {
	return ItemOnAll(item, sc, dbName, schema, object, rights)
}

// AllowsAllOn is AllowsOn for an action that needs a right from *each*
// of groups: every group is any-of, as AllowsOn's one set is, and every
// group must pass. It exists for the statement that checks two permissions and
// is refused with either missing — DROP SECURITY POLICY, which needs ALTER ANY
// SECURITY POLICY and ALTER on the policy's schema. Flattened into one any-of
// set, either half alone offered a drop the server refuses.
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

// Missing is the right a withheld item's note names: the first right of
// the first group that fails, or of the first group when none does. It is the
// first *failing* group rather than the first group because that one may well
// be held — naming ALTER ANY SECURITY POLICY to a principal who holds it and
// lacks the schema half sends them after the wrong grant.
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

// NoteName is how a withheld item's note names one right. Bare for the
// database- and server-wide rights, which is what the note has always said;
// scoped for a right on one schema, securable or object, where "needs ALTER"
// or "needs CONTROL" reads as the database-wide right — far wider than what is
// missing. Move to Schema is the item this matters most on: its note is the
// only one an object-scoped right leads, and "needs CONTROL" sent the reader
// after CONTROL on the database when CONTROL on the one object is the grant.
func NoteName(r Right) string {
	if r.Securable != "" || r.Schema || r.Object {
		return r.nameOnly()
	}
	return r.Name
}

// ItemOnAll is ItemOn for an action needing a right from each of groups — see
// AllowsAllOn. ItemOn is its one-group case, so the note, the DENY reading and
// the ANDing with the item's own predicate are the same code for both: nesting
// two gateOns instead ANDs the predicates but keeps only the outer note, which
// then names a right the principal may already hold.
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
		// Shown only while the item is disabled, and only one right — the
		// whole "Requires X (role) or Y or Z." sentence would double the width
		// of every context menu it appears in.
		r, _ := Missing(sc, dbName, schema, object, groups...)
		item.Note = "needs " + NoteName(r)
		// Unless a DENY on the object is what withheld it, and then naming a
		// right sends the user after one they may already hold — the denial
		// beats it. Read once here rather than in the predicate: the menu is
		// rebuilt each time it opens, and Note is a string, not a callback.
		if r, at, denied := DeniedOn(sc, dbName, schema, object, rights...); denied {
			switch {
			case at.Column != "":
				item.Note = r.Name + " denied on column " + at.Column
			case at.Schema != "":
				item.Note = r.Name + " denied on schema " + at.Schema
			case at.Database != "":
				item.Note = r.Name + " denied on database " + at.Database
			case at.Principal != "":
				item.Note = r.DeniedOnPrincipal + " denied on principal " + at.Principal
			case at.ServerSecurable != "":
				item.Note = r.DeniedOnServer + " denied on " + serverSecurableWord(at.ServerKind) + " " + at.ServerSecurable
			case at.AvailabilityGroup != "":
				item.Note = r.DeniedOnAG + " denied on availability group " + at.AvailabilityGroup
			default:
				item.Note = r.Name + " denied on this object"
			}
		}
		// And only when the rights are why it is disabled. An item its own
		// predicate has already withheld — a failover offered on secondaries
		// only — is grey for a reason this note does not describe, and naming
		// a permission there sends the user after one they may already hold.
		item.NoteWhen = func() bool { return (prev == nil || prev()) && !allowed() }
	}
	item.Enabled = func() bool {
		if prev != nil && !prev() {
			return false
		}
		return allowed()
	}
	return item
}
