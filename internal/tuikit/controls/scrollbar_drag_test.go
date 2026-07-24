package controls

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// TestDataGridScrollbarDragScrolls confirms a Button1 press on the
// scrollbar column (rect.Right()-1) jumps scrollRow proportionally instead
// of being read as a row click, and that a subsequent drag (still Button1,
// x drifted off the bar's exact column) keeps controlling the scroll.
func TestDataGridScrollbarDragScrolls(t *testing.T) {
	g := newTestDataGrid() // 40x10 -> dataH = 7
	cols := []string{"A"}
	rows := make([][]string, 20)
	for i := range rows {
		rows[i] = []string{"x"}
	}
	g.SetData(cols, rows)
	sbX := g.rect.Right() - 1

	if !g.HandleMouse(tcell.NewEventMouse(sbX, g.rect.Y+2, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse on scrollbar column should be handled")
	}
	if g.scrollRow != 0 {
		t.Errorf("scrollRow after clicking track top = %d, want 0", g.scrollRow)
	}
	if !g.sbDragging {
		t.Fatal("sbDragging should be true after pressing on the scrollbar")
	}
	if g.selRow != 0 {
		t.Errorf("selRow = %d, clicking the scrollbar must not also select a row", g.selRow)
	}

	// Drag downward, x drifted left off the bar's exact column — the drag
	// must still control scroll, not fall through to row selection.
	g.HandleMouse(tcell.NewEventMouse(sbX-2, g.rect.Y+2+6, tcell.Button1, tcell.ModNone))
	if g.scrollRow == 0 {
		t.Error("scrollRow should have advanced after dragging toward the bottom of the track")
	}
	if g.selRow != 0 {
		t.Errorf("selRow = %d, a scrollbar drag must never change row selection", g.selRow)
	}

	g.HandleMouse(tcell.NewEventMouse(sbX-2, g.rect.Y+2+6, tcell.ButtonNone, tcell.ModNone))
	if g.sbDragging {
		t.Error("sbDragging should reset on release")
	}
}

// TestTreeViewScrollbarDragScrolls mirrors the DataGrid case for TreeView,
// whose scrollbar is drawn over the border column rather than real content.
func TestTreeViewScrollbarDragScrolls(t *testing.T) {
	tv := NewTreeView()
	tv.SetBounds(0, 0, 40, 10) // inner.H = 8
	nodes := make([]TreeNode, 20)
	for i := range nodes {
		nodes[i] = TreeNode{ID: TreeNodeID(i + 1), Label: "n"}
	}
	tv.SetNodes(nodes)
	sbX := tv.rect.Right() - 1
	inner := tv.rect.Inner(1)

	if !tv.HandleMouse(tcell.NewEventMouse(sbX, inner.Y+inner.H-1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse on scrollbar column should be handled")
	}
	if !tv.sbDragging {
		t.Fatal("sbDragging should be true after pressing on the scrollbar")
	}
	if tv.scroll == 0 {
		t.Error("scroll should have jumped forward when clicking near the bottom of the track")
	}
	if tv.sel != 0 {
		t.Errorf("sel = %d, clicking the scrollbar must not also select a node", tv.sel)
	}

	tv.HandleMouse(tcell.NewEventMouse(sbX, inner.Y, tcell.ButtonNone, tcell.ModNone))
	if tv.sbDragging {
		t.Error("sbDragging should reset on release")
	}
}

// TestListBoxScrollbarDragScrolls mirrors the DataGrid case for ListBox.
func TestListBoxScrollbarDragScrolls(t *testing.T) {
	items := make([]string, 20)
	for i := range items {
		items[i] = "item"
	}
	l := newTestListBox(items...)
	sbX := l.rect.Right() - 1

	if !l.HandleMouse(tcell.NewEventMouse(sbX, l.rect.Y+l.rect.H-1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse on scrollbar column should be handled")
	}
	if !l.sbDragging {
		t.Fatal("sbDragging should be true after pressing on the scrollbar")
	}
	if l.scroll == 0 {
		t.Error("scroll should have jumped forward when clicking near the bottom of the track")
	}
	if l.sel != 0 {
		t.Errorf("sel = %d, clicking the scrollbar must not also select an item", l.sel)
	}

	l.HandleMouse(tcell.NewEventMouse(sbX, l.rect.Y, tcell.ButtonNone, tcell.ModNone))
	if l.sbDragging {
		t.Error("sbDragging should reset on release")
	}
}

// TestEditorScrollbarDragScrolls mirrors the DataGrid case for Editor in
// plain (non-wrap) mode.
func TestEditorScrollbarDragScrolls(t *testing.T) {
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "line"
	}
	e := newTestEditor(strings.Join(lines, "\n"))
	e.SetBounds(0, 0, 30, 10)
	sbX := e.rect.Right() - 1

	if !e.HandleMouse(tcell.NewEventMouse(sbX, e.rect.Y+e.rect.H-1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse on scrollbar column should be handled")
	}
	if !e.sbDragging {
		t.Fatal("sbDragging should be true after pressing on the scrollbar")
	}
	if e.scrollRow == 0 {
		t.Error("scrollRow should have jumped forward when clicking near the bottom of the track")
	}
	if e.cursorRow != 0 {
		t.Errorf("cursorRow = %d, clicking the scrollbar must not move the text cursor", e.cursorRow)
	}

	e.HandleMouse(tcell.NewEventMouse(sbX, e.rect.Y, tcell.ButtonNone, tcell.ModNone))
	if e.sbDragging {
		t.Error("sbDragging should reset on release")
	}
}

// TestEditorCompletionPopupScrollbarDragScrolls confirms dragging the
// IntelliSense popup's own scrollbar scrolls its candidate list instead of
// being read as a click that commits whatever candidate sits under it.
func TestEditorCompletionPopupScrollbarDragScrolls(t *testing.T) {
	e := newTestEditor("")
	e.SetBounds(0, 0, 40, 20)
	candidates := make([]string, 15)
	for i := range candidates {
		candidates[i] = "c" + string(rune('a'+i))
	}
	e.SetCompletionProvider(testCompletionProvider(candidates...))
	e.HandleKey(runeKey('c', tcell.ModNone))
	if !e.completionOpen {
		t.Fatal("completion popup should be open after typing a matching prefix")
	}
	if len(e.completionItems) <= maxCompletionRows {
		t.Fatalf("test needs more candidates than maxCompletionRows (%d) to exercise scrolling", maxCompletionRows)
	}

	rect := e.completionRect()
	sbX := rect.Right() - 1
	selBefore := e.completionSel

	if !e.HandleMouse(tcell.NewEventMouse(sbX, rect.Y+rect.H-1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse on the popup's scrollbar column should be handled")
	}
	if !e.completionSbDragging {
		t.Fatal("completionSbDragging should be true after pressing on the scrollbar")
	}
	if e.completionScroll == 0 {
		t.Error("completionScroll should have jumped forward when clicking near the bottom of the track")
	}
	if e.completionSel != selBefore {
		t.Errorf("completionSel changed to %d, clicking the scrollbar must not commit/select a candidate", e.completionSel)
	}
	if !e.completionOpen {
		t.Error("dragging the scrollbar must not close the popup")
	}

	e.HandleMouse(tcell.NewEventMouse(sbX, rect.Y, tcell.ButtonNone, tcell.ModNone))
	if e.completionSbDragging {
		t.Error("completionSbDragging should reset on release")
	}
}

// TestEditorScrollbarDragScrollsWrapMode confirms the same works in
// word-wrap mode, where the scrollbar's total is visual rows, not logical
// lines.
func TestEditorScrollbarDragScrollsWrapMode(t *testing.T) {
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "line"
	}
	e := newTestEditor(strings.Join(lines, "\n"))
	e.SetWrapMode(true)
	e.SetBounds(0, 0, 30, 10)
	sbX := e.rect.Right() - 1

	if !e.HandleMouse(tcell.NewEventMouse(sbX, e.rect.Y+e.rect.H-1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("HandleMouse on scrollbar column should be handled in wrap mode")
	}
	if !e.sbDragging {
		t.Fatal("sbDragging should be true after pressing on the scrollbar")
	}
	if e.scrollRow == 0 {
		t.Error("scrollRow should have jumped forward when clicking near the bottom of the track")
	}
	if e.cursorRow != 0 {
		t.Errorf("cursorRow = %d, clicking the scrollbar must not move the text cursor", e.cursorRow)
	}
}
