package controls

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSelectStatementAtCursorSemicolonSeparated(t *testing.T) {
	e := newTestEditor("SELECT 1;\nSELECT 2;")

	e.cursorRow, e.cursorCol = 0, 3
	if !e.SelectStatementAtCursor() {
		t.Fatal("expected a statement to be selected")
	}
	if got := e.SelectedText(); got != "SELECT 1;" {
		t.Fatalf("first statement = %q, want %q", got, "SELECT 1;")
	}

	e.cursorRow, e.cursorCol = 1, 3
	if !e.SelectStatementAtCursor() {
		t.Fatal("expected a statement to be selected")
	}
	if got := e.SelectedText(); got != "SELECT 2;" {
		t.Fatalf("second statement = %q, want %q", got, "SELECT 2;")
	}
}

func TestSelectStatementAtCursorGoSeparated(t *testing.T) {
	e := newTestEditor("SELECT 1\nGO\nSELECT 2\nGO\n")

	e.cursorRow, e.cursorCol = 0, 3
	if !e.SelectStatementAtCursor() || e.SelectedText() != "SELECT 1" {
		t.Fatalf("first batch = %q, want %q", e.SelectedText(), "SELECT 1")
	}

	e.cursorRow, e.cursorCol = 2, 3
	if !e.SelectStatementAtCursor() || e.SelectedText() != "SELECT 2" {
		t.Fatalf("second batch = %q, want %q", e.SelectedText(), "SELECT 2")
	}
}

func TestSelectStatementAtCursorIgnoresGoLikeIdentifier(t *testing.T) {
	// "goto_flag" starts with "go" but isn't a standalone GO separator —
	// the same rule the executor splits batches by (sqltext.GoSeparatorAt).
	e := newTestEditor("SELECT goto_flag\nFROM t;")

	e.cursorRow, e.cursorCol = 1, 0
	if !e.SelectStatementAtCursor() {
		t.Fatal("expected a statement to be selected")
	}
	want := "SELECT goto_flag\nFROM t;"
	if got := e.SelectedText(); got != want {
		t.Fatalf("statement = %q, want %q", got, want)
	}
}

func TestSelectStatementAtCursorIgnoresSemicolonInStringLiteral(t *testing.T) {
	e := newTestEditor("SELECT 'a;b';\nSELECT 2;")

	e.cursorRow, e.cursorCol = 0, 9 // inside the string literal, on the fake ';'
	if !e.SelectStatementAtCursor() {
		t.Fatal("expected a statement to be selected")
	}
	want := "SELECT 'a;b';"
	if got := e.SelectedText(); got != want {
		t.Fatalf("statement = %q, want %q — the ';' inside the string literal must not split it", got, want)
	}
}

func TestSelectStatementAtCursorIgnoresSemicolonInBlockComment(t *testing.T) {
	e := newTestEditor("SELECT 1 /* ; */;\nSELECT 2;")

	e.cursorRow, e.cursorCol = 0, 0
	if !e.SelectStatementAtCursor() {
		t.Fatal("expected a statement to be selected")
	}
	want := "SELECT 1 /* ; */;"
	if got := e.SelectedText(); got != want {
		t.Fatalf("statement = %q, want %q — the ';' inside the comment must not split it", got, want)
	}
}

func TestSelectStatementAtCursorTrimsSurroundingBlankLines(t *testing.T) {
	e := newTestEditor("\n\nSELECT 1;\n\nSELECT 2;\n")

	e.cursorRow, e.cursorCol = 2, 3
	if !e.SelectStatementAtCursor() {
		t.Fatal("expected a statement to be selected")
	}
	if got := e.SelectedText(); got != "SELECT 1;" {
		t.Fatalf("statement = %q, want %q — blank lines around it should be trimmed", got, "SELECT 1;")
	}
}

func TestSelectStatementAtCursorNoSemicolonBetweenStatements(t *testing.T) {
	e := newTestEditor("SELECT 1\nSELECT 2")

	e.cursorRow, e.cursorCol = 0, 3
	if !e.SelectStatementAtCursor() || e.SelectedText() != "SELECT 1" {
		t.Fatalf("first statement = %q, want %q", e.SelectedText(), "SELECT 1")
	}

	e.cursorRow, e.cursorCol = 1, 3
	if !e.SelectStatementAtCursor() || e.SelectedText() != "SELECT 2" {
		t.Fatalf("second statement = %q, want %q", e.SelectedText(), "SELECT 2")
	}
}

func TestSelectStatementAtCursorThreeStatementsNoSemicolons(t *testing.T) {
	e := newTestEditor("SELECT * FROM Patients\nSELECT * FROM Doctors\nUPDATE Foo SET Bar = 1")

	e.cursorRow, e.cursorCol = 1, 5
	if !e.SelectStatementAtCursor() || e.SelectedText() != "SELECT * FROM Doctors" {
		t.Fatalf("middle statement = %q, want %q", e.SelectedText(), "SELECT * FROM Doctors")
	}

	e.cursorRow, e.cursorCol = 2, 5
	if !e.SelectStatementAtCursor() || e.SelectedText() != "UPDATE Foo SET Bar = 1" {
		t.Fatalf("last statement = %q, want %q", e.SelectedText(), "UPDATE Foo SET Bar = 1")
	}
}

func TestSelectStatementAtCursorUnionedSelectsShareOneStatement(t *testing.T) {
	e := newTestEditor("SELECT Id FROM A\nUNION ALL\nSELECT Id FROM B\nSELECT Id FROM C")

	e.cursorRow, e.cursorCol = 2, 3
	want := "SELECT Id FROM A\nUNION ALL\nSELECT Id FROM B"
	if !e.SelectStatementAtCursor() || e.SelectedText() != want {
		t.Fatalf("UNION'd statement = %q, want %q", e.SelectedText(), want)
	}

	e.cursorRow, e.cursorCol = 3, 3
	if !e.SelectStatementAtCursor() || e.SelectedText() != "SELECT Id FROM C" {
		t.Fatalf("statement after UNION chain = %q, want %q", e.SelectedText(), "SELECT Id FROM C")
	}
}

func TestSelectStatementAtCursorCTEMainQueryNotSplitFromWith(t *testing.T) {
	e := newTestEditor("WITH cte AS (SELECT Id FROM A)\nSELECT Id FROM cte\nSELECT Id FROM B")

	e.cursorRow, e.cursorCol = 1, 3
	want := "WITH cte AS (SELECT Id FROM A)\nSELECT Id FROM cte"
	if !e.SelectStatementAtCursor() || e.SelectedText() != want {
		t.Fatalf("CTE statement = %q, want %q", e.SelectedText(), want)
	}

	e.cursorRow, e.cursorCol = 2, 3
	if !e.SelectStatementAtCursor() || e.SelectedText() != "SELECT Id FROM B" {
		t.Fatalf("statement after CTE = %q, want %q", e.SelectedText(), "SELECT Id FROM B")
	}
}

func TestSelectStatementAtCursorInsertSelectSharesOneStatement(t *testing.T) {
	e := newTestEditor("INSERT INTO A\nSELECT Id FROM B\nSELECT Id FROM C")

	e.cursorRow, e.cursorCol = 1, 3
	want := "INSERT INTO A\nSELECT Id FROM B"
	if !e.SelectStatementAtCursor() || e.SelectedText() != want {
		t.Fatalf("INSERT...SELECT statement = %q, want %q", e.SelectedText(), want)
	}
}

func TestSelectStatementAtCursorKeywordInsideParensNotABoundary(t *testing.T) {
	e := newTestEditor("SELECT * FROM A WHERE Id IN (SELECT Id FROM B)\nSELECT * FROM C")

	e.cursorRow, e.cursorCol = 0, 5
	want := "SELECT * FROM A WHERE Id IN (SELECT Id FROM B)"
	if !e.SelectStatementAtCursor() || e.SelectedText() != want {
		t.Fatalf("statement with subquery = %q, want %q", e.SelectedText(), want)
	}

	e.cursorRow, e.cursorCol = 1, 3
	if !e.SelectStatementAtCursor() || e.SelectedText() != "SELECT * FROM C" {
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

	e.cursorRow, e.cursorCol = 1, 0
	if !e.SelectStatementAtCursor() || e.SelectedText() != "SELECT * FROM Doctors" {
		t.Fatalf("statement at boundary = %q, want %q", e.SelectedText(), "SELECT * FROM Doctors")
	}
}

func TestSelectStatementAtCursorNoOpOnBlankSeparatorLine(t *testing.T) {
	e := newTestEditor("SELECT 1\nGO\n\nGO\nSELECT 2")

	e.cursorRow, e.cursorCol = 2, 0
	if e.SelectStatementAtCursor() {
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

	e.cursorRow, e.cursorCol = 4, 2
	if !e.SelectStatementAtCursor() {
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

	e.cursorRow, e.cursorCol = 4, 2
	if !e.SelectStatementAtCursor() {
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
		if i := strings.Index(line, "‸"); i >= 0 {
			row, col, found = r, utf8.RuneCountInString(line[:i]), true
		}
	}
	if !found {
		t.Fatalf("no ‸ marker in %q", script)
	}
	e := newTestEditor(strings.Replace(script, "‸", "", 1))
	e.cursorRow, e.cursorCol = row, col
	if !e.SelectStatementAtCursor() {
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
