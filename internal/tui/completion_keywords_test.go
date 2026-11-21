package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
)

func TestSQLKeywordCompletionContexts(t *testing.T) {
	for _, tt := range []struct {
		sql, label, text string
	}{
		{"sel|", "SELECT", "SELECT"},
		{"CREATE ta|", "TABLE", "TABLE"},
		{"CREATE or|", "OR ALTER", "OR ALTER"},
		{"CREATE OR al|", "ALTER", "ALTER"},
		{"create v| dbo.NewView AS SELECT 1 AS Id", "VIEW", "VIEW"},
		{"ALTER proc|", "PROCEDURE", "PROCEDURE"},
		{"CREATE OR ALTER fun|", "FUNCTION", "FUNCTION"},
		{"CREATE OR ALTER PROCEDURE dbo.p @id INT as|", "AS", "AS"},
		{"CREATE PROCEDURE dbo.p as|", "AS", "AS"},
		{"ALTER PROCEDURE dbo.p (@id INT) as|", "AS", "AS"},
		{"CREATE PROCEDURE dbo.p @id in|", "INT", "INT"},
		{"CREATE PROCEDURE dbo.p @id INT, @name va|", "VARCHAR", "VARCHAR()"},
		{"CREATE FUNCTION dbo.f(@id in|)", "INT", "INT"},
		{"CREATE PROCEDURE dbo.p @id INT = @value as|", "AS", "AS"},
		{"CREATE FUNCTION dbo.f(@id INT) ret|", "RETURNS", "RETURNS"},
		{"CREATE OR ALTER FUNCTION dbo.f(@id INT) RETURNS in|", "INT", "INT"},
		{"CREATE FUNCTION dbo.f() RETURNS ta|", "TABLE", "TABLE"},
		{"CREATE FUNCTION dbo.f() RETURNS @result ta|", "TABLE", "TABLE()"},
		{"CREATE FUNCTION dbo.f() RETURNS @result TABLE (Id in|)", "INT", "INT"},
		{"CREATE FUNCTION dbo.f() RETURNS INT as|", "AS", "AS"},
		{"ALTER FUNCTION dbo.f() RETURNS VARCHAR(50) as|", "AS", "AS"},
		{"CREATE FUNCTION dbo.f() RETURNS TABLE as|", "AS", "AS"},
		{"CREATE FUNCTION dbo.f() RETURNS @result TABLE (Id INT) as|", "AS", "AS"},
		{"CREATE PROCEDURE dbo.p AS be|", "BEGIN", "BEGIN"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS be|", "BEGIN", "BEGIN"},
		{"CREATE FUNCTION dbo.f() RETURNS TABLE AS ret|", "RETURN", "RETURN"},
		{"CREATE FUNCTION dbo.f() RETURNS TABLE AS RETURN sel|", "SELECT", "SELECT"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN ret|", "RETURN", "RETURN"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN RETURN 1 en|", "END", "END"},
		{"CREATE FUNCTION dbo.f() RETURNS @r TABLE (Id INT) AS BEGIN RETURN en|", "END", "END"},
		{"UPDATE dbo.Customers se|", "SET", "SET"},
		{"UPDATE TOP (10) dbo.Customers se|", "SET", "SET"},
		{"UPDATE c se| FROM dbo.Customers c", "SET", "SET"},
		{"DROP TABLE if|", "IF EXISTS", "IF EXISTS"},
		{"DROP TABLE IF ex|", "EXISTS", "EXISTS"},
		{"DROP VIEW if|", "IF EXISTS", "IF EXISTS"},
		{"DROP SCHEMA if|", "IF EXISTS", "IF EXISTS"},
		{"DROP INDEX IF ex|", "EXISTS", "EXISTS"},
		{"CREATE TABLE dbo.NewTable (Id in|)", "INT", "INT"},
		{"CREATE TABLE dbo.NewTable (Name char|)", "CHAR", "CHAR()"},
		{"CREATE TABLE dbo.NewTable (Id INT, [Order] va|)", "VARCHAR", "VARCHAR()"},
		{"CREATE TABLE dbo.NewTable (Amount DECIMAL(10, 2), Name nv|)", "NVARCHAR", "NVARCHAR()"},
		{"CREATE TABLE dbo.NewTable (Id INT nu|)", "NULL", "NULL"},
		{"CREATE TABLE dbo.NewTable (Name VARCHAR(50) not|)", "NOT NULL", "NOT NULL"},
		{"CREATE TABLE dbo.NewTable (Amount DECIMAL(10, 2) NOT nu|)", "NULL", "NULL"},
		{"CREATE TABLE Billing.dbo.NewTable (Id in|)", "INT", "INT"},
		{"SELECT * fr|", "FROM", "FROM"},
		{"SELECT 'value' fr|", "FROM", "FROM"},
		{"SELECT Name FROM dbo.Customers wh|", "WHERE", "WHERE"},
		{"SELECT Name FROM dbo.Customers ord|", "ORDER BY", "ORDER BY"},
		{"SELECT Name FROM dbo.Customers gro|", "GROUP BY", "GROUP BY"},
		{"SELECT Name FROM dbo.Customers WHERE Id = 1 gr|", "GROUP BY", "GROUP BY"},
		{"SELECT ROW_NUMBER() OVER win FROM dbo.Customers WHERE Id <> 0 win|", "WINDOW", "WINDOW"},
		{"SELECT Name FROM dbo.Customers win|", "WINDOW", "WINDOW"},
		{"SELECT Name FROM dbo.Customers GROUP BY Name win|", "WINDOW", "WINDOW"},
		{"SELECT Name FROM dbo.Customers GROUP BY Name HAVING COUNT(*) > 0 win|", "WINDOW", "WINDOW"},
		{"SELECT Name FROM dbo.Customers WHERE Name = 'x' a|", "AND", "AND"},
		{"SELECT * FROM dbo.Customers WHERE Id IS DISTINCT FROM 1 a|", "AND", "AND"},
		{"SELECT * FROM dbo.Customers WHERE Id IS NOT DISTINCT FROM 1 o|", "OR", "OR"},
		{"SELECT * FROM dbo.Customers c JOIN dbo.Orders o ON c.Id IS NOT DISTINCT FROM o.CustomerId wh|", "WHERE", "WHERE"},
		{"SELECT Name FROM dbo.Customers ORDER b|", "BY", "BY"},
		{"SELECT Name FROM dbo.Customers GROUP BY Name ha|", "HAVING", "HAVING"},
		{"SELECT * FROM dbo.Customers c JOIN dbo.Orders o ON c.Id = o.CustomerId wh|", "WHERE", "WHERE"},
		{"SELECT * FROM dbo.Customers le| JOIN dbo.Orders", "LEFT JOIN", "LEFT"},
		{"SELECT ROW_NUMBER() OVER (part|", "PARTITION BY", "PARTITION BY"},
		{"SELECT ROW_NUMBER() OVER win FROM dbo.Customers WINDOW win AS (part|)", "PARTITION BY", "PARTITION BY"},
		{"SELECT ROW_NUMBER() OVER win FROM dbo.Customers WINDOW win AS (PARTITION BY Id ord|)", "ORDER BY", "ORDER BY"},
		{"SELECT ROW_NUMBER() OVER (PARTITION BY Id ord|", "ORDER BY", "ORDER BY"},
		{"SELECT ROW_NUMBER() OVER (PARTITION b|", "BY", "BY"},
		{"SELECT Name FROM dbo.Customers\nord| BY Name", "ORDER BY", "ORDER"},
		{"SELECT Name FROM dbo.Customers ord| BY Name", "ORDER BY", "ORDER"},
		{"SELECT Name FROM dbo.Customers gro| /* keep */ BY Name", "GROUP BY", "GROUP"},
		{"SELECT ROW_NUMBER() OVER (part|\nBY Id)", "PARTITION BY", "PARTITION"},
		{"WITH c AS (SELECT * FROM dbo.Customers wh|) SELECT * FROM c", "WHERE", "WHERE"},
		{"SELECT 1;\nsel|", "SELECT", "SELECT"},
		{"SELECT 1\nGO\nsel|", "SELECT", "SELECT"},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			lines, row, col := linesAndCursor(t, tt.sql)
			items, from := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			for _, item := range items {
				if item.Detail != "keyword" || item.Label != tt.label {
					continue
				}
				if item.Text != tt.text {
					t.Errorf("insert = %q, want %q", item.Text, tt.text)
				}
				if got := string(lines[row][from:col]); !strings.HasPrefix(strings.ToLower(tt.label), strings.ToLower(got)) {
					t.Errorf("replacement span %q is not the keyword prefix", got)
				}
				return
			}
			t.Fatalf("items %v missing keyword %q", labels(items), tt.label)
		})
	}
}

func TestSQLRoutineSignatureCompletion(t *testing.T) {
	for _, tt := range []struct{ sql, want, absent string }{
		{"CREATE FUNCTION dbo.f(@id INT, @name nv|)", "NVARCHAR", "AS"},
		{"CREATE FUNCTION dbo.f(@amount DECIMAL(10,2), @id in|)", "INT", "RETURNS"},
		{"CREATE FUNCTION dbo.f(@id AS in|)", "INT", "AS"},
		{"CREATE PROCEDURE dbo.p @id AS in|", "INT", "BEGIN"},
		{"CREATE PROCEDURE dbo.p (@id INT, @name nv|)", "NVARCHAR", "AS"},
		{"CREATE PROCEDURE dbo.p @name NVARCHAR(ma|)", "MAX", "AS"},
		{"CREATE FUNCTION dbo.f(@name VARCHAR(ma|))", "MAX", "RETURNS"},
		{"CREATE FUNCTION dbo.f() RETURNS VARBINARY(ma|)", "MAX", "AS"},
		{"CREATE FUNCTION dbo.f() RETURNS @r TABLE (Name NVARCHAR(ma|))", "MAX", "AS"},
		{"CREATE PROCEDURE dbo.p @id INT ou|", "OUTPUT", "RETURNS"},
		{"CREATE PROCEDURE dbo.p (@id INT ou|)", "OUTPUT", "AS"},
		{"CREATE PROCEDURE dbo.p @id INT = 1 ou|", "OUTPUT", "RETURNS"},
		{"CREATE PROCEDURE dbo.p @id INT OUTPUT, @name nv|", "NVARCHAR", "RETURNS"},
		{"CREATE FUNCTION dbo.f(@id INT ou|)", "", "OUTPUT"},
		{"CREATE PROCEDURE dbo.p @id INT = as|", "", "AS"},
		{"CREATE PROCEDURE dbo.p @id INT = 1 as|", "AS", "INT"},
		{"CREATE PROCEDURE dbo.p @id INT = @value AS be|", "BEGIN", "INT"},
		{"CREATE FUNCTION dbo.f(@id INT = 1) ret|", "RETURNS", "AS"},
		{"CREATE FUNCTION dbo.f() RETURNS @r TABLE (Id INT, Name nv|)", "NVARCHAR", "AS"},
		{"CREATE PROCEDURE dbo.p AS BEGIN SELECT 1;\nen|", "END", "RETURNS"},
		{"CREATE PROCEDURE dbo.p AS BEGIN SELECT 1 en|", "END", "RETURNS"},
		{"CREATE PROCEDURE dbo.p AS BEGIN SELECT en|", "", "END"},
		{"CREATE PROCEDURE dbo.p AS BEGIN SELECT 1;\nSELECT Name FROM dbo.Customers wh|", "WHERE", "RETURNS"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN RETURN 1;\nen|", "END", "RETURNS"},
		{"CREATE FUNCTION dbo.f() RETURNS @r TABLE (Id INT) AS BEGIN INSERT INTO @r VALUES (1);\nret|", "RETURN", "RETURNS"},
		{"CREATE FUNCTION dbo.f() RETURNS @r TABLE (Id INT) AS BEGIN in|", "INSERT", "EXEC"},
		{"CREATE PROCEDURE dbo.p AS BEGIN SELECT CASE WHEN 1=1 THEN 1 END;\nen|", "END", "RETURNS"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN RETURN 1;\nEND\nen|", "", "END"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN RETURN CASE WHEN 1=1 THEN 1 ELSE 0 END en|", "END", "RETURNS"},
		{"CREATE PROCEDURE dbo.p AS BEGIN SELECT 1;\nEND\nGO\nen|", "", "END"},
		{"CREATE FUNCTION dbo.f() RETURNS TABLE AS be|", "", "BEGIN"},
		{"CREATE FUNCTION dbo.f() RETURNS TABLE AS RETURN (sel|)", "SELECT", "BEGIN"},
		{"CREATE FUNCTION dbo.f() RETURNS TABLE AS RETURN (SELECT * FROM dbo.Customers wh|)", "WHERE", "BEGIN"},
		{"CREATE FUNCTION dbo.f() RETURNS INT WITH SCHEMABINDING as|", "AS", "BEGIN"},
		{"CREATE FUNCTION dbo.f() RETURNS INT WITH SCHEMABINDING AS be|", "BEGIN", "AS"},
		{"CREATE FUNCTION dbo.f() RETURNS TABLE WITH SCHEMABINDING AS ret|", "RETURN", "BEGIN"},
		{"CREATE FUNCTION dbo.f() RETURNS TABLE WITH SCHEMABINDING AS RETURN (sel|)", "SELECT", "BEGIN"},
		{"CREATE PROCEDURE dbo.p WITH ENCRYPTION as|", "AS", "OUTPUT"},
		{"CREATE PROCEDURE dbo.p WITH RECOMPILE as|", "AS", "OUTPUT"},
		{"CREATE PROCEDURE dbo.p WITH ENCRYPTION, RECOMPILE as|", "AS", "OUTPUT"},
		{"CREATE PROCEDURE dbo.p @id INT WITH RECOMPILE as|", "AS", "OUTPUT"},
		{"CREATE PROCEDURE dbo.p @id INT WITH RECOMPILE ou|", "", "OUTPUT"},
		{"CREATE PROCEDURE dbo.p WITH ENCRYPTION, as|", "", "AS"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN RETURN ab| END", "ABS", "COUNT"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN RETURN 1 + ab| END", "ABS", "ROW_NUMBER"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN DECLARE @x INT; RETURN ab| END", "ABS", "COUNT"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN RETURN (SELECT Id FROM dbo.Customers wh|); END", "WHERE", "END"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN RETURN (SELECT Id FROM dbo.Customers wh| Id = 1); END", "WHERE", "END"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN RETURN (ab|); END", "ABS", "END"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN RETURN (SELECT Id FROM dbo.Customers) en|", "END", "RETURNS"},
		{"CREATE FUNCTION dbo.f() RETURNS @r TABLE (Id INT) AS BEGIN RETURN ab| END", "", "ABS"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN IF 1 = 0 RETURN 0; DECLARE @x INT; SELECT @x = Id FROM dbo.Customers wh|; RETURN @x; END", "WHERE", "END"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN IF 1 = 0 RETURN 0; DECLARE @x INT; SELECT @x = na| FROM dbo.Customers; RETURN @x; END", "Name", "END"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN IF 1 = 0 RETURN 0; DECLARE @x INT; SELECT @x = co| FROM dbo.Customers; RETURN @x; END", "COUNT", "END"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN DECLARE @x INT; IF 1 = 0 RETURN 0 SELECT @x = Id FROM dbo.Customers wh|; RETURN @x; END", "WHERE", "END"},
		{"CREATE FUNCTION dbo.f() RETURNS INT WITH SCHEMABINDING, as|", "", "AS"},
		{"CREATE PROCEDURE dbo.p AS BEGIN TRANSACTION; SELECT 1; COMMIT TRANSACTION; en|", "", "END"},
		{"CREATE PROCEDURE dbo.p AS BEGIN BEGIN TRAN; SELECT 1; COMMIT TRAN; END en|", "", "END"},
		{"CREATE PROCEDURE dbo.p AS BEGIN BEGIN TRANSACTION; SELECT 1; COMMIT TRANSACTION; en|", "END", "RETURNS"},
		{"CREATE PROCEDURE dbo.p AS BEGIN BEGIN DISTRIBUTED TRANSACTION; COMMIT TRANSACTION; END en|", "", "END"},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if tt.want != "" && !containsLabel(items, tt.want) {
				t.Fatalf("missing %q in %v", tt.want, labels(items))
			}
			if tt.absent != "" && containsLabel(items, tt.absent) {
				t.Fatalf("unexpected %q in %v", tt.absent, labels(items))
			}
		})
	}
}

func TestSQLDMLCompletionTransitions(t *testing.T) {
	for _, tt := range []struct{ sql, want, absent string }{
		{"UPDATE dbo.Customers SET Name = 'x' wh|", "WHERE", "GROUP BY"},
		{"UPDATE dbo.Customers SET Name = 'x', Email = 'y' wh|", "WHERE", "GROUP BY"},
		{"UPDATE dbo.Customers SET Name = CASE WHEN Id = 1 THEN 'x' ELSE 'y' END wh|", "WHERE", "GROUP BY"},
		{"UPDATE dbo.Customers SET Name.WRITE(N'x', 0, 1) wh|", "WHERE", "GROUP BY"},
		{"UPDATE dbo.Customers SET Name.WRITE(NULL, 0, 1) wh|", "WHERE", "GROUP BY"},
		{"UPDATE dbo.Customers SET [Name]. /* keep */ write(N'x', 0, 1) wh|", "WHERE", "GROUP BY"},
		{"UPDATE dbo.Customers SET Name.WRITE(N'x', 0, 1), Id = 1 wh|", "WHERE", "GROUP BY"},
		{"UPDATE dbo.Customers SET Name.WRITE(N'x', 0, wh|)", "", "WHERE"},
		{"UPDATE dbo.Customers SET Name.WRITE(N'x', 0, 1), wh|", "", "WHERE"},
		{"WITH x AS (SELECT * FROM dbo.Customers) UPDATE x SET Name = 'x' wh|", "WHERE", "GROUP BY"},
		{"DELETE dbo.Customers wh|", "WHERE", "JOIN"},
		{"DELETE TOP (10) PERCENT dbo.Customers wh|", "WHERE", "JOIN"},
		{"DELETE TOP (10) PERCENT dbo.Customers fr|", "FROM", "JOIN"},
		{"WITH x AS (SELECT Name FROM dbo.Customers) DELETE x wh|", "WHERE", "JOIN"},
		{"DELETE wh|", "", "WHERE"},
		{"DELETE TOP (wh|)", "", "WHERE"},
		{"UPDATE dbo.Customers SET Name = wh|", "", "WHERE"},
		{"UPDATE dbo.Customers SET Name = 'x', wh|", "", "WHERE"},
		{"UPDATE dbo.Customers SET Name = 'x' WHERE Id = 1 gro|", "", "GROUP BY"},
		{"UPDATE dbo.Customers SET Name = 'x' WHERE Id = 1 for|", "", "FOR JSON"},
		{"DELETE FROM dbo.Customers WHERE Id = 1 gro|", "", "GROUP BY"},
		{"DELETE FROM dbo.Customers WHERE Id = 1 for|", "", "FOR XML"},
		{"WITH x AS (SELECT * FROM dbo.Customers) DELETE FROM x WHERE Id = 1 gro|", "", "GROUP BY"},
		{"DELETE FROM dbo.Customers jo|", "", "JOIN"},
		{"DELETE FROM dbo.Customers as|", "", "AS"},
		{"DELETE FROM dbo.Customers fr|", "FROM", "JOIN"},
		{"DELETE TOP (10) FROM dbo.Customers jo|", "", "JOIN"},
		{"DELETE TOP (10) PERCENT FROM dbo.Customers jo|", "", "JOIN"},
		{"DELETE TOP (10) PERCENT FROM dbo.Customers fr|", "FROM", "JOIN"},
		{"DELETE TOP (10) PERCENT FROM c FROM dbo.Customers c jo|", "JOIN", "GROUP BY"},
		{"DELETE c FROM dbo.Customers c jo|", "JOIN", "GROUP BY"},
		{"DELETE FROM c FROM dbo.Customers c jo|", "JOIN", "GROUP BY"},
		{"UPDATE c SET Name = 'x' FROM dbo.Customers c jo|", "JOIN", "GROUP BY"},
		{"UPDATE dbo.Customers SET Name = (SELECT MAX(Name) FROM dbo.Customers gro|)", "GROUP BY", ""},
		{"DELETE FROM dbo.Customers WHERE Id IN (SELECT Id FROM dbo.Orders gro|)", "GROUP BY", ""},
		{"INSERT INTO dbo.Customers (Name) SELECT Name FROM dbo.Customers gro|", "GROUP BY", ""},
		{"SELECT * FROM dbo.Customers wh| Id IS DISTINCT FROM 1", "WHERE", ""},
		{"SELECT * FROM dbo.Customers wh| Id IS NOT DISTINCT FROM 1", "WHERE", ""},
		{"SELECT * FROM dbo.Customers c JOIN dbo.Orders o ON c.Id = o.CustomerId wh| c.Id IS DISTINCT FROM 1", "WHERE", ""},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			objects := testCustomersOrders()
			if strings.Contains(strings.ToUpper(tt.sql), "WRITE(") {
				objects[0].Columns[1].MaxLength = -1
			}
			qp := newTestQueryPanelWithInventory(t, "testdb", objects)
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if tt.want != "" && !containsLabel(items, tt.want) {
				t.Fatalf("missing %q in %v", tt.want, labels(items))
			}
			if tt.absent != "" && containsLabel(items, tt.absent) {
				t.Fatalf("unexpected %q in %v", tt.absent, labels(items))
			}
		})
	}
}

func TestSQLStackedDDLCompletion(t *testing.T) {
	for _, tt := range []struct{ sql, want string }{
		{"CREATE TABLE dbo.First (Id INT)\ncre|", "CREATE"},
		{"CREATE TABLE dbo.First (Id INT)\nCREATE ta|", "TABLE"},
		{"CREATE TABLE dbo.First (Id INT);\ncre|", "CREATE"},
		{"CREATE TABLE dbo.First (Id INT)\nCREATE TABLE dbo.Second (Name nv|)", "NVARCHAR"},
		{"CREATE TABLE dbo.First (Id INT, Name NVARCHAR(MAX))\n\ncr|", "CREATE"},
		{"CREATE TABLE dbo.First (Id INT)\n-- next table\ncre|", "CREATE"},
		{"CREATE TABLE dbo.First (Id INT) CREATE ta|", "TABLE"},
		{"CREATE TABLE dbo.First (Id INT) ON [PRIMARY]\ncre|", "CREATE"},
		{"CREATE TABLE dbo.First (Id INT)\nCREATE TABLE dbo.Second (Id INT)\ncre|", "CREATE"},
		{"CREATE TABLE dbo.First (Id INT)\n|", "CREATE"},
		{"CREATE TABLE dbo.First (Id INT)\nal|", "ALTER"},
		{"CREATE TABLE dbo.First (Id INT)\nALTER TABLE dbo.cu|", "dbo.Customers"},
		{"CREATE TABLE dbo.First (Id INT)\nDROP TABLE dbo.cu|", "dbo.Customers"},
		{"CREATE TABLE dbo.First (Id INT)\nCREATE OR ALTER fu|", "FUNCTION"},
		{"CREATE TABLE dbo.First (Id INT DEFAULT (1), Name nv|)", "NVARCHAR"},
		{"CREATE TABLE dbo.First ([CREATE] INT, Name nv|)", "NVARCHAR"},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if !containsLabel(items, tt.want) {
				t.Fatalf("missing %q in %v", tt.want, labels(items))
			}
		})
	}
}

func TestSQLCommonSyntaxReviewRound1(t *testing.T) {
	for _, tt := range []struct{ sql, want, absent string }{
		{"SELECT DISTINCT TOP (5) * fr|", "FROM", ""},
		{"SELECT ALL TOP (5) * fr|", "FROM", ""},
		{"SELECT DISTINCT TOP (5) * ab| FROM dbo.Customers", "", "ABS"},
		{"SELECT * FROM dbo.Customers c CROSS APPLY STRING_SPLIT(c.Name, ',') s, ord|", "dbo.Orders", ""},
		{"CREATE TABLE #stage (Id INT); DROP TABLE #st|", "#stage", ""},
		{"CREATE TABLE #stage (Id INT); ALTER TABLE #st|", "#stage", ""},
		{"CREATE TABLE #stage (Id INT);\nGO\nDROP TABLE IF EXISTS #st|", "#stage", ""},
		{"DECLARE @stage TABLE (Id INT); DROP TABLE @st|", "", "@stage"},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if tt.want != "" && !containsLabel(items, tt.want) {
				t.Fatalf("missing %q in %v", tt.want, labels(items))
			}
			if tt.absent != "" && containsLabel(items, tt.absent) {
				t.Fatalf("unexpected %q in %v", tt.absent, labels(items))
			}
		})
	}
}

func TestSQLCommonSyntaxReviewRound2(t *testing.T) {
	for _, tt := range []struct{ sql, want, absent string }{
		{"DECLARE @n INT\nEXEC dbo.get|", "dbo.GetCustomers", ""},
		{"DROP VIEW dbo.OldView\nDROP TABLE dbo.cu|", "dbo.Customers", "dbo.vActiveCustomers"},
		{"CREATE PROCEDURE dbo.p AS sel|", "SELECT", ""},
		{"CREATE PROCEDURE dbo.p AS se|", "SET", ""},
		{"CREATE PROCEDURE dbo.p AS BEGIN se|", "SET", ""},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS sel|", "", "SELECT"},
		{"CREATE FUNCTION dbo.f() RETURNS TABLE AS sel|", "", "SELECT"},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			inv := qp.app.completion.inventories[completionInventoryKey(qp.conn.Opts, qp.database)]
			inv.procedures = []*gosmo.StoredProcedure{{Schema: "dbo", Name: "GetCustomers"}}
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if tt.want != "" && !containsLabel(items, tt.want) {
				t.Fatalf("missing %q in %v", tt.want, labels(items))
			}
			if tt.absent != "" && containsLabel(items, tt.absent) {
				t.Fatalf("unexpected %q in %v", tt.absent, labels(items))
			}
		})
	}
}

func TestSQLCommonSyntaxReviewRound3(t *testing.T) {
	for _, tt := range []struct{ sql, want, absent string }{
		{"SELECT * FROM dbo.Customers WHERE (Id = 1 gr|)", "", "GROUP BY"},
		{"SELECT * FROM dbo.Customers WHERE (Id = 1 ord|)", "", "ORDER BY"},
		{"SELECT * FROM dbo.Customers WHERE (Id = 1 for|)", "", "FOR JSON"},
		{"SELECT * FROM dbo.Customers WHERE (Id = 1 a|)", "AND", ""},
		{"SELECT * FROM dbo.Customers WHERE (Id IS DISTINCT fr| 1)", "FROM", ""},
		{"SELECT * FROM dbo.Customers WHERE (SELECT MAX(Id) FROM dbo.Orders WHERE Id = 1 gr|) = 1", "GROUP BY", ""},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if tt.want != "" && !containsLabel(items, tt.want) {
				t.Fatalf("missing %q in %v", tt.want, labels(items))
			}
			if tt.absent != "" && containsLabel(items, tt.absent) {
				t.Fatalf("unexpected %q in %v", tt.absent, labels(items))
			}
		})
	}
}

func TestSQLCommonSyntaxReviewRound4(t *testing.T) {
	for _, tt := range []struct{ sql, want string }{
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN dec| @x INT; RETURN @x; END", "DECLARE"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN se| @x = 1; RETURN @x; END", "SET"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN sel| @x = 1; RETURN @x; END", "SELECT"},
		{"CREATE PROCEDURE dbo.p AS BEGIN SELECT 1;\nset| NOCOUNT ON; END", "SET"},
		{"CREATE FUNCTION dbo.f() RETURNS @r TABLE (Id INT) AS BEGIN INSERT @r VALUES (1);\nset| @x = 1; RETURN; END", "SET"},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if !containsLabel(items, tt.want) {
				t.Fatalf("missing %q in %v", tt.want, labels(items))
			}
		})
	}
}

func TestSQLCommonSyntaxReviewRound7(t *testing.T) {
	for _, tt := range []struct{ sql, want string }{
		{"CREATE OR ALTER FUNCTION dbo.f(@x INT) RETURNS INT BEGIN RETURN ab|(@x); END;", "ABS"},
		{"CREATE FUNCTION dbo.f(@x INT) RETURNS INT WITH SCHEMABINDING BEGIN RETURN ab|(@x); END;", "ABS"},
		{"CREATE FUNCTION dbo.f() RETURNS TABLE RETURN (sel| * FROM dbo.Customers);", "SELECT"},
		{"CREATE FUNCTION dbo.f() RETURNS @r TABLE (Id INT) BEGIN in| INTO @r VALUES (1); RETURN; END;", "INSERT"},
		{"CREATE OR ALTER PROCEDURE dbo.p AS BEGIN SELECT 1;\nwi| c AS (SELECT 2 AS n) SELECT n FROM c; END;", "WITH"},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if !containsLabel(items, tt.want) {
				t.Fatalf("missing %q in %v", tt.want, labels(items))
			}
		})
	}
}

func TestSQLKeywordCompletionAllowsRepeatedTokens(t *testing.T) {
	for _, tt := range []struct{ sql, want string }{
		{"SELECT co|, COUNT(*) FROM dbo.Customers", "COUNT"},
		{"SELECT av|, AVG(Id) FROM dbo.Customers", "AVG"},
		{"SELECT ab| + ABS(Id) FROM dbo.Customers", "ABS"},
		{"SELECT Name FROM dbo.Customers GROUP BY Name HAVING co| > COUNT(*)", "COUNT"},
		{"SELECT * FROM dbo.Customers WHERE Id = 1 a| Name = 'x' AND Email IS NOT NULL", "AND"},
		{"SELECT * FROM dbo.Customers WHERE Id = 1 o| Name = 'x' OR Email IS NOT NULL", "OR"},
		{"SELECT * FROM dbo.Customers c JOIN dbo.Orders o o| c.Id = o.CustomerId JOIN dbo.Customers c2 ON c2.Id = o.CustomerId", "ON"},
		{"SELECT * FROM dbo.Customers c le| JOIN dbo.Orders o ON c.Id = o.CustomerId LEFT JOIN dbo.Customers c2 ON c2.Id = o.CustomerId", "LEFT JOIN"},
		{"SELECT Name FROM dbo.Customers GROUP b| Name ORDER BY Name", "BY"},
		{"SELECT Name FROM dbo.Customers ORDER BY Name a|, Id ASC", "ASC"},
		{"SELECT Name FROM dbo.Customers ORDER BY Name d|, Id DESC", "DESC"},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if !containsLabel(items, tt.want) {
				t.Fatalf("items %v missing repeatable token %q", labels(items), tt.want)
			}
		})
	}
}

func TestSQLKeywordCompletionSuppressesInvalidContexts(t *testing.T) {
	for _, sql := range []string{
		"SELECT * FROM dbo.ord|",
		"SELECT c.ord| FROM dbo.Customers c",
		"SELECT * FROM [ord|",
		"SELECT 'ord|'",
		"SELECT 1 -- ord|",
		"SELECT 1 /* ord| */",
		"SELECT fr|",
		"SELECT * FROM wh|",
		"SELECT * FROM dbo.Customers WHERE Name = ord|",
		"SELECT * FROM dbo.Customers WHERE Id = 1 wh|",
		"SELECT * FROM dbo.Customers ORDER BY Name gro|",
		"SELECT COUNT(ord|) FROM dbo.Customers",
		"SELECT ROW_NUMBER() OVER (ORDER BY Id part|)",
		"SELECT * FROM dbo.Customers ord|er",
		"SELECT Name fr| FROM dbo.Customers",
		"SELECT * FROM dbo.Customers WHERE Id = 1 or|.Id",
		"SELECT CASE WHEN Id = 1 THEN Name ord| FROM dbo.Customers",
		"SELECT ROW_NUMBER() OVER (part| PARTITION BY Id)",
		"SELECT * FROM dbo.Customers ORDER BY Name ord|",
		"SELECT * FROM dbo.Customers ord| WHERE Id = 1",
		"SELECT * FROM dbo.Customers gro| WHERE Id = 1",
		"SELECT * FROM dbo.Customers ORDER BY Name DESC a|",
		"SELECT * FROM dbo.Customers ORDER BY Name a| DESC",
		"SELECT * FROM dbo.Customers ORDER BY Name d| ASC",
		"SELECT ROW_NUMBER() OVER (ord| PARTITION BY Id) FROM dbo.Customers",
		"SELECT Name FROM dbo.Customers WHERE win|",
		"SELECT Name FROM dbo.Customers ORDER BY Name win|",
		"SELECT Name FROM dbo.Customers win| WINDOW w AS (ORDER BY Id)",
		"SELECT Name FROM dbo.Customers WINDOW w AS (ORDER BY Id) win|",
		"SELECT * FROM dbo.Customers WHERE Id IS DISTINCT FROM a|",
		"UPDATE se|",
		"UPDATE TOP (10) se|",
		"UPDATE dbo.Customers SET Name = 'x' se|",
		"CREATE TABLE dbo.NewTable (in|)",
		"CREATE TABLE dbo.NewTable (Id INT, nv|)",
		"CREATE TABLE dbo.NewTable (Amount DECIMAL(in|, 2))",
		"CREATE TABLE dbo.NewTable (PRIMARY in|)",
		"CREATE TABLE dbo.NewTable (Name VARCHAR(nu|))",
		"CREATE TABLE dbo.NewTable (Id INT NULL not|)",
		"CREATE TABLE dbo.NewTable (Id INT NOT NULL nu|)",
		"CREATE TABLE dbo.NewTable (Id INT nu| NOT NULL)",
		"CREATE TABLE dbo.NewTable (Id INT not| NULL)",
		"CREATE FUNCTION dbo.f(@id INT ret|)",
		"CREATE FUNCTION dbo.f() RETURNS VARCHAR(as|)",
		"CREATE FUNCTION dbo.f() RETURNS @r TABLE (Id as|)",
		"CREATE FUNCTION dbo.f() RETURNS @r TABLE as|",
		"CREATE FUNCTION dbo.f() RETURNS TABLE AS be|",
		"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN RETURN en|",
		"CREATE PROCEDURE dbo.p @id INT = CAST(1 as| INT)",
		"CREATE PROCEDURE dbo.p @id INT = as|",
		"CREATE PROCEDURE dbo.p WITH EXECUTE AS be|",
	} {
		t.Run(sql, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			lines, row, col := linesAndCursor(t, sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			for _, item := range items {
				if item.Detail == "keyword" {
					t.Errorf("unexpected keyword %q", item.Label)
				}
			}
		})
	}
}

func TestSQLKeywordCompletionDuringCatalogLoad(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	qp.app.completion.inventories[completionInventoryKey(qp.conn.Opts, qp.database)] = &completionInventory{loading: true}
	lines, row, col := linesAndCursor(t, "sel|")
	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if !containsLabel(items, "SELECT") {
		t.Fatalf("items %v missing SELECT while catalog loads", labels(items))
	}
}

func TestSQLKeywordCompletionAcceptsPhrase(t *testing.T) {
	for _, tt := range []struct{ before, typed, after, want string }{
		{"", "sel", "", "SELECT "},
		{"", "create", "", "CREATE "},
		{"CREATE ", "ta", "", "CREATE TABLE "},
		{"CREATE ", "vi", " dbo.NewView AS SELECT 1 AS Id", "CREATE VIEW dbo.NewView AS SELECT 1 AS Id"},
		{"ALTER ", "proc", "", "ALTER PROCEDURE "},
		{"CREATE OR ALTER ", "fun", "", "CREATE OR ALTER FUNCTION "},
		{"CREATE ", "or", "", "CREATE OR ALTER "},
		{"CREATE FUNCTION dbo.f() ", "ret", "", "CREATE FUNCTION dbo.f() RETURNS "},
		{"CREATE FUNCTION dbo.f() RETURNS TABLE ", "as", "", "CREATE FUNCTION dbo.f() RETURNS TABLE AS "},
		{"CREATE FUNCTION dbo.f() RETURNS TABLE AS ", "ret", "", "CREATE FUNCTION dbo.f() RETURNS TABLE AS RETURN "},
		{"CREATE FUNCTION dbo.f() RETURNS TABLE AS RETURN (", "sel", ")", "CREATE FUNCTION dbo.f() RETURNS TABLE AS RETURN (SELECT )"},
		{"CREATE FUNCTION dbo.f() RETURNS INT WITH SCHEMABINDING ", "as", "", "CREATE FUNCTION dbo.f() RETURNS INT WITH SCHEMABINDING AS "},
		{"CREATE FUNCTION dbo.f() RETURNS INT WITH SCHEMABINDING AS ", "be", "", "CREATE FUNCTION dbo.f() RETURNS INT WITH SCHEMABINDING AS BEGIN "},
		{"CREATE PROCEDURE dbo.p WITH RECOMPILE ", "as", "", "CREATE PROCEDURE dbo.p WITH RECOMPILE AS "},
		{"CREATE TABLE dbo.First (Id INT)\n", "cre", "", "CREATE TABLE dbo.First (Id INT)\nCREATE "},
		{"CREATE TABLE dbo.First (Id INT)\nCREATE ", "ta", "", "CREATE TABLE dbo.First (Id INT)\nCREATE TABLE "},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN RETURN ", "ab", " END", "CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN RETURN ABS() END"},
		{"CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN RETURN (SELECT Id FROM dbo.Customers ", "wh", "); END", "CREATE FUNCTION dbo.f() RETURNS INT AS BEGIN RETURN (SELECT Id FROM dbo.Customers WHERE ); END"},
		{"CREATE PROCEDURE dbo.p @id INT ", "as", " BEGIN SELECT 1 END", "CREATE PROCEDURE dbo.p @id INT AS BEGIN SELECT 1 END"},
		{"CREATE PROCEDURE dbo.p AS ", "be", "", "CREATE PROCEDURE dbo.p AS BEGIN "},
		{"CREATE FUNCTION dbo.f(@id INT, @name ", "nv", ")", "CREATE FUNCTION dbo.f(@id INT, @name NVARCHAR())"},
		{"CREATE PROCEDURE dbo.p @name NVARCHAR(", "ma", ")", "CREATE PROCEDURE dbo.p @name NVARCHAR(MAX)"},
		{"CREATE PROCEDURE dbo.p @id INT ", "outp", "", "CREATE PROCEDURE dbo.p @id INT OUTPUT "},
		{"CREATE FUNCTION dbo.f() RETURNS @r TABLE (Id INT, Name ", "nv", ")", "CREATE FUNCTION dbo.f() RETURNS @r TABLE (Id INT, Name NVARCHAR())"},
		{"CREATE PROCEDURE dbo.p AS BEGIN SELECT 1;\n", "en", "", "CREATE PROCEDURE dbo.p AS BEGIN SELECT 1;\nEND "},
		{"CREATE FUNCTION dbo.f() RETURNS @r TABLE (Id INT) AS BEGIN INSERT INTO @r VALUES (1);\n", "ret", "", "CREATE FUNCTION dbo.f() RETURNS @r TABLE (Id INT) AS BEGIN INSERT INTO @r VALUES (1);\nRETURN "},
		{"DROP TABLE ", "if", "", "DROP TABLE IF EXISTS "},
		{"DROP TABLE IF ", "ex", "", "DROP TABLE IF EXISTS "},
		{"ALTER TABLE dbo.", "cu", "", "ALTER TABLE dbo.Customers "},
		{"DROP TABLE IF EXISTS dbo.", "cu", "", "DROP TABLE IF EXISTS dbo.Customers "},
		{"CREATE TABLE dbo.NewTable (Id ", "in", ")", "CREATE TABLE dbo.NewTable (Id INT)"},
		{"CREATE TABLE dbo.NewTable (Name ", "char", ")", "CREATE TABLE dbo.NewTable (Name CHAR())"},
		{"CREATE TABLE dbo.NewTable (Id INT ", "not", ")", "CREATE TABLE dbo.NewTable (Id INT NOT NULL)"},
		{"CREATE TABLE dbo.NewTable (Name VARCHAR(50) NOT ", "nu", ")", "CREATE TABLE dbo.NewTable (Name VARCHAR(50) NOT NULL)"},
		{"UPDATE dbo.Customers ", "se", "", "UPDATE dbo.Customers SET "},
		{"UPDATE dbo.Customers ", "se", " Name = 'x'", "UPDATE dbo.Customers SET Name = 'x'"},
		{"UPDATE dbo.Customers SET Name = 'x' ", "wh", "", "UPDATE dbo.Customers SET Name = 'x' WHERE "},
		{"UPDATE dbo.Customers SET Name.WRITE(N'x', 0, 1) ", "wh", "", "UPDATE dbo.Customers SET Name.WRITE(N'x', 0, 1) WHERE "},
		{"SELECT * FROM dbo.Customers ", "wh", " Id IS DISTINCT FROM 1", "SELECT * FROM dbo.Customers WHERE Id IS DISTINCT FROM 1"},
		{"", "exec", "", "EXEC "},
		{"SELECT * ", "from", "", "SELECT * FROM "},
		{"SELECT * FROM dbo.Customers ", "where", "", "SELECT * FROM dbo.Customers WHERE "},
		{"SELECT * FROM dbo.Customers ", "as", "", "SELECT * FROM dbo.Customers AS "},
		{"SELECT Id ", "as", "", "SELECT Id AS "},
		{"SELECT Name FROM dbo.Customers FOR JSON PATH ", "without", "", "SELECT Name FROM dbo.Customers FOR JSON PATH, WITHOUT_ARRAY_WRAPPER "},
		{"SELECT Name FROM dbo.Customers FOR JSON PATH, ", "without", "", "SELECT Name FROM dbo.Customers FOR JSON PATH, WITHOUT_ARRAY_WRAPPER "},
		{"SELECT Name FROM dbo.Customers FOR JSON PATH, WITHOUT_ARRAY_WRAPPER ", "incl", "", "SELECT Name FROM dbo.Customers FOR JSON PATH, WITHOUT_ARRAY_WRAPPER, INCLUDE_NULL_VALUES "},
		{"SELECT * FROM dbo.Customers c JOIN dbo.Orders o ", "on", "", "SELECT * FROM dbo.Customers c JOIN dbo.Orders o ON "},
		{"SELECT * FROM dbo.Customers c CROSS JOIN dbo.Orders o ", "whe", "", "SELECT * FROM dbo.Customers c CROSS JOIN dbo.Orders o WHERE "},
		{"SELECT * FROM dbo.Customers WHERE Id = 1 ", "and", " Name = 'x' AND Email IS NOT NULL", "SELECT * FROM dbo.Customers WHERE Id = 1 AND Name = 'x' AND Email IS NOT NULL"},
		{"SELECT ", "to", "", "SELECT TOP "},
		{"SELECT ", "cou", "", "SELECT COUNT()"},
		{"SELECT ", "av", "", "SELECT AVG()"},
		{"SELECT ", "cou", "(Id)", "SELECT COUNT(Id)"},
		{"SELECT * FROM dbo.Customers GROUP BY Name HAVING ", "cou", "", "SELECT * FROM dbo.Customers GROUP BY Name HAVING COUNT()"},
		{"SELECT * FROM dbo.Customers ", "ou", "", "SELECT * FROM dbo.Customers OUTER APPLY "},
		{"SELECT Name FROM dbo.Customers ", "ord", "", "SELECT Name FROM dbo.Customers ORDER BY "},
		{"SELECT Name FROM dbo.Customers WHERE Id <> 0 ", "win", "", "SELECT Name FROM dbo.Customers WHERE Id <> 0 WINDOW "},
		{"SELECT Name FROM dbo.Customers ", "ord", " BY Name", "SELECT Name FROM dbo.Customers ORDER BY Name"},
		{"SELECT Name FROM dbo.Customers ", "gro", " /* keep */ BY Name", "SELECT Name FROM dbo.Customers GROUP /* keep */ BY Name"},
		{"SELECT ROW_NUMBER() OVER (", "part", " BY Id)", "SELECT ROW_NUMBER() OVER (PARTITION BY Id)"},
		{"SELECT Name FROM dbo.Customers GROUP BY ", "rollup", "", "SELECT Name FROM dbo.Customers GROUP BY ROLLUP()"},
	} {
		for _, accept := range []struct {
			name string
			key  tcell.Key
		}{{"Tab", tcell.KeyTab}, {"Enter", tcell.KeyEnter}} {
			t.Run(tt.want+"/"+accept.name, func(t *testing.T) {
				qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
				qp.editor.SetText(tt.before + tt.after)
				qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModCtrl))
				for range []rune(tt.after) {
					qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
				}
				for _, r := range tt.typed {
					qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
				}
				if !qp.editor.OverlayActive() {
					t.Fatal("keyword popup did not open")
				}
				qp.editor.HandleKey(tcell.NewEventKey(accept.key, "", tcell.ModNone))
				if got := qp.editor.Text(); got != tt.want {
					t.Fatalf("Text() = %q, want %q", got, tt.want)
				}
			})
		}
	}
}

func TestSQLCompletionDDLTargets(t *testing.T) {
	for _, tt := range []struct{ sql, want, absent string }{
		{"CREATE TABLE d|", "dbo", "dbo.Customers"},
		{"CREATE TABLE dbo.cu|", "", "dbo.Customers"},
		{"CREATE VIEW d|", "dbo", "dbo.vActiveCustomers"},
		{"ALTER TABLE cu|", "dbo.Customers", "dbo.vActiveCustomers"},
		{"ALTER TABLE dbo.cu|", "dbo.Customers", "Name"},
		{"ALTER VIEW dbo.cu|", "dbo.vActiveCustomers", "dbo.Customers"},
		{"DROP TABLE IF EXISTS dbo.cu|", "dbo.Customers", "dbo.vActiveCustomers"},
		{"DROP VIEW dbo.cu|", "dbo.vActiveCustomers", "dbo.Customers"},
		{"ALTER TABLE Billing.a|", "audit", "dbo.Customers"},
		{"ALTER TABLE Billing.audit.Tr|", "audit.Trail", "At"},
		{"ALTER TABLE Billing..In|", "dbo.Invoices", "InvoiceNo"},
		{"TRUNCATE TABLE Billing.audit.Tr|", "audit.Trail", "At"},
		{"TRUNCATE TABLE Billing.dbo.In|", "dbo.Invoices", "InvoiceNo"},
		{"TRUNCATE TABLE Pending.dbo.In|", loadingCompletionItem.Label, "dbo.Invoices"},
		{"DROP TABLE IF EXISTS Billing.dbo.In|", "dbo.Invoices", "InvoiceNo"},
		{"DROP TABLE dbo.Customers, dbo.Orders, Billing.dbo.In|", "dbo.Invoices", "InvoiceNo"},
		{"CREATE TABLE Billing.a|", "audit", "audit.Trail"},
		{"CREATE TABLE Billing.audit.Tr|", "", "audit.Trail"},
		{"ALTER TABLE Pending.dbo.In|", loadingCompletionItem.Label, "dbo.Invoices"},
		{"ALTER TABLE Locked.dbo.In|", "", "dbo.Invoices"},
		{"ALTER VIEW Billing.dbo.In|", "", "dbo.Invoices"},
		{"CREATE TABLE dbo.NewTable (|)", "", "dbo.Customers"},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newCrossDBPanel(t)
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if tt.want != "" && !containsLabel(items, tt.want) {
				t.Fatalf("items %v missing %q", labels(items), tt.want)
			}
			if containsLabel(items, tt.absent) {
				t.Fatalf("items %v unexpectedly contain %q", labels(items), tt.absent)
			}
		})
	}
}

func TestSQLCompletionDDLDialects(t *testing.T) {
	for _, tt := range []struct{ sql, absent string }{
		{"CREATE |", "INDEX"},
		{"CREATE OR ALTER |", "TRIGGER"},
		{"CREATE TABLE |", "testdb"},
		{"CREATE TABLE dbo.NewTable (Name |)", "NVARCHAR"},
		{"CREATE TABLE dbo.NewTable (Name |)", "DATETIME"},
		{"CREATE TABLE dbo.NewTable (Name |)", "BINARY"},
	} {
		t.Run(tt.sql+"/"+tt.absent, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			qp.conn.Server = fabricConn(t).Server
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if containsLabel(items, tt.absent) {
				t.Fatalf("items %v unexpectedly contain %q in Fabric", labels(items), tt.absent)
			}
		})
	}
}

func TestSQLCompletionDDLSyntaxVersions(t *testing.T) {
	for _, tt := range []struct {
		version           string
		orAlter, ifExists bool
	}{
		{"12.0.2000.0", false, false},
		{"13.0.1601.5", false, true},
		{"13.0.4001.0", true, true},
		{"14.0.1000.169", true, true},
	} {
		t.Run(tt.version, func(t *testing.T) {
			sc, _ := newGConnAtVersion(t, tt.version)
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			qp.conn.Server = sc.Server
			for _, syntax := range []struct {
				sql, label string
				want       bool
			}{
				{"CREATE or|", "OR ALTER", tt.orAlter},
				{"DROP TABLE if|", "IF EXISTS", tt.ifExists},
			} {
				lines, row, col := linesAndCursor(t, syntax.sql)
				items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
				if got := containsLabel(items, syntax.label); got != syntax.want {
					t.Fatalf("%s present=%v, want %v in %v", syntax.label, got, syntax.want, labels(items))
				}
			}
		})
	}
}

func TestSQLCompletionUpdateSetThenColumns(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	qp.editor.SetText("UPDATE dbo.Customers ")
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModCtrl))
	for _, prefix := range []string{"se", "na"} {
		for _, r := range prefix {
			qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
		}
		if !qp.editor.OverlayActive() {
			t.Fatalf("completion popup did not open for %q", prefix)
		}
		qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
	}
	if got, want := qp.editor.Text(), "UPDATE dbo.Customers SET Name"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

func TestSQLKeywordCompletionStarAfterAcceptedSelect(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	for _, r := range "sel" {
		qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
	}
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModCtrl))
	if !qp.editor.OverlayActive() {
		t.Fatal("Ctrl+Space did not offer SELECT expressions")
	}
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
	if got, want := qp.editor.Text(), "SELECT * "; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

func TestSQLSmallAliasAndJSONOptions(t *testing.T) {
	for _, tt := range []struct {
		sql, label   string
		fabric, want bool
	}{
		{"SELECT * FROM dbo.Customers as|", "AS", false, true},
		{"SELECT * FROM dbo.Customers JOIN dbo.Orders as|", "AS", false, true},
		{"SELECT * FROM dbo.Customers CROSS APPLY dbo.fnOrdersFor(1) as|", "AS", false, true},
		{"SELECT Id as| FROM dbo.Customers", "AS", false, true},
		{"SELECT c.Name, c.Id as| FROM dbo.Customers c", "AS", false, true},
		{"SELECT * FROM dbo.Customers c as|", "AS", false, false},
		{"SELECT Id label as| FROM dbo.Customers", "AS", false, false},
		{"SELECT Id AS label as| FROM dbo.Customers", "AS", false, false},
		{"SELECT * as| FROM dbo.Customers", "AS", false, false},
		{"SELECT c.* as| FROM dbo.Customers c", "AS", false, false},
		{"SELECT * FROM dbo.Customers WHERE Id as|", "AS", false, false},
		{"SELECT * FROM dbo.Customers CROSS APPLY |", "Id", false, false},
		{"SELECT * FROM dbo.Customers CROSS APPLY |", "dbo.Customers", false, false},
		{"SELECT * FROM dbo.Customers CROSS APPLY |", "dbo.fnOrdersFor", false, true},
		{"SELECT * FROM dbo.Customers CROSS APPLY |", "STRING_SPLIT", false, true},
		{"SELECT * FROM dbo.Customers CROSS APPLY |", "VALUES", false, true},
		{"SELECT * FROM dbo.Customers OUTER APPLY dbo.|", "dbo.Customers", false, false},
		{"SELECT * FROM dbo.Customers OUTER APPLY dbo.|", "dbo.fnOrdersFor", false, true},
		{"SELECT * FROM dbo.Customers c CROSS APPLY dbo.fnOrdersFor(c.|)", "Id", false, true},
		{"SELECT * FROM dbo.Customers WHERE EXISTS(fr|)", "FROM", true, true},
		{"SELECT * FROM dbo.Customers WHERE NOT EXISTS(fr|)", "FROM", true, true},
		{"SELECT * FROM dbo.Customers WHERE EXISTS(fr|)", "FROM", false, false},
		{"SELECT Name FROM dbo.Customers FOR JSON PATH wi|", "WITHOUT_ARRAY_WRAPPER", false, true},
		{"SELECT Name FROM dbo.Customers FOR JSON AUTO, wi|", "WITHOUT_ARRAY_WRAPPER", true, true},
		{"SELECT Name FROM dbo.Customers FOR JSON PATH, WITHOUT_ARRAY_WRAPPER wi|", "WITHOUT_ARRAY_WRAPPER", false, false},
		{"SELECT Name FROM dbo.Customers FOR XML PATH wi|", "WITHOUT_ARRAY_WRAPPER", false, false},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			if tt.fabric {
				qp.conn.Server = fabricConn(t).Server
			}
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if got := containsLabel(items, tt.label); got != tt.want {
				t.Fatalf("%s present=%v, want %v in %v", tt.label, got, tt.want, labels(items))
			}
		})
	}
}

func TestSQLKeywordCompletionExpandedContexts(t *testing.T) {
	for _, tt := range []struct {
		sql, label string
		fabric     bool
	}{
		{"SELECT |", "*", false},
		{"SELECT |", "COUNT", false},
		{"SELECT |", "AVG", false},
		{"SELECT DISTINCT co|", "COUNT", false},
		{"SELECT Name, av| FROM dbo.Customers", "AVG", false},
		{"SELECT TOP (10) co|", "COUNT", false},
		{"SELECT * FROM dbo.Customers GROUP BY Name HAVING co|", "COUNT", false},
		{"SELECT * FROM dbo.Customers GROUP BY Name HAVING |", "AVG", false},
		{"SELECT * FROM dbo.Customers GROUP BY Name HAVING COUNT(*) > 1 AND av|", "AVG", false},
		{"fr|", "FROM", true},
		{"FROM dbo.Customers sel|", "SELECT", true},
		{"FROM dbo.Customers c JOIN dbo.Orders o ON c.Id = o.CustomerId sel|", "SELECT", true},
		{"FROM dbo.Customers SELECT co|", "COUNT", true},
		{"SELECT Name FROM dbo.Customers GROUP BY a|", "ALL", true},
		{"SELECT Name FROM dbo.Customers qua|", "QUALIFY", true},
		{"SELECT Name FROM dbo.Customers HAVING COUNT(*) > 1 qua|", "QUALIFY", true},
		{"SELECT Name FROM dbo.Customers QUALIFY ROW_NUMBER() OVER (ORDER BY Id) = 1 ord|", "ORDER BY", true},
		{"SELECT Name FROM dbo.Customers fo|", "FOR JSON", false},
		{"SELECT Name FROM dbo.Customers fo|", "FOR XML", false},
		{"SELECT Name FROM dbo.Customers fo|", "FOR BROWSE", false},
		{"SELECT Name FROM dbo.Customers ORDER BY Name DESC fo|", "FOR JSON", false},
		{"SELECT Name FROM dbo.Customers ord| FOR XML PATH", "ORDER BY", false},
		{"SELECT Name FROM dbo.Customers FOR j|", "JSON", false},
		{"SELECT Name FROM dbo.Customers FOR x|", "XML", false},
		{"SELECT Name FROM dbo.Customers FOR b|", "BROWSE", false},
		{"SELECT Name FROM dbo.Customers FOR JSON p|", "PATH", false},
		{"SELECT Name FROM dbo.Customers FOR XML au|", "AUTO", false},
		{"SELECT Name FROM dbo.Customers fo|", "FOR JSON", true},
		{"SELECT * FROM dbo.Customers cr|", "CROSS APPLY", false},
		{"SELECT * FROM dbo.Customers ou|", "OUTER APPLY", false},
		{"SELECT * FROM dbo.Customers CROSS a|", "APPLY", false},
		{"SELECT * FROM dbo.Customers OUTER a|", "APPLY", false},
		{"SELECT * FROM dbo.Customers cr| APPLY dbo.fn(1)", "CROSS APPLY", false},
	} {
		t.Run(tt.sql+"/"+tt.label, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			if tt.fabric {
				qp.conn.Server = fabricConn(t).Server
			}
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if !containsLabel(items, tt.label) {
				t.Fatalf("items %v missing %q", labels(items), tt.label)
			}
		})
	}
}

func TestSQLKeywordCompletionDialectAndClauseBoundaries(t *testing.T) {
	for _, tt := range []struct {
		sql, absent string
		fabric      bool
	}{
		{"fr|", "FROM", false},
		{"SELECT Name FROM dbo.Customers qua|", "QUALIFY", false},
		{"SELECT Name FROM dbo.Customers fo|", "FOR XML", true},
		{"SELECT Name FROM dbo.Customers fo|", "FOR BROWSE", true},
		{"SELECT Name FROM dbo.Customers FOR b|", "BROWSE", true},
		{"SELECT Name FROM dbo.Customers FOR x|", "XML", true},
		{"SELECT (SELECT Id FROM dbo.Orders fo|) FROM dbo.Customers", "FOR JSON", true},
		{"SELECT Name FROM dbo.Customers QUALIFY Id = 1 wh|", "WHERE", true},
		{"SELECT Name FROM dbo.Customers GROUP BY Name a|", "ALL", true},
		{"SELECT Name FROM dbo.Customers ord| QUALIFY Id = 1", "ORDER BY", true},
		{"SELECT Name FROM dbo.Customers fo| WHERE Id = 1", "FOR JSON", false},
		{"SELECT Name FROM dbo.Customers FOR JSON PATH ord|", "ORDER BY", false},
		{"SELECT Name FROM dbo.Customers fo| FOR JSON PATH", "FOR JSON", false},
		{"SELECT Name FROM dbo.Customers ORDER BY Name a| ASC", "ASC", false},
		{"SELECT Name FROM dbo.Customers WHERE Id = co|", "COUNT", false},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			if tt.fabric {
				qp.conn.Server = fabricConn(t).Server
			}
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if containsLabel(items, tt.absent) {
				t.Fatalf("items %v unexpectedly contain %q", labels(items), tt.absent)
			}
		})
	}
}

func TestSQLFunctionCompletionInsertsCallAtCursor(t *testing.T) {
	for _, tt := range []struct{ before, typed, after, want string }{
		{"SELECT ", "cou", "", "SELECT COUNT(|)"},
		{"SELECT ", "av", "", "SELECT AVG(|)"},
		{"SELECT ", "av", ", AVG(Id) FROM dbo.Customers", "SELECT AVG(|), AVG(Id) FROM dbo.Customers"},
		{"SELECT ", "subs", "", "SELECT SUBSTRING(|)"},
		{"SELECT ", "ab", "", "SELECT ABS(|)"},
		{"SELECT ", "cou", "(Id)", "SELECT COUNT(|Id)"},
		{"SELECT ", "cou", " /* keep */ (Id)", "SELECT COUNT /* keep */ (|Id)"},
		{"SELECT ", "cou", "\n(Id)", "SELECT COUNT\n(|Id)"},
		{"SELECT * FROM ", "openj", "", "SELECT * FROM OPENJSON(|)"},
		{"SELECT * FROM dbo.Customers c CROSS APPLY ", "string_s", "", "SELECT * FROM dbo.Customers c CROSS APPLY STRING_SPLIT(|)"},
		{"SELECT Name FROM dbo.Customers GROUP BY ", "gro", " SETS(Name)", "SELECT Name FROM dbo.Customers GROUP BY GROUPING SETS(|Name)"},
	} {
		for _, accept := range []struct {
			name string
			key  tcell.Key
		}{{"Tab", tcell.KeyTab}, {"Enter", tcell.KeyEnter}} {
			t.Run(tt.want+"/"+accept.name, func(t *testing.T) {
				qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
				qp.editor.SetText(tt.before + tt.after)
				qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModCtrl))
				for range []rune(tt.after) {
					qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
				}
				for _, r := range tt.typed {
					qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
				}
				if !qp.editor.OverlayActive() {
					t.Fatal("function popup did not open")
				}
				qp.editor.HandleKey(tcell.NewEventKey(accept.key, "", tcell.ModNone))
				if got, want := qp.editor.Text(), strings.Replace(tt.want, "|", "", 1); got != want {
					t.Fatalf("Text() = %q, want %q", got, want)
				}
				qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, "x", tcell.ModNone))
				if got, want := qp.editor.Text(), strings.Replace(tt.want, "|", "x", 1); got != want {
					t.Fatalf("typing at cursor produced %q, want %q", got, want)
				}
			})
		}
	}
}

func TestSQLFunctionCompletionLabelsAndContexts(t *testing.T) {
	for _, tt := range []struct{ sql, want, absent string }{
		{"SELECT co|", "COUNT", "COUNT("},
		{"SELECT ab|", "ABS", "ABS()"},
		{"SELECT sub|", "SUBSTRING", "SUBSTRING("},
		{"SELECT * FROM openr|", "OPENROWSET", "OPENROWSET("},
		{"SELECT * FROM dbo.Customers JOIN openj|", "OPENJSON", "OPENJSON()"},
		{"SELECT openj|", "", "OPENJSON"},
		{"UPDATE openj|", "", "OPENJSON"},
		{"SELECT c.ab| FROM dbo.Customers c", "", "ABS"},
		{"SELECT * FROM dbo.openj|", "", "OPENJSON"},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if tt.want != "" && !containsLabel(items, tt.want) {
				t.Errorf("items %v missing %q", labels(items), tt.want)
			}
			if containsLabel(items, tt.absent) {
				t.Errorf("items %v unexpectedly contain %q", labels(items), tt.absent)
			}
		})
	}
}

func TestSQLCompletionPrefixColumnPrecedesPartialReference(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT id| FROM dbo.Customers xId")
	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if got, want := strings.Join(labels(items), ","), "Id,xId"; got != want {
		t.Fatalf("matching order = %q, want %q", got, want)
	}
}

func TestSQLCompletionOnlyKeywordsAfterSource(t *testing.T) {
	for _, tt := range []struct {
		sql    string
		fabric bool
		want   []string
	}{
		{"CREATE |", false, []string{"TABLE", "VIEW", "PROCEDURE", "FUNCTION", "INDEX"}},
		{"ALTER |", false, []string{"TABLE", "VIEW", "PROCEDURE", "FUNCTION", "INDEX"}},
		{"CREATE OR ALTER |", false, []string{"VIEW", "PROCEDURE", "FUNCTION", "TRIGGER"}},
		{"CREATE /* keep */ |", false, []string{"TABLE", "VIEW"}},
		{"SELECT 1;\nALTER |", false, []string{"TABLE", "VIEW"}},
		{"CREATE |", true, []string{"TABLE", "VIEW", "PROCEDURE", "FUNCTION", "SCHEMA"}},
		{"ALTER |", true, []string{"TABLE", "VIEW", "PROCEDURE", "FUNCTION", "SCHEMA"}},
		{"UPDATE dbo.Customers |", false, []string{"SET"}},
		{"UPDATE TOP (10) dbo.Customers |", false, []string{"SET"}},
		{"UPDATE c | FROM dbo.Customers c", false, []string{"SET"}},
		{"UPDATE dbo.Customers se| SET Name = 'x'", false, nil},
		{"SELECT * FROM dbo.Customers t |", false, []string{"JOIN", "WHERE", "GROUP BY"}},
		{"FROM dbo.Customers t |", true, []string{"JOIN", "WHERE", "GROUP BY", "SELECT"}},
		{"SELECT * FROM dbo.Customers |", false, []string{"JOIN", "WHERE", "GROUP BY"}},
		{"SELECT * FROM dbo.Customers AS t |", false, []string{"JOIN", "WHERE", "GROUP BY"}},
		{"SELECT * FROM dbo.Customers [t] |", false, []string{"JOIN", "WHERE", "GROUP BY"}},
		{"SELECT * FROM (SELECT Id FROM dbo.Customers) t |", false, []string{"JOIN", "WHERE", "GROUP BY"}},
		{"SELECT * FROM dbo.Customers c JOIN dbo.Orders o |", false, []string{"ON"}},
		{"SELECT * FROM dbo.Customers t cus|", false, nil},
		{"SELECT * FROM dbo.Customers t wh|", false, []string{"WHERE"}},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			if tt.fabric {
				qp.conn.Server = fabricConn(t).Server
			}
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			for _, item := range items {
				if item.Detail != "keyword" {
					t.Fatalf("unexpected identifier %q after completed source", item.Label)
				}
			}
			for _, want := range tt.want {
				if !containsLabel(items, want) {
					t.Errorf("items %v missing %q", labels(items), want)
				}
			}
		})
	}
}

func TestSQLCompletionGroupByAllFirst(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	qp.conn.Server = fabricConn(t).Server
	lines, row, col := linesAndCursor(t, "SELECT Name FROM dbo.Customers GROUP BY |")
	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if len(items) == 0 || items[0].Label != "ALL" {
		t.Fatalf("items %v do not start with ALL", labels(items))
	}
}

func TestSQLCompletionHavingAggregatesFirst(t *testing.T) {
	for _, fabric := range []bool{false, true} {
		for _, sql := range []string{
			"SELECT Name FROM dbo.Customers c GROUP BY Name HAVING |",
			"SELECT Name FROM dbo.Customers c GROUP BY Name HAVING COUNT(*) > 1 AND |",
			"SELECT Name FROM dbo.Customers c GROUP BY Name HAVING (|)",
		} {
			name := sql
			if fabric {
				name += "/Fabric"
			}
			t.Run(name, func(t *testing.T) {
				qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
				if fabric {
					qp.conn.Server = fabricConn(t).Server
				}
				lines, row, col := linesAndCursor(t, sql)
				items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
				first := "APPROX_COUNT_DISTINCT"
				if fabric {
					first = "ANY_VALUE"
				}
				if len(items) == 0 || items[0].Label != first {
					t.Fatalf("first aggregate = %v, want %s", labels(items), first)
				}
				avg, name, scalar, ref, anyValue := -1, -1, -1, -1, -1
				for i, item := range items {
					switch item.Label {
					case "AVG":
						avg = i
					case "Name":
						name = i
					case "ABS":
						scalar = i
					case "c":
						ref = i
					case "ANY_VALUE":
						anyValue = i
					}
				}
				if avg < 0 || name <= avg || scalar <= avg || ref <= avg {
					t.Fatalf("AVG must precede columns, references and scalar functions: %v", labels(items))
				}
				if fabric && (anyValue < 0 || anyValue >= avg) || !fabric && anyValue >= 0 {
					t.Fatalf("unexpected ANY_VALUE ordering/availability: %v", labels(items))
				}
			})
		}
	}
}

func TestSQLCompletionWindowVersion(t *testing.T) {
	for _, version := range []string{"15.0.2000.5", "16.0.1000.6"} {
		t.Run(version, func(t *testing.T) {
			sc, _ := newGConnAtVersion(t, version)
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			qp.conn.Server = sc.Server
			lines, row, col := linesAndCursor(t, "SELECT Name FROM dbo.Customers WHERE Id > 0 win|")
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if got, want := containsLabel(items, "WINDOW"), strings.HasPrefix(version, "16."); got != want {
				t.Fatalf("WINDOW present=%v, want %v in %v", got, want, labels(items))
			}
		})
	}
}

func TestSQLCompletionCrossJoinContinuations(t *testing.T) {
	for _, fabric := range []bool{false, true} {
		for _, sql := range []string{
			"SELECT * FROM dbo.Customers c CROSS JOIN dbo.Orders o |",
			"SELECT * FROM dbo.Customers c CROSS /* join kind */ JOIN dbo.Orders AS o |",
			"SELECT * FROM dbo.Customers c CROSS JOIN (SELECT Id FROM dbo.Orders) o |",
			"SELECT * FROM dbo.Customers c CROSS JOIN OPENJSON('[]') j |",
		} {
			t.Run(sql, func(t *testing.T) {
				qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
				if fabric {
					qp.conn.Server = fabricConn(t).Server
				}
				lines, row, col := linesAndCursor(t, sql)
				items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
				for _, want := range []string{"WHERE", "JOIN", "GROUP BY", "ORDER BY"} {
					if !containsLabel(items, want) {
						t.Errorf("items %v missing %q", labels(items), want)
					}
				}
				for _, item := range items {
					if item.Detail != "keyword" || item.Label == "ON" {
						t.Errorf("invalid CROSS JOIN continuation: %+v", item)
					}
				}
			})
		}
	}
}

func TestSQLCompletionAcceptsKeywordAfterSourceAlias(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	qp.editor.SetText("SELECT * FROM dbo.Customers t ")
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModCtrl))
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModCtrl))
	if !qp.editor.OverlayActive() {
		t.Fatal("Ctrl+Space did not open source clause suggestions")
	}
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
	if got, want := qp.editor.Text(), "SELECT * FROM dbo.Customers t WHERE "; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

func TestSQLCompletionApplicableKeywordsAlwaysFirst(t *testing.T) {
	for _, tt := range []struct{ sql, want string }{
		{"SELECT | FROM dbo.Customers c", "TOP"},
		{"SELECT * FROM dbo.Customers c WHERE Id = 1 |", "AND"},
		{"SELECT * FROM dbo.Customers c ORDER BY Name |", "ASC"},
		{"SELECT * FROM dbo.Customers c ORDER BY Name des|", "DESC"},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if !containsLabel(items, tt.want) {
				t.Fatalf("items %v missing keyword %q", labels(items), tt.want)
			}
			seenOther := false
			for _, item := range items {
				if item.Detail != "keyword" {
					seenOther = true
				} else if seenOther {
					t.Fatalf("keyword %q follows other suggestions in %v", item.Label, labels(items))
				}
			}
		})
	}
}

func TestSQLCompletionContextOrdering(t *testing.T) {
	for _, sql := range []string{"SELECT * FROM |", "SELECT * FROM dbo.Customers JOIN |", "SELECT * FROM dbo.Customers t, |"} {
		t.Run(sql, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "aaa_db", testCustomersOrders())
			lines, row, col := linesAndCursor(t, sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			want := "dbo,sales,dbo.Customers,dbo.Orders,dbo.vActiveCustomers,sales.Region,GENERATE_SERIES,OPENJSON,OPENROWSET,REGEXP_MATCHES,REGEXP_SPLIT_TO_TABLE,STRING_SPLIT,VECTOR_SEARCH,aaa_db"
			if got := strings.Join(labels(items), ","); got != want {
				t.Fatalf("source ordering = %q, want %q", got, want)
			}
		})
	}
	for _, sql := range []string{
		"SELECT | FROM dbo.Customers z JOIN dbo.Orders a ON z.Id = a.CustomerId",
		"SELECT * FROM dbo.Customers z JOIN dbo.Orders a ON z.Id = a.CustomerId WHERE |",
		"SELECT * FROM dbo.Customers z JOIN dbo.Orders a ON z.Id = a.CustomerId ORDER BY |",
	} {
		t.Run(sql, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			lines, row, col := linesAndCursor(t, sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			for len(items) > 0 && items[0].Detail == "keyword" {
				items = items[1:]
			}
			want := "a,z,CustomerId,Email,Id,Name,Total"
			if got := strings.Join(labels(items), ","); got != want && !strings.HasPrefix(got, want+",") {
				t.Fatalf("expression ordering = %q, want references then columns %q", got, want)
			}
		})
	}
}

func TestSQLKeywordCompletionKeepsFullyTypedKeywords(t *testing.T) {
	for _, sql := range []string{
		"SELECT",
		"SELECT * FROM",
		"SELECT * FROM dbo.Customers WHERE",
		"SELECT * FROM dbo.Customers WHERE Id = 1 OR",
		"SELECT * FROM dbo.Customers c JOIN dbo.Orders o ON",
	} {
		t.Run(sql, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			split := strings.LastIndex(sql, " ") + 1
			qp.editor.SetText(sql[:split])
			qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModCtrl))
			for _, r := range sql[split:] {
				qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
			}
			if !qp.editor.OverlayActive() {
				t.Fatal("popup closed after typing the full keyword")
			}
			qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
			if got, want := qp.editor.Text(), sql+" "; got != want {
				t.Fatalf("Enter produced %q, want %q", got, want)
			}
		})
	}
}

func TestSQLKeywordCompletionAllowsTypingPastFullKeyword(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	qp.editor.SetText("SELECT * FROM dbo.Customers c JOIN dbo.Orders o ")
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModCtrl))
	for _, r := range "on" {
		qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
	}
	if !qp.editor.OverlayActive() {
		t.Fatal("ON suggestion disappeared")
	}
	for _, r := range "ward" {
		qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
	}
	if got, want := qp.editor.Text(), "SELECT * FROM dbo.Customers c JOIN dbo.Orders o onward"; got != want {
		t.Fatalf("continued typing produced %q, want %q", got, want)
	}
	if qp.editor.OverlayActive() {
		t.Fatal("ON suggestion remained for a nonmatching word")
	}
}

func TestSQLKeywordCompletionGroupByOptions(t *testing.T) {
	for _, tt := range []struct {
		sql, label string
		fabric     bool
		absent     bool
	}{
		{"SELECT Name FROM dbo.Customers GROUP BY Name |", "WITH ROLLUP", false, true},
		{"SELECT Name FROM dbo.Customers GROUP BY Name |", "WITH CUBE", false, true},
		{"SELECT Name FROM dbo.Customers GROUP BY |", "ROLLUP", false, false},
		{"SELECT Name FROM dbo.Customers GROUP BY |", "ROLLUP", true, false},
		{"SELECT Name FROM dbo.Customers GROUP BY |", "CUBE", false, false},
		{"SELECT Name FROM dbo.Customers GROUP BY |", "CUBE", true, false},
		{"SELECT Name FROM dbo.Customers GROUP BY Name, |", "GROUPING SETS", false, false},
		{"SELECT Name FROM dbo.Customers GROUP BY Name, |", "GROUPING SETS", true, false},
		{"SELECT Name FROM dbo.Customers GROUP BY |", "ALL", true, false},
		{"SELECT Name FROM dbo.Customers GROUP BY |", "ALL", false, true},
		{"SELECT Name FROM dbo.Customers GROUP BY Name |", "WITH ROLLUP", true, true},
	} {
		t.Run(tt.sql+"/"+tt.label, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			if tt.fabric {
				qp.conn.Server = fabricConn(t).Server
			}
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if containsLabel(items, tt.label) == tt.absent {
				t.Fatalf("items %v: want %q present = %v", labels(items), tt.label, !tt.absent)
			}
		})
	}
}

func TestSQLKeywordCompletionDDLAndExecuteStarters(t *testing.T) {
	for _, sql := range []string{"|", "SELECT 1;\n|", "SELECT 1\nGO\n|"} {
		t.Run(sql, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			lines, row, col := linesAndCursor(t, sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			for _, label := range []string{"CREATE", "ALTER", "DROP", "TRUNCATE", "EXEC", "EXECUTE"} {
				if !containsLabel(items, label) {
					t.Errorf("items %v missing starter %q", labels(items), label)
				}
			}
			for _, item := range items {
				if item.Detail != "keyword" && item.Detail != "stored procedure" {
					t.Errorf("unexpected statement-start suggestion %q (%s)", item.Label, item.Detail)
				}
			}
		})
	}
}

func TestSQLCompletionStoredProceduresAtStatementStart(t *testing.T) {
	for _, tt := range []struct{ sql, label, text string }{
		{"|", "dbo.GetCustomers", "dbo.GetCustomers"},
		{"get|", "dbo.GetCustomers", "dbo.GetCustomers"},
		{"-- batch comment\nget|", "dbo.GetCustomers", "dbo.GetCustomers"},
		{"SELECT 1;\nGO\nget|", "dbo.GetCustomers", "dbo.GetCustomers"},
		{"dbo.|", "dbo.GetCustomers", "GetCustomers"},
		{"EXEC |", "dbo.GetCustomers", "dbo.GetCustomers"},
		{"EXECUTE dbo.get|", "dbo.GetCustomers", "GetCustomers"},
		{"EXEC @rc = dbo.get|", "dbo.GetCustomers", "GetCustomers"},
		{"EXECUTE @rc = get|", "dbo.GetCustomers", "dbo.GetCustomers"},
		{"DECLARE @rc INT; EXEC @rc = dbo.get|", "dbo.GetCustomers", "GetCustomers"},
		{"DECLARE @rc INT; EXEC @rc /* status */ = dbo.get|", "dbo.GetCustomers", "GetCustomers"},
		{"SELECT 1;\nEXEC get|", "dbo.GetCustomers", "dbo.GetCustomers"},
		{"SELECT 1;\nEXECUTE dbo.get|", "dbo.GetCustomers", "GetCustomers"},
		{"|", "odd schema.Get Customers", "[odd schema].[Get Customers]"},
	} {
		t.Run(tt.sql+"/"+tt.label, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			inv := qp.app.completion.inventories[completionInventoryKey(qp.conn.Opts, qp.database)]
			inv.procedures = []*gosmo.StoredProcedure{
				{Schema: "dbo", Name: "GetCustomers"},
				{Schema: "odd schema", Name: "Get Customers"},
			}
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			found := false
			for _, item := range items {
				if item.Detail != "keyword" && item.Detail != "stored procedure" {
					t.Errorf("unexpected suggestion %q (%s)", item.Label, item.Detail)
				}
				if item.Label == tt.label {
					found = true
					if item.Text != tt.text {
						t.Errorf("insert = %q, want %q", item.Text, tt.text)
					}
				}
			}
			if !found {
				t.Fatalf("items %v missing procedure %q", labels(items), tt.label)
			}
		})
	}
}

func TestSQLCompletionStoredProceduresRequireExecuteAfterStatement(t *testing.T) {
	for _, sql := range []string{
		"SELECT 1;\n|",
		"SELECT 1;\nget|",
		"SELECT 1;\ndbo.get|",
		"SELECT 1;\ndbo.[Get|",
		"SELECT 1;\n-- GO\nget|",
		"SELECT 'GO';\nget|",
		"SELECT 1;\n/* GO */\nget|",
		"EXEC @sql dbo.get|",
		"EXEC @rc + dbo.get|",
	} {
		t.Run(sql, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			inv := qp.app.completion.inventories[completionInventoryKey(qp.conn.Opts, qp.database)]
			inv.procedures = []*gosmo.StoredProcedure{{Schema: "dbo", Name: "GetCustomers"}}
			lines, row, col := linesAndCursor(t, sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			for _, item := range items {
				if item.Detail != "keyword" {
					t.Errorf("only keywords are valid before EXEC in a later statement, got %+v", item)
				}
			}
		})
	}
}

func TestSQLCompletionAcceptsProcedureInLaterBatchOrExplicitCall(t *testing.T) {
	for _, tt := range []struct{ before, want string }{
		{"SELECT 1;\nGO\n", "SELECT 1;\nGO\ndbo.GetCustomers "},
		{"SELECT 1;\nEXEC ", "SELECT 1;\nEXEC dbo.GetCustomers "},
		{"SELECT 1;\nEXECUTE dbo.", "SELECT 1;\nEXECUTE dbo.GetCustomers "},
		{"DECLARE @rc INT;\nEXEC @rc = dbo.", "DECLARE @rc INT;\nEXEC @rc = dbo.GetCustomers "},
	} {
		t.Run(tt.before, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
			inv := qp.app.completion.inventories[completionInventoryKey(qp.conn.Opts, qp.database)]
			inv.procedures = []*gosmo.StoredProcedure{{Schema: "dbo", Name: "GetCustomers"}}
			qp.editor.SetText(tt.before)
			qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModCtrl))
			for _, r := range "get" {
				qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
			}
			if !qp.editor.OverlayActive() {
				t.Fatal("procedure popup did not open")
			}
			qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
			if got := qp.editor.Text(); got != tt.want {
				t.Fatalf("accepting procedure produced %q, want %q", got, tt.want)
			}
		})
	}
}
