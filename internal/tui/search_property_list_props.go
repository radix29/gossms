package tui

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/propsheet"
	"github.com/radix29/tuikit/widgets"
)

// -- Search property list ------------------------------------------------------

func searchPropertyListPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	find := func(ctx context.Context) (*gosmo.SearchPropertyList, error) {
		return findSearchPropertyList(ctx, sc, dbName, name)
	}
	return []propPage{
		{
			title: "General",
			load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
				p, err := find(ctx)
				if err != nil {
					return nil, nil, err
				}
				return fullTextListGeneralForm("Search property list", p.Name, p.Owner, p.CreateDate, p.ModifyDate,
					"Its properties are registered on the Properties page."), nil, nil
			},
		},
		withRequiresOn(pageSearchProperties(sc, dbName, find), dbName, "", name, gate.AlterOnSearchPropertyList),
	}
}

var searchPropertyHeaders = []string{"Property Name", "Property Set GUID", "Int ID", "Internal ID", "Description"}

func searchPropertyRows(props []gosmo.SearchProperty) [][]string {
	rows := make([][]string, len(props))
	for k, p := range props {
		id := strconv.Itoa(p.ID)
		if p.ID == 0 {
			id = "" // not registered yet: the server assigns it
		}
		rows[k] = []string{p.Name, p.SetGUID, strconv.Itoa(p.IntID), id, p.Description}
	}
	return rows
}

// searchPropertyEdit is one row of the Properties page.
type searchPropertyEdit struct {
	prop gosmo.SearchProperty
	pendingState
}

// guidPattern is PROPERTY_SET_GUID's shape; the server refuses anything else
// with a conversion error that does not name the field.
var guidPattern = regexp.MustCompile(`^\{?[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}?$`)

// pageSearchProperties is the list's registered properties, master-detail
// over pendingEdits: the four fields to Add, Remove for the selected row. A
// property is identified by name within the list (ALTER … DROP names it), so
// Apply drops before it adds and a removed name can be registered again.
func pageSearchProperties(sc *db.ServerConn, dbName string, find func(context.Context) (*gosmo.SearchPropertyList, error)) propPage {
	return propPage{
		title: "Properties",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			p, err := find(ctx)
			if err != nil {
				return nil, nil, err
			}
			props, err := p.Properties(ctx)
			if err != nil {
				return nil, nil, err
			}
			loaded := make([]*searchPropertyEdit, len(props))
			for k, x := range props {
				loaded[k] = &searchPropertyEdit{prop: x}
			}
			edits := newPendingEdits(databaseCollation(p.Database()), loaded,
				func(e *searchPropertyEdit) string { return e.prop.Name }, nil, nil)
			rowsFor := func() [][]string {
				vis := edits.visible()
				out := make([]gosmo.SearchProperty, len(vis))
				for k, e := range vis {
					out[k] = e.prop
				}
				return searchPropertyRows(out)
			}
			grid := controls.NewDataGrid()
			grid.SetData(searchPropertyHeaders, rowsFor())
			redraw := func(row int) { resetGrid(grid, searchPropertyHeaders, rowsFor(), row) }

			nameField := propsheet.Text("Property name", "", 30)
			guidField := propsheet.Text("Property set GUID", "", 38)
			intField := propsheet.Text("Property int ID", "", 10)
			descField := propsheet.Text("Description", "", 40)
			for _, r := range []*propsheet.TextRow{nameField, guidField, intField, descField} {
				r.SetDirtyTracked(false)
			}
			hint := propsheet.Hint()

			addBtn := widgets.NewButton("Add", func() {
				prop, err := searchPropertyInput(nameField.Value(), guidField.Value(), intField.Value(), descField.Value())
				if err != nil {
					hint.SetError(err.Error())
					return
				}
				if edits.listed(prop.Name) {
					hint.SetError("A property named " + prop.Name + " is already listed.")
					return
				}
				hint.Clear()
				edits.add(&searchPropertyEdit{prop: prop})
				for _, r := range []*propsheet.TextRow{nameField, guidField, intField, descField} {
					r.SetValue("")
				}
				redraw(len(edits.visible()) - 1)
			})
			removeBtn := widgets.NewButton("Remove", func() {
				vis := edits.visible()
				i := grid.SelectedRow()
				if i < 0 || i >= len(vis) {
					hint.Set("Select a property in the grid to remove it.")
					return
				}
				hint.Clear()
				edits.remove(vis[i])
				redraw(max(0, i-1))
			})

			gridRow := propsheet.NewGridRow(grid, 10)
			gridRow.DirtyFn = edits.dirty
			gridRow.RevertFn = func() {
				edits.revert()
				redraw(0)
			}
			f := propsheet.NewForm(
				propsheet.Section("Registered search properties"),
				gridRow,
				propsheet.Section("Register a property"),
				nameField, guidField, intField, descField,
				propsheet.Buttons(addBtn, removeBtn),
				hint,
				propsheet.Note("A property is the set GUID and integer ID its document filter publishes it under "+
					"(Title is F29F85E0-4FF9-1068-AB91-08002B27B3D9, 2). An index using this list picks a change up "+
					"at its next full population."),
			)
			name := p.Name
			return f, func(ctx context.Context) error {
				l := sc.Server.DatabaseRef(dbName).FullText().SearchPropertyListRef(name)
				rows := edits.all()
				for _, e := range rows {
					if e.removing && !e.isNew {
						if err := l.DropProperty(ctx, e.prop.Name); err != nil {
							return err
						}
					}
				}
				for _, e := range rows {
					if e.isNew {
						if err := l.AddProperty(ctx, e.prop); err != nil {
							return err
						}
					}
				}
				return nil
			}, nil
		},
	}
}

// searchPropertyInput validates the Add fields into a property: a name, a
// GUID in its usual spelling (braces optional, dropped), and an integer ID.
func searchPropertyInput(name, guid, intID, desc string) (gosmo.SearchProperty, error) {
	name, guid, intID = strings.TrimSpace(name), strings.TrimSpace(guid), strings.TrimSpace(intID)
	if name == "" {
		return gosmo.SearchProperty{}, fmt.Errorf("type a property name first")
	}
	if !guidPattern.MatchString(guid) {
		return gosmo.SearchProperty{}, fmt.Errorf("the property set GUID must look like F29F85E0-4FF9-1068-AB91-08002B27B3D9")
	}
	id, err := strconv.Atoi(intID)
	if err != nil || id < 0 {
		return gosmo.SearchProperty{}, fmt.Errorf("the property int ID must be a whole number, 0 or more")
	}
	return gosmo.SearchProperty{
		Name:        name,
		SetGUID:     strings.Trim(guid, "{}"),
		IntID:       id,
		Description: strings.TrimSpace(desc),
	}, nil
}
