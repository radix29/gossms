package cmd

import (
	"context"
	"fmt"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// object dependencies' options.
const (
	optDirection = "direction"
	optDepth     = "depth"
)

// The directions object dependencies follows, --direction's enum.
const (
	dirUses   = "uses"
	dirUsedBy = "used-by"
	dirBoth   = "both"
)

// dependenciesSelect is what reading sys.sql_expression_dependencies takes,
// the hint for its Msg 229 (object dependencies, and a table's DROP script,
// which reads its schema-bound dependents there). public lacks it: checked on
// 17, 2026-11-05, where a user granted only SELECT on a table — or VIEW
// DEFINITION on the database — was refused.
const dependenciesSelect = "SELECT on sys.sql_expression_dependencies, which public lacks: db_datareader and " +
	"db_owner have it, or GRANT SELECT ON sys.sql_expression_dependencies. A DENY on it refuses even those."

// maxDepth bounds --depth (D7): each level is a read per object reached.
const maxDepth = 5

func (d Deps) objectDependenciesCommand(nameArg spec.Arg) *spec.Command {
	return &spec.Command{
		Path:    []string{"object", "dependencies"},
		Summary: "Show what an object uses and what uses it, as View Dependencies does",
		Description: "One row per dependency edge. uses: the objects <object>'s own definition references (tables, " +
			"views, functions, procedures it calls); used-by: the objects whose definition references <object>. " +
			"--depth n follows the edges n levels out: level 1 is <object>'s own, level 2 those of the objects at " +
			"level 1, and so on; via names the object at the level before. An object reached twice is followed " +
			"once, so a cycle ends. Only objects in the connection's database are listed: a reference to another " +
			"database, or to an object that does not exist, is not. schema_bound marks a WITH SCHEMABINDING " +
			"reference; caller_dependent a one-part name resolved, as the module's owner would, against its " +
			"schema and then dbo — another caller may get a different object.",
		Args: []spec.Arg{nameArg},
		Options: append(ConnectOptions(),
			spec.Option{Name: optDirection, Type: spec.TypeEnum, Enum: []string{dirUses, dirUsedBy, dirBoth},
				Default: dirBoth, Help: "Which edges to follow"},
			spec.Option{Name: optDepth, Type: spec.TypeInt, Default: "1", Help: "Levels to follow, 1 to 5"},
		),
		Class: spec.ClassRead,
		Run:   d.objectDependencies,
		Examples: []string{
			"gossms object dependencies -p dev Reporting.usp_MonthlySales --direction uses",
			"gossms object dependencies -p dev Sales.SalesOrderDetail --direction used-by --depth 3 -o json",
		},
	}
}

// depEdge is one object dependencies row.
type depEdge struct {
	direction string
	level     int
	via       string
	dep       *gosmo.Dependency
}

func (d Deps) objectDependencies(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	depth := inv.Values[optDepth].(int)
	if depth < 1 || depth > maxDepth {
		return nil, usageError(fmt.Sprintf("--depth: %d is not between 1 and %d", depth, maxDepth),
			"Give --depth 1 for the object's own edges, up to 5.")
	}
	res := &output.Result{}
	_, dbase, o, done, err := d.lookupObject(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer done()

	direction := inv.Values[optDirection].(string)
	var edges []depEdge
	if direction != dirUsedBy {
		edges, err = walkDependencies(ctx, o, depth, dirUses, dbase.Dependencies, edges)
	}
	if err == nil && direction != dirUses {
		edges, err = walkDependencies(ctx, o, depth, dirUsedBy, dbase.Dependents, edges)
	}
	if err != nil {
		e := readError(err)
		if e.Code == output.CodePermissionDenied {
			e.Hint = "Reading dependencies takes " + dependenciesSelect
		}
		return res, e
	}
	// Both reads are all-or-nothing on VIEW DEFINITION on the database
	// (gosmo Database.HasPermission): without it they answer no rows rather
	// than refuse, so an empty list is checked before it is believed. A row
	// at all means the permission is held.
	if len(edges) == 0 {
		switch held, err := dbase.HasPermission(ctx, "VIEW DEFINITION"); {
		case err != nil:
			res.Warnings = append(res.Warnings, "No dependencies are listed, and whether the login may see them "+
				"could not be checked: "+err.Error())
		case !held:
			res.Warnings = append(res.Warnings, "No dependencies are listed, but the login lacks VIEW DEFINITION "+
				"on the database, without which SQL Server lists none: there may be some. GRANT VIEW DEFINITION "+
				"(or db_owner) shows them.")
		}
	}

	res.Fields = append(objectFields(o),
		output.Field{Key: "direction", Value: direction},
		output.Field{Key: "depth", Value: strconv.Itoa(depth)})
	set := output.Set{Name: "dependencies", Columns: []output.Column{
		{Name: "direction", Type: "nvarchar(10)"}, {Name: "level", Type: "int"}, {Name: "via", Type: "nvarchar(300)"},
		{Name: "schema", Type: "sysname"}, {Name: "name", Type: "sysname"}, {Name: "type", Type: "nvarchar(60)"},
		{Name: "schema_bound", Type: "bit"}, {Name: "caller_dependent", Type: "bit"},
	}}
	for _, e := range edges {
		set.Rows = append(set.Rows, []*string{ptr(e.direction), ptr(strconv.Itoa(e.level)), ptr(e.via),
			ptr(e.dep.Schema), ptr(e.dep.Name), ptr(kindOfDesc(e.dep.TypeDesc)),
			bitCell(e.dep.IsSchemaBound), bitCell(e.dep.IsCallerDependent)})
	}
	set.TotalRowCount = len(set.Rows)
	res.Sets = []output.Set{set}
	return res, nil
}

// walkDependencies appends to edges, breadth first, every edge read reaches
// from root within depth levels (D7). Each object is expanded once: an edge
// back to one already reached — a cycle, or two paths to it — is listed but
// not followed again. Catalog names are compared exactly.
func walkDependencies(ctx context.Context, root *gosmo.ObjectInfo, depth int, direction string,
	read func(context.Context, string, string) ([]*gosmo.Dependency, error), edges []depEdge) ([]depEdge, error) {
	type node struct{ schema, name string }
	seen := map[node]bool{{root.Schema, root.Name}: true}
	level := []node{{root.Schema, root.Name}}
	for l := 1; l <= depth && len(level) > 0; l++ {
		var next []node
		for _, n := range level {
			deps, err := read(ctx, n.schema, n.name)
			if err != nil {
				return nil, err
			}
			for _, dep := range deps {
				// A table's filtered index or statistic records the table as
				// referencing itself, through its filter's columns: not a
				// dependency, and View Dependencies does not show it.
				k := node{dep.Schema, dep.Name}
				if k == n {
					continue
				}
				edges = append(edges, depEdge{direction, l, gosmo.QualifiedName(n.schema, n.name), dep})
				if !seen[k] {
					seen[k] = true
					next = append(next, k)
				}
			}
		}
		level = next
	}
	return edges, nil
}
