package tui

import (
	"context"
	"strconv"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// keyPropPages builds the page set for Primary/Unique Key Properties — SSMS
// as reference, no mockup for this one. A PRIMARY KEY or UNIQUE constraint
// is implemented as a unique index (sys.indexes.is_primary_key/
// is_unique_constraint), so most pages reuse Index Properties' own page
// builders unchanged. Two Index Properties pages are dropped entirely:
// Included Columns and Filter — ALTER TABLE ADD CONSTRAINT has no INCLUDE
// or WHERE clause, so a constraint-backed index can never have either.
// name is boxed in a *string shared by every page below: renaming a key is
// the one edit in this dialog that changes the identity every other page's
// findIndex lookup depends on, so pageKeyGeneral's apply closure updates
// *name in place on success — otherwise Apply (which reloads every page via
// PropDialog.InvalidateAll, see prop_dialog.go's runApply) would send the
// very next reload looking for an index name that no longer exists.
func keyPropPages(d *PropDialog, sc *db.ServerConn, dbName, schema, table, name string) []propPage {
	namePtr := &name
	return []propPage{
		pageKeyGeneral(sc, dbName, schema, table, namePtr),
		pageKeyOptions(sc, dbName, schema, table, namePtr),
		pageIndexStorage(sc, dbName, schema, table, namePtr),
		pageIndexFragmentation(d, sc, dbName, schema, table, namePtr),
		pageIndexExtendedProperties(sc, dbName, schema, table, namePtr),
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
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, idx, err := findIndex(ctx, sc, dbName, schema, table, *name)
			if err != nil {
				return nil, nil, err
			}

			rows := make([][]string, len(idx.KeyColumns))
			for i, c := range idx.KeyColumns {
				order := "Ascending"
				if c.Descending {
					order = "Descending"
				}
				rows[i] = []string{strconv.Itoa(i + 1), c.Name, order}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Ord", "Column name", "Sort order"}, rows)
			grid.SetCellCursor(true)

			nameRow := propsheet.Text("Key name", idx.Name, 24)

			f := propsheet.NewForm(
				propsheet.Section("Key identity"),
				nameRow,
				propsheet.Static("Type", keyTypeName(idx.IsPrimaryKey)),
				propsheet.Static("Index type", indexTypeNames[idx.Type]),
				propsheet.Static("Disabled", boolStr(idx.IsDisabled)),
				propsheet.Section("Table or view"),
				propsheet.Static("Schema", t.Schema),
				propsheet.Static("Object", t.Name),
				propsheet.Static("Object type", "Table"),
				propsheet.Section("Key columns"),
				propsheet.NewGridRow(grid, 8),
				propsheet.Note("Key columns are fixed when the constraint is created — dropping and re-adding it is the only way to change them."),
			)

			apply := func(ctx context.Context) error {
				if !nameRow.Dirty() {
					return nil
				}
				t, idx, err := findIndex(ctx, sc, dbName, schema, table, *name)
				if err != nil {
					return err
				}
				if err := idx.RenameContext(ctx, t, nameRow.Value()); err != nil {
					return err
				}
				*name = nameRow.Value()
				return nil
			}
			return f, apply, nil
		},
	}
}

// pageKeyOptions mirrors Index Properties' Options page but drops Ignore
// duplicate keys entirely — live-verified against a real PK-backed index:
// SQL Server rejects IGNORE_DUP_KEY outright in ALTER INDEX ... SET on any
// index enforcing a PRIMARY KEY or UNIQUE constraint, even to set it to its
// existing value, so the option can't just be hidden-but-still-applied the
// way it would for a no-op.
func pageKeyOptions(sc *db.ServerConn, dbName, schema, table string, name *string) propPage {
	return propPage{
		title: "Options",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			_, idx, err := findIndex(ctx, sc, dbName, schema, table, *name)
			if err != nil {
				return nil, nil, err
			}

			fillFactorRow := propsheet.Int("Fill factor", int64(idx.FillFactor), 0, 100, "%")
			padRow := propsheet.Check("Pad index", idx.IsPadded)
			rowLocksRow := propsheet.Check("Allow row locks", idx.AllowRowLocks)
			pageLocksRow := propsheet.Check("Allow page locks", idx.AllowPageLocks)
			compressionRow := propsheet.Select("Data compression", indexDataCompressionOptions,
				indexOf(indexDataCompressionOptions, idx.DataCompression))

			f := propsheet.NewForm(
				propsheet.Section("Key options"),
				fillFactorRow, padRow, rowLocksRow, pageLocksRow,
				propsheet.Section("Compression"),
				compressionRow,
				propsheet.Note("Fill factor, pad index, and data compression only take effect after a rebuild — Apply issues one automatically when any of these three change."),
			)

			apply := func(ctx context.Context) error {
				t, idx, err := findIndex(ctx, sc, dbName, schema, table, *name)
				if err != nil {
					return err
				}
				if rowLocksRow.Dirty() || pageLocksRow.Dirty() {
					if err := idx.SetLockOptionsContext(ctx, t, rowLocksRow.Checked(), pageLocksRow.Checked()); err != nil {
						return err
					}
				}
				if fillFactorRow.Dirty() || padRow.Dirty() || compressionRow.Dirty() {
					fillFactor, err := fillFactorRow.IntValue()
					if err != nil {
						return err
					}
					compression := ""
					if compressionRow.Dirty() {
						compression = compressionRow.Value()
					}
					if err := idx.RebuildWithOptionsContext(ctx, t, int(fillFactor), padRow.Checked(), compression); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}
