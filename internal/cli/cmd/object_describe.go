package cmd

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

func (d Deps) objectDescribe(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	dbase, o, done, err := d.lookupObject(ctx, inv, res)
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
	var describe func(context.Context, *gosmo.Database, *gosmo.ObjectInfo, *output.Result) error
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
		if err := describe(ctx, dbase, o, res); err != nil {
			return res, readError(err)
		}
	}
	return res, nil
}

func describeTable(ctx context.Context, dbase *gosmo.Database, o *gosmo.ObjectInfo, res *output.Result) error {
	t, err := dbase.TableByName(ctx, o.Schema, o.Name)
	if err != nil {
		return err
	}
	rows, err := t.RowCount(ctx)
	if err != nil {
		return err
	}
	res.Fields = append(res.Fields, output.Field{Key: "rowCount", Value: strconv.FormatInt(rows, 10)})
	cols, err := t.Columns(ctx)
	if err != nil {
		return err
	}
	idx, err := t.Indexes(ctx)
	if err != nil {
		return err
	}
	fks, err := t.ForeignKeys(ctx)
	if err != nil {
		return err
	}
	checks, err := t.CheckConstraints(ctx)
	if err != nil {
		return err
	}
	trs, err := t.Triggers(ctx)
	if err != nil {
		return err
	}
	colSet, hiddenCols := columnsSet(cols)
	checkSet, hiddenChecks := checksSet(checks)
	res.Sets = []output.Set{colSet, keysSet(idx, fks), indexesSet(idx), checkSet, triggersSet(trs)}
	hiddenDefinitions(res, hiddenCols+hiddenChecks)
	hiddenTypes(res, countHidden(cols, func(c *gosmo.Column) bool { return c.IsTypeHidden }))
	hiddenFilters(res, "index(es)", hiddenIndexFilters(idx))
	return nil
}

func describeView(ctx context.Context, dbase *gosmo.Database, o *gosmo.ObjectInfo, res *output.Result) error {
	v, err := dbase.ViewByName(ctx, o.Schema, o.Name)
	if err != nil {
		return err
	}
	res.Fields = append(res.Fields, output.Field{Key: "isSchemaBound", Value: boolText(v.IsSchemaBound)})
	cols, err := v.Columns(ctx)
	if err != nil {
		return err
	}
	idx, err := v.Indexes(ctx)
	if err != nil {
		return err
	}
	trs, err := v.Triggers(ctx)
	if err != nil {
		return err
	}
	colSet, hidden := columnsSet(cols)
	res.Sets = []output.Set{colSet, indexesSet(idx), triggersSet(trs)}
	hiddenDefinitions(res, hidden)
	hiddenTypes(res, countHidden(cols, func(c *gosmo.Column) bool { return c.IsTypeHidden }))
	hiddenFilters(res, "index(es)", hiddenIndexFilters(idx))
	return nil
}

// hiddenDefinitions warns that n computed-column, default or check
// expressions read NULL. The catalog views give a login holding SELECT but
// not VIEW DEFINITION the object and its columns, yet NULL for every
// definition — gosmo reads that as "", which would print as an empty
// default or a computed column of nothing but " PERSISTED".
func hiddenDefinitions(res *output.Result, n int) {
	if n > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%d computed, default or check definitions read NULL: "+
			"the login lacks VIEW DEFINITION on the object.", n))
	}
}

// hiddenTypes warns that n data types read as their base system type (or
// empty, a CLR type's): user-defined types the login holds no permission on
// are invisible to it, and gosmo names the storage type instead of dropping
// the column or parameter (gosmo Column.IsTypeHidden).
func hiddenTypes(res *output.Result, n int) {
	if n > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("The login cannot see %d user-defined data type(s): "+
			"each reads as its base system type (empty for a CLR type). It needs REFERENCES or VIEW DEFINITION on the type.", n))
	}
}

// hiddenFilters warns, naming them, of the filtered indexes (or statistics,
// as kind says) whose predicate the login cannot read: without VIEW
// DEFINITION filter_definition is NULL while has_filter is 1 (gosmo
// Index.IsFilterHidden). The filter cell is NULL either way, so without
// the warning the index reads as unfiltered.
func hiddenFilters(res *output.Result, kind string, names []string) {
	if len(names) > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("The login cannot read the filter of %d filtered %s (%s): "+
			"filter reads NULL, as an unfiltered one's does. It needs VIEW DEFINITION on the object.",
			len(names), kind, strings.Join(names, ", ")))
	}
}

// hiddenIndexFilters is the names of the indexes whose filter is hidden.
func hiddenIndexFilters(idx []*gosmo.Index) []string {
	var names []string
	for _, i := range idx {
		if i.IsFilterHidden {
			names = append(names, i.Name)
		}
	}
	return names
}

// countHidden counts the items hidden reports.
func countHidden[T any](items []T, hidden func(T) bool) int {
	n := 0
	for _, it := range items {
		if hidden(it) {
			n++
		}
	}
	return n
}

// tableValued is the function types that return a table, not a scalar.
var tableValued = []string{"IF", "TF", "FT"}

func describeModule(ctx context.Context, dbase *gosmo.Database, o *gosmo.ObjectInfo, res *output.Result) error {
	hidden := 0 // types the login cannot see, the return type's included
	if kindOfDesc(o.TypeDesc) == kindFunction {
		returns := output.Field{Key: "returns", Value: "TABLE"}
		if !slices.Contains(tableValued, o.Type) {
			rt, err := dbase.ReturnType(ctx, o.Schema, o.Name)
			if err != nil {
				return err
			}
			if rt == nil {
				returns = output.Field{Key: "returns", Null: true}
			} else {
				returns.Value = rt.TypeString()
				if rt.IsTypeHidden {
					hidden++
				}
			}
		}
		res.Fields = append(res.Fields, returns)
	}
	ps, err := dbase.Parameters(ctx, o.Schema, o.Name)
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
		if p.IsTypeHidden {
			hidden++
		}
	}
	set.TotalRowCount = len(set.Rows)
	res.Sets = []output.Set{set}
	hiddenTypes(res, hidden)
	return nil
}

func describeTrigger(ctx context.Context, dbase *gosmo.Database, o *gosmo.ObjectInfo, res *output.Result) error {
	t, err := dbase.TriggerByName(ctx, o.Schema, o.Name)
	if err != nil {
		return err
	}
	res.Fields = append(res.Fields,
		output.Field{Key: "isEnabled", Value: boolText(t.IsEnabled)},
		output.Field{Key: "events", Value: strings.Join(t.Events, ", ")})
	return nil
}

func describeSynonym(ctx context.Context, dbase *gosmo.Database, o *gosmo.ObjectInfo, res *output.Result) error {
	s, err := dbase.SynonymByName(ctx, o.Schema, o.Name)
	if err != nil {
		return err
	}
	res.Fields = append(res.Fields, output.Field{Key: "baseObject", Value: s.BaseObject})
	return nil
}

func describeSequence(ctx context.Context, dbase *gosmo.Database, o *gosmo.ObjectInfo, res *output.Result) error {
	s, err := dbase.SequenceByName(ctx, o.Schema, o.Name)
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
	if s.IsTypeHidden {
		hiddenTypes(res, 1)
	}
	return nil
}

// columnsSet is describe's columns set, and how many of its computed and
// default expressions the server hid (hiddenDefinitions).
func columnsSet(cols []*gosmo.Column) (output.Set, int) {
	set := output.Set{Name: "columns", Columns: []output.Column{
		{Name: "column_id", Type: "int"}, {Name: "name", Type: "sysname"}, {Name: "data_type", Type: "nvarchar(256)"},
		{Name: "nullable", Type: "bit"}, {Name: "identity", Type: "nvarchar(100)", Nullable: true},
		{Name: "computed", Type: "nvarchar(max)", Nullable: true}, {Name: "default", Type: "nvarchar(max)", Nullable: true},
		{Name: "collation", Type: "sysname", Nullable: true}, {Name: "primary_key", Type: "bit"},
	}}
	hidden := 0
	for _, c := range cols {
		var identity, computed, def *string
		switch {
		case c.IsIdentity && c.IdentitySeed == "":
			// A view's column over an identity: the view has no seed.
			identity = ptr("IDENTITY")
		case c.IsIdentity:
			identity = ptr(fmt.Sprintf("IDENTITY(%s,%s)", c.IdentitySeed, c.IdentityIncrement))
		}
		switch {
		case c.IsComputed && c.ComputedText == "":
			hidden++
		case c.IsComputed:
			text := c.ComputedText
			if c.IsPersisted {
				text += " PERSISTED"
			}
			computed = &text
		}
		switch {
		case c.DefaultValue != nil && c.DefaultValue.Definition == "":
			hidden++
		case c.DefaultValue != nil:
			def = ptr(c.DefaultValue.Definition)
		}
		set.Rows = append(set.Rows, []*string{ptr(strconv.Itoa(c.OrdinalPosition)), ptr(c.Name), ptr(c.TypeString()),
			bitCell(c.IsNullable), identity, computed, def, nonEmpty(c.Collation), bitCell(c.IsPrimaryKey)})
	}
	set.TotalRowCount = len(set.Rows)
	return set, hidden
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

// checksSet is describe's check_constraints set, and how many definitions
// the server hid (hiddenDefinitions).
func checksSet(checks []*gosmo.CheckConstraint) (output.Set, int) {
	set := output.Set{Name: "check_constraints", Columns: []output.Column{
		{Name: "name", Type: "sysname"}, {Name: "column", Type: "sysname", Nullable: true},
		{Name: "definition", Type: "nvarchar(max)", Nullable: true}, {Name: "disabled", Type: "bit"}, {Name: "not_trusted", Type: "bit"},
	}}
	hidden := 0
	for _, c := range checks {
		if c.Definition == "" {
			hidden++
		}
		set.Rows = append(set.Rows, []*string{ptr(c.Name), nonEmpty(c.Column), nonEmpty(c.Definition),
			bitCell(c.IsDisabled), bitCell(c.IsNotTrusted)})
	}
	set.TotalRowCount = len(set.Rows)
	return set, hidden
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
