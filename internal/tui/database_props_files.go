package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// fileEdit tracks one Files-page row's pending state: an existing file
// whose logical name/size/growth/max size changed, a brand-new file
// pending Add (isNew), or an existing file pending Remove.
type fileEdit struct {
	origName string // "" for a brand-new file
	isNew    bool

	name      string // current (possibly renamed) logical name
	fileType  string // "ROWS" or "LOG"; fixed for an existing file, chosen for a new one
	fileGroup string // "" for LOG files
	path      string // only meaningful for a new file — MODIFY FILE can't move/rename the physical file

	sizeKB          int64
	isPercentGrowth bool
	growthKB        int64
	growthPercent   int
	maxSizeKB       int64 // -1 = unlimited

	// originals, for diffing at apply time (zero-valued when isNew)
	origSizeKB          int64
	origIsPercentGrowth bool
	origGrowthKB        int64
	origGrowthPercent   int
	origMaxSizeKB       int64

	pendingRemove bool
}

func fileEditFromInfo(fl *gosmo.DatabaseFileInfo) *fileEdit {
	return &fileEdit{
		origName: fl.Name, name: fl.Name, fileType: fl.Type, fileGroup: fl.FileGroup, path: fl.PhysicalName,
		sizeKB: fl.SizeKB, isPercentGrowth: fl.IsPercentGrowth, growthKB: fl.GrowthKB, growthPercent: fl.GrowthPercent, maxSizeKB: fl.MaxSizeKB,
		origSizeKB: fl.SizeKB, origIsPercentGrowth: fl.IsPercentGrowth, origGrowthKB: fl.GrowthKB, origGrowthPercent: fl.GrowthPercent, origMaxSizeKB: fl.MaxSizeKB,
	}
}

func growthText(isPercent bool, growthKB int64, growthPercent int) string {
	if isPercent {
		return strconv.Itoa(growthPercent) + "%"
	}
	return strconv.FormatInt(growthKB/1024, 10) + " MB"
}

func maxSizeText(maxSizeKB int64) string {
	if maxSizeKB < 0 {
		return "Unlimited"
	}
	return strconv.FormatInt(maxSizeKB/1024, 10) + " MB"
}

func pageDatabaseFiles(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Files",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			opts, err := d.OptionsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			files, err := d.FilesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			fgs, err := d.FileGroupsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			fgNames := make([]string, len(fgs))
			for i, fg := range fgs {
				fgNames[i] = fg.Name
			}

			edits := make([]*fileEdit, len(files))
			for i, fl := range files {
				edits[i] = fileEditFromInfo(fl)
			}

			visible := func() []*fileEdit {
				out := make([]*fileEdit, 0, len(edits))
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
					rows[i] = []string{
						e.name, e.fileType, e.fileGroup,
						strconv.FormatInt(e.sizeKB/1024, 10),
						growthText(e.isPercentGrowth, e.growthKB, e.growthPercent),
						maxSizeText(e.maxSizeKB), e.path,
					}
				}
				return rows
			}

			grid := controls.NewDataGrid()
			grid.SetData([]string{"Logical name", "Type", "Filegroup", "Size (MB)", "Autogrowth", "Max size", "Path"}, rowsFor())
			grid.SetCellCursor(true)

			nameField := propsheet.Text("Logical name", "", 24)
			typeSelect := propsheet.Select("File type", []string{"ROWS", "LOG"}, 0)
			filegroupSelect := propsheet.Select("Filegroup", fgNames, 0)
			pathField := propsheet.Text("Path", "", 40)
			sizeField := propsheet.Int("Initial size", 0, 0, 16777216, "MB")
			growthKind := propsheet.Radio("Growth by", []string{"Megabytes", "Percent"}, 0)
			growthField := propsheet.Int("Growth amount", 0, 0, 2097151, "")
			maxKind := propsheet.Radio("Max size", []string{"Unlimited", "Limited"}, 0)
			maxField := propsheet.Int("Max size limit", 0, 0, 16777216, "MB")

			selected := func() *fileEdit {
				vis := visible()
				i := grid.SelectedRow()
				if i < 0 || i >= len(vis) {
					return nil
				}
				return vis[i]
			}
			var current *fileEdit
			commitCurrent := func() {
				if current == nil {
					return
				}
				current.name = nameField.Value()
				current.fileType = typeSelect.Value()
				current.fileGroup = filegroupSelect.Value()
				current.path = pathField.Value()
				if n, err := sizeField.IntValue(); err == nil {
					current.sizeKB = n * 1024
				}
				current.isPercentGrowth = growthKind.Selected() == 1
				if n, err := growthField.IntValue(); err == nil {
					if current.isPercentGrowth {
						current.growthPercent = int(n)
					} else {
						current.growthKB = n * 1024
					}
				}
				if maxKind.Selected() == 0 {
					current.maxSizeKB = -1
				} else if n, err := maxField.IntValue(); err == nil {
					current.maxSizeKB = n * 1024
				}
			}
			syncFieldsFromSelection := func() {
				current = selected()
				if current == nil {
					nameField.SetValue("")
					pathField.SetValue("")
					sizeField.SetValue("0")
					growthField.SetValue("0")
					maxField.SetValue("0")
					return
				}
				nameField.SetValue(current.name)
				typeSelect.SetSelected(indexOf([]string{"ROWS", "LOG"}, current.fileType))
				filegroupSelect.SetSelected(indexOf(fgNames, current.fileGroup))
				pathField.SetValue(current.path)
				sizeField.SetValue(strconv.FormatInt(current.sizeKB/1024, 10))
				if current.isPercentGrowth {
					growthKind.SetSelected(1)
					growthField.SetValue(strconv.Itoa(current.growthPercent))
				} else {
					growthKind.SetSelected(0)
					growthField.SetValue(strconv.FormatInt(current.growthKB/1024, 10))
				}
				if current.maxSizeKB < 0 {
					maxKind.SetSelected(0)
					maxField.SetValue("0")
				} else {
					maxKind.SetSelected(1)
					maxField.SetValue(strconv.FormatInt(current.maxSizeKB/1024, 10))
				}
			}
			grid.OnSelectRow = func(row int) {
				commitCurrent()
				syncFieldsFromSelection()
			}
			syncFieldsFromSelection()

			var addBtn, removeBtn *widgets.Button
			addBtn = widgets.NewButton("Add", func() {
				// Deliberately does NOT call commitCurrent(): these fields
				// double as the previously-selected file's live edit, and
				// commitCurrent() writes nameField's text into that file's
				// rename target — if the user typed a brand-new name here
				// intending to Add, that write would silently rename the
				// wrong file instead (Logical name is the one field this
				// page lets you both edit-in-place and repurpose for Add,
				// unlike Extended Properties' Name, which is never
				// rewritten by its own commitCurrent). Any not-yet-applied
				// edit to the previously selected file is simply left as
				// last synced from its own selection instead.
				name := nameField.Value()
				if name == "" {
					return
				}
				for _, e := range visible() {
					if e.name == name {
						return // already present — edit its row instead
					}
				}
				e := &fileEdit{
					isNew: true, name: name, fileType: typeSelect.Value(), fileGroup: filegroupSelect.Value(), path: pathField.Value(),
					maxSizeKB: -1,
				}
				if n, err := sizeField.IntValue(); err == nil {
					e.sizeKB = n * 1024
				}
				e.isPercentGrowth = growthKind.Selected() == 1
				if n, err := growthField.IntValue(); err == nil {
					if e.isPercentGrowth {
						e.growthPercent = int(n)
					} else {
						e.growthKB = n * 1024
					}
				}
				if maxKind.Selected() == 1 {
					if n, err := maxField.IntValue(); err == nil {
						e.maxSizeKB = n * 1024
					}
				}
				edits = append(edits, e)
				grid.SetData([]string{"Logical name", "Type", "Filegroup", "Size (MB)", "Autogrowth", "Max size", "Path"}, rowsFor())
				grid.SetSelectedRow(len(visible()) - 1)
				syncFieldsFromSelection()
			})
			removeBtn = widgets.NewButton("Remove", func() {
				if e := selected(); e != nil {
					e.pendingRemove = true
					current = nil
					grid.SetData([]string{"Logical name", "Type", "Filegroup", "Size (MB)", "Autogrowth", "Max size", "Path"}, rowsFor())
					grid.SetSelectedRow(0)
					syncFieldsFromSelection()
				}
			})

			gridRow := propsheet.NewGridRow(grid, 10)
			dirty := func() bool {
				for _, e := range edits {
					if e.isNew || e.pendingRemove {
						return true
					}
					if e.name != e.origName || e.sizeKB != e.origSizeKB ||
						e.isPercentGrowth != e.origIsPercentGrowth || e.growthKB != e.origGrowthKB ||
						e.growthPercent != e.origGrowthPercent || e.maxSizeKB != e.origMaxSizeKB {
						return true
					}
				}
				return false
			}
			gridRow.DirtyFn = dirty
			gridRow.RevertFn = func() {
				edits = edits[:0]
				for _, fl := range files {
					edits = append(edits, fileEditFromInfo(fl))
				}
				grid.SetData([]string{"Logical name", "Type", "Filegroup", "Size (MB)", "Autogrowth", "Max size", "Path"}, rowsFor())
				syncFieldsFromSelection()
			}

			f := propsheet.NewForm(
				propsheet.Section("Database files"),
				propsheet.Static("Owner", opts.Owner),
				gridRow,
				propsheet.Section("Selected file"),
				nameField, typeSelect, filegroupSelect, sizeField,
				growthKind, growthField, maxKind, maxField, pathField,
				propsheet.Buttons(addBtn, removeBtn),
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
						if err := d.RemoveFileContext(ctx, e.origName); err != nil {
							return err
						}
					case e.isNew && !e.pendingRemove:
						spec := gosmo.DatabaseFileSpec{
							Name: e.name, Type: e.fileType, Path: e.path, SizeKB: e.sizeKB, MaxSizeKB: e.maxSizeKB,
						}
						if e.fileType != "LOG" {
							spec.FileGroup = e.fileGroup
						}
						if e.isPercentGrowth {
							spec.GrowthPercent = e.growthPercent
						} else {
							spec.GrowthKB = e.growthKB
						}
						if err := d.AddFileContext(ctx, spec); err != nil {
							return err
						}
					case !e.isNew && !e.pendingRemove:
						growthChanged := e.isPercentGrowth != e.origIsPercentGrowth || e.growthKB != e.origGrowthKB || e.growthPercent != e.origGrowthPercent
						maxSizeChanged := e.maxSizeKB != e.origMaxSizeKB
						nameChanged := e.name != e.origName
						sizeChanged := e.sizeKB != e.origSizeKB
						if !nameChanged && !sizeChanged && !growthChanged && !maxSizeChanged {
							continue // nothing about this file actually changed
						}
						var m gosmo.FileModify
						if nameChanged {
							m.NewName = e.name
						}
						if sizeChanged {
							m.SizeKB = e.sizeKB
						}
						if growthChanged {
							if e.isPercentGrowth {
								m.GrowthPercent = e.growthPercent
							} else {
								m.GrowthKB = e.growthKB
							}
						}
						if maxSizeChanged {
							m.MaxSizeKB = e.maxSizeKB
						}
						if err := d.AlterFileContext(ctx, e.origName, m); err != nil {
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
