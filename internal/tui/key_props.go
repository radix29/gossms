package tui

import (
	"context"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// keyPropPages builds the page set for Primary/Unique Key Properties. A
// PRIMARY KEY or UNIQUE constraint is implemented as a unique index
// (sys.indexes.is_primary_key/is_unique_constraint), so most pages reuse
// Index Properties' own page builders unchanged. Included Columns and
// Filter are left out: ALTER TABLE ADD CONSTRAINT has no INCLUDE or WHERE
// clause, so a constraint-backed index can never have either.
//
// name is boxed in a *string shared by every page below: renaming a key
// changes the identity every other page's lookup depends on. The
// rename is the last write of an Apply/OK run (see propPage.renames),
// and commitRename then updates the box so PropDialog.InvalidateAll's
// reload re-fetches under the new name.
func keyPropPages(d *PropDialog, sc *db.ServerConn, dbName, schema, table, name string) []propPage {
	namePtr := &name
	w := gate.ObjectWriteRights()
	return []propPage{
		withRequiresOn(pageKeyGeneral(sc, dbName, schema, table, namePtr), dbName, schema, table, w...),
		withRequiresOn(pageKeyOptions(sc, dbName, schema, table, namePtr), dbName, schema, table, w...),
		pageIndexStorage(sc, dbName, schema, table, namePtr),
		pageIndexFragmentation(d, sc, dbName, schema, table, namePtr),
		withRequiresOn(pageExtendedProperties(sc, dbName, func() gosmo.ExtendedPropertyLevel {
			return gosmo.ExtendedPropertyLevel{
				Level0Type: "SCHEMA", Level0Name: schema,
				Level1Type: "TABLE", Level1Name: table,
				Level2Type: "INDEX", Level2Name: *namePtr,
			}
		}), dbName, schema, table, w...),
	}
}

// keyTypeName renders whether idx backs a PRIMARY KEY or a UNIQUE
// constraint, the way SSMS's Key Properties General page does.
func keyTypeName(isPrimaryKey bool) string {
	if isPrimaryKey {
		return "Primary Key"
	}
	return "Unique Key"
}

func pageKeyGeneral(sc *db.ServerConn, dbName, schema, table string, name *string) propPage {
	return propPage{
		title:   "General",
		renames: true,
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			idx, err := findIndex(ctx, sc, dbName, schema, table, *name)
			if err != nil {
				return nil, nil, err
			}

			grid := indexKeyColumnGrid(idx)

			nameRow := propsheet.Text("Key name", idx.Name, 24)

			f := propsheet.NewForm(
				propsheet.Section("Key identity"),
				nameRow,
				propsheet.Static("Type", keyTypeName(idx.IsPrimaryKey)),
				propsheet.Static("Index type", indexTypeName(idx.Type)),
				propsheet.Static("Disabled", boolStr(idx.IsDisabled)),
				propsheet.Section("Table or view"),
				propsheet.Static("Schema", idx.Table().Schema),
				propsheet.Static("Object", idx.Table().Name),
				propsheet.Static("Object type", "Table"),
				propsheet.Section("Key columns"),
				propsheet.NewGridRow(grid, 8),
				propsheet.Note("Key columns are fixed when the constraint is created — dropping and re-adding it is the only way to change them."),
			)

			apply := func(ctx context.Context) error {
				if !nameRow.Dirty() {
					return nil
				}
				idx, err := findIndex(ctx, sc, dbName, schema, table, *name)
				if err != nil {
					return err
				}
				if err := idx.Rename(ctx, nameRow.Value()); err != nil {
					return err
				}
				commitRename(ctx, name, nameRow.Value())
				return nil
			}
			return f, apply, nil
		},
	}
}

// pageKeyOptions mirrors Index Properties' Options page but drops Ignore
// duplicate keys entirely: SQL Server rejects IGNORE_DUP_KEY outright in
// ALTER INDEX ... SET on any index enforcing a PRIMARY KEY or UNIQUE
// constraint, even to set it to its existing value, so it can't be
// hidden-but-still-applied the way a no-op option could.
func pageKeyOptions(sc *db.ServerConn, dbName, schema, table string, name *string) propPage {
	return propPage{
		title: "Options",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			idx, err := findIndex(ctx, sc, dbName, schema, table, *name)
			if err != nil {
				return nil, nil, err
			}

			rebuild := newRebuildOptions(idx)
			rowLocksRow := propsheet.Check("Allow row locks", idx.AllowRowLocks)
			pageLocksRow := propsheet.Check("Allow page locks", idx.AllowPageLocks)

			rows := []propsheet.Row{
				propsheet.Section("Key options"),
				rebuild.fillFactor, rebuild.pad, rowLocksRow, pageLocksRow,
			}
			f := propsheet.NewForm(append(rows, rebuild.compressionRows()...)...)

			apply := func(ctx context.Context) error {
				idx, err := findIndex(ctx, sc, dbName, schema, table, *name)
				if err != nil {
					return err
				}
				if err := applySetOptions(ctx, idx, nil, rowLocksRow, pageLocksRow); err != nil {
					return err
				}
				return rebuild.apply(ctx, idx)
			}
			return f, apply, nil
		},
	}
}
