package tui

import (
	"context"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// partition_props.go is the read-only Properties for a partition function
// and a partition scheme. Read-only for the same reason Foreign Key
// Properties is: neither has an ALTER that changes what these pages show —
// ALTER PARTITION FUNCTION only splits or merges a boundary, and ALTER
// PARTITION SCHEME only names the next filegroup to use.

// findPartitionFunction resolves a partition function by name. It exists
// only to save the DatabaseByNameContext step at the four call sites; the
// lookup itself is gosmo's, which matches the name in SQL and so under the
// server's collation.
func findPartitionFunction(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.PartitionFunction, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.PartitionFunctionByNameContext(ctx, name)
}

func findPartitionScheme(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.PartitionScheme, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.PartitionSchemeByNameContext(ctx, name)
}

func partitionFunctionPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			pf, err := findPartitionFunction(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"#", "Boundary value"}, partitionBoundaryRows(pf))
			grid.SetCellCursor(true)

			// RANGE LEFT/RIGHT says which side of a boundary the boundary
			// value itself falls on, which is the one thing about a
			// partition function that is easy to get backwards.
			rangeText := "LEFT — a boundary value belongs to the partition below it"
			if pf.IsRight {
				rangeText = "RIGHT — a boundary value belongs to the partition above it"
			}
			return propsheet.NewForm(
				propsheet.Section("Partition function"),
				propsheet.Static("Name", pf.Name),
				propsheet.Static("Input type", string(pf.InputType)),
				propsheet.Static("Range", rangeText),
				propsheet.Static("Boundary values", strconv.Itoa(pf.BoundaryCount)),
				propsheet.Static("Partitions", strconv.Itoa(pf.BoundaryCount+1)),
				propsheet.Section("Boundaries"),
				propsheet.NewGridRow(grid, 10),
			), nil, nil
		},
	}}
}

func partitionBoundaryRows(pf *gosmo.PartitionFunction) [][]string {
	rows := make([][]string, len(pf.Boundaries))
	for i, b := range pf.Boundaries {
		rows[i] = []string{strconv.Itoa(i + 1), b}
	}
	return rows
}

func partitionSchemePropPages(sc *db.ServerConn, dbName, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			ps, err := findPartitionScheme(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			rows := make([][]string, len(ps.FileGroups))
			for i, fg := range ps.FileGroups {
				rows[i] = []string{strconv.Itoa(i + 1), fg}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Partition", "Filegroup"}, rows)
			grid.SetCellCursor(true)

			return propsheet.NewForm(
				propsheet.Section("Partition scheme"),
				propsheet.Static("Name", ps.Name),
				propsheet.Static("Partition function", ps.FunctionName),
				propsheet.Static("Filegroups", strings.Join(ps.FileGroups, ", ")),
				propsheet.Section("Partition mapping"),
				propsheet.NewGridRow(grid, 10),
			), nil, nil
		},
	}}
}
