package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// fgEdit tracks one Filegroups-page row's pending state, as fileEdit does.
// Read-only/Default toggles live in the ToggleGridRow itself (see
// syncToggles); fgEdit only needs to know their loaded baseline to diff
// against at apply time.
type fgEdit struct {
	name      string
	fileCount int
	pendingState
	isReadOnly    bool
	origReadOnly  bool
	isDefault     bool
	origIsDefault bool
}

func pageDatabaseFilegroups(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Filegroups",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByName(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			fgs, err := d.FileGroups(ctx)
			if err != nil {
				return nil, nil, err
			}

			loaded := make([]*fgEdit, len(fgs))
			for i, fg := range fgs {
				loaded[i] = &fgEdit{
					name: fg.Name, fileCount: len(fg.Files),
					isReadOnly: fg.IsReadOnly, origReadOnly: fg.IsReadOnly,
					isDefault: fg.IsDefault, origIsDefault: fg.IsDefault,
				}
			}
			edits := newPendingEdits(databaseCollation(d), loaded,
				func(e *fgEdit) string { return e.name },
				func(e *fgEdit) bool { return e.isReadOnly != e.origReadOnly || e.isDefault != e.origIsDefault },
				func(e *fgEdit) { e.isReadOnly, e.isDefault = e.origReadOnly, e.origIsDefault })
			visible := edits.visible
			rowsFor := func() ([][]string, [][]bool) {
				vis := visible()
				text := make([][]string, len(vis))
				values := make([][]bool, len(vis))
				for i, e := range vis {
					text[i] = []string{e.name, strconv.Itoa(e.fileCount)}
					values[i] = []bool{e.isReadOnly, e.isDefault}
				}
				return text, values
			}
			fgRow := propsheet.NewToggleGrid([]string{"Name", "Files", "Read-only", "Default"}, []int{2, 3}, min(len(fgs)+3, 10))
			text, values := rowsFor()
			fgRow.SetRows(text, values)

			// syncToggles pulls the grid's current toggle state back into
			// edits before any row-count change (Add/Remove) or Apply —
			// SetRows resets ToggleGridRow's own dirty baseline every time
			// it's called, so edits is the only durable record.
			syncToggles := func() {
				vis := visible()
				for i, v := range fgRow.Values() {
					if i < len(vis) {
						vis[i].isReadOnly, vis[i].isDefault = v[0], v[1]
					}
				}
			}

			nameField := propsheet.Text("New filegroup name", "", 24)
			hint := propsheet.Hint()
			var addBtn, removeBtn *widgets.Button
			addBtn = widgets.NewButton("Add", func() {
				syncToggles()
				name := nameField.Value()
				if name == "" {
					hint.Set("Type a filegroup name first.")
					return
				}
				if edits.listed(name) {
					hint.Set("A filegroup named " + name + " is already listed.")
					return
				}
				hint.Clear()
				edits.add(&fgEdit{name: name})
				text, values := rowsFor()
				fgRow.SetRows(text, values)
				nameField.SetValue("")
			})
			removeBtn = widgets.NewButton("Remove", func() {
				syncToggles()
				vis := visible()
				row := fgRow.Grid.SelectedRow()
				if row < 0 || row >= len(vis) {
					hint.Set("Select a filegroup in the grid above to remove it.")
					return
				}
				hint.Clear()
				edits.remove(vis[row])
				text, values := rowsFor()
				fgRow.SetRows(text, values)
			})

			fgRow.DirtyFn = func() bool {
				syncToggles()
				return edits.dirty()
			}
			fgRow.RevertFn = func() {
				edits.revert()
				text, values := rowsFor()
				fgRow.SetRows(text, values)
			}

			f := propsheet.NewForm(
				propsheet.Section("Filegroups"),
				fgRow,
				propsheet.Section("Add filegroup"),
				nameField,
				propsheet.Buttons(addBtn, removeBtn),
				hint,
				propsheet.Note("Only one row-data filegroup can be the default. A filegroup must be empty before it can be removed."),
			)

			apply := func(ctx context.Context) error {
				syncToggles()
				d, err := sc.Server.DatabaseByName(ctx, dbName)
				if err != nil {
					return err
				}
				for _, e := range edits.all() {
					switch {
					case e.removing:
						if err := d.FileGroupRef(e.name).Drop(ctx); err != nil {
							return err
						}
						continue
					case e.isNew:
						if err := d.AddFileGroup(ctx, e.name); err != nil {
							return err
						}
					}
					if e.isReadOnly != e.origReadOnly {
						if err := d.FileGroupRef(e.name).SetReadOnly(ctx, e.isReadOnly, gosmo.TerminationNone); err != nil {
							return err
						}
					}
					if e.isDefault && !e.origIsDefault {
						if err := d.FileGroupRef(e.name).SetDefault(ctx); err != nil {
							return err
						}
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}
