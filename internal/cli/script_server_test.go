package cli

import (
	"fmt"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/clitest"
)

// scriptServer is objectServer with the reads real Script As makes (gosmo's
// Scripter, through internal/scripting) for each family script object
// covers, and script db's, registered ahead of the object group's rules,
// some of which would otherwise answer a script read with another shape.
// Its database adds to objectServer's a procedure SQL Server ships, and
// PK_Customer's table.
type scriptServer struct {
	*objectServer

	// hiddenDefinition reads usp_Get's definition as a login without VIEW
	// DEFINITION on it does: NULL, though it is not encrypted.
	hiddenDefinition bool
	// boundErr fails a table DROP's read of what is schema-bound to it.
	boundErr error
}

func newScriptServer(t *testing.T) *scriptServer {
	s := &scriptServer{}
	s.objectServer = newObjectServerWith(t, func(o *objectServer) {
		s.objectServer = o
		s.rules()
	})
	s.objects = append(s.objects, &gosmo.ObjectInfo{ObjectID: 99, Schema: "dbo", Name: "sp_shipped", Type: "P",
		TypeDesc: "SQL_STORED_PROCEDURE", IsMSShipped: true, Created: objectsAt, Modified: objectsAt})
	pk := s.object("Sales", "PK_Customer")
	pk.ParentSchema, pk.ParentName = "Sales", "Customer"
	return s
}

// definition is the module row scriptModule reads — definition, ANSI_NULLS,
// QUOTED_IDENTIFIER, CLR, object_id — for the object the query names, none
// when it is not one of s's.
func (s *scriptServer) definition(q gosmotest.Query) [][]any {
	o := s.object(str(q.Arg(0)), str(q.Arg(1)))
	if o == nil {
		return nil
	}
	var def any
	switch {
	case o.Name == "p_secret", o.Name == "usp_Get" && s.hiddenDefinition:
		def = nil
	case o.Type == "PC":
		return [][]any{{nil, false, false, true, o.ObjectID}}
	default:
		def = fmt.Sprintf("CREATE %s %s.%s AS RETURN 1", map[string]string{"V": "VIEW", "P": "PROCEDURE", "TR": "TRIGGER"}[o.Type],
			o.Schema, o.Name)
		switch o.Type {
		case "V":
			def = "CREATE VIEW Sales.vCustomer WITH SCHEMABINDING\r\nAS\r\nSELECT id FROM Sales.Customer"
		case "IF":
			def = "CREATE FUNCTION dbo.fInline() RETURNS TABLE AS RETURN SELECT 1 AS one"
		case "TR":
			def = "CREATE TRIGGER Sales.trCustomer ON Sales.Customer AFTER INSERT AS SET NOCOUNT ON"
		}
	}
	return [][]any{{def, true, true, false, o.ObjectID}}
}

func (s *scriptServer) rules() {
	// A table's options: a plain rowstore table.
	s.on(func(gosmotest.Query) [][]any {
		return [][]any{{false, "", "", 0, "", "TABLE", "", "", nil, "Latin1_General_CI_AS",
			"", "", false, "",
			0, false, "", "", nil,
			"", "", false, nil,
			"", "", "", "", nil, nil,
			"", "", "", "",
			"", ""}}
	}, "SELECT CAST(CASE WHEN t.temporal_type = 2 THEN 1 ELSE 0 END AS BIT)")
	// Its data space, from its clustered index: PRIMARY, the default.
	s.on(func(gosmotest.Query) [][]any { return [][]any{{"PRIMARY", false, true, ""}} },
		"FROM   sys.indexes i", "i.index_id IN (0, 1)")
	// What a DROP names ahead of it: the schema-bound view, and a foreign
	// key on another table.
	s.OnFunc(func(q gosmotest.Query) bool {
		return has(q, "FROM   sys.sql_expression_dependencies sed", "is_schema_bound_reference = 1")
	},
		func(gosmotest.Query) gosmotest.Result {
			if s.boundErr != nil {
				return gosmotest.Result{Err: s.boundErr}
			}
			return gosmotest.Result{Sets: []gosmotest.Set{{Rows: [][]any{{"Sales", "vCustomer", "VIEW", false}}}}}
		})
	s.on(func(gosmotest.Query) [][]any { return [][]any{{"Sales", "Order", "FK_Order_Customer"}} },
		"FROM   sys.foreign_keys fk", "fk.referenced_object_id = @p1")
	// A table's or view's columns by name, for a DML template.
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, c := range s.columns(q.Arg(0) == "[Sales].[vCustomer]") {
			rows = append(rows, clitest.ColumnRow(c))
		}
		return rows
	}, "WHERE  c.object_id = OBJECT_ID(@p1)")
	// Each module's definition.
	s.on(s.definition, "JOIN sys.sql_modules m ON m.object_id =")
	s.on(s.definition, "JOIN   sys.sql_modules m ON m.object_id =")
	s.on(func(q gosmotest.Query) [][]any {
		if q.Arg(0) == int64(5) { // p_secret
			return [][]any{{int64(1)}}
		}
		return [][]any{{int64(0)}}
	}, "SELECT OBJECTPROPERTY(@p1, 'IsEncrypted')")
	// The trigger is INSERT's first, and disabled.
	s.on(func(gosmotest.Query) [][]any { return [][]any{{"Customer", true, "INSERT", true, false}} },
		"SELECT OBJECT_NAME(tr.parent_id), tr.is_disabled")
	// Each object's extended properties: the table's description, read
	// back as the script's sp_addextendedproperty.
	s.on(func(q gosmotest.Query) [][]any {
		if q.Arg(0) != int64(customerID) {
			return nil
		}
		return [][]any{{"Sales", "TABLE", "Customer", "", "", "MS_Description", "nvarchar", 0, 0, 18, "Customers"}}
	}, "FROM   sys.extended_properties ep", "CROSS  JOIN (")
	// A securable's own extended properties (a synonym's): none.
	s.on(func(gosmotest.Query) [][]any { return nil },
		"FROM   sys.extended_properties ep", "WHERE  ep.class = @p1 AND ep.major_id = @p2")
}
