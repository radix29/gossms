package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
)

// objectFake is fakeCatalog plus the object reads the data, script and
// import commands make through cmd.Catalog, over a small database:
// Sales.Customer (a table), a schema-bound view, procedures (one encrypted,
// one CLR), a scalar and an inline function, a trigger, a synonym, a
// sequence, three types, and a few shipped objects.
//
// The object group no longer reads through cmd.Catalog: its tests serve
// gosmo canned rows instead (objectServer, object_test.go).
type objectFake struct {
	*fakeCatalog
	objects []*gosmo.ObjectInfo
}

func newObjectFake() *objectFake {
	at := time.Date(2026, 3, 14, 9, 12, 44, 0, time.UTC)
	obj := func(id int, schema, name, typ, desc string) *gosmo.ObjectInfo {
		return &gosmo.ObjectInfo{ObjectID: id, Schema: schema, Name: name, Type: typ, TypeDesc: desc, Created: at, Modified: at}
	}
	trigger := obj(9, "Sales", "trCustomer", "TR", "SQL_TRIGGER")
	trigger.ParentSchema, trigger.ParentName = "Sales", "Customer"
	return &objectFake{fakeCatalog: newFakeCatalog(), objects: []*gosmo.ObjectInfo{
		obj(1, "Sales", "Customer", "U", "USER_TABLE"),
		obj(2, "dbo", "Customer", "U", "USER_TABLE"),
		obj(3, "Sales", "vCustomer", "V", "VIEW"),
		obj(4, "Sales", "usp_Get", "P", "SQL_STORED_PROCEDURE"),
		obj(5, "Sales", "p_secret", "P", "SQL_STORED_PROCEDURE"),
		obj(6, "Sales", "p_clr", "PC", "CLR_STORED_PROCEDURE"),
		obj(7, "dbo", "fScalar", "FN", "SQL_SCALAR_FUNCTION"),
		obj(8, "dbo", "fInline", "IF", "SQL_INLINE_TABLE_VALUED_FUNCTION"),
		trigger,
		obj(10, "dbo", "sn", "SN", "SYNONYM"),
		obj(11, "dbo", "seq", "SO", "SEQUENCE_OBJECT"),
		obj(12, "Sales", "PK_Customer", "PK", "PRIMARY_KEY_CONSTRAINT"),
	}}
}

func (c *objectFake) ObjectByName(_ context.Context, _ *gosmo.Database, schema, name string) (*gosmo.ObjectInfo, error) {
	for _, o := range c.objects {
		if o.Schema == schema && o.Name == name {
			return o, nil
		}
	}
	return nil, fmt.Errorf("gosmo: object: %w", gosmo.ErrNotFound)
}

func (c *objectFake) ObjectsNamed(_ context.Context, _ *gosmo.Database, name string) ([]*gosmo.ObjectInfo, error) {
	var out []*gosmo.ObjectInfo
	for _, o := range c.objects {
		if o.Name == name {
			out = append(out, o)
		}
	}
	return out, nil
}

func (c *objectFake) Search(_ context.Context, _ *gosmo.Database, pattern string) ([]*gosmo.SearchResult, error) {
	var out []*gosmo.SearchResult
	for _, o := range c.objects {
		if strings.Contains(strings.ToLower(o.Name), strings.ToLower(pattern)) && o.Type != "PK" {
			out = append(out, &gosmo.SearchResult{Schema: o.Schema, Name: o.Name, TypeDesc: o.TypeDesc})
		}
	}
	return out, nil
}

func (c *objectFake) UserDefinedDataTypes(context.Context, *gosmo.Database) ([]*gosmo.UserDefinedDataType, error) {
	return []*gosmo.UserDefinedDataType{{Schema: "dbo", Name: "Phone"}}, nil
}

func (c *objectFake) UserDefinedTableTypes(context.Context, *gosmo.Database) ([]*gosmo.UserDefinedTableType, error) {
	return []*gosmo.UserDefinedTableType{{Schema: "Sales", Name: "OrderLines"}}, nil
}

func (c *objectFake) ClrTypes(context.Context, *gosmo.Database) ([]*gosmo.ClrType, error) {
	return []*gosmo.ClrType{{Schema: "dbo", Name: "Point"}, {Schema: "sys", Name: "geography"}}, nil
}

func (c *objectFake) Schemas(context.Context, *gosmo.Database) ([]*gosmo.Schema, error) {
	return []*gosmo.Schema{{ID: 1, Name: "dbo"}, {ID: 2, Name: "guest"}, {ID: 4, Name: "sys"},
		{ID: 5, Name: "Sales"}, {ID: 16384, Name: "db_owner"}}, nil
}

func (c *objectFake) TableByName(_ context.Context, _ *gosmo.Database, schema, name string) (*gosmo.Table, error) {
	return &gosmo.Table{ObjectID: 1, Schema: schema, Name: name}, nil
}

func (c *objectFake) ViewByName(_ context.Context, _ *gosmo.Database, schema, name string) (*gosmo.View, error) {
	return &gosmo.View{ObjectID: 3, Schema: schema, Name: name, IsSchemaBound: true}, nil
}

func (c *objectFake) TableColumns(context.Context, *gosmo.Table) ([]*gosmo.Column, error) {
	return []*gosmo.Column{
		{Name: "id", OrdinalPosition: 1, DataType: "int", IsIdentity: true, IdentitySeed: "1", IdentityIncrement: "1",
			IsPrimaryKey: true, TypeSchema: "sys"},
		{Name: "CustomerName", OrdinalPosition: 2, DataType: "nvarchar", MaxLength: 100, IsNullable: true,
			Collation: "Latin1_General_CI_AS", DefaultValue: &gosmo.ColumnDefault{Name: "DF_n", Definition: "(N'x')"}, TypeSchema: "sys"},
		{Name: "pct", OrdinalPosition: 3, DataType: "int", IsComputed: true, ComputedText: "([id]*(2))", IsPersisted: true,
			IsNullable: true, TypeSchema: "sys"},
	}, nil
}

var fakeIndexes = []*gosmo.Index{
	{Name: "PK_Customer", Type: gosmo.IndexTypeClustered, IsUnique: true, IsPrimaryKey: true,
		KeyColumns: []gosmo.IndexColumn{{Name: "id"}}},
	{Name: "IX_Customer_Name", Type: gosmo.IndexTypeNonClustered, KeyColumns: []gosmo.IndexColumn{{Name: "CustomerName", Descending: true}},
		IncludedColumns: []gosmo.IndexColumn{{Name: "pct", IsIncluded: true}}, FilterDefinition: "([pct]>(0))"},
	{Name: "UQ_Customer_Name", Type: gosmo.IndexTypeNonClustered, IsUnique: true, IsUniqueConstraint: true,
		KeyColumns: []gosmo.IndexColumn{{Name: "CustomerName"}}},
}

func (c *objectFake) TableIndexes(context.Context, *gosmo.Table) ([]*gosmo.Index, error) {
	return fakeIndexes, nil
}

func (c *objectFake) RowCount(context.Context, *gosmo.Table) (int64, error) { return 1234, nil }

// ViewColumns is the table's first two, the identity without its seed, as
// sys.identity_columns has no row for a view's column.
func (c *objectFake) ViewColumns(ctx context.Context, _ *gosmo.View) ([]*gosmo.Column, error) {
	cols, _ := c.TableColumns(ctx, nil)
	id := *cols[0]
	id.IdentitySeed, id.IdentityIncrement, id.IsPrimaryKey = "", "", false
	return []*gosmo.Column{&id, cols[1]}, nil
}

func (c *objectFake) ViewIndexes(context.Context, *gosmo.View) ([]*gosmo.Index, error) {
	return fakeIndexes[:1], nil
}
