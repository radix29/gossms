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
			Options: append(connectOptions(),
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
			Options: append(connectOptions(),
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
			Options: connectOptions(),
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
			Options: connectOptions(),
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

// isSystemSchema is a schema SQL Server creates in every database: guest,
// INFORMATION_SCHEMA, sys and the fixed roles' (schema_id 16384–16393). dbo
// holds user objects and is not one.
func isSystemSchema(s *gosmo.Schema) bool {
	return (s.ID >= 2 && s.ID <= 4) || (s.ID >= 16384 && s.ID < 16400)
}

func (d Deps) objectList(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, dbase, err := d.connectDatabase(ctx, inv, res)
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
		if err := listKind(ctx, cat, dbase, kind, f, system, add); err != nil {
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
func listKind(ctx context.Context, cat Catalog, dbase *gosmo.Database, kind string, f listFilter, system bool,
	add func(schema, name, kind string, system bool)) error {
	of := f.gosmo()
	switch kind {
	case kindTable:
		ts, err := cat.Tables(ctx, dbase, gosmo.TableQuery{Filter: of})
		if err != nil {
			return err
		}
		if system {
			st, err := cat.Tables(ctx, dbase, gosmo.TableQuery{Kind: gosmo.TableKindSystem, Filter: of})
			if err != nil {
				return err
			}
			ts = append(ts, st...)
		}
		for _, t := range ts {
			add(t.Schema, t.Name, kind, t.IsSystem)
		}
	case kindView:
		return listModules(ctx, dbase, of, system, cat.Views, cat.SystemViews,
			func(v *gosmo.View, sys bool) { add(v.Schema, v.Name, kind, sys) })
	case kindProcedure:
		return listModules(ctx, dbase, of, system, cat.StoredProcedures, cat.SystemStoredProcedures,
			func(p *gosmo.StoredProcedure, sys bool) { add(p.Schema, p.Name, kind, sys) })
	case kindFunction:
		return listModules(ctx, dbase, of, system, cat.UserDefinedFunctions, cat.SystemFunctions,
			func(fn *gosmo.UserDefinedFunction, sys bool) { add(fn.Schema, fn.Name, kind, sys) })
	case kindTrigger:
		trs, err := cat.Triggers(ctx, dbase)
		if err != nil {
			return err
		}
		for _, t := range trs {
			if f.matches(t.Schema, t.Name) {
				add(t.Schema, t.Name, kind, false)
			}
		}
	case kindSynonym:
		ss, err := cat.Synonyms(ctx, dbase)
		if err != nil {
			return err
		}
		for _, s := range ss {
			if f.matches(s.Schema, s.Name) {
				add(s.Schema, s.Name, kind, false)
			}
		}
	case kindSequence:
		ss, err := cat.Sequences(ctx, dbase)
		if err != nil {
			return err
		}
		for _, s := range ss {
			if f.matches(s.Schema, s.Name) {
				add(s.Schema, s.Name, kind, false)
			}
		}
	case kindType:
		aliases, err := cat.UserDefinedDataTypes(ctx, dbase)
		if err != nil {
			return err
		}
		tables, err := cat.UserDefinedTableTypes(ctx, dbase)
		if err != nil {
			return err
		}
		clr, err := cat.ClrTypes(ctx, dbase)
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
		ss, err := cat.Schemas(ctx, dbase)
		if err != nil {
			return err
		}
		for _, s := range ss {
			sys := isSystemSchema(s)
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
func listModules[T any](ctx context.Context, dbase *gosmo.Database, f gosmo.ObjectFilter, system bool,
	user, sys func(context.Context, *gosmo.Database, gosmo.ObjectFilter) ([]T, error), add func(T, bool)) error {
	items, err := user(ctx, dbase, f)
	if err != nil {
		return err
	}
	for _, it := range items {
		add(it, false)
	}
	if !system {
		return nil
	}
	if items, err = sys(ctx, dbase, f); err != nil {
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
	sc, cat, dbase, err := d.connectDatabase(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	cols := []output.Column{{Name: "schema", Type: "sysname"}, {Name: "name", Type: "sysname"}, {Name: "type", Type: "nvarchar(60)"}}
	set := output.Set{}
	switch inv.Values[optIn].(string) {
	case searchColumn:
		found, err := cat.SearchColumns(ctx, dbase, text)
		if err != nil {
			return res, readError(err)
		}
		set.Columns = append(cols, output.Column{Name: "column", Type: "sysname"}, output.Column{Name: "data_type", Type: "nvarchar(256)"})
		for _, c := range found {
			set.Rows = append(set.Rows, []*string{ptr(c.Schema), ptr(c.Object), ptr(kindOfDesc(c.TypeDesc)), ptr(c.Column), ptr(c.DataType)})
		}
	default:
		search := cat.Search
		if inv.Values[optIn].(string) == searchDefinition {
			search = cat.SearchDefinitions
		}
		found, err := search(ctx, dbase, text)
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
func (d Deps) lookupObject(ctx context.Context, inv *spec.Invocation, res *output.Result) (Catalog, *gosmo.Database, *gosmo.ObjectInfo, func(), error) {
	sc, cat, dbase, err := d.connectDatabase(ctx, inv, res)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	n, err := parseObjectName(inv.Args[0], dbase.Name)
	if err == nil {
		var o *gosmo.ObjectInfo
		if o, err = resolveObject(ctx, cat, dbase, n); err == nil {
			return cat, dbase, o, sc.Close, nil
		}
	}
	sc.Close()
	return nil, nil, nil, nil, err
}

// objectFields is the record every object command starts with.
func objectFields(o *gosmo.ObjectInfo) []output.Field {
	return []output.Field{
		{Key: "schema", Value: o.Schema},
		{Key: "name", Value: o.Name},
		{Key: "type", Value: kindOfDesc(o.TypeDesc)},
	}
}

func (d Deps) objectDescribe(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	cat, dbase, o, done, err := d.lookupObject(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer done()

	parent := ""
	if o.ParentName != "" {
		parent = gosmo.QualifiedName(o.ParentSchema, o.ParentName)
	}
	res.Fields = append(objectFields(o),
		output.Field{Key: "typeDesc", Value: o.TypeDesc},
		output.Field{Key: "objectId", Value: strconv.Itoa(o.ObjectID)},
		output.Field{Key: "created", Value: gridTime(o.Created)},
		output.Field{Key: "modified", Value: gridTime(o.Modified)},
		output.Field{Key: "parent", Value: parent},
	)
	var describe func(context.Context, Catalog, *gosmo.Database, *gosmo.ObjectInfo, *output.Result) error
	switch kindOfDesc(o.TypeDesc) {
	case kindTable:
		describe = describeTable
	case kindView:
		describe = describeView
	case kindProcedure, kindFunction:
		describe = describeModule
	case kindTrigger:
		describe = describeTrigger
	case kindSynonym:
		describe = describeSynonym
	case kindSequence:
		describe = describeSequence
	}
	if describe != nil {
		if err := describe(ctx, cat, dbase, o, res); err != nil {
			return res, readError(err)
		}
	}
	return res, nil
}

func describeTable(ctx context.Context, cat Catalog, dbase *gosmo.Database, o *gosmo.ObjectInfo, res *output.Result) error {
	t, err := cat.TableByName(ctx, dbase, o.Schema, o.Name)
	if err != nil {
		return err
	}
	rows, err := cat.RowCount(ctx, t)
	if err != nil {
		return err
	}
	res.Fields = append(res.Fields, output.Field{Key: "rowCount", Value: strconv.FormatInt(rows, 10)})
	cols, err := cat.TableColumns(ctx, t)
	if err != nil {
		return err
	}
	idx, err := cat.TableIndexes(ctx, t)
	if err != nil {
		return err
	}
	fks, err := cat.ForeignKeys(ctx, t)
	if err != nil {
		return err
	}
	checks, err := cat.CheckConstraints(ctx, t)
	if err != nil {
		return err
	}
	trs, err := cat.TableTriggers(ctx, t)
	if err != nil {
		return err
	}
	res.Sets = []output.Set{columnsSet(cols), keysSet(idx, fks), indexesSet(idx), checksSet(checks), triggersSet(trs)}
	return nil
}

func describeView(ctx context.Context, cat Catalog, dbase *gosmo.Database, o *gosmo.ObjectInfo, res *output.Result) error {
	v, err := cat.ViewByName(ctx, dbase, o.Schema, o.Name)
	if err != nil {
		return err
	}
	res.Fields = append(res.Fields, output.Field{Key: "isSchemaBound", Value: boolText(v.IsSchemaBound)})
	cols, err := cat.ViewColumns(ctx, v)
	if err != nil {
		return err
	}
	idx, err := cat.ViewIndexes(ctx, v)
	if err != nil {
		return err
	}
	trs, err := cat.ViewTriggers(ctx, v)
	if err != nil {
		return err
	}
	res.Sets = []output.Set{columnsSet(cols), indexesSet(idx), triggersSet(trs)}
	return nil
}

// tableValued is the function types that return a table, not a scalar.
var tableValued = []string{"IF", "TF", "FT"}

func describeModule(ctx context.Context, cat Catalog, dbase *gosmo.Database, o *gosmo.ObjectInfo, res *output.Result) error {
	if kindOfDesc(o.TypeDesc) == kindFunction {
		returns := output.Field{Key: "returns", Value: "TABLE"}
		if !slices.Contains(tableValued, o.Type) {
			rt, err := cat.ReturnType(ctx, dbase, o.Schema, o.Name)
			if err != nil {
				return err
			}
			if rt == nil {
				returns = output.Field{Key: "returns", Null: true}
			} else {
				returns.Value = rt.TypeString()
			}
		}
		res.Fields = append(res.Fields, returns)
	}
	ps, err := cat.Parameters(ctx, dbase, o.Schema, o.Name)
	if err != nil {
		return err
	}
	set := output.Set{Name: "parameters", Columns: []output.Column{
		{Name: "ordinal", Type: "int"}, {Name: "name", Type: "sysname"}, {Name: "data_type", Type: "nvarchar(256)"},
		{Name: "is_output", Type: "bit"}, {Name: "has_default", Type: "bit", Nullable: true},
	}}
	// sys.parameters records a default only for a CLR module's parameter; a
	// T-SQL one reads 0 whether it has one or not, so it is NULL — unknown.
	clr := slices.Contains(clrModules, o.Type)
	for _, p := range ps {
		var hasDefault *string
		if clr {
			hasDefault = bitCell(p.HasDefault)
		}
		set.Rows = append(set.Rows, []*string{ptr(strconv.Itoa(p.Ordinal)), ptr(p.Name), ptr(p.TypeString()),
			bitCell(p.IsOutput), hasDefault})
	}
	set.TotalRowCount = len(set.Rows)
	res.Sets = []output.Set{set}
	return nil
}

func describeTrigger(ctx context.Context, cat Catalog, dbase *gosmo.Database, o *gosmo.ObjectInfo, res *output.Result) error {
	t, err := cat.TriggerByName(ctx, dbase, o.Schema, o.Name)
	if err != nil {
		return err
	}
	res.Fields = append(res.Fields,
		output.Field{Key: "isEnabled", Value: boolText(t.IsEnabled)},
		output.Field{Key: "events", Value: strings.Join(t.Events, ", ")})
	return nil
}

func describeSynonym(ctx context.Context, cat Catalog, dbase *gosmo.Database, o *gosmo.ObjectInfo, res *output.Result) error {
	s, err := cat.SynonymByName(ctx, dbase, o.Schema, o.Name)
	if err != nil {
		return err
	}
	res.Fields = append(res.Fields, output.Field{Key: "baseObject", Value: s.BaseObject})
	return nil
}

func describeSequence(ctx context.Context, cat Catalog, dbase *gosmo.Database, o *gosmo.ObjectInfo, res *output.Result) error {
	s, err := cat.SequenceByName(ctx, dbase, o.Schema, o.Name)
	if err != nil {
		return err
	}
	dataType := string(s.DataType)
	if s.DataTypeSchema != "" && s.DataTypeSchema != "sys" {
		dataType = gosmo.QualifiedName(s.DataTypeSchema, dataType)
	} else if s.DataType == "decimal" || s.DataType == "numeric" {
		dataType = fmt.Sprintf("%s(%d,%d)", s.DataType, s.Precision, s.Scale)
	}
	cache := "NO CACHE"
	if s.IsCached {
		cache = "CACHE"
		if s.CacheSize > 0 {
			cache += " " + strconv.Itoa(s.CacheSize)
		}
	}
	res.Fields = append(res.Fields,
		output.Field{Key: "dataType", Value: dataType},
		output.Field{Key: "startValue", Value: s.StartValue},
		output.Field{Key: "increment", Value: s.Increment},
		output.Field{Key: "minValue", Value: s.MinValue},
		output.Field{Key: "maxValue", Value: s.MaxValue},
		output.Field{Key: "isCycling", Value: boolText(s.IsCycling)},
		output.Field{Key: "cache", Value: cache},
		output.Field{Key: "currentValue", Value: s.CurrentValue},
	)
	return nil
}

// columnsSet is describe's columns set.
func columnsSet(cols []*gosmo.Column) output.Set {
	set := output.Set{Name: "columns", Columns: []output.Column{
		{Name: "column_id", Type: "int"}, {Name: "name", Type: "sysname"}, {Name: "data_type", Type: "nvarchar(256)"},
		{Name: "nullable", Type: "bit"}, {Name: "identity", Type: "nvarchar(100)", Nullable: true},
		{Name: "computed", Type: "nvarchar(max)", Nullable: true}, {Name: "default", Type: "nvarchar(max)", Nullable: true},
		{Name: "collation", Type: "sysname", Nullable: true}, {Name: "primary_key", Type: "bit"},
	}}
	for _, c := range cols {
		var identity, computed, def *string
		switch {
		case c.IsIdentity && c.IdentitySeed == "":
			// A view's column over an identity: the view has no seed.
			identity = ptr("IDENTITY")
		case c.IsIdentity:
			identity = ptr(fmt.Sprintf("IDENTITY(%s,%s)", c.IdentitySeed, c.IdentityIncrement))
		}
		if c.IsComputed {
			text := c.ComputedText
			if c.IsPersisted {
				text += " PERSISTED"
			}
			computed = &text
		}
		if c.DefaultValue != nil {
			def = ptr(c.DefaultValue.Definition)
		}
		set.Rows = append(set.Rows, []*string{ptr(strconv.Itoa(c.OrdinalPosition)), ptr(c.Name), ptr(c.TypeString()),
			bitCell(c.IsNullable), identity, computed, def, nonEmpty(c.Collation), bitCell(c.IsPrimaryKey)})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// indexColumns is an index's columns as one cell, a descending key marked.
func indexColumns(cols []gosmo.IndexColumn) string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
		if c.Descending {
			names[i] += " DESC"
		}
	}
	return strings.Join(names, ", ")
}

// keysSet is describe's keys set: the primary key and unique constraints
// (from the indexes behind them), then the foreign keys.
func keysSet(idx []*gosmo.Index, fks []*gosmo.ForeignKey) output.Set {
	set := output.Set{Name: "keys", Columns: []output.Column{
		{Name: "name", Type: "sysname"}, {Name: "type", Type: "nvarchar(60)"}, {Name: "columns", Type: "nvarchar(max)"},
		{Name: "references", Type: "nvarchar(max)", Nullable: true}, {Name: "on_delete", Type: "nvarchar(60)", Nullable: true},
		{Name: "on_update", Type: "nvarchar(60)", Nullable: true}, {Name: "disabled", Type: "bit"},
		{Name: "not_trusted", Type: "bit"},
	}}
	for _, i := range idx {
		kind := "UNIQUE"
		switch {
		case i.IsPrimaryKey:
			kind = "PRIMARY KEY"
		case !i.IsUniqueConstraint:
			continue
		}
		set.Rows = append(set.Rows, []*string{ptr(i.Name), ptr(kind), ptr(indexColumns(i.KeyColumns)),
			nil, nil, nil, bitCell(i.IsDisabled), bitCell(false)})
	}
	for _, fk := range fks {
		refs := fmt.Sprintf("%s (%s)", gosmo.QualifiedName(fk.ReferencedSchema, fk.ReferencedTable), strings.Join(fk.ReferencedColumns, ", "))
		set.Rows = append(set.Rows, []*string{ptr(fk.Name), ptr("FOREIGN KEY"), ptr(strings.Join(fk.Columns, ", ")),
			&refs, ptr(fk.DeleteAction), ptr(fk.UpdateAction), bitCell(fk.IsDisabled), bitCell(fk.IsNotTrusted)})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// indexesSet is describe's indexes set.
func indexesSet(idx []*gosmo.Index) output.Set {
	set := output.Set{Name: "indexes", Columns: []output.Column{
		{Name: "name", Type: "sysname"}, {Name: "type", Type: "nvarchar(60)"}, {Name: "unique", Type: "bit"},
		{Name: "primary_key", Type: "bit"}, {Name: "key_columns", Type: "nvarchar(max)"},
		{Name: "included_columns", Type: "nvarchar(max)", Nullable: true}, {Name: "filter", Type: "nvarchar(max)", Nullable: true},
		{Name: "disabled", Type: "bit"},
	}}
	for _, i := range idx {
		set.Rows = append(set.Rows, []*string{ptr(i.Name), ptr(string(i.Type)), bitCell(i.IsUnique), bitCell(i.IsPrimaryKey),
			ptr(indexColumns(i.KeyColumns)), nonEmpty(indexColumns(i.IncludedColumns)), nonEmpty(i.FilterDefinition),
			bitCell(i.IsDisabled)})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// checksSet is describe's check_constraints set.
func checksSet(checks []*gosmo.CheckConstraint) output.Set {
	set := output.Set{Name: "check_constraints", Columns: []output.Column{
		{Name: "name", Type: "sysname"}, {Name: "column", Type: "sysname", Nullable: true},
		{Name: "definition", Type: "nvarchar(max)"}, {Name: "disabled", Type: "bit"}, {Name: "not_trusted", Type: "bit"},
	}}
	for _, c := range checks {
		set.Rows = append(set.Rows, []*string{ptr(c.Name), nonEmpty(c.Column), ptr(c.Definition),
			bitCell(c.IsDisabled), bitCell(c.IsNotTrusted)})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// triggersSet is describe's triggers set.
func triggersSet(trs []*gosmo.Trigger) output.Set {
	set := output.Set{Name: "triggers", Columns: []output.Column{
		{Name: "name", Type: "sysname"}, {Name: "enabled", Type: "bit"}, {Name: "events", Type: "nvarchar(100)"},
	}}
	for _, t := range trs {
		set.Rows = append(set.Rows, []*string{ptr(t.Name), bitCell(t.IsEnabled), ptr(strings.Join(t.Events, ", "))})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// moduleKinds maps the sys.objects types object definition reads to the
// family whose Definition reads them; the CLR types have no T-SQL to read.
var moduleKinds = map[string]ModuleKind{
	"V": ModuleView, "P": ModuleProcedure, "FN": ModuleFunction, "IF": ModuleFunction, "TF": ModuleFunction,
	"TR": ModuleTrigger,
}

// clrModules is the CLR procedure, function and trigger types.
var clrModules = []string{"PC", "FS", "FT", "TA"}

func (d Deps) objectDefinition(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	cat, dbase, o, done, err := d.lookupObject(ctx, inv, res)
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
		text, err := cat.ModuleDefinition(ctx, dbase, kind, o.Schema, o.Name)
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
