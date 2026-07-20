package tui

import (
	"context"
	"strconv"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// fgEdit tracks one Filegroups-page row's pending state, mirroring
// fileEdit's isNew/pendingRemove shape. Read-only/Default toggles live in
// the ToggleGridRow itself (see syncToggles); fgEdit only needs to know
// their loaded baseline to diff against at apply time.
type fgEdit struct {
	name          string // "" for a brand-new filegroup
	fileCount     int
	isNew         bool
	pendingRemove bool
	isReadOnly    bool
	origReadOnly  bool
	isDefault     bool
	origIsDefault bool
}

func pageDatabaseFilegroups(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Filegroups",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			fgs, err := d.FileGroupsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			edits := make([]*fgEdit, len(fgs))
			for i, fg := range fgs {
				edits[i] = &fgEdit{
					name: fg.Name, fileCount: len(fg.Files),
					isReadOnly: fg.IsReadOnly, origReadOnly: fg.IsReadOnly,
					isDefault: fg.IsDefault, origIsDefault: fg.IsDefault,
				}
			}

			visible := func() []*fgEdit {
				out := make([]*fgEdit, 0, len(edits))
				for _, e := range edits {
					if !e.pendingRemove {
						out = append(out, e)
					}
				}
				return out
			}
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
			var addBtn, removeBtn *widgets.Button
			addBtn = widgets.NewButton("Add", func() {
				syncToggles()
				name := nameField.Value()
				if name == "" {
					return
				}
				for _, e := range visible() {
					if e.name == name {
						return
					}
				}
				edits = append(edits, &fgEdit{name: name, isNew: true})
				text, values := rowsFor()
				fgRow.SetRows(text, values)
				nameField.SetValue("")
			})
			removeBtn = widgets.NewButton("Remove", func() {
				syncToggles()
				vis := visible()
				row := fgRow.Grid.SelectedRow()
				if row < 0 || row >= len(vis) {
					return
				}
				vis[row].pendingRemove = true
				text, values := rowsFor()
				fgRow.SetRows(text, values)
			})

			fgRow.DirtyFn = func() bool {
				syncToggles()
				for _, e := range edits {
					if e.isNew || e.pendingRemove || e.isReadOnly != e.origReadOnly || e.isDefault != e.origIsDefault {
						return true
					}
				}
				return false
			}
			fgRow.RevertFn = func() {
				edits = edits[:0]
				for _, fg := range fgs {
					edits = append(edits, &fgEdit{
						name: fg.Name, fileCount: len(fg.Files),
						isReadOnly: fg.IsReadOnly, origReadOnly: fg.IsReadOnly,
						isDefault: fg.IsDefault, origIsDefault: fg.IsDefault,
					})
				}
				text, values := rowsFor()
				fgRow.SetRows(text, values)
			}

			f := propsheet.NewForm(
				propsheet.Section("Filegroups"),
				fgRow,
				propsheet.Section("Add filegroup"),
				nameField,
				propsheet.Buttons(addBtn, removeBtn),
				propsheet.Note("Only one row-data filegroup can be the default. A filegroup must be empty before it can be removed."),
			)

			apply := func(ctx context.Context) error {
				syncToggles()
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				for _, e := range edits {
					switch {
					case e.pendingRemove && !e.isNew:
						if err := d.RemoveFileGroupContext(ctx, e.name); err != nil {
							return err
						}
						continue
					case e.isNew && !e.pendingRemove:
						if err := d.AddFileGroupContext(ctx, e.name); err != nil {
							return err
						}
					}
					if e.pendingRemove {
						continue
					}
					if e.isReadOnly != e.origReadOnly {
						if err := d.SetFileGroupReadOnlyContext(ctx, e.name, e.isReadOnly); err != nil {
							return err
						}
					}
					if e.isDefault && !e.origIsDefault {
						if err := d.SetDefaultFileGroupContext(ctx, e.name); err != nil {
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
