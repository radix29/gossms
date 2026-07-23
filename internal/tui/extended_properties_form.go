package tui

import (
	"context"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// extPropEdit tracks one extended property's pending state: an existing
// property whose value changed, or a brand-new one pending Add.
type extPropEdit struct {
	name          string
	origValue     string
	value         string
	isNew         bool
	pendingRemove bool
}

// buildExtendedPropertiesForm builds the Extended Properties page every
// object-Properties dialog shares: a Name/Value grid, a "selected
// property" field pair below it for editing, and Add/Remove — level
// determines which object the Add/Set/DropExtendedPropertyContext calls
// target (empty for database-level, SCHEMA+TABLE for a table, also usable
// for column-level extended properties with SCHEMA+TABLE+COLUMN once a
// caller needs that). props is whatever the caller already fetched
// (DatabaseExtendedProperties for database-level, ExtendedProperties(level)
// for anything narrower) — this function only builds the UI and the
// apply closure, it doesn't decide how to read the initial list.
func buildExtendedPropertiesForm(sc *db.ServerConn, dbName string, level gosmo.ExtendedPropertyLevel, props []*gosmo.ExtendedProperty) (*propsheet.Form, propApply) {
	edits := make([]*extPropEdit, 0, len(props))
	for _, p := range props {
		edits = append(edits, &extPropEdit{name: p.Name, origValue: p.Value, value: p.Value})
	}

	visible := func() []*extPropEdit {
		out := make([]*extPropEdit, 0, len(edits))
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
			rows[i] = []string{e.name, e.value}
		}
		return rows
	}

	grid := controls.NewDataGrid()
	grid.SetData([]string{"Name", "Value"}, rowsFor())

	nameField := propsheet.Text("Name", "", 24)
	valueField := propsheet.Text("Value", "", 30)
	selected := func() *extPropEdit {
		vis := visible()
		i := grid.SelectedRow()
		if i < 0 || i >= len(vis) {
			return nil
		}
		return vis[i]
	}
	// current tracks whichever edit the fields below the grid are
	// currently showing, so a value typed into valueField can be
	// committed back into it before the selection moves on (a plain
	// OnSelectRow callback only tells us the *new* selection, not which
	// edit the still-visible field text belongs to).
	var current *extPropEdit
	commitCurrent := func() {
		if current != nil {
			current.value = valueField.Value()
		}
	}
	syncFieldsFromSelection := func() {
		current = selected()
		if current != nil {
			nameField.SetValue(current.name)
			valueField.SetValue(current.value)
		} else {
			nameField.SetValue("")
			valueField.SetValue("")
		}
	}
	grid.OnSelectRow = func(row int) {
		commitCurrent()
		syncFieldsFromSelection()
	}
	syncFieldsFromSelection() // seed `current` for the initial selection (row 0)

	var addBtn, removeBtn *widgets.Button
	addBtn = widgets.NewButton("Add", func() {
		// Deliberately does NOT call commitCurrent(): valueField doubles as
		// the previously-selected property's live edit box, and
		// commitCurrent() writes its text into that property's .value — if
		// the user typed a value here meaning it for the brand-new property
		// being Added, that write would silently overwrite the previously
		// selected property's value instead. Any not-yet-applied edit to
		// the previously selected property is simply left as last synced
		// from its own selection (same trade-off as the Files page's Add).
		name := nameField.Value()
		if name == "" {
			return
		}
		for _, e := range visible() {
			if e.name == name {
				return // already present — edit its Value row instead
			}
		}
		edits = append(edits, &extPropEdit{name: name, value: valueField.Value(), isNew: true})
		grid.SetData([]string{"Name", "Value"}, rowsFor())
		grid.SetSelectedRow(len(visible()) - 1)
		syncFieldsFromSelection()
	})
	removeBtn = widgets.NewButton("Remove", func() {
		if e := selected(); e != nil {
			e.pendingRemove = true
			current = nil // its old value is void; don't let commitCurrent write back into it
			grid.SetData([]string{"Name", "Value"}, rowsFor())
			grid.SetSelectedRow(0)
			syncFieldsFromSelection()
		}
	})

	gridRow := propsheet.NewGridRow(grid, 12)
	dirty := func() bool {
		for _, e := range edits {
			if e.pendingRemove || e.isNew || e.value != e.origValue {
				return true
			}
		}
		return false
	}
	gridRow.DirtyFn = dirty
	gridRow.RevertFn = func() {
		kept := edits[:0]
		for _, e := range edits {
			if e.isNew {
				continue
			}
			e.value = e.origValue
			e.pendingRemove = false
			kept = append(kept, e)
		}
		edits = kept
		grid.SetData([]string{"Name", "Value"}, rowsFor())
	}

	f := propsheet.NewForm(
		propsheet.Section("Extended properties"),
		gridRow,
		propsheet.Section("Selected property"),
		nameField, valueField,
		propsheet.Buttons(addBtn, removeBtn),
		propsheet.Note("Extended properties are metadata only. They can be scripted via sp_addextendedproperty, sp_updateextendedproperty, and sp_dropextendedproperty."),
	)
	apply := func(ctx context.Context) error {
		commitCurrent()
		d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
		if err != nil {
			return err
		}
		for _, e := range edits {
			switch {
			case e.pendingRemove && !e.isNew:
				if err := d.DropExtendedPropertyContext(ctx, e.name, level); err != nil {
					return err
				}
			case e.isNew && !e.pendingRemove:
				if err := d.AddExtendedPropertyContext(ctx, e.name, e.value, level); err != nil {
					return err
				}
			case !e.isNew && !e.pendingRemove && e.value != e.origValue:
				if err := d.SetExtendedPropertyContext(ctx, e.name, e.value, level); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return f, apply
}
