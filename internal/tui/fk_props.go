package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// fkPropPages builds the page set for Foreign Key Properties — read-only,
// unlike every other Properties dialog here: a foreign key's shape
// (columns, referenced table, actions) can only change by dropping and
// recreating it. One page only — no Options/Storage/Extended Properties,
// since there's no persisted per-object state beyond what General shows.
func fkPropPages(sc *db.ServerConn, dbName, schema, table, name string) []propPage {
	return []propPage{
		pageForeignKeyGeneral(sc, dbName, schema, table, name),
	}
}

// findForeignKey resolves dbName/schema/table/name to the owning
// *gosmo.Table and its *gosmo.ForeignKey, mirroring findIndex/findStatistic.
func findForeignKey(ctx context.Context, sc *db.ServerConn, dbName, schema, table, name string) (*gosmo.Table, *gosmo.ForeignKey, error) {
	t, err := findTable(ctx, sc, dbName, schema, table)
	if err != nil {
		return nil, nil, err
	}
	fk, err := t.ForeignKeyByNameContext(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	return t, fk, nil
}

func pageForeignKeyGeneral(sc *db.ServerConn, dbName, schema, table, name string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, fk, err := findForeignKey(ctx, sc, dbName, schema, table, name)
			if err != nil {
				return nil, nil, err
			}

			rows := make([][]string, len(fk.Columns))
			for i, c := range fk.Columns {
				refCol := ""
				if i < len(fk.ReferencedColumns) {
					refCol = fk.ReferencedColumns[i]
				}
				rows[i] = []string{c, refCol}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Column (referencing)", "Referenced column"}, rows)
			grid.SetCellCursor(true)

			formRows := []propsheet.Row{
				propsheet.Section("Key identity"),
				propsheet.Static("Name", fk.Name),
				propsheet.Static("Enabled", boolStr(!fk.IsDisabled)),
				propsheet.Static("Not for replication", boolStr(fk.IsNotForReplication)),
				propsheet.Section("Tables"),
				propsheet.Static("Table (referencing)", fqn(t.Schema, t.Name)),
				propsheet.Static("Referenced table", fqn(fk.ReferencedSchema, fk.ReferencedTable)),
				propsheet.Section("Column mapping"),
				propsheet.NewGridRow(grid, 6),
				propsheet.Section("Enforcement"),
				propsheet.Static("On delete", fk.DeleteAction),
				propsheet.Static("On update", fk.UpdateAction),
			}
			if fk.IsDisabled {
				formRows = append(formRows,
					propsheet.Section("Note"),
					propsheet.Note("A disabled foreign key (WITH NOCHECK) is not enforced — existing rows may violate it."),
				)
			}

			f := propsheet.NewForm(formRows...)
			return f, nil, nil
		},
	}
}
