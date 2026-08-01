package tui

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/tuikit/controls"
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

	// A real Editor, since Document has no exported constructor — the
	// highlighter only reads Line(idx) off it.
	ed := controls.NewEditor(nil)
	ed.SetText("(1 row affected)\nboom")
	lines := ed.Document()
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

// TestQueryPanelCtrlUpDownReachesEditorByDefault pins down that Ctrl+Up/Down
// reaches the editor while it holds focus: giving the splitter first
// refusal of every key would resize instead of ever reaching the editor.
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

	// The release between the two clicks is what makes the second one a
	// second click rather than the continuation of a drag out of the
	// results grid — see QueryPanel.dragZone, which would otherwise (quite
	// correctly) route it back to the grid.
	qp.HandleMouse(tcell.NewEventMouse(resultsRect.X+1, resultsRect.Y+2, tcell.ButtonNone, tcell.ModNone))

	editorRect := qp.splitter.FirstRect()
	click = tcell.NewEventMouse(editorRect.X+10, editorRect.Y, tcell.Button1, tcell.ModNone)
	if !qp.HandleMouse(click) {
		t.Fatalf("click into editor area not handled")
	}
	if qp.resultsFocused {
		t.Errorf("resultsFocused = true after clicking into editor, want false")
	}
}

// TestQueryPanelEditorDragDoesNotStealSplitterOrTabs pins down the gesture
// ownership QueryPanel.dragZone establishes: a text-selection drag started
// in the editor keeps every event until the button comes back up, even
// once the pointer has wandered down over the splitter bar and the results
// tab bar directly below it. Without it the splitter grabs the drag and
// resizes the panes mid-selection, and every motion event landing on the
// tab row flips the active results tab.
func TestQueryPanelEditorDragDoesNotStealSplitterOrTabs(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	qp.editor.SetText("SELECT 1\nSELECT 2\nSELECT 3\n")
	// Two result sets, so there is more than one tab to switch between.
	qp.setResult(newTestResult(2, false), false)
	qp.setActiveTab(0)

	ratioBefore := qp.splitter.Ratio()
	editorRect := qp.splitter.FirstRect()
	barY := qp.splitter.SplitPos()

	qp.HandleMouse(tcell.NewEventMouse(editorRect.X+10, editorRect.Y, tcell.Button1, tcell.ModNone))
	if qp.dragZone != qZoneEditor {
		t.Fatalf("dragZone = %v after pressing in the editor, want qZoneEditor", qp.dragZone)
	}
	// Drag on down across the splitter bar and the tab row below it.
	for _, y := range []int{barY - 1, barY, qp.tabRect.Y, qp.tabRect.Y + 2} {
		qp.HandleMouse(tcell.NewEventMouse(editorRect.X+10, y, tcell.Button1, tcell.ModNone))
	}
	qp.HandleMouse(tcell.NewEventMouse(editorRect.X+10, qp.tabRect.Y+2, tcell.ButtonNone, tcell.ModNone))

	if got := qp.splitter.Ratio(); got != ratioBefore {
		t.Errorf("splitter ratio = %v after an editor drag crossing the bar, want %v", got, ratioBefore)
	}
	if qp.activeTab != 0 {
		t.Errorf("activeTab = %d after an editor drag crossing the tab row, want 0", qp.activeTab)
	}
	if qp.resultsFocused {
		t.Errorf("resultsFocused = true after a drag that started in the editor, want false")
	}
	if qp.dragZone != qZoneNone {
		t.Errorf("dragZone = %v after the release, want qZoneNone", qp.dragZone)
	}
}

// TestQueryPanelSwallowsWheelDuringADrag is the other half of that gesture
// ownership: a wheel tick isn't part of the drag, so it must be swallowed
// rather than fall through to the positional routing below, which would hand
// it to whichever sub-region the pointer has drifted over — scrolling the
// results grid out from under a text selection still in progress.
//
// App.handleMouse's gestureOwner happens to swallow this one level up today
// (it arms ownerPanels for any press in this column), so this pins the
// invariant where it belongs rather than relying on a caller to keep arming
// one.
func TestQueryPanelSwallowsWheelDuringADrag(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	qp.editor.SetText("SELECT 1\nSELECT 2\nSELECT 3\n")
	// Enough rows that the grid genuinely has somewhere to scroll to —
	// newTestResult's one-row sets would make the assertion below vacuous.
	big := &query.Result{Sets: []query.ResultSet{{Columns: []string{"c"}}}}
	for i := 0; i < 200; i++ {
		big.Sets[0].Rows = append(big.Sets[0].Rows, []string{"v"})
	}
	qp.setResult(big, false)
	qp.setActiveTab(0)

	editorRect := qp.splitter.FirstRect()
	qp.HandleMouse(tcell.NewEventMouse(editorRect.X+10, editorRect.Y, tcell.Button1, tcell.ModNone))
	if qp.dragZone != qZoneEditor {
		t.Fatalf("dragZone = %v after pressing in the editor, want qZoneEditor", qp.dragZone)
	}
	scrollBefore := qp.results.ScrollRow()

	// Wheel, mid-drag, down over the results grid. Several ticks, so the
	// assertion doesn't depend on one being enough to move a short grid.
	for i := 0; i < 5; i++ {
		if !qp.HandleMouse(tcell.NewEventMouse(editorRect.X+10, qp.tabRect.Y+3, tcell.WheelDown, tcell.ModNone)) {
			t.Fatal("HandleMouse returned false for a wheel tick during a drag; it must consume it")
		}
	}
	if got := qp.results.ScrollRow(); got != scrollBefore {
		t.Errorf("results grid scrolled %d -> %d during an editor drag, want it unchanged", scrollBefore, got)
	}
	if qp.dragZone != qZoneEditor {
		t.Errorf("dragZone = %v after a mid-drag wheel, want it still qZoneEditor", qp.dragZone)
	}

	// The release ends the gesture, so the swallow is scoped to it.
	qp.HandleMouse(tcell.NewEventMouse(editorRect.X+10, qp.tabRect.Y+3, tcell.ButtonNone, tcell.ModNone))
	if qp.dragZone != qZoneNone {
		t.Errorf("dragZone = %v after the release, want qZoneNone", qp.dragZone)
	}
}

// TestQueryPanelResultsClickDoesNotAccumulateBlockSelection pins down that
// QueryPanel's HandleMouse forwards release events to the results grid, not
// only to the splitter/editor/messages: without that, DataGrid's
// mouseDragging flag (which distinguishes a fresh click from a continued
// drag) never resets, and every click after the first is mistaken for a
// drag continuing from that first click's anchor. Routing every click
// through qp.HandleMouse, including the release in between, rather than
// calling qp.results.HandleMouse directly, is what exercises that path.
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

// TestResultsToTextRendersAlignedTable confirms Query > Results To Text
// renders into qp.resultsText (a read-only Editor) as a header row, a
// dashed separator, and one line per data row, each column padded to its
// widest value so they line up like a real table.
func TestResultsToTextRendersAlignedTable(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	qp.resultsMode = ResultsModeText
	res := &query.Result{Sets: []query.ResultSet{{
		Columns: []string{"ID", "Name"},
		Rows: [][]string{
			{"1", "Alice"},
			{"22", "Bob"},
		},
	}}}
	qp.setResult(res, false)

	if qp.onMessagesTab() {
		t.Fatal("expected the Results tab, not Messages, to be active")
	}
	if !qp.textTabActive() {
		t.Fatal("expected textTabActive() = true in ResultsModeText")
	}
	want := "ID Name \n-- -----\n1  Alice\n22 Bob  "
	if got := qp.resultsText.Text(); got != want {
		t.Errorf("resultsText.Text() = %q, want %q", got, want)
	}
}

// TestResultsToTextKeysRouteToResultsTextNotGrid confirms that once Results
// To Text is active, keys handed to the results sub-region land on
// qp.resultsText, not the (now hidden) qp.results grid — the same rect
// backs both, so misrouting would be invisible until Select All/Copy or
// scrolling silently acted on the wrong widget (mirrors
// TestMessagesTabKeysRouteToMessagesEditorNotGrid).
func TestResultsToTextKeysRouteToResultsTextNotGrid(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	qp.resultsMode = ResultsModeText
	qp.setResult(newTestResult(1, false), false)
	qp.setResultsFocused(true)

	if !qp.HandleKey(tcell.NewEventKey(tcell.KeyCtrlA, "", tcell.ModNone)) {
		t.Fatalf("Ctrl+A not consumed while on the Results To Text tab")
	}
	if !qp.resultsText.HasSelection() {
		t.Errorf("expected Ctrl+A to select all in the results-text editor")
	}
	if qp.results.HasSelection() {
		t.Errorf("results grid gained a selection; Ctrl+A should not have reached it")
	}
}

// TestCSVSinkWritesHeaderRowsAndBlankLineBetweenSets pins the CSV shape
// Results To File relies on: one header + data rows per set, a blank line
// between sets. The expected bytes are unchanged from the buffer-everything
// writeCSV this replaced — the switch to streaming was meant to change when
// rows are written, not what ends up in the file.
func TestCSVSinkWritesHeaderRowsAndBlankLineBetweenSets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.csv")
	sets := []query.ResultSet{
		{Columns: []string{"a", "b"}, Rows: [][]string{{"1", "2"}, {"3", "4"}}},
		{Columns: []string{"x"}, Rows: [][]string{{"y"}}},
	}

	sink, err := newCSVSink(path)
	if err != nil {
		t.Fatalf("newCSVSink: %v", err)
	}
	n := 0
	for _, set := range sets {
		if err := sink.BeginSet(set.Columns); err != nil {
			t.Fatalf("BeginSet: %v", err)
		}
		for _, row := range set.Rows {
			if err := sink.Row(row); err != nil {
				t.Fatalf("Row: %v", err)
			}
			n++
		}
		if err := sink.EndSet(len(set.Rows)); err != nil {
			t.Fatalf("EndSet: %v", err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if n != 3 {
		t.Errorf("wrote %d data rows, want 3 (headers excluded)", n)
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

// A cell containing the delimiter, a quote, or a newline must come back out
// as the same value — csv.Writer quotes it, and the streaming path must not
// have bypassed that.
func TestCSVSinkQuotesAwkwardCells(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.csv")
	sink, err := newCSVSink(path)
	if err != nil {
		t.Fatalf("newCSVSink: %v", err)
	}
	if err := sink.BeginSet([]string{"col"}); err != nil {
		t.Fatalf("BeginSet: %v", err)
	}
	for _, cell := range []string{"a,b", `say "hi"`, "line1\nline2"} {
		if err := sink.Row([]string{cell}); err != nil {
			t.Fatalf("Row(%q): %v", cell, err)
		}
	}
	if err := sink.EndSet(3); err != nil {
		t.Fatalf("EndSet: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	want := [][]string{{"col"}, {"a,b"}, {`say "hi"`}, {"line1\nline2"}}
	if len(recs) != len(want) {
		t.Fatalf("read %d records, want %d: %q", len(recs), len(want), recs)
	}
	for i := range want {
		if recs[i][0] != want[i][0] {
			t.Errorf("record %d = %q, want %q", i, recs[i][0], want[i][0])
		}
	}
}

// TestReconnectNeverConnectedNoOp confirms Reconnect on a panel that never
// had a connection (p.conn == nil, e.g. "New Query" with nothing selected
// in Object Explorer) just reports status — there's no Opts to redial with,
// and nothing else about the panel should change.
func TestReconnectNeverConnectedNoOp(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")

	qp.Reconnect()

	if qp.conn != nil {
		t.Errorf("conn = %+v, want nil", qp.conn)
	}
	if a.statusText != "Nothing to reconnect — this query window was never connected" {
		t.Errorf("status = %q, want the never-connected notice", a.statusText)
	}
}

// TestReconnectWhileExecutingNoOp confirms Reconnect refuses to tear down
// the connection a query is actively running on.
func TestReconnectWhileExecutingNoOp(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	sc := &db.ServerConn{Opts: config.Connection{Server: "fake-server"}}
	qp.conn = sc
	qp.executing = true

	qp.Reconnect()

	if qp.conn != sc {
		t.Errorf("conn = %+v, want unchanged (sc)", qp.conn)
	}
	if !sc.IsOpen() {
		t.Error("sc was closed despite the panel still executing")
	}
	if a.statusText != "Cannot reconnect while a query is executing" {
		t.Errorf("status = %q, want the executing notice", a.statusText)
	}
}

// TestNotConnectedMessageDistinguishesNeverConnected confirms
// notConnectedMessage points at Query > Reconnect only when there's an
// actual (if now-closed) connection to redial with — a panel that was never
// connected at all has nothing for Reconnect to act on, so it still points
// at File > Connect.
func TestNotConnectedMessageDistinguishesNeverConnected(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")

	if got := qp.notConnectedMessage(); got != "Not connected — use File > Connect" {
		t.Errorf("notConnectedMessage() with nil conn = %q, want the File > Connect notice", got)
	}

	sc := &db.ServerConn{Opts: config.Connection{Server: "fake-server"}}
	sc.Close()
	qp.conn = sc
	if got := qp.notConnectedMessage(); got != "Not connected — use Query > Reconnect" {
		t.Errorf("notConnectedMessage() with a dropped conn = %q, want the Reconnect notice", got)
	}
}

// The results status line used to live on the DataGrid, which isn't drawn
// on the Messages, Results To Text or Execution Plan tabs — so elapsed
// time, row counts and the live "Executing..." counter all vanished the
// moment the tab changed. Every tab has to produce one.
func TestResultsStatusTextOnEveryTab(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	qp.setResult(newTestResult(1, false), false)

	// Results grid.
	qp.setActiveTab(0)
	if got := qp.resultsStatusText(); !strings.Contains(got, "1 rows") || !strings.Contains(got, "Row: 1") {
		t.Errorf("grid tab status = %q, want the row/column position and row count", got)
	}

	// Results To Text renders the same set through the editor instead.
	qp.resultsMode = ResultsModeText
	if !qp.textTabActive() {
		t.Fatal("textTabActive() = false after switching to Results To Text")
	}
	if got := qp.resultsStatusText(); !strings.Contains(got, "1 rows") {
		t.Errorf("text tab status = %q, want the row count", got)
	}
	qp.resultsMode = ResultsModeGrid

	// Messages.
	qp.setActiveTab(qp.messagesTabIndex())
	if got := qp.resultsStatusText(); !strings.Contains(got, "message") {
		t.Errorf("messages tab status = %q, want a message count", got)
	}
}

// While a query runs the status is the live elapsed counter, whichever tab
// is showing — that's the whole point of tickExecuting waking the loop.
func TestResultsStatusTextWhileExecuting(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	qp.setResult(newTestResult(1, false), false)
	qp.resultsMode = ResultsModeText
	qp.executing = true
	qp.execStart = time.Now()

	if got := qp.resultsStatusText(); !strings.Contains(got, "Executing") {
		t.Errorf("status while executing on the text tab = %q, want the elapsed/Executing counter", got)
	}
}

// setEstimatedPlan clears p.result; the previous run's row count must not
// be left sitting there describing a plan it has nothing to do with.
func TestResultsStatusTextNotStaleAfterEstimatedPlan(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	qp.setResult(newTestResult(1, false), false)
	stale := qp.resultsStatusText()

	qp.result = nil
	qp.planView = qp.newPlanView()

	got := qp.resultsStatusText()
	if got == stale {
		t.Errorf("status still %q after the result was cleared", got)
	}
	if !strings.Contains(got, "Estimated") {
		t.Errorf("status with a plan but no result = %q, want it to say so", got)
	}
}

// The three non-grid tabs are laid out one row shorter so the status line
// has somewhere to go; the grid keeps the full height and draws its own.
func TestNonGridTabsReserveTheStatusRow(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.setResult(newTestResult(1, false), false)
	qp.SetBounds(0, 0, 80, 24)

	bottom := qp.splitter.SecondRect()
	if qp.statusRect.H != 1 {
		t.Fatalf("statusRect.H = %d, want 1", qp.statusRect.H)
	}
	if want := bottom.Bottom() - 1; qp.statusRect.Y != want {
		t.Errorf("statusRect.Y = %d, want %d — the results area's own bottom row, the same one the grid puts its status on",
			qp.statusRect.Y, want)
	}
	if qp.statusRect.X != bottom.X || qp.statusRect.W != bottom.W {
		t.Errorf("statusRect spans %d..%d, want the full results width %d..%d",
			qp.statusRect.X, qp.statusRect.X+qp.statusRect.W, bottom.X, bottom.X+bottom.W)
	}
}

// Too short to spare a row: no status rather than a status where a line of
// content should be.
func TestNoStatusRowWhenResultsAreaIsTiny(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.setResult(newTestResult(1, false), false)
	qp.SetBounds(0, 0, 80, 5)

	if qp.splitter.SecondRect().H > 1 {
		t.Skip("results area still has room to spare at this size")
	}
	if qp.statusRect.H != 0 {
		t.Errorf("statusRect = %+v, want the zero rect in a results area with no row to spare", qp.statusRect)
	}
}

// TestClearResultsEmptiesPreviousRun checks the results area is emptied
// before a new execution — the previous run's grid rows, tab bar, messages
// and plan must not sit there looking current while the query runs.
func TestClearResultsEmptiesPreviousRun(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	qp.setResult(newTestResult(1, false), false)
	qp.setActiveTab(0)
	if qp.results.Row(0) == nil {
		t.Fatalf("grid has no rows after setResult, nothing for clearResults to clear")
	}

	qp.clearResults()

	if qp.result != nil {
		t.Errorf("result = %v, want nil", qp.result)
	}
	if qp.planView != nil {
		t.Error("planView is still set, want nil")
	}
	if got := qp.results.Row(0); got != nil {
		t.Errorf("grid row 0 = %v, want nil (grid cleared)", got)
	}
	if got := qp.messages.Text(); got != "" {
		t.Errorf("messages text = %q, want empty", got)
	}
	if qp.messageErrorLines != nil {
		t.Errorf("messageErrorLines = %v, want nil", qp.messageErrorLines)
	}
	if qp.tabCount() != 0 {
		t.Errorf("tabCount() = %d, want 0 (tab bar gone)", qp.tabCount())
	}
}
