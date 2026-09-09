package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// type_props.go is the read-only Properties for the four user-type families
// SSMS files under Programmability > Types, plus XML schema collections.
//
// Read-only by the objects' shape, not by omission: CREATE TYPE has no ALTER
// at all, so anything a form could change here would be a drop-and-recreate,
// and dropping a type breaks every column, parameter and variable declared on
// it. ALTER XML SCHEMA COLLECTION exists but only *adds* schema documents,
// which is a text edit rather than a form. All of these pages are named in
// prop_page_requires_test.go's pagesThatOnlyRead for that reason.
//
// System Data Types has no Properties dialog: SSMS offers none either, and
// there is nothing to show about `int` that its name does not already say.

// findUserDefinedDataType resolves an alias type, saving its callers the
// DatabaseByNameContext step. The pattern every finder in this file follows,
// and the reason the Detail Browser and this dialog can never disagree about
// which object a node names.
func findUserDefinedDataType(ctx context.Context, sc *db.ServerConn, dbName, schema, name string) (*gosmo.UserDefinedDataType, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.UserDefinedDataTypeByNameContext(ctx, schema, name)
}

func findUserDefinedTableType(ctx context.Context, sc *db.ServerConn, dbName, schema, name string) (*gosmo.UserDefinedTableType, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.UserDefinedTableTypeByNameContext(ctx, schema, name)
}

func findClrType(ctx context.Context, sc *db.ServerConn, dbName, schema, name string) (*gosmo.ClrType, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.ClrTypeByNameContext(ctx, schema, name)
}

func findXmlSchemaCollection(ctx context.Context, sc *db.ServerConn, dbName, schema, name string) (*gosmo.XmlSchemaCollection, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.XmlSchemaCollectionByNameContext(ctx, schema, name)
}

// findSystemDataType picks one built-in type out of the instance's list.
// sys.types has no by-name finder for the built-ins in gosmo — they are read
// as a list and nothing else — so this is the whole listing filtered, not a
// second query shape. The Detail Browser is its only caller.
func findSystemDataType(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.SystemDataType, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	types, err := d.SystemDataTypesContext(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range types {
		if strings.EqualFold(t.Name, name) {
			return t, nil
		}
	}
	return nil, fmt.Errorf("no system data type named %q in %s", name, dbName)
}

func userDefinedDataTypePropPages(sc *db.ServerConn, dbName, schema, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, err := findUserDefinedDataType(ctx, sc, dbName, schema, name)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("Alias type"),
				propsheet.Static("Name", t.Name),
				propsheet.Static("Schema", t.Schema),
				propsheet.Section("Data type"),
				propsheet.Static("Base type", formatDataTypeLen(t.BaseType, t.MaxLength, t.Precision, t.Scale)),
				propsheet.Static("Length (bytes)", typeLengthText(t.MaxLength)),
				propsheet.Static("Precision", strconv.Itoa(t.Precision)),
				propsheet.Static("Scale", strconv.Itoa(t.Scale)),
				propsheet.Static("Allow nulls", boolStr(t.IsNullable)),
				propsheet.Static("Collation", t.Collation),
				propsheet.Section("Bindings"),
				propsheet.Static("Bound rule", boundOrNone(t.Rule)),
				propsheet.Static("Bound default", boundOrNone(t.Default)),
				propsheet.Note("An alias type has no ALTER: changing one means dropping and recreating it, which every column declared on it refuses. sp_bindrule and sp_bindefault, which set the two bindings above, are deprecated by Microsoft."),
			)
			return f, nil, nil
		},
	}}
}

// boundOrNone renders an unbound rule or default as words rather than as an
// empty value, which reads as a value the page failed to load.
func boundOrNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func userDefinedTableTypePropPages(sc *db.ServerConn, dbName, schema, name string) []propPage {
	return []propPage{
		pageTableTypeGeneral(sc, dbName, schema, name),
		pageTableTypeColumns(sc, dbName, schema, name),
	}
}

func pageTableTypeGeneral(sc *db.ServerConn, dbName, schema, name string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, err := findUserDefinedTableType(ctx, sc, dbName, schema, name)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("Table type"),
				propsheet.Static("Name", t.Name),
				propsheet.Static("Schema", t.Schema),
				propsheet.Static("Memory optimized", boolStr(t.IsMemoryOptimized)),
				propsheet.Note("A table type is immutable: CREATE TYPE ... AS TABLE has no ALTER, and dropping one is refused while any procedure or function declares a parameter of it."),
			)
			return f, nil, nil
		},
	}
}

// pageTableTypeColumns is the type's shape. The columns come from the
// internal table sys.table_types points at, not from the type's own id — see
// gosmo's UserDefinedTableType.ColumnsContext, where reaching for the obvious
// id returns nothing at all.
func pageTableTypeColumns(sc *db.ServerConn, dbName, schema, name string) propPage {
	return propPage{
		title: "Columns",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, err := findUserDefinedTableType(ctx, sc, dbName, schema, name)
			if err != nil {
				return nil, nil, err
			}
			cols, err := t.ColumnsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			rows := make([][]string, len(cols))
			for i, c := range cols {
				rows[i] = []string{strconv.Itoa(c.OrdinalPosition), c.Name,
					formatDataTypeLen(string(c.DataType), c.MaxLength, c.Precision, c.Scale),
					boolStr(c.IsNullable), c.Collation}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"#", "Name", "Data Type", "Allow Nulls", "Collation"}, rows)
			grid.SetCellCursor(true)

			return propsheet.NewForm(
				propsheet.Section("Columns"),
				propsheet.NewGridRow(grid, 14),
			), nil, nil
		},
	}
}

func clrTypePropPages(sc *db.ServerConn, dbName, schema, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, err := findClrType(ctx, sc, dbName, schema, name)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("CLR type"),
				propsheet.Static("Name", t.Name),
				propsheet.Static("Schema", t.Schema),
				propsheet.Section("Implementation"),
				propsheet.Static("Assembly", t.Assembly),
				propsheet.Static("Assembly class", t.AssemblyClass),
				propsheet.Section("Storage"),
				propsheet.Static("Length (bytes)", typeLengthText(t.MaxLength)),
				propsheet.Static("Precision", strconv.Itoa(t.Precision)),
				propsheet.Static("Scale", strconv.Itoa(t.Scale)),
				propsheet.Static("Allow nulls", boolStr(t.IsNullable)),
				propsheet.Note("A CLR type is defined by the assembly above. Changing it means rebuilding that assembly and running ALTER ASSEMBLY, which needs the new binary."),
			)
			return f, nil, nil
		},
	}}
}

func xmlSchemaCollectionPropPages(sc *db.ServerConn, dbName, schema, name string) []propPage {
	return []propPage{
		pageXmlSchemaCollectionGeneral(sc, dbName, schema, name),
		pageXmlSchemaCollectionSchema(sc, dbName, schema, name),
	}
}

func pageXmlSchemaCollectionGeneral(sc *db.ServerConn, dbName, schema, name string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			c, err := findXmlSchemaCollection(ctx, sc, dbName, schema, name)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("XML schema collection"),
				propsheet.Static("Name", c.Name),
				propsheet.Static("Schema", c.Schema),
				propsheet.Section("Dates"),
				propsheet.Static("Created", formatSQLDate(c.CreateDate)),
				propsheet.Static("Last modified", formatSQLDate(c.ModifyDate)),
				propsheet.Note("ALTER XML SCHEMA COLLECTION only adds schema documents — it cannot change or remove one already in the collection."),
			)
			return f, nil, nil
		},
	}
}

// pageXmlSchemaCollectionSchema shows the collection's documents as the
// server reassembles them, in a read-only XML editor.
//
// The read is XML_SCHEMA_NAMESPACE, which is a second round trip after the
// catalog row and can return nothing for a collection the caller cannot see
// into; an empty result reports itself rather than drawing an empty editor,
// which would read as a collection holding no documents.
func pageXmlSchemaCollectionSchema(sc *db.ServerConn, dbName, schema, name string) propPage {
	return propPage{
		title: "Schema",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			c, err := findXmlSchemaCollection(ctx, sc, dbName, schema, name)
			if err != nil {
				return nil, nil, err
			}
			def, err := c.DefinitionContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			if strings.TrimSpace(def) == "" {
				return propsheet.NewForm(
					propsheet.Section("Schema"),
					propsheet.Note("The collection's schema documents could not be read. XML_SCHEMA_NAMESPACE returns nothing without VIEW DEFINITION on the collection."),
				), nil, nil
			}

			ed := controls.NewEditor(controls.XMLHighlighter(theme.Active()))
			ed.SetText(def)
			ed.SetReadOnly(true)

			return propsheet.NewForm(
				propsheet.Section("Schema"),
				propsheet.NewEditorRow("Documents", ed, 16),
			), nil, nil
		},
	}
}
