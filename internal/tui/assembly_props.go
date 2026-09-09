package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// assembly_props.go is the read-only Properties for a CLR assembly: General,
// Files and Routines.
//
// Nothing here writes, and no dialog could. CREATE and ALTER ASSEMBLY both
// need the assembly binary — a compiled .dll, or its hex literal — which is
// not something a form can produce, and the two flags a form *could* set
// (PERMISSION_SET, VISIBILITY) are changed by an ALTER that a user scripts
// rather than fills in. All three pages are named in
// prop_page_requires_test.go's pagesThatOnlyRead.

func findAssembly(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.Assembly, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.AssemblyByNameContext(ctx, name)
}

func assemblyPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	return []propPage{
		pageAssemblyGeneral(sc, dbName, name),
		pageAssemblyFiles(sc, dbName, name),
		pageAssemblyRoutines(sc, dbName, name),
	}
}

func pageAssemblyGeneral(sc *db.ServerConn, dbName, name string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			a, err := findAssembly(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("Assembly"),
				propsheet.Static("Name", a.Name),
				propsheet.Static("Owner", a.Owner),
				propsheet.Static("Strong name", a.ClrName),
				propsheet.Section("Host policy"),
				propsheet.Static("Permission set", string(a.PermissionSet)),
				propsheet.Static("Visible to routines", boolStr(a.IsVisible)),
				propsheet.Static("Shipped with SQL Server", boolStr(!a.IsUserDefined)),
				propsheet.Section("Dates"),
				propsheet.Static("Created", formatSQLDate(a.CreateDate)),
				propsheet.Static("Last modified", formatSQLDate(a.ModifyDate)),
				propsheet.Note("An assembly is defined by its binary. Changing the permission set or the visibility is an ALTER ASSEMBLY, and replacing the code needs the new .dll — neither is something this dialog can supply."),
			)
			return f, nil, nil
		},
	}
}

// pageAssemblyFiles lists sys.assembly_files: the assembly binary itself
// (file id 1) and any source or debug files registered beside it.
//
// The size comes from DATALENGTH in gosmo's listing, not from reading the
// payload — an assembly is a multi-megabyte binary, and the page has nothing
// to show of it but its length.
func pageAssemblyFiles(sc *db.ServerConn, dbName, name string) propPage {
	return propPage{
		title: "Files",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			a, err := findAssembly(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			files, err := a.FilesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			rows := make([][]string, len(files))
			for i, fl := range files {
				rows[i] = []string{strconv.Itoa(fl.FileID), fl.Name, formatBytes(fl.ContentLength)}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"File ID", "Name", "Size"}, rows)
			grid.SetCellCursor(true)

			return propsheet.NewForm(
				propsheet.Section("Files"),
				propsheet.NewGridRow(grid, 10),
				propsheet.Note("File 1 is the assembly binary. Any others are the source or debug files registered with it by CREATE ASSEMBLY ... ADD FILE."),
			), nil, nil
		},
	}
}

// pageAssemblyRoutines lists what the assembly is actually used for — the
// procedures, functions, triggers and aggregates bound to it. An assembly
// with none is either a dependency of another assembly or dead weight, and
// that is the question this page answers before a Delete.
func pageAssemblyRoutines(sc *db.ServerConn, dbName, name string) propPage {
	return propPage{
		title: "Routines",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			a, err := findAssembly(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			mods, err := a.ModulesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			rows := make([][]string, len(mods))
			for i, m := range mods {
				rows[i] = []string{dottedName(m.Schema, m.Name), assemblyModuleKind(m.Type),
					m.AssemblyClass, m.AssemblyMethod}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Name", "Kind", "Class", "Method"}, rows)
			grid.SetCellCursor(true)

			return propsheet.NewForm(
				propsheet.Section("Routines bound to this assembly"),
				propsheet.NewGridRow(grid, 12),
			), nil, nil
		},
	}
}

// assemblyModuleKind spells out the sys.objects type code gosmo reports, so
// the grid does not ask the reader to remember that "FS" is a scalar
// function and "AF" an aggregate.
func assemblyModuleKind(code string) string {
	switch code {
	case "P":
		return "Stored procedure"
	case "FS":
		return "Scalar function"
	case "FT":
		return "Table-valued function"
	case "AF":
		return "Aggregate"
	case "TA":
		return "Trigger"
	}
	return code
}
