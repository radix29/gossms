package tui

import (
	"strings"

	"github.com/radix29/gossms/internal/format"
)

// cellValueKind is what a result-set cell's text was recognised as, and so
// which highlighter and panel title it gets. cellPlain means the grid's own
// value popup handles it, unchanged.
type cellValueKind int

const (
	cellPlain cellValueKind = iota
	cellXML
	cellJSON
	cellPicture
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

// classifyCellKind decides how a "Show Value" cell is displayed when the column's
// declared SQL Server type is known (see query.ResultSet.ColumnTypes; sqlType may
// be empty for a set that doesn't carry types).
//
// The declared type wins over the text sniff, and has to: a real xml column's
// value is not reliably bracket-shaped. SQL Server serialises an xml fragment with
// its text nodes entity-escaped, so the blocked-process idiom
// `try_cast('<?query --' + text + '--?>' as xml)` comes back ending in "--?&gt;"
// whenever the batch text closed the processing instruction early — last byte ';',
// which classifyCellValue reads as plain text and drops into the 60-column popup.
func classifyCellKind(sqlType, value string) cellValueKind {
	// A picture is told by its magic bytes, read off the 0x… text's first few
	// digits — size-independent like every other test here. Only a binary
	// column (or one with no declared type) is sniffed: an nvarchar holding the
	// text "0x89504E47…" is text, and stays in the popup.
	if binarySQLType(sqlType) {
		if _, ok := sniffPictureHex(value); ok {
			return cellPicture
		}
		if sqlType != "" {
			return cellPlain
		}
	}
	if k := classifySQLType(sqlType); k != cellPlain {
		// Nothing to open. A NULL never gets here: the grid keeps it from
		// OnShowValue (controls.NullSource).
		if strings.TrimSpace(value) == "" {
			return cellPlain
		}
		return k
	}
	return classifyCellValue(value)
}

// binarySQLType reports whether a declared column type holds raw bytes, which
// the grid shows as 0x… hex (query.appendValue) and which may be a picture. An
// undeclared type counts too, so a set without types still sniffs its hex.
func binarySQLType(sqlType string) bool {
	base, _, _ := strings.Cut(sqlType, "(")
	switch strings.ToLower(strings.TrimSpace(base)) {
	case "binary", "varbinary", "image", "":
		return true
	}
	return false
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
// file (see openQueryFile), rather than the grid's fixed 60-column value popup.
// Reports whether it took the value; a plain one is left to the popup.
//
// Deliberately a panel rather than a highlighted popup. The popup is an Editor in
// wrap mode, which resolves each drawn column through a linear scan of that line's
// ColorRuns (editor_draw.go's styleAt) — fine for SQL's few coarse runs, but not
// for a highlighter emitting one run per token across a whole XML or JSON
// document. A panel draws unwrapped and pays nothing extra.
//
// The panel has no connection of its own: it exists to read a value, not run it.
// savedText is seeded so the panel isn't born dirty and closing it doesn't prompt
// to save.
func (a *App) openCellValuePanel(sqlType, column, value string) bool {
	switch classifyCellKind(sqlType, value) {
	case cellXML:
		return a.openValuePanel(column, format.LangXML, value)
	case cellJSON:
		return a.openValuePanel(column, format.LangJSON, value)
	case cellPicture:
		return a.openPicturePanel(column, value)
	}
	return false
}

// showSQLCellValue is the DataGrid OnShowValue hook for a grid whose rows
// carry a query's text: the Query Store reports keep the whole statement in
// the cell and only the grid's column width cuts it short, so "Show Value" on
// that column opens the full text in its own SQL-highlighted query panel
// instead of the 60-column popup. Every other column falls through to it.
func (a *App) showSQLCellValue(_ int, column, value string) bool {
	if column != qsQueryColumn || strings.TrimSpace(value) == "" {
		return false
	}
	return a.openValuePanel(column, format.LangSQL, value)
}

// openValuePanel puts one cell's value in a new query panel titled after its
// column plus lang's extension, in lang.
func (a *App) openValuePanel(column string, lang format.Language, value string) bool {
	title := strings.ToUpper(strings.TrimPrefix(lang.Ext(), "."))
	if column != "" {
		title = column + lang.Ext()
	}
	a.queryPanelCnt++
	qp := NewQueryPanel(a, title)
	qp.editor.SetText(value)
	// Read back from the editor, not from value: SetText expands tabs and
	// folds CRLF, so seeding from value leaves an XML or JSON cell containing
	// either one born dirty — and with no filePath, closing it pushes the user
	// into Save As for a value they only wanted to look at.
	qp.markSaved()
	qp.setLanguage(lang)
	a.panels.SetActive(a.panels.AddPanel(qp))
	a.focusPanels()
	a.setStatus("Opened " + title)
	return true
}
