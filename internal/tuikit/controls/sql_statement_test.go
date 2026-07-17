package controls

import "testing"

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
	// mirrors go-mssqldb's batch.Split treatment of "goto"/"gone".
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
