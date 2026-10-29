package cmd

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// The db table-sizes options; --schema and --sort-by are object list's and
// db files' names.
const optTop = "top"

// defaultTop is --top's default: the SSMS report's "Top Tables".
const defaultTop = 20

// tableSortKeys is --sort-by's enum; the first is the default.
var tableSortKeys = []string{"reserved", "rows", "data", "index", "unused"}

func dbTableSizesCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"db", "table-sizes"},
		Summary: "List a database's largest tables: rows, reserved, data, index and unused space",
		Description: "One row per user table (sys.tables), read inside the database in one round trip, in " +
			"sp_spaceused's per-object arithmetic so each row matches `EXEC sp_spaceused 'schema.table'` " +
			"exactly: data counts the table's in-row data pages and its LOB and row-overflow pages, index the " +
			"rest of the used pages, and reserved and index include the table's XML, spatial and full-text " +
			"index internal tables. rows is the heap's or clustered index's row count. The figures come from " +
			"sys.partitions and sys.allocation_units, so no VIEW DATABASE STATE is needed; the login sees the " +
			"tables it has metadata visibility of. --sort-by orders largest first (default reserved), schema " +
			"and table name breaking ties; --top keeps that many rows (default 20, 0 for every table); " +
			"--schema keeps the one schema, case-insensitively, as object list's. A memory-optimized table's " +
			"four sizes are NULL, as sp_spaceused prints them (SQL Server 2016's prints 0 KB and 0 rows; rows " +
			"here is still its row count), with a warning, and it sorts last on a size. A " +
			"database that cannot be read fails: exit 8 for its state, 9 for the login's access. A snapshot's " +
			"figures are its source's as of the snapshot's creation, with a warning.",
		Args: []spec.Arg{{Name: "name", Help: "Database name", Required: true}},
		Options: append(ConnectOptions(),
			spec.Option{Name: optTop, Type: spec.TypeInt, Default: strconv.Itoa(defaultTop),
				Help: "Keep this many tables, the largest first; 0 for every table"},
			spec.Option{Name: optSchema, Type: spec.TypeString, Help: "Only tables in this schema (case-insensitive)"},
			spec.Option{Name: optSortBy, Type: spec.TypeEnum, Enum: tableSortKeys, Default: tableSortKeys[0],
				Help: "Order the tables, largest first: reserved, rows, data, index or unused"},
			unitOption(),
		),
		Class: spec.ClassRead,
		Run:   d.dbTableSizes,
		Examples: []string{
			"gossms db table-sizes -p dev AdventureWorks",
			"gossms db table-sizes -p dev AdventureWorks --top 10 --sort-by rows",
			"gossms db table-sizes -p dev AdventureWorks --schema Sales --top 0 --unit kb -o json",
		},
	}
}

func (d Deps) dbTableSizes(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	top := inv.Values[optTop].(int)
	if top < 0 {
		return nil, usageError(fmt.Sprintf("--top: %d is negative", top),
			"Give the number of tables to keep, or --top 0 for every table.")
	}
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	t, err := storageDatabase(ctx, cat, inv.Args[0], res)
	if err == nil && t.Unreadable != "" {
		err = t.unreadableError()
	}
	if err != nil {
		return res, err
	}
	tables, err := cat.TableSizes(ctx, t.DB)
	if err != nil {
		return res, readError(err)
	}

	if schema, _ := inv.Values[optSchema].(string); schema != "" {
		tables = slices.DeleteFunc(tables, func(ts *gosmo.TableSize) bool { return !strings.EqualFold(ts.Schema, schema) })
	}
	sortTableSizes(tables, inv.Values[optSortBy].(string))
	if top > 0 && len(tables) > top {
		tables = tables[:top]
	}

	res.Sets = []output.Set{tableSizeSet(tables, unitOf(inv))}
	if t.DB.IsSnapshot() {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"%s: a database snapshot; its figures are its source's as of the snapshot's creation.", gosmo.QuoteName(t.DB.Name)))
	}
	for _, ts := range tables {
		if ts.IsMemoryOptimized {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"%s.%s: a memory-optimized table; sp_spaceused reports no sizes for it.",
				gosmo.QuoteName(ts.Schema), gosmo.QuoteName(ts.Name)))
		}
	}
	return res, nil
}

// sortTableSizes orders tables by --sort-by, largest first; a
// memory-optimized table, which has no sizes, sorts last on a size. Ties go
// by schema, then name.
func sortTableSizes(tables []*gosmo.TableSize, by string) {
	figure := func(ts *gosmo.TableSize) int64 {
		switch by {
		case "rows":
			return ts.Rows
		case "data":
			return ts.DataKB
		case "index":
			return ts.IndexKB
		case "unused":
			return ts.UnusedKB
		}
		return ts.ReservedKB
	}
	slices.SortStableFunc(tables, func(a, b *gosmo.TableSize) int {
		var c int
		if by != "rows" {
			// false sorts first: sized tables before memory-optimized ones.
			c = cmpBool(a.IsMemoryOptimized, b.IsMemoryOptimized)
		}
		return cmp.Or(c, cmp.Compare(figure(b), figure(a)), cmp.Compare(a.Schema, b.Schema), cmp.Compare(a.Name, b.Name))
	})
}

// cmpBool orders false before true.
func cmpBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// tableSizeSet is tables as db table-sizes' result set, sizes in u: §10.8's
// columns. A memory-optimized table's sizes are NULL, as sp_spaceused's.
func tableSizeSet(tables []*gosmo.TableSize, u sizeUnit) output.Set {
	set := output.Set{Columns: []output.Column{
		{Name: "schema", Type: "sysname"}, {Name: "table", Type: "sysname"}, {Name: "rows", Type: "bigint"},
		u.column("reserved", true), u.column("data", true), u.column("index", true), u.column("unused", true),
	}}
	for _, ts := range tables {
		var reserved, data, index, unused *string
		if !ts.IsMemoryOptimized {
			reserved, data, index, unused = u.cell(ts.ReservedKB), u.cell(ts.DataKB), u.cell(ts.IndexKB), u.cell(ts.UnusedKB)
		}
		set.Rows = append(set.Rows, []*string{ptr(ts.Schema), ptr(ts.Name), ptr(strconv.FormatInt(ts.Rows, 10)),
			reserved, data, index, unused})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}
