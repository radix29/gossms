package tui

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gdamore/tcell/v3"

	"github.com/radix29/gossms/internal/format"
	"github.com/radix29/tuikit/controls"
)

// newFormatTestPanel opens a query panel holding text in language l.
func newFormatTestPanel(t *testing.T, l format.Language, text string) (*App, *QueryPanel) {
	t.Helper()
	a := newKeyTestApp(t)
	a.newQueryPanel()
	qp := a.activeQueryPanel()
	qp.setLanguage(l)
	qp.editor.SetText(text)
	return a, qp
}

// Format Document re-indents the whole JSON document as one undo step: one
// Ctrl+Z restores the exact previous text. Alt+F and Alt+Shift+F both run it.
func TestFormatDocumentWholeJSONIsOneUndoStep(t *testing.T) {
	const src = `{"a":[1,2],"b":{}}`
	const want = "{\n    \"a\": [\n        1,\n        2\n    ],\n    \"b\": {}\n}"
	for _, ev := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyRune, "f", tcell.ModAlt),
		tcell.NewEventKey(tcell.KeyRune, "F", tcell.ModAlt|tcell.ModShift),
	} {
		a, qp := newFormatTestPanel(t, format.LangJSON, src)
		a.handleKey(ev)
		if got := qp.editor.Text(); got != want {
			t.Fatalf("%s: text = %q, want %q (status %q)", ev.Name(), got, want, a.statusText)
		}
		if !strings.HasPrefix(a.statusText, "Formatted as JSON") {
			t.Errorf("%s: status = %q", ev.Name(), a.statusText)
		}
		qp.editor.Undo()
		if got := qp.editor.Text(); got != src {
			t.Fatalf("%s: after one undo text = %q, want %q", ev.Name(), got, src)
		}
	}
}

// An XML panel formats too, with the editor's indent width, as one undo step;
// a selection inside a document formats just its element, and the error for
// half an element reports document lines.
func TestFormatDocumentXML(t *testing.T) {
	const src = `<a x="1"><b>t</b><c/></a>`
	a, qp := newFormatTestPanel(t, format.LangXML, src)
	if !a.canFormatActiveDocument() {
		t.Fatal("disabled on an XML panel")
	}
	a.formatActiveDocument()
	want := "<a x=\"1\">\n" + strings.Repeat(" ", qp.editor.IndentWidth()) + "<b>t</b>\n" +
		strings.Repeat(" ", qp.editor.IndentWidth()) + "<c/>\n</a>"
	if got := qp.editor.Text(); got != want || a.statusText != "Formatted as XML (Standard)" {
		t.Fatalf("text = %q, status = %q; want %q", got, a.statusText, want)
	}
	qp.editor.Undo()
	if got := qp.editor.Text(); got != src {
		t.Fatalf("after one undo text = %q, want %q", got, src)
	}

	a, qp = newFormatTestPanel(t, format.LangXML, "<root>\n  <p><q/></p>\n  <r/>\n</root>")
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModShift))
	a.formatActiveDocument()
	ind := strings.Repeat(" ", qp.editor.IndentWidth())
	want = "<root>\n  <p>\n  " + ind + "<q/>\n  </p>\n  <r/>\n</root>"
	if got := qp.editor.Text(); got != want {
		t.Fatalf("selection: text = %q, want %q (status %q)", got, want, a.statusText)
	}

	a, qp = newFormatTestPanel(t, format.LangXML, "<root>\n  <p>\n  </p>\n</root>")
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModShift))
	a.formatActiveDocument()
	if a.statusText != "Format failed: selection is not a complete XML value: line 2 col 3: <p> is never closed" {
		t.Errorf("half an element: status = %q", a.statusText)
	}
}

// Invalid JSON leaves the text and the panel's clean state alone and says
// where it failed.
func TestFormatDocumentParseErrorLeavesTextUntouched(t *testing.T) {
	const src = "{\n\"a\": 1,\n\"b\" 2}"
	a, qp := newFormatTestPanel(t, format.LangJSON, src)
	qp.markSaved()
	a.formatActiveDocument()
	if got := qp.editor.Text(); got != src {
		t.Fatalf("text changed to %q", got)
	}
	if qp.Dirty() {
		t.Error("a failed format marked the panel dirty")
	}
	if !strings.HasPrefix(a.statusText, "Format failed: JSON line 3 col") {
		t.Errorf("status = %q", a.statusText)
	}
}

// With a selection only its whole lines are formatted, re-indented at the
// first line's indent; the lines around it are untouched. A selection that is
// not a complete value reports document line numbers.
func TestFormatDocumentSelectionLines(t *testing.T) {
	a, qp := newFormatTestPanel(t, format.LangJSON, "before\n  {\"a\":1,\n  \"b\":[]}\nafter")
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone))
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModShift))
	a.formatActiveDocument()
	want := "before\n  {\n      \"a\": 1,\n      \"b\": []\n  }\nafter"
	if got := qp.editor.Text(); got != want {
		t.Fatalf("text = %q, want %q (status %q)", got, want, a.statusText)
	}
	// The replacement stays selected, so a repeat is a no-op.
	a.formatActiveDocument()
	if got := qp.editor.Text(); got != want || !strings.HasPrefix(a.statusText, "Already formatted") {
		t.Fatalf("repeat: text = %q, status = %q", got, a.statusText)
	}

	a, qp = newFormatTestPanel(t, format.LangJSON, "x\n{\"a\":\n1,")
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModShift))
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModShift))
	a.formatActiveDocument()
	if !strings.HasPrefix(a.statusText, "Format failed: selection is not a complete JSON value: line 3 ") {
		t.Errorf("status = %q", a.statusText)
	}
}

// The command is offered only where it can act: a writable query panel whose
// language has a formatter, with no block selection.
func TestFormatDocumentEnabled(t *testing.T) {
	a, qp := newFormatTestPanel(t, format.LangJSON, "{}")
	if !a.canFormatActiveDocument() {
		t.Fatal("disabled on a JSON panel")
	}
	qp.editor.SetReadOnly(true)
	if a.canFormatActiveDocument() {
		t.Error("enabled on a read-only editor")
	}
	a.formatActiveDocument()
	if !strings.Contains(a.statusText, "read-only") {
		t.Errorf("read-only status = %q", a.statusText)
	}

	a, qp = newFormatTestPanel(t, format.LangText, "select 1")
	if a.canFormatActiveDocument() {
		t.Error("enabled for plain text, which has no formatter")
	}
	a.formatActiveDocument()
	if qp.editor.Text() != "select 1" || !strings.Contains(a.statusText, "no formatter for Text") {
		t.Errorf("Text: text = %q, status = %q", qp.editor.Text(), a.statusText)
	}

	a = newKeyTestApp(t)
	if a.canFormatActiveDocument() {
		t.Error("enabled with no query panel")
	}
	a.formatActiveDocument() // must not panic
}

// A SQL panel formats as one undo step, and the status names the style.
func TestFormatDocumentSQL(t *testing.T) {
	const src = "select a, b from t where x = 1"
	a, qp := newFormatTestPanel(t, format.LangSQL, src)
	if !a.canFormatActiveDocument() {
		t.Fatal("disabled on a SQL panel")
	}
	a.formatActiveDocument()
	ind := strings.Repeat(" ", qp.editor.IndentWidth())
	lead := strings.Repeat(" ", qp.editor.IndentWidth()-2) + ", "
	want := "SELECT\n" + ind + "a\n" + lead + "b\nFROM t\nWHERE x = 1"
	if got := qp.editor.Text(); got != want || a.statusText != "Formatted as SQL (Standard)" {
		t.Fatalf("text = %q, status = %q; want %q", got, a.statusText, want)
	}
	qp.editor.Undo()
	if got := qp.editor.Text(); got != src {
		t.Fatalf("after one undo text = %q, want %q", got, src)
	}
}

// Format Document lays SQL out in Tools > Options' SQL Format Style; a stale
// ID formats in the default rather than failing.
func TestFormatDocumentSQLUsesConfiguredStyle(t *testing.T) {
	const src = "SELECT a FROM t"
	a, qp := newFormatTestPanel(t, format.LangSQL, src)
	a.cfg.SQLFormatStyle = "lowercase"
	a.formatActiveDocument()
	if got := qp.editor.Text(); got != "select a\nfrom\n    t" ||
		a.statusText != "Formatted as SQL (Lowercase)" {
		t.Errorf("lowercase: text = %q, status = %q", got, a.statusText)
	}
	a, qp = newFormatTestPanel(t, format.LangSQL, src)
	a.cfg.SQLFormatStyle = "no-such-style"
	a.formatActiveDocument()
	if a.statusText != "Formatted as SQL (Standard)" {
		t.Errorf("stale ID: text = %q, status = %q; want the Standard style", qp.editor.Text(), a.statusText)
	}
}

// findMenu returns the top-level menu with the given label.
func findMenu(t *testing.T, menus []controls.Menu, label string) controls.Menu {
	t.Helper()
	for _, m := range menus {
		if m.Label == label {
			return m
		}
	}
	t.Fatalf("no %q menu", label)
	return controls.Menu{}
}

// Edit > Format Document is a cascade of SQL's styles with the configured
// one marked "(default)" and carrying Alt+Shift+F; a pick formats once in its
// style without changing the default the key uses. JSON, with one style,
// keeps the plain item.
func TestFormatDocumentMenuCascade(t *testing.T) {
	const src = "SELECT a FROM t"
	a, qp := newFormatTestPanel(t, format.LangSQL, src)
	a.cfg.SQLFormatStyle = "lowercase"
	edit := findMenu(t, a.buildMenus(), "Edit")
	item := findMenuItem(edit.Items, "Format Document")
	if item == nil || item.Action != nil || item.Shortcut != "" {
		t.Fatalf("SQL item = %+v, want a cascade", item)
	}
	var labels, shortcuts []string
	for _, sub := range item.Sub {
		labels = append(labels, sub.Label)
		shortcuts = append(shortcuts, sub.Shortcut)
	}
	if got := strings.Join(labels, "|"); got != "Standard|Lowercase (default)" {
		t.Errorf("labels = %q", got)
	}
	if got := strings.Join(shortcuts, "|"); got != "|Alt+Shift+F" {
		t.Errorf("shortcuts = %q", got)
	}

	item.Sub[0].Action() // Standard, not the default
	if got := qp.editor.Text(); !strings.HasPrefix(got, "SELECT") ||
		a.statusText != "Formatted as SQL (Standard)" {
		t.Errorf("Standard pick: text = %q, status = %q", got, a.statusText)
	}
	if a.cfg.SQLFormatStyle != "lowercase" {
		t.Errorf("a pick changed the configured style to %q", a.cfg.SQLFormatStyle)
	}
	a.formatActiveDocument() // the key still uses the default
	if a.statusText != "Formatted as SQL (Lowercase)" {
		t.Errorf("default after a pick: status = %q", a.statusText)
	}

	qp.editor.SetReadOnly(true)
	edit = findMenu(t, a.buildMenus(), "Edit")
	if item = findMenuItem(edit.Items, "Format Document"); item.Enabled() {
		t.Error("cascade enabled on a read-only editor")
	}

	a, _ = newFormatTestPanel(t, format.LangJSON, "{}")
	edit = findMenu(t, a.buildMenus(), "Edit")
	if item = findMenuItem(edit.Items, "Format Document"); item == nil || len(item.Sub) != 0 ||
		item.Action == nil || item.Shortcut != "Alt+Shift+F" {
		t.Errorf("JSON item = %+v, want the plain item", item)
	}
	a = newKeyTestApp(t)
	edit = findMenu(t, a.buildMenus(), "Edit")
	if item = findMenuItem(edit.Items, "Format Document"); item == nil || len(item.Sub) != 0 || item.Enabled() {
		t.Errorf("no panel: item = %+v, want the plain item, disabled", item)
	}
}

// A selection is re-indented at its first line's indent by the formatter
// itself, so the second line of a multi-line literal — part of the value —
// keeps exactly the text it had. Prefixing every line afterwards changed it.
func TestFormatDocumentSQLSelectionKeepsLiteralLines(t *testing.T) {
	a, qp := newFormatTestPanel(t, format.LangSQL, "begin\n  print 'a\nb'; select 1\nend")
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModShift))
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModShift))
	a.formatActiveDocument()
	want := "begin\n  PRINT 'a\nb';\n  SELECT 1\nend"
	if got := qp.editor.Text(); got != want {
		t.Fatalf("text = %q, want %q (status %q)", got, want, a.statusText)
	}

	a, qp = newFormatTestPanel(t, format.LangSQL, "select 1\nselect 'x")
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModShift))
	a.formatActiveDocument()
	if want := "Format failed: selection does not lex as SQL: line 2 col 8: unterminated string literal"; a.statusText != want {
		t.Errorf("status = %q, want %q", a.statusText, want)
	}
}

// slowFormat makes every Format Document run asynchronous and holds call n
// (from 1) until gate[n] closes; entered reports each call as it starts. Its
// output names the call, so a test can tell whose result landed.
func slowFormat(t *testing.T) (entered chan int, gate [4]chan struct{}) {
	t.Helper()
	entered = make(chan int, len(gate))
	for i := range gate {
		gate[i] = make(chan struct{})
	}
	var n atomic.Int32
	oldSync, oldText := formatSyncBytes, formatText
	formatSyncBytes = -1
	formatText = func(format.Language, string, string, format.Options) (string, error) {
		k := int(n.Add(1))
		entered <- k
		<-gate[k]
		return fmt.Sprintf("OUT%d", k), nil
	}
	t.Cleanup(func() { formatSyncBytes, formatText = oldSync, oldText })
	return entered, gate
}

// A large format runs off the UI goroutine: the text stays as it was until the
// result lands, which is then one undo step.
func TestFormatDocumentAsyncAppliesAsOneUndoStep(t *testing.T) {
	entered, gate := slowFormat(t)
	const src = "select 1"
	a, qp := newFormatTestPanel(t, format.LangSQL, src)
	a.formatActiveDocument()
	<-entered
	if got := qp.editor.Text(); got != src || a.statusText != "Formatting as SQL..." {
		t.Fatalf("while running: text = %q, status = %q", got, a.statusText)
	}
	close(gate[1])
	waitAndDrain(t, a)
	if got := qp.editor.Text(); got != "OUT1" || a.statusText != "Formatted as SQL (Standard)" {
		t.Fatalf("text = %q, status = %q", got, a.statusText)
	}
	qp.editor.Undo()
	if got := qp.editor.Text(); got != src {
		t.Fatalf("after one undo text = %q, want %q", got, src)
	}
}

// A result for text that changed while it was formatting — typed into, or the
// selection moved — is dropped: applying it would undo the typing or rewrite
// lines that were never formatted.
func TestFormatDocumentAsyncDropsStaleResult(t *testing.T) {
	entered, gate := slowFormat(t)
	a, qp := newFormatTestPanel(t, format.LangSQL, "select 1")
	a.formatActiveDocument()
	<-entered
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, "x", tcell.ModNone))
	typed := qp.editor.Text()
	close(gate[1])
	waitAndDrain(t, a)
	if got := qp.editor.Text(); got != typed || !strings.Contains(a.statusText, "changed while formatting") {
		t.Fatalf("typed: text = %q, status = %q; want %q kept", got, a.statusText, typed)
	}

	// The selection moved down a line onto identical text: only its place
	// tells the two apart.
	const src = "select 1\nselect 1\nselect 1"
	a, qp = newFormatTestPanel(t, format.LangSQL, src)
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModShift))
	a.formatActiveDocument()
	<-entered
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyHome, "", tcell.ModNone))
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModShift))
	close(gate[2])
	waitAndDrain(t, a)
	if got := qp.editor.Text(); got != src || !strings.Contains(a.statusText, "changed while formatting") {
		t.Fatalf("moved selection: text = %q, status = %q", got, a.statusText)
	}
}

// A second Format Document supersedes the first: the first's result, landing
// while the text is still unchanged, is dropped, and the second's applies.
func TestFormatDocumentAsyncRepeatSupersedes(t *testing.T) {
	entered, gate := slowFormat(t)
	const src = "select 1"
	a, qp := newFormatTestPanel(t, format.LangSQL, src)
	a.formatActiveDocument()
	<-entered
	a.formatActiveDocument()
	<-entered
	close(gate[1])
	waitAndDrain(t, a)
	if got := qp.editor.Text(); got != src || a.statusText != "Formatting as SQL..." {
		t.Fatalf("superseded run applied: text = %q, status = %q", got, a.statusText)
	}
	close(gate[2])
	waitAndDrain(t, a)
	if got := qp.editor.Text(); got != "OUT2" {
		t.Fatalf("text = %q, want the second run's OUT2", got)
	}
}

// selectLines selects whole lines from..to (0-based) of qp's editor.
func selectLines(qp *QueryPanel, from, to int) {
	e := qp.editor
	e.HandleKey(tcell.NewEventKey(tcell.KeyHome, "", tcell.ModCtrl))
	for range from {
		e.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	}
	for range to - from {
		e.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModShift))
	}
	e.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModShift))
}

// A selection whose lines lie inside a string, comment or CDATA section that
// began above it is refused, and the text is unchanged. Cut out of the
// document those lines lex as code, so formatting them on their own passed
// the guard and silently rewrote the value.
func TestFormatDocumentSelectionInsideValueIsRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		lang format.Language
		src  string
		from int
		to   int
		want string
	}{
		{"SQL literal", format.LangSQL, "SELECT N'header\n   select a,b from t\nfooter' AS template;", 1, 1,
			"Format failed: the selection starts inside a string, comment or quoted identifier"},
		{"SQL block comment", format.LangSQL, "/* note\n   select a,b from t\n*/\nselect 1", 1, 1,
			"Format failed: the selection starts inside a string, comment or quoted identifier"},
		// Ends inside a literal: the selection's own lexing already refuses it.
		{"SQL literal opened in the selection", format.LangSQL, "select 1\nselect N'x\n   select a,b from t\n'", 1, 2,
			"Format failed: selection does not lex as SQL: line 2 col 9: unterminated string literal"},
		{"XML CDATA", format.LangXML, "<r><![CDATA[\n<a><b>x</b></a>\n]]></r>", 1, 1,
			"Format failed: the selection starts or ends inside a string, comment or CDATA section"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, qp := newFormatTestPanel(t, c.lang, c.src)
			selectLines(qp, c.from, c.to)
			a.formatActiveDocument()
			if got := qp.editor.Text(); got != c.src || a.statusText != c.want {
				t.Errorf("text = %q, status = %q; want unchanged and %q", got, a.statusText, c.want)
			}
		})
	}
}

// The document check also runs on the asynchronous path, and an ordinary
// selection still formats there and on the synchronous one.
func TestFormatDocumentSelectionInsideValueIsRefusedAsync(t *testing.T) {
	const src = "SELECT N'header\nselect a,b from t\nfooter' AS template;\nselect a,b from t"
	for _, async := range []bool{false, true} {
		if async {
			old := formatSyncBytes
			formatSyncBytes = -1
			t.Cleanup(func() { formatSyncBytes = old })
		}
		run := func(a *App) {
			a.formatActiveDocument()
			if async {
				waitAndDrain(t, a)
			}
		}
		// The XML case reaches the document guard; the SQL one is refused
		// before formatting starts.
		const xml = "<r><![CDATA[\n<a><b>x</b></a>\n]]></r>"
		a, qp := newFormatTestPanel(t, format.LangXML, xml)
		selectLines(qp, 1, 1)
		run(a)
		if got := qp.editor.Text(); got != xml || !strings.Contains(a.statusText, "inside a string, comment or CDATA") {
			t.Errorf("async=%v XML: text = %q, status = %q", async, got, a.statusText)
		}

		a, qp = newFormatTestPanel(t, format.LangSQL, src)
		selectLines(qp, 3, 3)
		run(a)
		ind := strings.Repeat(" ", qp.editor.IndentWidth())
		lead := strings.Repeat(" ", qp.editor.IndentWidth()-2) + ", "
		want := "SELECT N'header\nselect a,b from t\nfooter' AS template;\nSELECT\n" + ind + "a\n" + lead + "b\nFROM t"
		if got := qp.editor.Text(); got != want {
			t.Errorf("async=%v control: text = %q, want %q (status %q)", async, got, want, a.statusText)
		}
	}
}
