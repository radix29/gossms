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
	isNew         bool
	pendingRemove bool
}

// membershipConfig is what buildMembershipForm needs that differs between
// the Database Role and Server Role Members pages.
type membershipConfig struct {
	// members is the role's current membership.
	members []*gosmo.RoleMember

	// candidates lists every principal that can be added, already filtered
	// to exclude current members and the role itself; principalType maps a
	// candidate name to the type shown in the grid.
	candidates    []string
	principalType map[string]string

	// note is the page's explanatory footer.
	note string

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
// the grid can be reverted without having touched the server.
//
// The two pages had this as ~95 identical lines each, differing only in how
// candidates and principalType are seeded and which gosmo call applies a
// change — both now supply those through membershipConfig.
func buildMembershipForm(cfg membershipConfig) (*propsheet.Form, propApply) {
	edits := make([]*memberEdit, len(cfg.members))
	memberNames := make(map[string]bool, len(cfg.members))
	for i, m := range cfg.members {
		edits[i] = &memberEdit{name: m.Name, principalType: m.Type}
		memberNames[m.Name] = true
	}

	visible := func() []*memberEdit {
		out := make([]*memberEdit, 0, len(edits))
		for _, e := range edits {
			if !e.pendingRemove {
				out = append(out, e)
			}
		}
		return out
	}
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

	// indexOfVisible finds a name's row in the grid as currently displayed,
	// so a duplicate Add can point at it. -1 when it isn't shown (a member
	// pending removal, which Add treats as addable again).
	indexOfVisible := func(name string) int {
		for i, e := range visible() {
			if e.name == name {
				return i
			}
		}
		return -1
	}

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
		if memberNames[name] {
			// Already a member. Say so and select the row, rather than
			// leaving a button that looks broken.
			hint.Set(name + " is already a member.")
			if i := indexOfVisible(name); i >= 0 {
				grid.SetSelectedRow(i)
			}
			return
		}
		hint.Clear()
		edits = append(edits, &memberEdit{name: name, principalType: cfg.principalType[name], isNew: true})
		memberNames[name] = true
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
		delete(memberNames, vis[i].name)
		vis[i].pendingRemove = true
		resetGrid(grid, membershipColumns, rowsFor(), 0)
	})

	gridRow := propsheet.NewGridRow(grid, 10)
	gridRow.DirtyFn = func() bool {
		for _, e := range edits {
			if e.pendingRemove || e.isNew {
				return true
			}
		}
		return false
	}
	gridRow.RevertFn = func() {
		kept := edits[:0]
		for _, e := range edits {
			if e.isNew {
				continue
			}
			e.pendingRemove = false
			kept = append(kept, e)
		}
		edits = kept
		memberNames = make(map[string]bool, len(edits))
		for _, e := range edits {
			memberNames[e.name] = true
		}
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
		for _, e := range edits {
			switch {
			case e.pendingRemove && !e.isNew:
				if err := cfg.remove(ctx, e.name); err != nil {
					return err
				}
			case e.isNew && !e.pendingRemove:
				if err := cfg.add(ctx, e.name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return f, apply
}
