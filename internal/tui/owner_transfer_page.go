package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// ownerTransferItem is one object owned by the principal a properties dialog
// is open for, together with the owner the user has picked for it. origOwner
// is what the server reported, so a row is dirty exactly when newOwner
// differs from it.
type ownerTransferItem[T any] struct {
	obj       T
	name      string
	origOwner string
	newOwner  string
}

// ownerTransferSpec is what differs between the owner-transfer pages — Schema
// Ownership in User and Database Role Properties, Owned Roles in Database
// Role and Server Role Properties. Everything else about those pages is
// identical and lives in newOwnerTransferPage.
type ownerTransferSpec[T any] struct {
	// Headers are the grid's column headers. Cells returns one cell per
	// header for a single item, and re-runs whenever an owner changes, so a
	// column showing the owner tracks edits.
	Headers []string
	Cells   func(it *ownerTransferItem[T]) []string

	// DetailLabels are extra Static rows shown under "Current owner" for the
	// selected object, filled from DetailValues in the same order. Both are
	// nil for a page with no extra detail.
	DetailLabels []string
	DetailValues func(it *ownerTransferItem[T]) []string

	// GridSection and ItemSection head the grid and the selected-object rows.
	// Note is the warning at the foot of the page.
	GridSection string
	ItemSection string
	Note        string

	// ChangeOwner issues the ownership change for one object.
	ChangeOwner func(ctx context.Context, obj T, newOwner string) error
}

// newOwnerTransferPage builds the form and apply function for an
// owner-transfer page over items, offering ownerNames as the new owner.
func newOwnerTransferPage[T any](items []*ownerTransferItem[T], ownerNames []string, spec ownerTransferSpec[T]) (*propsheet.Form, propApply) {
	// An owner the server reports but ownerNames doesn't list is appended
	// rather than left to indexOf's not-found 0: the page commits whatever the
	// row displays, so a fallback to the first principal would transfer
	// ownership on a page that was merely opened and OK'd.
	//
	// No current caller can reach this — all three filter items by
	// Owner == the principal the dialog is for, which is itself in the list.
	// It guards the invariant for a caller that doesn't.
	owners := slices.Clone(ownerNames)
	for _, it := range items {
		if !slices.Contains(owners, it.origOwner) {
			owners = append(owners, it.origOwner)
		}
	}

	rowsFor := func() [][]string {
		rows := make([][]string, len(items))
		for i, it := range items {
			rows[i] = spec.Cells(it)
		}
		return rows
	}
	grid := controls.NewDataGrid()
	grid.SetData(spec.Headers, rowsFor())
	grid.SetCellCursor(true)

	nameStatic := propsheet.Static("Name", "")
	ownerStatic := propsheet.Static("Current owner", "")
	detailStatics := make([]*propsheet.StaticRow, len(spec.DetailLabels))
	for i, label := range spec.DetailLabels {
		detailStatics[i] = propsheet.Static(label, "")
	}
	transferRow := propsheet.Select("Transfer owner to", owners, 0)
	// The warning SSMS puts in a modal before an ownership change. It is a
	// row rather than a modal because the change is not issued here — it is
	// staged until Apply/OK, so there is no point at which a blocking prompt
	// would be answering "do it now?".
	warnRow := propsheet.Hint()
	refreshWarning := func() {
		var pending int
		for _, it := range items {
			if it.newOwner != it.origOwner {
				pending++
			}
		}
		if pending == 0 {
			warnRow.Clear()
			return
		}
		warnRow.Set(fmt.Sprintf("%d ownership change(s) pending. Transferring ownership gives the new owner full control of the object and can revoke permissions that flowed from the old owner. Applied on OK/Apply.", pending))
	}

	// selected is the grid row whose edit transferRow currently holds, so a
	// move to another row can write the old one back first.
	selected := -1
	commitCurrent := func() {
		if selected >= 0 && selected < len(items) {
			items[selected].newOwner = transferRow.Value()
			refreshWarning()
		}
	}
	syncFromSelection := func(row int) {
		commitCurrent()
		selected = row
		if row < 0 || row >= len(items) {
			nameStatic.SetValue("")
			ownerStatic.SetValue("")
			for _, s := range detailStatics {
				s.SetValue("")
			}
			return
		}
		it := items[row]
		nameStatic.SetValue(it.name)
		ownerStatic.SetValue(it.origOwner)
		if spec.DetailValues != nil {
			for i, v := range spec.DetailValues(it) {
				detailStatics[i].SetValue(v)
			}
		}
		transferRow.SetSelected(indexOf(owners, it.newOwner))
	}
	// Staying on the row and changing the dropdown must update the warning
	// too, not only moving off it.
	transferRow.SetOnChange(func(string) {
		commitCurrent()
		redrawGrid(grid, spec.Headers, rowsFor())
	})
	grid.OnSelectRow = syncFromSelection
	if len(items) > 0 {
		syncFromSelection(0)
	}

	gridRow := propsheet.NewGridRow(grid, 8)
	gridRow.DirtyFn = func() bool {
		// commitCurrent is deliberately not called here: DirtyFn runs on
		// every redraw, and writing the edit back from a draw path would
		// make a mere visit to the page look like a change.
		for _, it := range items {
			if it.newOwner != it.origOwner {
				return true
			}
		}
		return false
	}
	gridRow.RevertFn = func() {
		for _, it := range items {
			it.newOwner = it.origOwner
		}
		redrawGrid(grid, spec.Headers, rowsFor())
		if selected >= 0 && selected < len(items) {
			transferRow.SetSelected(indexOf(owners, items[selected].newOwner))
		}
		refreshWarning()
	}

	rows := []propsheet.Row{
		propsheet.Section(spec.GridSection),
		gridRow,
		propsheet.Section(spec.ItemSection),
		nameStatic, ownerStatic,
	}
	for _, s := range detailStatics {
		rows = append(rows, s)
	}
	rows = append(rows, transferRow, warnRow, propsheet.Note(spec.Note))

	apply := func(ctx context.Context) error {
		commitCurrent()
		for _, it := range items {
			if it.newOwner == it.origOwner {
				continue
			}
			if err := spec.ChangeOwner(ctx, it.obj, it.newOwner); err != nil {
				return err
			}
		}
		return nil
	}
	return propsheet.NewForm(rows...), apply
}

// ownedSchema pairs a schema with its object count. The count is a per-schema
// query, so it is fetched once at load time rather than from the draw path
// that fills the detail rows.
type ownedSchema struct {
	schema      *gosmo.Schema
	objectCount int
}

// pagePrincipalOwnedSchemas is the "Owned Schemas" page for both Database
// Role Properties and User Properties. The two are the same page and differ
// only in the word for the principal, which principalKind supplies ("role" or
// "user"). principalName is dereferenced at load time, not at page
// construction, so a reload after a rename picks up the new name.
func pagePrincipalOwnedSchemas(sc *db.ServerConn, dbName string, principalName *string, principalKind string) propPage {
	return propPage{
		title: "Owned Schemas",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			allSchemas, err := d.SchemasContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			users, err := d.UsersContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			roles, err := d.DatabaseRolesContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			var items []*ownerTransferItem[ownedSchema]
			for _, s := range allSchemas {
				if s.Owner != *principalName {
					continue
				}
				count, err := s.ObjectCountContext(ctx)
				if err != nil {
					return nil, nil, err
				}
				items = append(items, &ownerTransferItem[ownedSchema]{
					obj:       ownedSchema{schema: s, objectCount: count},
					name:      s.Name,
					origOwner: s.Owner,
					newOwner:  s.Owner,
				})
			}

			f, apply := newOwnerTransferPage(items, principalNames(users, roles), ownerTransferSpec[ownedSchema]{
				Headers: []string{"Schema", "Owner"},
				Cells: func(it *ownerTransferItem[ownedSchema]) []string {
					return []string{it.name, it.newOwner}
				},
				DetailLabels: []string{"Object count"},
				DetailValues: func(it *ownerTransferItem[ownedSchema]) []string {
					return []string{strconv.Itoa(it.obj.objectCount)}
				},
				GridSection: "Schemas owned by this " + principalKind,
				ItemSection: "Selected schema",
				Note:        "Changing schema ownership can affect permission chaining and deployment scripts.",
				ChangeOwner: func(ctx context.Context, o ownedSchema, newOwner string) error {
					return o.schema.ChangeOwnerContext(ctx, newOwner)
				},
			})
			return f, apply, nil
		},
	}
}
