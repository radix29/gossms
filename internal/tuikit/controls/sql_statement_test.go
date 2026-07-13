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
