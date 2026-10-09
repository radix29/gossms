package tui

import (
	"context"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// memberEdit is one pending change to a principal's membership list —
// unapplied until the page's apply runs, so Cancel discards it.
type memberEdit struct {
	name          string
	principalType string
	pendingState
}

// membershipConfig is what buildMembershipForm needs that differs between
// the Database Role and Server Role Members pages.
type membershipConfig struct {
	// members is the role's current membership.
	members []*gosmo.RoleMember

	// candidates lists every principal that can be added, already filtered to
	// exclude current members and the role itself; principalType maps a candidate
	// name to the type shown in the grid.
	candidates    []string
	principalType map[string]string

	// note is the page's explanatory footer.
	note string

	// collation is the role's scope's (the database's for a database role, the
	// server's for a server role) and decides whether Add's name is already listed.
	collation string

	// add and remove issue the real membership change for one principal.
	add    func(ctx context.Context, name string) error
	remove func(ctx context.Context, name string) error
}

// membershipColumns is the members grid's header.
var membershipColumns = []string{"Member", "Type"}

// roleMemberSet indexes a role's current members by name, for filtering them
// out of the addable-candidate list without a scan per candidate.
func roleMemberSet(members []*gosmo.RoleMember) map[string]bool {
	set := make(map[string]bool, len(members))
	for _, m := range members {
		set[m.Name] = true
	}
	return set
}

// buildMembershipForm builds the "Role members" page shared by Database Role
// Properties (pageRoleMembers) and Server Role Properties
// (pageServerRoleMembers): a grid of current members, a dropdown of addable
// principals, and Add/Remove. Edits are collected and applied on OK/Apply, so
// the grid can be reverted without touching the server. The pages differ only
// in how candidates and principalType are seeded and which gosmo call applies a
// change, supplied through membershipConfig.
func buildMembershipForm(cfg membershipConfig) (*propsheet.Form, propApply) {
	loaded := make([]*memberEdit, len(cfg.members))
	for i, m := range cfg.members {
		loaded[i] = &memberEdit{name: m.Name, principalType: m.Type}
	}
	edits := newPendingEdits(cfg.collation, loaded, func(e *memberEdit) string { return e.name }, nil, nil)
	visible := edits.visible
	rowsFor := func() [][]string {
		vis := visible()
		rows := make([][]string, len(vis))
		for i, e := range vis {
			rows[i] = []string{e.name, e.principalType}
		}
		return rows
	}

	grid := controls.NewDataGrid()
	grid.SetData(membershipColumns, rowsFor())
	grid.SetCellCursor(true)

	candidates := cfg.candidates
	if len(candidates) == 0 {
		candidates = []string{noneItem}
	}
	addSelect := propsheet.Select("Add member", candidates, 0)
	hint := propsheet.Hint()

	addBtn := widgets.NewButton("Add", func() {
		name := addSelect.Value()
		if name == noneItem {
			hint.Set("There is no principal left to add.")
			return
		}
		// A member pending removal is not listed, and Add takes it back.
		if i := edits.index(name); i >= 0 {
			// Already a member: say so and select the row rather than leave a button that
			// looks broken.
			hint.Set(name + " is already a member.")
			grid.SetSelectedRow(i)
			return
		}
		hint.Clear()
		edits.add(&memberEdit{name: name, principalType: cfg.principalType[name]})
		resetGrid(grid, membershipColumns, rowsFor(), len(visible())-1)
	})

	removeBtn := widgets.NewButton("Remove", func() {
		vis := visible()
		i := grid.SelectedRow()
		if i < 0 || i >= len(vis) {
			hint.Set("Select a member in the grid above to remove it.")
			return
		}
		hint.Clear()
		edits.remove(vis[i])
		resetGrid(grid, membershipColumns, rowsFor(), 0)
	})

	gridRow := propsheet.NewGridRow(grid, 10)
	gridRow.DirtyFn = edits.dirty
	gridRow.RevertFn = func() {
		edits.revert()
		resetGrid(grid, membershipColumns, rowsFor(), 0)
		hint.Clear()
	}

	f := propsheet.NewForm(
		propsheet.Section("Role members"),
		gridRow,
		addSelect,
		propsheet.Buttons(addBtn, removeBtn),
		hint,
		propsheet.Note(cfg.note),
	)

	apply := func(ctx context.Context) error {
		for _, e := range edits.all() {
			switch {
			case e.removing:
				if err := cfg.remove(ctx, e.name); err != nil {
					return err
				}
			case e.isNew:
				if err := cfg.add(ctx, e.name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return f, apply
}
