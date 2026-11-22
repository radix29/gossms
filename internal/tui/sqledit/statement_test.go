package sqledit

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/radix29/tuikit/controls/editor"
)

func newTestEditor(text string) *editor.Editor {
	e := editor.NewEditor(nil)
	e.SetText(text)
	return e
}

// setCursor puts e's cursor at (row, col) with nothing selected.
func setCursor(e *editor.Editor, row, col int) {
	e.SelectRange(row, col, row, col)
	e.ClearSelection()
}

func TestSelectStatementAtCursorSemicolonSeparated(t *testing.T) {
	e := newTestEditor("SELECT 1;\nSELECT 2;")

	setCursor(e, 0, 3)
	if !SelectStatementAtCursor(e) {
		t.Fatal("expected a statement to be selected")
	}
	if got := e.SelectedText(); got != "SELECT 1;" {
		t.Fatalf("first statement = %q, want %q", got, "SELECT 1;")
	}

	setCursor(e, 1, 3)
	if !SelectStatementAtCursor(e) {
		t.Fatal("expected a statement to be selected")
	}
	if got := e.SelectedText(); got != "SELECT 2;" {
		t.Fatalf("second statement = %q, want %q", got, "SELECT 2;")
	}
}

func TestSelectStatementAtCursorGoSeparated(t *testing.T) {
	e := newTestEditor("SELECT 1\nGO\nSELECT 2\nGO\n")

	setCursor(e, 0, 3)
	if !SelectStatementAtCursor(e) || e.SelectedText() != "SELECT 1" {
		t.Fatalf("first batch = %q, want %q", e.SelectedText(), "SELECT 1")
	}

	setCursor(e, 2, 3)
	if !SelectStatementAtCursor(e) || e.SelectedText() != "SELECT 2" {
		t.Fatalf("second batch = %q, want %q", e.SelectedText(), "SELECT 2")
	}
}

func TestSelectStatementAtCursorIgnoresGoLikeIdentifier(t *testing.T) {
	// "goto_flag" starts with "go" but isn't a standalone GO separator —
	// the same rule the executor splits batches by (sqltext.GoSeparatorAt).
	e := newTestEditor("SELECT goto_flag\nFROM t;")

	setCursor(e, 1, 0)
	if !SelectStatementAtCursor(e) {
		t.Fatal("expected a statement to be selected")
	}
	want := "SELECT goto_flag\nFROM t;"
	if got := e.SelectedText(); got != want {
		t.Fatalf("statement = %q, want %q", got, want)
	}
}

func TestSelectStatementAtCursorIgnoresSemicolonInStringLiteral(t *testing.T) {
	e := newTestEditor("SELECT 'a;b';\nSELECT 2;")

	setCursor(e, 0, 9) // inside the string literal, on the fake ';'
	if !SelectStatementAtCursor(e) {
		t.Fatal("expected a statement to be selected")
	}
	want := "SELECT 'a;b';"
	if got := e.SelectedText(); got != want {
		t.Fatalf("statement = %q, want %q — the ';' inside the string literal must not split it", got, want)
	}
}

func TestSelectStatementAtCursorIgnoresSemicolonInBlockComment(t *testing.T) {
	e := newTestEditor("SELECT 1 /* ; */;\nSELECT 2;")

	setCursor(e, 0, 0)
	if !SelectStatementAtCursor(e) {
		t.Fatal("expected a statement to be selected")
	}
	want := "SELECT 1 /* ; */;"
	if got := e.SelectedText(); got != want {
		t.Fatalf("statement = %q, want %q — the ';' inside the comment must not split it", got, want)
	}
}

func TestSelectStatementAtCursorTrimsSurroundingBlankLines(t *testing.T) {
	e := newTestEditor("\n\nSELECT 1;\n\nSELECT 2;\n")

	setCursor(e, 2, 3)
	if !SelectStatementAtCursor(e) {
		t.Fatal("expected a statement to be selected")
	}
	if got := e.SelectedText(); got != "SELECT 1;" {
		t.Fatalf("statement = %q, want %q — blank lines around it should be trimmed", got, "SELECT 1;")
	}
}

func TestSelectStatementAtCursorNoSemicolonBetweenStatements(t *testing.T) {
	e := newTestEditor("SELECT 1\nSELECT 2")

	setCursor(e, 0, 3)
	if !SelectStatementAtCursor(e) || e.SelectedText() != "SELECT 1" {
		t.Fatalf("first statement = %q, want %q", e.SelectedText(), "SELECT 1")
	}

	setCursor(e, 1, 3)
	if !SelectStatementAtCursor(e) || e.SelectedText() != "SELECT 2" {
		t.Fatalf("second statement = %q, want %q", e.SelectedText(), "SELECT 2")
	}
}

func TestSelectStatementAtCursorFromFirst(t *testing.T) {
	for _, query := range []string{
		"FROM dbo.Customers c\nSELECT c.Name",
		"FROM dbo.Customers c JOIN dbo.Orders o ON c.Id = o.CustomerId\nSELECT c.Name, o.Total",
		"FROM dbo.Customers c CROSS APPLY dbo.f(c.Id) x OUTER APPLY dbo.g(x.Id) y\nSELECT c.Name, y.Id",
		"WITH c AS (FROM dbo.Customers SELECT Name)\nFROM c SELECT Name",
		"FROM t SELECT a UNION ALL FROM u SELECT b",
		"INSERT INTO archive (a)\nFROM t SELECT a",
		"CREATE VIEW v AS FROM t SELECT a",
		"FROM t AS window SELECT a",
		"FROM t SELECT a UNION SELECT b FROM u",
		"SELECT * FROM t FOR SYSTEM_TIME FROM @a TO @b",
		"DELETE FROM t FROM t JOIN u ON t.a = u.a",
		"DECLARE c CURSOR FOR SELECT a FROM t",
		"DECLARE c CURSOR FOR FROM t SELECT a",
		"BULK INSERT t FROM 'data.csv'",
		"RESTORE DATABASE d FROM DISK = 'backup.bak'",
		"RECEIVE TOP (1) * FROM q",
		"SELECT * FROM t WHERE a IS DISTINCT FROM b",
		"SELECT * FROM t WHERE a IS NOT DISTINCT FROM b",
		"SELECT * FROM t ORDER BY ALL DESC",
	} {
		t.Run(query, func(t *testing.T) {
			e := newTestEditor("SELECT 0;\n" + query + ";\nSELECT 2;")
			for offset := range len(query) {
				before := query[:offset]
				row := 1 + strings.Count(before, "\n")
				col := len(before) - strings.LastIndex(before, "\n") - 1
				setCursor(e, row, col)
				if !SelectStatementAtCursor(e) || e.SelectedText() != query+";" {
					t.Fatalf("cursor %d: selected %q, want %q", offset, e.SelectedText(), query+";")
				}
			}
		})
	}
}

func TestSelectStatementAtCursorFromFirstDoesNotAbsorbExec(t *testing.T) {
	for _, query := range []string{
		"FROM t",
		"INSERT INTO archive FROM t",
		"WITH c AS (FROM t) FROM c",
		"FROM t SELECT a UNION ALL FROM u",
	} {
		for _, exec := range []string{"EXEC p", "EXECUTE p"} {
			t.Run(query+"/"+exec, func(t *testing.T) {
				e := newTestEditor(query + "\n" + exec)
				setCursor(e, 0, 0)
				if !SelectStatementAtCursor(e) || e.SelectedText() != query {
					t.Fatalf("query selection = %q, want %q", e.SelectedText(), query)
				}
				setCursor(e, 1, 0)
				if !SelectStatementAtCursor(e) || e.SelectedText() != exec {
					t.Fatalf("EXEC selection = %q, want %q", e.SelectedText(), exec)
				}
			})
		}
	}
}

func TestSelectStatementAtCursorFromFirstWithoutProjection(t *testing.T) {
	for _, source := range []string{
		"FROM t",
		"WITH c AS (FROM t) FROM c",
		"INSERT INTO archive FROM t",
		"FROM t SELECT a UNION ALL FROM u",
	} {
		for _, clause := range []string{
			"WHERE a = 1", "GROUP BY a", "HAVING COUNT(*) > 0", "QUALIFY a = 1",
			"ORDER BY ALL", "WINDOW w AS (ORDER BY a)", "FOR JSON PATH",
		} {
			query := source + " " + clause
			t.Run(query, func(t *testing.T) {
				e := newTestEditor(query + "\nSELECT 2")
				setCursor(e, 0, 0)
				if !SelectStatementAtCursor(e) || e.SelectedText() != query {
					t.Fatalf("query selection = %q, want %q", e.SelectedText(), query)
				}
				setCursor(e, 1, 0)
				if !SelectStatementAtCursor(e) || e.SelectedText() != "SELECT 2" {
					t.Fatalf("next selection = %q, want SELECT 2", e.SelectedText())
				}
			})
		}
	}
}

func TestSelectStatementAtCursorFromFirstStacked(t *testing.T) {
	for _, first := range []string{
		"FROM t SELECT a",
		"SELECT a FROM t",
		"FROM t WHERE a = 1",
		"WITH c AS (FROM t) FROM c SELECT a",
		"FROM t SELECT a UNION SELECT b FROM u",
	} {
		t.Run(first, func(t *testing.T) {
			second := "FROM v SELECT c"
			e := newTestEditor(first + "\n" + second)
			for row, want := range []string{first, second} {
				setCursor(e, row, 0)
				if !SelectStatementAtCursor(e) || e.SelectedText() != want {
					t.Fatalf("row %d: selected %q, want %q", row, e.SelectedText(), want)
				}
			}
		})
	}
}

func TestSelectStatementAtCursorFromFirstAfterOtherStatements(t *testing.T) {
	for _, first := range []string{
		"USE db", "PRINT 1", "EXEC p", "SET NOCOUNT ON", "DECLARE @i int",
		"INSERT INTO t VALUES (1)", "INSERT INTO t EXEC p", "BEGIN",
	} {
		t.Run(first, func(t *testing.T) {
			second := "FROM u SELECT b"
			e := newTestEditor(first + "\n" + second)
			for row, want := range []string{first, second} {
				setCursor(e, row, 0)
				if !SelectStatementAtCursor(e) || e.SelectedText() != want {
					t.Fatalf("row %d: selected %q, want %q", row, e.SelectedText(), want)
				}
			}
		})
	}
}

// A Service Broker statement's own FROM is not a FROM-first query: splitting
// there made F5 run "FROM SERVICE ..." alone.
func TestSelectStatementAtCursorServiceBrokerFromIsNotFromFirst(t *testing.T) {
	for _, stmt := range []string{
		"BEGIN DIALOG CONVERSATION @h\nFROM SERVICE [s] TO SERVICE 't'",
		"BEGIN TRAN RECEIVE TOP (1) @m = message_body\nFROM q",
		"IF @x = 1 GET CONVERSATION GROUP @g\nFROM q",
	} {
		t.Run(stmt, func(t *testing.T) {
			e := newTestEditor(stmt + "\nFROM u SELECT b")
			setCursor(e, 1, 0)
			if !SelectStatementAtCursor(e) || e.SelectedText() != stmt {
				t.Fatalf("selected %q, want %q", e.SelectedText(), stmt)
			}
			setCursor(e, 2, 0)
			if !SelectStatementAtCursor(e) || e.SelectedText() != "FROM u SELECT b" {
				t.Fatalf("selected %q, want the FROM-first query", e.SelectedText())
			}
		})
	}
}

func TestSelectStatementAtCursorThreeStatementsNoSemicolons(t *testing.T) {
	e := newTestEditor("SELECT * FROM Patients\nSELECT * FROM Doctors\nUPDATE Foo SET Bar = 1")

	setCursor(e, 1, 5)
	if !SelectStatementAtCursor(e) || e.SelectedText() != "SELECT * FROM Doctors" {
		t.Fatalf("middle statement = %q, want %q", e.SelectedText(), "SELECT * FROM Doctors")
	}

	setCursor(e, 2, 5)
	if !SelectStatementAtCursor(e) || e.SelectedText() != "UPDATE Foo SET Bar = 1" {
		t.Fatalf("last statement = %q, want %q", e.SelectedText(), "UPDATE Foo SET Bar = 1")
	}
}

func TestSelectStatementAtCursorUnionedSelectsShareOneStatement(t *testing.T) {
	e := newTestEditor("SELECT Id FROM A\nUNION ALL\nSELECT Id FROM B\nSELECT Id FROM C")

	setCursor(e, 2, 3)
	want := "SELECT Id FROM A\nUNION ALL\nSELECT Id FROM B"
	if !SelectStatementAtCursor(e) || e.SelectedText() != want {
		t.Fatalf("UNION'd statement = %q, want %q", e.SelectedText(), want)
	}

	setCursor(e, 3, 3)
	if !SelectStatementAtCursor(e) || e.SelectedText() != "SELECT Id FROM C" {
		t.Fatalf("statement after UNION chain = %q, want %q", e.SelectedText(), "SELECT Id FROM C")
	}
}

func TestSelectStatementAtCursorCTEMainQueryNotSplitFromWith(t *testing.T) {
	e := newTestEditor("WITH cte AS (SELECT Id FROM A)\nSELECT Id FROM cte\nSELECT Id FROM B")

	setCursor(e, 1, 3)
	want := "WITH cte AS (SELECT Id FROM A)\nSELECT Id FROM cte"
	if !SelectStatementAtCursor(e) || e.SelectedText() != want {
		t.Fatalf("CTE statement = %q, want %q", e.SelectedText(), want)
	}

	setCursor(e, 2, 3)
	if !SelectStatementAtCursor(e) || e.SelectedText() != "SELECT Id FROM B" {
		t.Fatalf("statement after CTE = %q, want %q", e.SelectedText(), "SELECT Id FROM B")
	}
}

func TestSelectStatementAtCursorInsertSelectSharesOneStatement(t *testing.T) {
	e := newTestEditor("INSERT INTO A\nSELECT Id FROM B\nSELECT Id FROM C")

	setCursor(e, 1, 3)
	want := "INSERT INTO A\nSELECT Id FROM B"
	if !SelectStatementAtCursor(e) || e.SelectedText() != want {
		t.Fatalf("INSERT...SELECT statement = %q, want %q", e.SelectedText(), want)
	}
}

func TestSelectStatementAtCursorKeywordInsideParensNotABoundary(t *testing.T) {
	e := newTestEditor("SELECT * FROM A WHERE Id IN (SELECT Id FROM B)\nSELECT * FROM C")

	setCursor(e, 0, 5)
	want := "SELECT * FROM A WHERE Id IN (SELECT Id FROM B)"
	if !SelectStatementAtCursor(e) || e.SelectedText() != want {
		t.Fatalf("statement with subquery = %q, want %q", e.SelectedText(), want)
	}

	setCursor(e, 1, 3)
	if !SelectStatementAtCursor(e) || e.SelectedText() != "SELECT * FROM C" {
		t.Fatalf("statement after subquery-containing one = %q, want %q", e.SelectedText(), "SELECT * FROM C")
	}
}

func TestSelectStatementAtCursorAtStartOfLineAfterUnseparatedStatement(t *testing.T) {
	// Cursor sits exactly on the shared boundary between the first
	// statement's end and the second's start (col 0 of the second
	// statement's first line, reached e.g. via Home or a mouse click) —
	// must resolve to the statement it's at the START of, not the one
	// trailing it.
	e := newTestEditor("SELECT * FROM Patients\nSELECT * FROM Doctors\nUPDATE Foo SET Bar = 1")

	setCursor(e, 1, 0)
	if !SelectStatementAtCursor(e) || e.SelectedText() != "SELECT * FROM Doctors" {
		t.Fatalf("statement at boundary = %q, want %q", e.SelectedText(), "SELECT * FROM Doctors")
	}
}

func TestSelectStatementAtCursorNoOpOnBlankSeparatorLine(t *testing.T) {
	e := newTestEditor("SELECT 1\nGO\n\nGO\nSELECT 2")

	setCursor(e, 2, 0)
	if SelectStatementAtCursor(e) {
		t.Fatalf("expected no-op on a blank line between two GO separators, got selection %q", e.SelectedText())
	}
	if e.HasSelection() {
		t.Fatal("HasSelection should stay false after a no-op")
	}
}

// Ctrl+Enter's own GO detection is already inside the state machine — the
// sqltext.IsGoSeparatorLine test at the top of the line loop only runs in stNormal —
// so a GO commented out with a block comment never splits a statement in two.
// Pinned here because the completion-side scan in internal/tui had exactly
// this bug and the two rules are meant to stay in step.
func TestSelectStatementAtCursorIgnoresGoInsideBlockComment(t *testing.T) {
	const script = "SELECT 1\n/*\nGO\n*/\nFROM dbo.T"
	e := newTestEditor(script)

	setCursor(e, 4, 2)
	if !SelectStatementAtCursor(e) {
		t.Fatal("expected a statement to be selected")
	}
	if got := e.SelectedText(); got != script {
		t.Fatalf("statement = %q, want the whole script %q — the commented-out GO split it", got, script)
	}
}

// T-SQL nests block comments, so the first "*/" closes only the inner one and
// the GO below it is still commented out — as the executor splits it.
func TestSelectStatementAtCursorIgnoresGoInsideNestedBlockComment(t *testing.T) {
	const script = "SELECT 1\n/* /* */\nGO\n*/\nFROM dbo.T"
	e := newTestEditor(script)

	setCursor(e, 4, 2)
	if !SelectStatementAtCursor(e) {
		t.Fatal("expected a statement to be selected")
	}
	if got := e.SelectedText(); got != script {
		t.Fatalf("statement = %q, want the whole script %q — the GO in the nested comment split it", got, script)
	}
}

// statementAtMarker returns what Ctrl+Enter selects with the cursor at the
// "‸" in script (the marker itself removed).
func statementAtMarker(t *testing.T, script string) string {
	t.Helper()
	var row, col int
	found := false
	for r, line := range strings.Split(script, "\n") {
		if before, _, ok := strings.Cut(line, "‸"); ok {
			row, col, found = r, utf8.RuneCountInString(before), true
		}
	}
	if !found {
		t.Fatalf("no ‸ marker in %q", script)
	}
	e := newTestEditor(strings.Replace(script, "‸", "", 1))
	setCursor(e, row, col)
	if !SelectStatementAtCursor(e) {
		return ""
	}
	return e.SelectedText()
}

func TestSelectStatementAtCursorLeaderBoundaries(t *testing.T) {
	cases := []struct{ name, script, want string }{
		// T2: a non-DML statement after the cursor's is no longer swept in.
		{"DDL and EXEC after a SELECT", "‸SELECT * FROM dbo.Orders\nDROP TABLE dbo.Staging\nEXEC dbo.Purge",
			"SELECT * FROM dbo.Orders"},
		{"the DROP itself", "SELECT 1\n‸DROP TABLE dbo.Staging\nEXEC dbo.Purge", "DROP TABLE dbo.Staging"},
		{"SET NOCOUNT ON before a DELETE", "‸SET NOCOUNT ON\nDELETE FROM t", "SET NOCOUNT ON"},
		{"TRUNCATE after an UPDATE", "‸UPDATE t SET a = 1\nTRUNCATE TABLE t", "UPDATE t SET a = 1"},
		{"KILL after a SELECT", "‸SELECT 1\nKILL 55", "SELECT 1"},
		{"IF after a DECLARE", "‸DECLARE @x int = 1\nIF @x = 1 PRINT 'a'", "DECLARE @x int = 1"},

		// T3: a WITH followed by '(' is a hint or column list, never a CTE.
		{"DELETE with a table hint", "‸DELETE FROM dbo.Orders WITH (ROWLOCK) WHERE OrderID = 42",
			"DELETE FROM dbo.Orders WITH (ROWLOCK) WHERE OrderID = 42"},
		{"hint on a later line", "DELETE FROM dbo.Orders\nWITH (ROWLOCK)\n‸WHERE OrderID = 42",
			"DELETE FROM dbo.Orders\nWITH (ROWLOCK)\nWHERE OrderID = 42"},
		{"OPENJSON column list", "‸SELECT * FROM OPENJSON(@j) WITH (a int) AS j",
			"SELECT * FROM OPENJSON(@j) WITH (a int) AS j"},

		// WITH options that are neither a hint nor a CTE.
		{"RESTORE WITH MOVE", "‸RESTORE DATABASE d FROM DISK = 'x'\nWITH MOVE 'd' TO 'y', REPLACE",
			"RESTORE DATABASE d FROM DISK = 'x'\nWITH MOVE 'd' TO 'y', REPLACE"},
		{"WITH ROLLBACK IMMEDIATE", "‸ALTER DATABASE d SET SINGLE_USER WITH ROLLBACK IMMEDIATE",
			"ALTER DATABASE d SET SINGLE_USER WITH ROLLBACK IMMEDIATE"},
		{"WITH GRANT OPTION", "‸GRANT SELECT ON t TO u WITH GRANT OPTION\nSELECT 1",
			"GRANT SELECT ON t TO u WITH GRANT OPTION"},
		{"RAISERROR WITH NOWAIT then SELECT", "‸RAISERROR('x', 0, 1) WITH NOWAIT\nSELECT 1",
			"RAISERROR('x', 0, 1) WITH NOWAIT"},
		{"GROUP BY WITH ROLLUP", "‸SELECT a, COUNT(*) FROM t GROUP BY a WITH ROLLUP\nORDER BY a",
			"SELECT a, COUNT(*) FROM t GROUP BY a WITH ROLLUP\nORDER BY a"},

		// CTEs: WITH is the boundary and the main statement continues it.
		{"CTE then UPDATE ... SET", "‸WITH c AS (SELECT 1 AS a)\nUPDATE c SET a = 2",
			"WITH c AS (SELECT 1 AS a)\nUPDATE c SET a = 2"},
		{"CTE with a column list", "SELECT 0\n‸WITH c (a) AS (SELECT 1)\nSELECT a FROM c",
			"WITH c (a) AS (SELECT 1)\nSELECT a FROM c"},
		{"view over a CTE", "‸CREATE VIEW v AS WITH c AS (SELECT 1 AS a) SELECT a FROM c",
			"CREATE VIEW v AS WITH c AS (SELECT 1 AS a) SELECT a FROM c"},

		// Continuations of the cursor's statement.
		{"INSERT ... EXEC", "‸INSERT INTO t\nEXEC dbo.p", "INSERT INTO t\nEXEC dbo.p"},
		{"INSERT ... VALUES then EXEC", "‸INSERT INTO t VALUES (1)\nEXEC dbo.p", "INSERT INTO t VALUES (1)"},
		{"GRANT's permission list", "‸GRANT SELECT, INSERT, EXECUTE ON SCHEMA::dbo TO u\nDROP TABLE x",
			"GRANT SELECT, INSERT, EXECUTE ON SCHEMA::dbo TO u"},
		{"ALTER TABLE ALTER COLUMN", "‸ALTER TABLE t ALTER COLUMN c int NULL", "ALTER TABLE t ALTER COLUMN c int NULL"},
		{"ALTER TABLE DROP CONSTRAINT", "‸ALTER TABLE t DROP CONSTRAINT pk\nDROP TABLE x",
			"ALTER TABLE t DROP CONSTRAINT pk"},
		{"ALTER ROLE DROP MEMBER", "‸ALTER ROLE r DROP MEMBER u", "ALTER ROLE r DROP MEMBER u"},
		{"ALTER DATABASE SET", "‸ALTER DATABASE d SET RECOVERY SIMPLE", "ALTER DATABASE d SET RECOVERY SIMPLE"},
		{"DROP TABLE IF EXISTS", "‸DROP TABLE IF EXISTS t\nSELECT 1", "DROP TABLE IF EXISTS t"},
		{"foreign key actions", "‸ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY (a) REFERENCES p (a)\nON DELETE CASCADE ON UPDATE SET NULL",
			"ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY (a) REFERENCES p (a)\nON DELETE CASCADE ON UPDATE SET NULL"},
		{"MERGE actions", "‸MERGE t USING s ON t.a = s.a\nWHEN MATCHED THEN UPDATE SET b = s.b\nWHEN NOT MATCHED THEN INSERT (a) VALUES (s.a)\nWHEN NOT MATCHED BY SOURCE THEN DELETE",
			"MERGE t USING s ON t.a = s.a\nWHEN MATCHED THEN UPDATE SET b = s.b\nWHEN NOT MATCHED THEN INSERT (a) VALUES (s.a)\nWHEN NOT MATCHED BY SOURCE THEN DELETE"},
		{"CREATE OR ALTER", "‸CREATE OR ALTER VIEW v AS SELECT 1 AS a", "CREATE OR ALTER VIEW v AS SELECT 1 AS a"},
		{"cursor FOR SELECT ... FOR UPDATE", "‸DECLARE c CURSOR FOR SELECT a FROM t FOR UPDATE OF a\nOPEN c",
			"DECLARE c CURSOR FOR SELECT a FROM t FOR UPDATE OF a"},
		{"OFFSET ... FETCH", "‸SELECT a FROM t ORDER BY a OFFSET 5 ROWS FETCH NEXT 5 ROWS ONLY",
			"SELECT a FROM t ORDER BY a OFFSET 5 ROWS FETCH NEXT 5 ROWS ONLY"},
		{"trigger UPDATE()", "‸IF UPDATE(a) OR UPDATE(b) RETURN", "IF UPDATE(a) OR UPDATE(b) RETURN"},
		{"BULK INSERT", "‸BULK INSERT t FROM 'f'\nSELECT 1", "BULK INSERT t FROM 'f'"},
		{"WRITETEXT BULK", "‸WRITETEXT BULK dbo.t.x @p 'z'\nSELECT 1", "WRITETEXT BULK dbo.t.x @p 'z'"},
		{"UPDATETEXT BULK", "‸UPDATETEXT BULK dbo.t.x @p 0 NULL 'z'\nSELECT 1", "UPDATETEXT BULK dbo.t.x @p 0 NULL 'z'"},
		{"BULK INSERT after WRITETEXT", "WRITETEXT dbo.t.x @p 'z'\n‸BULK INSERT t FROM 'f'", "BULK INSERT t FROM 'f'"},
		{"INNER MERGE JOIN", "‸SELECT * FROM a INNER MERGE JOIN b ON a.x = b.x", "SELECT * FROM a INNER MERGE JOIN b ON a.x = b.x"},
		{"keyword-named variable", "‸SELECT @Delete = 1, @Drop = 2", "SELECT @Delete = 1, @Drop = 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statementAtMarker(t, tc.script); got != tc.want {
				t.Fatalf("selected %q, want %q", got, tc.want)
			}
		})
	}
}
