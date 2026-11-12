package cmd

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// The object commands' options.
const (
	optType   = "type"
	optSchema = "schema"
	optName   = "name"
	optIn     = "in"
)

// The object kinds, as `object list --type` takes them and every object
// command's type column and field print them: the Object Explorer folders.
const (
	kindTable     = "table"
	kindView      = "view"
	kindProcedure = "procedure"
	kindFunction  = "function"
	kindTrigger   = "trigger"
	kindSynonym   = "synonym"
	kindSequence  = "sequence"
	kindType      = "type"
	kindSchema    = "schema"
)

// objectKinds is --type's enum, in the order object list prints the kinds.
var objectKinds = []string{kindTable, kindView, kindProcedure, kindFunction, kindTrigger,
	kindSynonym, kindSequence, kindType, kindSchema}

// The places object search looks, --in's enum.
const (
	searchName       = "name"
	searchDefinition = "definition"
	searchColumn     = "column"
)

// kindOfDesc is the kind word for a sys.objects type_desc; one with no
// folder of its own (a constraint, a rule) is its type_desc lower-cased.
func kindOfDesc(typeDesc string) string {
	switch typeDesc {
	case "USER_TABLE", "SYSTEM_TABLE":
		return kindTable
	case "VIEW":
		return kindView
	case "SQL_STORED_PROCEDURE", "CLR_STORED_PROCEDURE", "EXTENDED_STORED_PROCEDURE", "REPLICATION_FILTER_PROCEDURE":
		return kindProcedure
	case "SQL_SCALAR_FUNCTION", "SQL_INLINE_TABLE_VALUED_FUNCTION", "SQL_TABLE_VALUED_FUNCTION",
		"CLR_SCALAR_FUNCTION", "CLR_TABLE_VALUED_FUNCTION":
		return kindFunction
	case "SQL_TRIGGER", "CLR_TRIGGER":
		return kindTrigger
	case "SYNONYM":
		return kindSynonym
	case "SEQUENCE_OBJECT":
		return kindSequence
	}
	return strings.ToLower(typeDesc)
}

// ObjectCommands is the object group: its node and its read commands.
func ObjectCommands(d Deps) []*spec.Command {
	nameArg := spec.Arg{Name: "object", Help: "The object: schema.name, or a name alone when one schema has it",
		Format: "schema.name", Required: true}
	return []*spec.Command{
		{
			Path:    []string{"object"},
			Summary: "List, find and describe the objects in the connection's database",
			Class:   spec.ClassRead,
		},
		{
			Path:    []string{"object", "list"},
			Summary: "List objects: tables, views, procedures, functions, triggers, synonyms, sequences, types, schemas",
			Description: "One row per object of the connection's database (-d, or the profile's), by kind as --type " +
				"lists them, then schema and name. --name keeps the names containing the text and --schema the one " +
				"schema, both case-insensitively, as Object Explorer's filter does — no wildcards. Objects SQL Server " +
				"ships (system tables, the sys schema's views, procedures and functions, the system schemas) only " +
				"with --include-system. A trigger is a table's or view's DML trigger; a type is an alias, table or " +
				"CLR type.",
			Options: append(ConnectOptions(),
				spec.Option{Name: optType, Type: spec.TypeList, Enum: objectKinds, Help: "Only these kinds (default: all)"},
				spec.Option{Name: optSchema, Type: spec.TypeString, Help: "Only objects in this schema"},
				spec.Option{Name: optName, Type: spec.TypeString, Help: "Only names containing this text"},
				spec.Option{Name: optIncludeSystem, Type: spec.TypeBool, Help: "Also list the objects SQL Server ships"},
			),
			Class: spec.ClassRead,
			Run:   d.objectList,
			Examples: []string{
				"gossms object list -p dev --type table --schema Sales",
				"gossms object list -p dev --type procedure,function --name order -o json",
				"gossms object list -p dev -d master --type view --name dm_exec --include-system",
			},
		},
		{
			Path:    []string{"object", "search"},
			Summary: "Find objects by name, by text in their T-SQL, or by column name",
			Description: "Looks in the connection's database for <text>, case-insensitively and literally (% and _ " +
				"are not wildcards). --in name finds tables, views, procedures, functions and triggers whose name " +
				"contains it; --in definition finds the modules whose T-SQL contains it (an encrypted or CLR module " +
				"has none to search); --in column finds the columns of tables and views whose name contains it, " +
				"adding the column and its data type to each row.",
			Args: []spec.Arg{{Name: "text", Help: "The text to find", Required: true}},
			Options: append(ConnectOptions(),
				spec.Option{Name: optIn, Type: spec.TypeEnum, Enum: []string{searchName, searchDefinition, searchColumn},
					Default: searchName, Help: "Where to look"},
			),
			Class: spec.ClassRead,
			Run:   d.objectSearch,
			Examples: []string{
				`gossms object search -p dev "Customer"`,
				`gossms object search -p dev "CustomerID" --in column`,
				`gossms object search -p dev "GETDATE()" --in definition -o json`,
			},
		},
		{
			Path:    []string{"object", "describe"},
			Summary: "Describe one object: columns, keys, indexes, constraints and triggers, or a module's parameters",
			Description: "A record — schema, name, type, typeDesc, objectId, created, modified, parent, then what the " +
				"kind adds — and the sets that kind has. A table: rowCount; sets columns, keys (primary key, unique " +
				"and foreign keys), indexes, check_constraints, triggers. A view: isSchemaBound; sets columns, " +
				"indexes, triggers. A procedure: set parameters; a function also returns. A trigger: isEnabled and " +
				"events; a synonym its baseObject; a sequence its type and values. Any other object (a constraint, " +
				"a rule) is the record alone.",
			Args:    []spec.Arg{nameArg},
			Options: ConnectOptions(),
			Class:   spec.ClassRead,
			Run:     d.objectDescribe,
			Examples: []string{
				"gossms object describe -p dev Sales.SalesOrderHeader",
				"gossms object describe -p dev Sales.SalesOrderHeader -o json",
				"gossms object describe -p dev dbo.uspGetBillOfMaterials",
			},
		},
		{
			Path:    []string{"object", "definition"},
			Summary: "Show the T-SQL source of a view, procedure, function or trigger",
			Description: "A record — schema, name, type — and the definition as the server stores it. Text prints " +
				"it after the record under an === DEFINITION (n lines) === marker, line breaks kept, and " +
				"--max-col-width does not cut it; JSON gives it as one string. An encrypted module, or a CLR one, " +
				"has no T-SQL to show: the definition is null with a warning. This is the module's text, not a " +
				"generated CREATE script.",
			Args:    []spec.Arg{nameArg},
			Options: ConnectOptions(),
			Class:   spec.ClassRead,
			Run:     d.objectDefinition,
			Examples: []string{
				"gossms object definition -p dev Sales.vSalesPerson",
				"gossms object definition -p dev dbo.uspGetBillOfMaterials -o json",
			},
		},
		d.objectDependenciesCommand(nameArg),
		d.objectIndexesCommand(),
		d.objectStatsCommand(),
	}
}

// listedObject is one object list row.
type listedObject struct {
	schema, name, kind string
	system             bool
}

// listFilter is object list's --schema and --name: what the families that
// take an ObjectFilter narrow at the server, and the rest in matches.
type listFilter struct{ schema, name string }

func (f listFilter) gosmo() gosmo.ObjectFilter {
	var o gosmo.ObjectFilter
	if f.schema != "" {
		o.Schema = []gosmo.TextCriterion{{Op: gosmo.TextEquals, Value: f.schema}}
	}
	if f.name != "" {
		o.Name = []gosmo.TextCriterion{{Op: gosmo.TextContains, Value: f.name}}
	}
	return o
}

// matches is the ObjectFilter's rule in Go — schema equal, name containing,
// both case-insensitive — for the families whose listing takes no filter.
func (f listFilter) matches(schema, name string) bool {
	return (f.schema == "" || strings.EqualFold(schema, f.schema)) &&
		(f.name == "" || strings.Contains(strings.ToLower(name), strings.ToLower(f.name)))
}

func (d Deps) objectList(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, dbase, err := d.connectDatabase(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	kinds, _ := inv.Values[optType].([]string)
	if len(kinds) == 0 {
		kinds = objectKinds
	}
	f := listFilter{schema: strings.TrimSpace(inv.Values[optSchema].(string)), name: strings.TrimSpace(inv.Values[optName].(string))}
	system := inv.Values[optIncludeSystem].(bool)
	var rows []listedObject
	add := func(schema, name, kind string, sys bool) {
		rows = append(rows, listedObject{schema, name, kind, sys})
	}
	for _, kind := range orderedKinds(kinds) {
		if err := listKind(ctx, dbase, kind, f, system, add); err != nil {
			return res, readError(err)
		}
	}
	slices.SortStableFunc(rows, func(a, b listedObject) int {
		return cmp.Or(cmp.Compare(slices.Index(objectKinds, a.kind), slices.Index(objectKinds, b.kind)),
			cmp.Compare(strings.ToLower(a.schema), strings.ToLower(b.schema)), cmp.Compare(a.schema, b.schema),
			cmp.Compare(strings.ToLower(a.name), strings.ToLower(b.name)), cmp.Compare(a.name, b.name))
	})

	set := output.Set{Columns: []output.Column{
		{Name: "schema", Type: "sysname", Nullable: true}, {Name: "name", Type: "sysname"},
		{Name: "type", Type: "nvarchar(60)"}, {Name: "is_system", Type: "bit"},
	}}
	for _, r := range rows {
		set.Rows = append(set.Rows, []*string{nonEmpty(r.schema), ptr(r.name), ptr(r.kind), bitCell(r.system)})
	}
	set.TotalRowCount = len(set.Rows)
	res.Sets = []output.Set{set}
	return res, nil
}

// orderedKinds is kinds once each, in objectKinds order: --type table,table
// lists the tables once.
func orderedKinds(kinds []string) []string {
	var out []string
	for _, k := range objectKinds {
		if slices.Contains(kinds, k) {
			out = append(out, k)
		}
	}
	return out
}

// listKind adds every object of kind in dbase that f keeps; system adds the
// ones SQL Server ships.
func listKind(ctx context.Context, dbase *gosmo.Database, kind string, f listFilter, system bool,
	add func(schema, name, kind string, system bool)) error {
	of := f.gosmo()
	switch kind {
	case kindTable:
		ts, err := dbase.Tables(ctx, gosmo.TableQuery{Filter: of})
		if err != nil {
			return err
		}
		if system {
			st, err := dbase.Tables(ctx, gosmo.TableQuery{Kind: gosmo.TableKindSystem, Filter: of})
			if err != nil {
				return err
			}
			ts = append(ts, st...)
		}
		for _, t := range ts {
			add(t.Schema, t.Name, kind, t.IsSystem)
		}
	case kindView:
		return listModules(ctx, of, system, dbase.Views, dbase.SystemViews,
			func(v *gosmo.View, sys bool) { add(v.Schema, v.Name, kind, sys) })
	case kindProcedure:
		return listModules(ctx, of, system, dbase.StoredProcedures, dbase.SystemStoredProcedures,
			func(p *gosmo.StoredProcedure, sys bool) { add(p.Schema, p.Name, kind, sys) })
	case kindFunction:
		return listModules(ctx, of, system, dbase.UserDefinedFunctions, dbase.SystemFunctions,
			func(fn *gosmo.UserDefinedFunction, sys bool) { add(fn.Schema, fn.Name, kind, sys) })
	case kindTrigger:
		trs, err := dbase.Triggers(ctx)
		if err != nil {
			return err
		}
		for _, t := range trs {
			if f.matches(t.Schema, t.Name) {
				add(t.Schema, t.Name, kind, false)
			}
		}
	case kindSynonym:
		ss, err := dbase.Synonyms(ctx)
		if err != nil {
			return err
		}
		for _, s := range ss {
			if f.matches(s.Schema, s.Name) {
				add(s.Schema, s.Name, kind, false)
			}
		}
	case kindSequence:
		ss, err := dbase.Sequences(ctx)
		if err != nil {
			return err
		}
		for _, s := range ss {
			if f.matches(s.Schema, s.Name) {
				add(s.Schema, s.Name, kind, false)
			}
		}
	case kindType:
		aliases, err := dbase.UserDefinedDataTypes(ctx)
		if err != nil {
			return err
		}
		tables, err := dbase.UserDefinedTableTypes(ctx)
		if err != nil {
			return err
		}
		clr, err := dbase.ClrTypes(ctx)
		if err != nil {
			return err
		}
		for _, t := range aliases {
			if f.matches(t.Schema, t.Name) {
				add(t.Schema, t.Name, kind, false)
			}
		}
		for _, t := range tables {
			if f.matches(t.Schema, t.Name) {
				add(t.Schema, t.Name, kind, false)
			}
		}
		// sys.assembly_types holds geography, geometry and hierarchyid in the
		// sys schema: types SQL Server ships, though user-defined to the
		// catalog.
		for _, t := range clr {
			sys := t.Schema == "sys"
			if (!sys || system) && f.matches(t.Schema, t.Name) {
				add(t.Schema, t.Name, kind, sys)
			}
		}
	case kindSchema:
		// A schema is in no schema: --schema and --name both match its name.
		ss, err := dbase.Schemas(ctx)
		if err != nil {
			return err
		}
		for _, s := range ss {
			sys := s.IsSystem()
			if (sys && !system) || !f.matches(s.Name, s.Name) {
				continue
			}
			add("", s.Name, kind, sys)
		}
	}
	return nil
}

// listModules adds the user objects one listing returns and, with system, the
// sys schema's from the other.
func listModules[T any](ctx context.Context, f gosmo.ObjectFilter, system bool,
	user, sys func(context.Context, gosmo.ObjectFilter) ([]T, error), add func(T, bool)) error {
	items, err := user(ctx, f)
	if err != nil {
		return err
	}
	for _, it := range items {
		add(it, false)
	}
	if !system {
		return nil
	}
	if items, err = sys(ctx, f); err != nil {
		return err
	}
	for _, it := range items {
		add(it, true)
	}
	return nil
}

func (d Deps) objectSearch(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	text := inv.Args[0]
	if strings.TrimSpace(text) == "" {
		return nil, usageError("the search text is empty", "Give the text to find: gossms object search <text>.")
	}
	res := &output.Result{}
	sc, dbase, err := d.connectDatabase(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	cols := []output.Column{{Name: "schema", Type: "sysname"}, {Name: "name", Type: "sysname"}, {Name: "type", Type: "nvarchar(60)"}}
	set := output.Set{}
	switch inv.Values[optIn].(string) {
	case searchColumn:
		found, err := dbase.SearchColumns(ctx, text)
		if err != nil {
			return res, readError(err)
		}
		set.Columns = append(cols, output.Column{Name: "column", Type: "sysname"}, output.Column{Name: "data_type", Type: "nvarchar(256)"})
		for _, c := range found {
			set.Rows = append(set.Rows, []*string{ptr(c.Schema), ptr(c.Object), ptr(kindOfDesc(c.TypeDesc)), ptr(c.Column), ptr(c.DataType)})
		}
		hiddenTypes(res, countHidden(found, func(c *gosmo.ColumnSearchResult) bool { return c.IsTypeHidden }))
	default:
		search := dbase.Search
		if inv.Values[optIn].(string) == searchDefinition {
			search = dbase.SearchDefinitions
		}
		found, err := search(ctx, text)
		if err != nil {
			return res, readError(err)
		}
		set.Columns = cols
		for _, r := range found {
			set.Rows = append(set.Rows, []*string{ptr(r.Schema), ptr(r.Name), ptr(kindOfDesc(r.TypeDesc))})
		}
	}
	set.TotalRowCount = len(set.Rows)
	res.Sets = []output.Set{set}
	return res, nil
}

// lookupObject is the start of every command naming one object: connect to
// the connection's database and resolve inv's object argument there (D2).
// The caller closes the connection through the returned func.
func (d Deps) lookupObject(ctx context.Context, inv *spec.Invocation, res *output.Result) (*gosmo.Database, *gosmo.ObjectInfo, func(), error) {
	sc, dbase, err := d.connectDatabase(ctx, inv, res)
	if err != nil {
		return nil, nil, nil, err
	}
	n, err := parseObjectName(inv.Args[0], dbase.Name)
	if err == nil {
		var o *gosmo.ObjectInfo
		if o, err = resolveObject(ctx, dbase, n); err == nil {
			return dbase, o, sc.Close, nil
		}
	}
	sc.Close()
	return nil, nil, nil, err
}

// objectFields is the record every object command starts with.
func objectFields(o *gosmo.ObjectInfo) []output.Field {
	return []output.Field{
		{Key: "schema", Value: o.Schema},
		{Key: "name", Value: o.Name},
		{Key: "type", Value: kindOfDesc(o.TypeDesc)},
	}
}

// moduleKind is which family's Definition object definition reads.
type moduleKind int

const (
	moduleView moduleKind = iota
	moduleProcedure
	moduleFunction
	moduleTrigger
)

// moduleKinds maps the sys.objects types object definition reads to the
// family whose Definition reads them; the CLR types have no T-SQL to read.
var moduleKinds = map[string]moduleKind{
	"V": moduleView, "P": moduleProcedure, "FN": moduleFunction, "IF": moduleFunction, "TF": moduleFunction,
	"TR": moduleTrigger,
}

// definition is schema.name's T-SQL text, "" when it has none to read
// (encrypted). It reads through the family's Ref: Definition reads by name,
// so the handle needs nothing loaded.
func (k moduleKind) definition(ctx context.Context, d *gosmo.Database, schema, name string) (string, error) {
	switch k {
	case moduleView:
		return d.ViewRef(schema, name).Definition(ctx)
	case moduleProcedure:
		return d.StoredProcedureRef(schema, name).Definition(ctx)
	case moduleFunction:
		return d.UserDefinedFunctionRef(schema, name).Definition(ctx)
	case moduleTrigger:
		return d.TriggerRef(schema, name).Definition(ctx)
	}
	return "", fmt.Errorf("module kind %d has no definition read", k)
}

// clrModules is the CLR procedure, function and trigger types.
var clrModules = []string{"PC", "FS", "FT", "TA"}

func (d Deps) objectDefinition(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	dbase, o, done, err := d.lookupObject(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer done()

	name := gosmo.QualifiedName(o.Schema, o.Name)
	definition := output.Field{Key: "definition", Verbatim: true, Null: true}
	kind, ok := moduleKinds[o.Type]
	switch {
	case slices.Contains(clrModules, o.Type):
		res.Warnings = append(res.Warnings, name+" is a CLR module: its code is in an assembly, not T-SQL.")
	case !ok:
		return res, usageError(fmt.Sprintf("%s is a %s, which has no T-SQL definition", name, kindOfDesc(o.TypeDesc)),
			"Run `gossms object describe` for its structure; a definition is a view's, procedure's, function's or trigger's.")
	default:
		text, err := kind.definition(ctx, dbase, o.Schema, o.Name)
		if err != nil {
			return res, readError(err)
		}
		if text == "" {
			res.Warnings = append(res.Warnings, "The server shows no definition for "+name+
				": it is encrypted (WITH ENCRYPTION), or the login lacks VIEW DEFINITION on it.")
		} else {
			definition.Value, definition.Null = text, false
		}
	}
	res.Fields = append(objectFields(o), definition)
	return res, nil
}
