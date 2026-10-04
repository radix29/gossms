package tui

import (
	"fmt"
	"slices"

	"github.com/radix29/gosmo"
)

// pendingState is a master-detail row's place in its page's pending-edit
// list: added since the page loaded (isNew), or loaded and to be removed on
// Apply (removing). A row type embeds it and so satisfies pendingRow.
type pendingState struct {
	isNew, removing bool
}

func (s *pendingState) pending() *pendingState { return s }

// pendingRow is a pointer to a row type embedding pendingState.
type pendingRow interface {
	comparable
	pending() *pendingState
}

// pendingEdits is a master-detail page's pending-edit list: the rows as
// loaded, and the rows as the page now says, Apply's to write. Every page
// with a grid of rows, an Add and a Remove keeps one — Resource Governor
// pools and groups, Database Mail profiles and accounts, database files and
// filegroups, New Database filegroups, extended properties, role members, job
// steps, key signatures, symmetric-key encryptions.
//
// changed reports an edit to a loaded row's values, and reset undoes it; both
// are nil on a page whose rows are only added and removed. nameOf is the
// row's name for the Add duplicate check (taken, index) — nil on a page with
// no name to type.
type pendingEdits[E pendingRow] struct {
	collation string
	nameOf    func(E) string
	changed   func(E) bool
	reset     func(E)

	loaded, rows []E
}

// newPendingEdits is the list for loaded under collation, the scope's — see
// pendingNameIndex.
func newPendingEdits[E pendingRow](collation string, loaded []E, nameOf func(E) string, changed func(E) bool, reset func(E)) *pendingEdits[E] {
	return &pendingEdits[E]{collation: collation, nameOf: nameOf, changed: changed, reset: reset,
		loaded: loaded, rows: slices.Clone(loaded)}
}

// all is every row, those being removed included, in the order loaded then
// added — Apply's input.
func (p *pendingEdits[E]) all() []E { return p.rows }

// visible is the rows the grid lists: all but those being removed.
func (p *pendingEdits[E]) visible() []E {
	out := make([]E, 0, len(p.rows))
	for _, e := range p.rows {
		if !e.pending().removing {
			out = append(out, e)
		}
	}
	return out
}

// isChanged reports an edit to loaded row e's values.
func (p *pendingEdits[E]) isChanged(e E) bool { return p.changed != nil && p.changed(e) }

// rowDirty reports whether e has anything for Apply to write.
func (p *pendingEdits[E]) rowDirty(e E) bool {
	s := e.pending()
	return s.isNew || s.removing || p.isChanged(e)
}

// dirty reports whether any row has anything for Apply to write.
func (p *pendingEdits[E]) dirty() bool { return slices.ContainsFunc(p.rows, p.rowDirty) }

// revert puts the list back as loaded: added rows gone, removals and edits
// undone.
func (p *pendingEdits[E]) revert() {
	p.rows = p.rows[:0]
	for _, e := range p.loaded {
		e.pending().removing = false
		if p.reset != nil {
			p.reset(e)
		}
		p.rows = append(p.rows, e)
	}
}

// swap exchanges rows a and b — a page that reorders its rows moves them in
// all, not in visible, which skips rows being removed.
func (p *pendingEdits[E]) swap(a, b E) {
	i, j := slices.Index(p.rows, a), slices.Index(p.rows, b)
	p.rows[i], p.rows[j] = p.rows[j], p.rows[i]
}

// add appends e as a new row.
func (p *pendingEdits[E]) add(e E) {
	e.pending().isNew = true
	p.rows = append(p.rows, e)
}

// remove takes e off the list: a new row is forgotten, a loaded one marked
// for removal on Apply.
func (p *pendingEdits[E]) remove(e E) {
	if e.pending().isNew {
		p.rows = slices.DeleteFunc(p.rows, func(x E) bool { return x == e })
		return
	}
	e.pending().removing = true
}

// restore takes back e's pending removal — an Add of a row being removed,
// on a page that would otherwise drop and re-create it.
func (p *pendingEdits[E]) restore(e E) { e.pending().removing = false }

// index is the index in visible of the row named name, or -1 — the Add
// duplicate check of a page that selects the row it finds.
func (p *pendingEdits[E]) index(name string) int {
	return pendingNameIndex(p.collation, p.visible(), p.nameOf, name)
}

// listed reports whether a visible row is named name. A row being removed is
// not: on a page whose Apply drops before it creates, its name is free, and
// on one that runs refusal first, that explains a name a removal frees only
// on Apply better than "already listed" about a row no longer shown.
func (p *pendingEdits[E]) listed(name string) bool { return p.index(name) >= 0 }

// taken reports whether any row is named name, those being removed included:
// the Add check of a page whose Apply creates before it drops and has no
// refusal to catch the clash.
func (p *pendingEdits[E]) taken(name string) bool {
	return pendingNameTaken(p.collation, p.rows, p.nameOf, name)
}

// refusal is pendingNamesRefusal over the rows, stored being a loaded row's
// name on the server.
func (p *pendingEdits[E]) refusal(noun string, stored func(E) string) error {
	names := make([]pendingName, len(p.rows))
	for i, e := range p.rows {
		s := e.pending()
		names[i] = pendingName{name: p.nameOf(e), removing: s.removing}
		if !s.isNew {
			names[i].stored = stored(e)
		}
	}
	return pendingNamesRefusal(p.collation, noun, names)
}

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
// pendingEdits.index is the pages' way in.
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
