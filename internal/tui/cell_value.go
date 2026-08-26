package tui

import (
	"strings"

	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// cellValueKind is what a result-set cell's text was recognised as, and so
// which highlighter and panel title it gets. cellPlain means the grid's own
// value popup handles it, unchanged.
type cellValueKind int

const (
	cellPlain cellValueKind = iota
	cellXML
	cellJSON
)

// classifyCellValue decides how a "Show Value" cell should be displayed.
//
// Every test here is a shape sniff on the trimmed text, never a parse: this
// runs on the UI goroutine and an xml/varchar(max) cell can be megabytes, so
// the cost has to stay independent of the value's size. A malformed document
// still opens in its own panel — brackets around the whole value are what the
// user meant by XML or JSON even when it doesn't parse, and highlighting is
// what makes the malformation visible.
func classifyCellValue(s string) cellValueKind {
	t := strings.TrimSpace(s)
	if len(t) < 2 {
		return cellPlain
	}
	first, last := t[0], t[len(t)-1]
	switch {
	case first == '<' && last == '>':
		return cellXML
	case first == '{' && last == '}':
		return cellJSON
	case first == '[' && last == ']' && jsonArrayLike(t):
		return cellJSON
	}
	return cellPlain
}

// jsonArrayLike distinguishes a JSON array from an ordinary bracketed value.
// A `[…]` shape alone is not enough: result sets are full of bracket-quoted
// SQL Server names (QUOTENAME output, `[dbo]`, `[Ord.ers]`), and opening a
// whole panel for one instead of the grid's popup would be a regression in
// the common case. So the first thing inside the bracket has to be something
// a JSON array element can actually start with.
func jsonArrayLike(t string) bool {
	for i := 1; i < len(t); i++ {
		switch c := t[i]; c {
		case ' ', '\t', '\n', '\r':
			continue
		case '"', '{', '[', ']', '-', 't', 'f', 'n':
			return true
		default:
			return c >= '0' && c <= '9'
		}
	}
	return false
}

// classifyCellKind decides how a "Show Value" cell is displayed when the
// column's declared SQL Server type is known (see query.ResultSet.ColumnTypes;
// sqlType may be empty for a set that doesn't carry types).
//
// The declared type wins over the text sniff, and has to: a real xml column's
// value is not reliably bracket-shaped. SQL Server serialises an xml fragment
// with its text nodes entity-escaped, so the blocked-process idiom
// `try_cast('<?query --' + text + '--?>' as xml)` comes back ending in
// "--?&gt;" whenever the batch text closed the processing instruction early —
// last byte ';', which classifyCellValue reads as plain text and drops into
// the 60-column popup.
func classifyCellKind(sqlType, value string) cellValueKind {
	if k := classifySQLType(sqlType); k != cellPlain {
		// A NULL cell renders as the literal "NULL" — nothing to open.
		if strings.TrimSpace(value) == "" || value == "NULL" {
			return cellPlain
		}
		return k
	}
	return classifyCellValue(value)
}

// classifySQLType maps a declared column type ("xml", "nvarchar(50)", "json")
// to the panel kind it always gets, or cellPlain when the type says nothing
// about the value's shape.
func classifySQLType(sqlType string) cellValueKind {
	base, _, _ := strings.Cut(sqlType, "(")
	switch strings.ToLower(strings.TrimSpace(base)) {
	case "xml":
		return cellXML
	case "json":
		return cellJSON
	}
	return cellPlain
}

// openCellValuePanel shows a structured cell value in a brand new query panel
// with the matching highlighter — the same treatment File > Open gives a .xml
// file (see openQueryFile), rather than the grid's fixed 60-column value
// popup. Reports whether it took the value; a plain one is left to the popup.
//
// Deliberately a panel rather than a highlighted popup. The popup is an
// Editor in wrap mode, which resolves each drawn column through a linear scan
// of that line's ColorRuns (editor_draw.go's styleAt) — fine against SQL's
// few coarse runs, but a highlighter emitting one run per token across a
// whole XML or JSON document is exactly the case that scan is not arranged
// for, and this would be the call site where it landed. A panel draws
// unwrapped and pays nothing extra.
//
// The panel has no connection of its own: it exists to read a value, not to
// run it. savedText is seeded so the panel isn't born dirty and closing it
// doesn't prompt to save.
func (a *App) openCellValuePanel(sqlType, column, value string) bool {
	var suffix string
	var highlighter controls.Highlighter
	switch classifyCellKind(sqlType, value) {
	case cellXML:
		suffix, highlighter = ".xml", controls.XMLHighlighter(theme.Active())
	case cellJSON:
		suffix, highlighter = ".json", controls.JSONHighlighter(theme.Active())
	default:
		return false
	}

	title := strings.ToUpper(strings.TrimPrefix(suffix, "."))
	if column != "" {
		title = column + suffix
	}
	a.queryPanelCnt++
	qp := NewQueryPanel(a, title)
	qp.editor.SetText(value)
	// Read back from the editor, not from value: SetText expands tabs and
	// folds CRLF, so seeding from value leaves an XML or JSON cell containing
	// either one born dirty — and with no filePath, closing it pushes the user
	// into Save As for a value they only wanted to look at.
	qp.savedText = qp.editor.Text()
	qp.editor.SetHighlighter(highlighter)
	a.panels.SetActive(a.panels.AddPanel(qp))
	a.focusPanels()
	a.setStatus("Opened " + title)
	return true
}
