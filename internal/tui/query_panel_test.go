package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/query"
)

func newTestResult(sets int, withError bool) *query.Result {
	r := &query.Result{}
	for i := 0; i < sets; i++ {
		r.Sets = append(r.Sets, query.ResultSet{Columns: []string{"c"}, Rows: [][]string{{"v"}}})
	}
	r.Messages = append(r.Messages, query.Message{Text: "(1 row affected)"})
	if withError {
		r.Messages = append(r.Messages, query.Message{Text: "boom", IsError: true})
	}
	return r
}

// TestMessagesErrorLinesColoredRed confirms the Messages tab tracks which
// rendered line came from an error message (query.Message.IsError) and
// that messagesHighlighter colors exactly those lines red — a plain
// message stays uncolored, an error message's line(s) don't.
func TestMessagesErrorLinesColoredRed(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.setResult(newTestResult(1, true), false) // "(1 row affected)" + error "boom"

	if !qp.onMessagesTab() {
		t.Fatalf("onMessagesTab() = false after a result with errors, want true (Messages selected)")
	}
	if len(qp.messageErrorLines) != 2 {
		t.Fatalf("messageErrorLines = %v, want 2 entries (one per message)", qp.messageErrorLines)
	}
	if qp.messageErrorLines[0] {
		t.Errorf("messageErrorLines[0] = true for a plain message, want false")
	}
	if !qp.messageErrorLines[1] {
		t.Errorf("messageErrorLines[1] = false for the error message, want true")
	}

	lines := [][]rune{[]rune("(1 row affected)"), []rune("boom")}
	if runs := qp.messagesHighlighter(lines, 0); runs != nil {
		t.Errorf("messagesHighlighter(0) = %v, want nil (not an error line)", runs)
	}
	runs := qp.messagesHighlighter(lines, 1)
	if len(runs) != 1 || runs[0].Start != 0 || runs[0].Len != len("boom") {
		t.Errorf("messagesHighlighter(1) = %v, want a single run covering the whole line", runs)
	}
}

// TestMessagesErrorLinesSpanMultipleLines confirms a single error message
// whose Text itself contains embedded newlines marks every line it
// produces as an error line, not just its first.
func TestMessagesErrorLinesSpanMultipleLines(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	res := &query.Result{Messages: []query.Message{
		{Text: "line one\nline two", IsError: true},
	}}
	qp.setResult(res, false)

	if len(qp.messageErrorLines) != 2 || !qp.messageErrorLines[0] || !qp.messageErrorLines[1] {
		t.Fatalf("messageErrorLines = %v, want [true true]", qp.messageErrorLines)
	}
	if got := qp.messages.Text(); got != "line one\nline two" {
		t.Fatalf("messages.Text() = %q, want %q", got, "line one\nline two")
	}
}

func TestResultTabsAndInitialSelection(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")

	if got := qp.resultTabs(); got != nil {
		t.Fatalf("resultTabs before any run = %v, want nil", got)
	}

	// Single result set: tab labels are "Results" + "Messages", grid selected.
	qp.setResult(newTestResult(1, false), false)
	tabs := qp.resultTabs()
	if len(tabs) != 2 || tabs[0] != "Results" || tabs[1] != "Messages" {
		t.Errorf("tabs = %v, want [Results Messages]", tabs)
	}
	if qp.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0 (first grid)", qp.activeTab)
	}

	// Multiple result sets: numbered labels.
	qp.setResult(newTestResult(2, false), false)
	tabs = qp.resultTabs()
	if len(tabs) != 3 || tabs[0] != "Results 1" || tabs[1] != "Results 2" || tabs[2] != "Messages" {
		t.Errorf("tabs = %v, want [Results 1, Results 2, Messages]", tabs)
	}

	// Errors select the Messages tab, like SSMS.
	qp.setResult(newTestResult(1, true), false)
	if qp.activeTab != 1 {
		t.Errorf("activeTab with errors = %d, want 1 (Messages)", qp.activeTab)
	}

	// No result sets at all: Messages is the only sensible tab.
	qp.setResult(newTestResult(0, false), false)
	if qp.activeTab != 0 || len(qp.resultTabs()) != 1 {
		t.Errorf("activeTab/tabs with no sets = %d/%v, want 0/[Messages]", qp.activeTab, qp.resultTabs())
	}
}

func TestSetActiveTabBounds(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.setResult(newTestResult(2, false), false)

	qp.setActiveTab(2) // Messages
	if qp.activeTab != 2 {
		t.Errorf("activeTab = %d, want 2", qp.activeTab)
	}
	qp.setActiveTab(3) // out of range — ignored
	if qp.activeTab != 2 {
		t.Errorf("activeTab after out-of-range = %d, want 2", qp.activeTab)
	}
	qp.setActiveTab(-1) // out of range — ignored
	if qp.activeTab != 2 {
		t.Errorf("activeTab after negative = %d, want 2", qp.activeTab)
	}
}

// TestQueryPanelCtrlUpDownReachesEditorByDefault is a regression test: the
// splitter used to get first refusal for every key, so Ctrl+Up/Down never
// reached the editor at all while typing — it always resized instead.
func TestQueryPanelCtrlUpDownReachesEditorByDefault(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	qp.setResult(newTestResult(1, false), false)

	if qp.resultsFocused {
		t.Fatalf("resultsFocused = true by default, want false (editor)")
	}
	before := qp.splitter.Ratio()
	if !qp.HandleKey(tcell.NewEventKey(tcell.KeyUp, "", tcell.ModCtrl)) {
		t.Fatalf("Ctrl+Up not consumed")
	}
	if qp.splitter.Ratio() != before {
		t.Errorf("splitter ratio changed %v -> %v while the editor was focused; Ctrl+Up leaked to the resize shortcut", before, qp.splitter.Ratio())
	}
}

// TestQueryPanelCtrlUpDownResizesWhenResultsFocused confirms the resize
// shortcut still works — just gated to when the results grid, not the
// editor, holds focus (mirrors the App-level explorer/panels splitter gate).
func TestQueryPanelCtrlUpDownResizesWhenResultsFocused(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	qp.setResult(newTestResult(1, false), false)
	qp.setResultsFocused(true)

	before := qp.splitter.Ratio()
	if !qp.HandleKey(tcell.NewEventKey(tcell.KeyUp, "", tcell.ModCtrl)) {
		t.Fatalf("Ctrl+Up not consumed while results focused")
	}
	if got := qp.splitter.Ratio(); got >= before {
		t.Errorf("splitter ratio = %v, want less than %v (Ctrl+Up should shrink the results pane)", got, before)
	}
}

// TestQueryPanelEscapeReturnsFocusToEditor confirms Escape is the keyboard
// way back out of the results grid, since nothing else moves focus there
// except a mouse click.
func TestQueryPanelEscapeReturnsFocusToEditor(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	qp.setResultsFocused(true)

	if !qp.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone)) {
		t.Fatalf("Escape not consumed while results focused")
	}
	if qp.resultsFocused {
		t.Errorf("resultsFocused still true after Escape")
	}
}

// TestQueryPanelClickRoutesFocusBetweenEditorAndResults confirms a click is
// what actually moves focus between the two sub-regions — the only way in,
// besides the default, to reach the results grid.
func TestQueryPanelClickRoutesFocusBetweenEditorAndResults(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	qp.setResult(newTestResult(1, false), false)

	resultsRect := qp.splitter.SecondRect()
	click := tcell.NewEventMouse(resultsRect.X+1, resultsRect.Y+2, tcell.Button1, tcell.ModNone)
	if !qp.HandleMouse(click) {
		t.Fatalf("click into results area not handled")
	}
	if !qp.resultsFocused {
		t.Errorf("resultsFocused = false after clicking into results, want true")
	}

	editorRect := qp.splitter.FirstRect()
	click = tcell.NewEventMouse(editorRect.X+10, editorRect.Y, tcell.Button1, tcell.ModNone)
	if !qp.HandleMouse(click) {
		t.Fatalf("click into editor area not handled")
	}
	if qp.resultsFocused {
		t.Errorf("resultsFocused = true after clicking into editor, want false")
	}
}

// TestQueryPanelResultsClickDoesNotAccumulateBlockSelection is a regression
// test for a bug where clicking a cell in the results grid, releasing, and
// then clicking a different cell produced an unwanted block selection
// spanning from the very first click ever made instead of a fresh
// single-cell selection: QueryPanel's HandleMouse forwarded release events
// to the splitter/editor/messages but not to the results grid, so
// DataGrid's mouseDragging flag (which distinguishes a fresh click from a
// continued drag) never got reset — every click after the first one in the
// grid's lifetime was mistaken for a continued drag from that first
// click's anchor. Routing every click here through qp.HandleMouse
// (including the release in between), rather than calling
// qp.results.HandleMouse directly, is what exercises that forwarding path.
func TestQueryPanelResultsClickDoesNotAccumulateBlockSelection(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	res := &query.Result{Sets: []query.ResultSet{{
		Columns: []string{"A", "B"},
		Rows: [][]string{
			{"a0", "b0"},
			{"a1", "b1"},
			{"a2", "b2"},
		},
	}}}
	qp.setResult(res, false)

	// Derived from QueryPanel.layoutChildren + DataGrid.Draw's own layout:
	// the results grid's rect starts one row below the results tab bar
	// (bottom.Y+1), then reserves its own header row and separator before
	// the first data row — so data row i sits at bottom.Y+3+i. The
	// row-number gutter (always on for the results grid) is 3 columns wide
	// for a single-digit row count (3 rows here): 1 digit + 2 padding.
	bottom := qp.splitter.SecondRect()
	row0Y := bottom.Y + 3
	row2Y := bottom.Y + 3 + 2
	colX := bottom.X + 4 // inside column 0, just past the 3-wide gutter

	if !qp.HandleMouse(tcell.NewEventMouse(colX, row0Y, tcell.Button1, tcell.ModNone)) {
		t.Fatal("first click into results not handled")
	}
	qp.HandleMouse(tcell.NewEventMouse(colX, row0Y, tcell.ButtonNone, tcell.ModNone))

	if !qp.HandleMouse(tcell.NewEventMouse(colX, row2Y, tcell.Button1, tcell.ModNone)) {
		t.Fatal("second click into results not handled")
	}
	if r0, c0, r1, c1 := qp.results.SelectionBounds(); r0 != r1 || c0 != c1 {
		t.Fatalf("SelectionBounds() = (%d,%d,%d,%d), want a single cell, not a block accumulated from the first click", r0, c0, r1, c1)
	}
	if row, col := qp.results.SelectedCell(); row != 2 || col != 0 {
		t.Fatalf("SelectedCell() after second click = (%d,%d), want (2,0)", row, col)
	}
}

// TestMessagesTabRendersPlainText confirms the Messages tab's content comes
// from qp.messages (a read-only Editor, filling the whole results pane) and
// not qp.results (the grid) — one line per message, joined with newlines.
func TestMessagesTabRendersPlainText(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)

	qp.setResult(newTestResult(1, true), false) // an error selects the Messages tab
	if !qp.onMessagesTab() {
		t.Fatal("expected Messages tab to be active after an error result")
	}
	want := "(1 row affected)\nboom"
	if got := qp.messages.Text(); got != want {
		t.Errorf("messages.Text() = %q, want %q", got, want)
	}
}

// TestMessagesTabKeysRouteToMessagesEditorNotGrid confirms that once the
// Messages tab is active, keys handed to the results sub-region land on
// qp.messages, not the (now hidden) qp.results grid — the same rect backs
// both, so misrouting would be invisible until Select All/Copy or scrolling
// silently acted on the wrong widget.
func TestMessagesTabKeysRouteToMessagesEditorNotGrid(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	qp.setResult(newTestResult(1, true), false) // Messages tab active
	qp.setResultsFocused(true)

	if !qp.HandleKey(tcell.NewEventKey(tcell.KeyCtrlA, "", tcell.ModNone)) {
		t.Fatalf("Ctrl+A not consumed while on Messages tab")
	}
	if !qp.messages.HasSelection() {
		t.Errorf("expected Ctrl+A to select all in the messages editor")
	}
	if qp.results.HasSelection() {
		t.Errorf("results grid gained a selection; Ctrl+A should not have reached it")
	}
}

// TestWriteCSVWritesHeaderRowsAndBlankLineBetweenSets pins the CSV shape
// Results To File relies on: one header + data rows per set, a blank line
// between sets, and a returned count of data rows only (headers excluded).
func TestWriteCSVWritesHeaderRowsAndBlankLineBetweenSets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.csv")
	sets := []query.ResultSet{
		{Columns: []string{"a", "b"}, Rows: [][]string{{"1", "2"}, {"3", "4"}}},
		{Columns: []string{"x"}, Rows: [][]string{{"y"}}},
	}

	n, err := writeCSV(path, sets)
	if err != nil {
		t.Fatalf("writeCSV: %v", err)
	}
	if n != 3 {
		t.Errorf("n = %d, want 3 (data rows only, headers excluded)", n)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	want := "a,b\n1,2\n3,4\n\nx\ny\n"
	if string(data) != want {
		t.Errorf("file content = %q, want %q", data, want)
	}
}
