package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/layout"
)

// Object Explorer geometry for the drag-arming tests: a 100x30 screen whose
// explorer pane is the splitter's first rect, columns [0, 29]. The tree
// box's border takes columns 0 and 29 and its top/bottom rows, so nodes are
// drawn inside rect.Inner(1) and the vertical scrollbar over column 29 —
// inside the pane, and distinct from the splitter bar at column 30.
const (
	dragTestScreenW = 100
	dragTestScreenH = 30
	dragTestSplitX  = 30 // explorerSplit.SplitPos() at ratio 0.3
	dragTestLabelX  = 10 // well clear of the expander glyph at either depth
)

// eventQScreen is fakeSizedScreen plus a real EventQ, so a background load
// kicked off by a click (postAndWake → wakeEventLoop) writes to a channel
// instead of dereferencing the embedded nil tcell.Screen.
type eventQScreen struct {
	*fakeSizedScreen
	q chan tcell.Event
}

func (s *eventQScreen) EventQ() chan tcell.Event { return s.q }

// newDragTestApp builds an App laid out like the real one, with an Object
// Explorer holding one expanded server root and childCount draggable
// children. Columns are used for the children: they're draggable
// (isDraggableNode) but childless (hasChildren), so clicking one can't
// trigger an expand and a background fetch.
func newDragTestApp(t *testing.T, childCount int) *App {
	t.Helper()
	a := newTestApp()
	a.screen = &eventQScreen{
		fakeSizedScreen: &fakeSizedScreen{w: dragTestScreenW, h: dragTestScreenH},
		q:               make(chan tcell.Event, 16),
	}
	a.menuBar = controls.NewMenuBar()
	a.toolbar = controls.NewToolbar()
	a.contextMenu = &controls.ContextMenu{}
	a.alertDialog = dialogs.NewAlertDialog(a.screen)
	a.allDialogs = []Dialog{a.alertDialog}
	a.explorerSplit = layout.NewVerticalSplitter()
	a.explorerSplit.SetBounds(0, 1, dragTestScreenW, dragTestScreenH-2)
	a.explorerSplit.SetRatio(0.3)

	// Bounds before content: the tree scrolls to keep its selection visible
	// on every SetNodes, and doing that against a still-zero-sized rect
	// leaves the scroll offset somewhere far past the end.
	left := a.explorerSplit.FirstRect()
	a.explorer.SetBounds(left.X, left.Y, left.W, left.H)
	if got := left.Right(); got != dragTestSplitX {
		t.Fatalf("explorer pane right edge = %d, want %d (test geometry drifted)", got, dragTestSplitX)
	}

	sc := addTestConn(a, "testsrv")
	root := a.explorer.Selected()
	if root == nil {
		t.Fatal("AddRoot did not select the new root")
	}
	kids := make([]*explorerNode, childCount)
	for i := range kids {
		kids[i] = &explorerNode{
			label: "col",
			data:  nodeData{Type: NodeColumn, Schema: "dbo", Name: "col", conn: sc},
		}
	}
	root.expanded = true
	a.explorer.SetChildren(root, kids)
	return a
}

// draggableChildRow finds the screen row of the first draggable child, so
// the tests don't hard-code the tree's internal row arithmetic.
func draggableChildRow(t *testing.T, a *App) int {
	t.Helper()
	for y := 0; y < dragTestScreenH; y++ {
		if n := a.explorer.NodeAt(dragTestLabelX, y); n != nil && isDraggableNode(n.data.Type) {
			return y
		}
	}
	t.Fatal("no draggable child row found in the explorer pane")
	return 0
}

// selectDraggableChild leaves the selection on a draggable node — the
// precondition for the bug, since arming used to read Selected() rather than
// what the press actually landed on.
func selectDraggableChild(t *testing.T, a *App) {
	t.Helper()
	y := draggableChildRow(t, a)
	a.handleMouse(tcell.NewEventMouse(dragTestLabelX, y, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(dragTestLabelX, y, tcell.ButtonNone, tcell.ModNone))
	a.dragNode = nil // that press legitimately armed one; clear it for the real assertion
	if n := a.explorer.Selected(); n == nil || !isDraggableNode(n.data.Type) {
		t.Fatalf("setup: selection is not a draggable node, got %+v", n)
	}
}

// A press on the tree's vertical scrollbar must not arm a node drag. It used
// to: arming was conditioned only on the press landing somewhere in the
// explorer pane and on Selected() being draggable. Because handleMouse's
// dragNode branch swallows every later event, that killed the scrollbar drag
// outright — the thumb jumped to the press and then never followed the mouse.
func TestScrollbarPressDoesNotArmNodeDrag(t *testing.T) {
	a := newDragTestApp(t, 60) // far more rows than the pane's height
	selectDraggableChild(t, a)

	sbX := dragTestSplitX - 1 // tree rect.Right()-1, where DrawScrollbar paints
	a.handleMouse(tcell.NewEventMouse(sbX, 10, tcell.Button1, tcell.ModNone))
	if a.dragNode != nil {
		t.Errorf("press on the explorer scrollbar armed a node drag (%q)", a.dragNode.label)
	}
	a.handleMouse(tcell.NewEventMouse(sbX, 10, tcell.ButtonNone, tcell.ModNone))
}

// A press on blank space below the last node leaves the selection untouched,
// so arming from Selected() picked up a node the user never grabbed —
// dropping it on the editor then inserted the wrong object.
func TestBlankAreaPressDoesNotArmNodeDrag(t *testing.T) {
	a := newDragTestApp(t, 2) // a couple of rows; the rest of the pane is blank
	selectDraggableChild(t, a)

	a.handleMouse(tcell.NewEventMouse(dragTestLabelX, 20, tcell.Button1, tcell.ModNone))
	if a.dragNode != nil {
		t.Errorf("press on blank space below the tree armed a drag of %q", a.dragNode.label)
	}
	a.handleMouse(tcell.NewEventMouse(dragTestLabelX, 20, tcell.ButtonNone, tcell.ModNone))
}

// The real gesture must still work: a press on a draggable node's own row
// arms a drag of that node.
func TestNodeRowPressArmsNodeDrag(t *testing.T) {
	a := newDragTestApp(t, 5)
	y := draggableChildRow(t, a)

	a.handleMouse(tcell.NewEventMouse(dragTestLabelX, y, tcell.Button1, tcell.ModNone))
	if a.dragNode == nil {
		t.Fatal("press on a draggable node row did not arm a drag")
	}
	if a.dragNode.data.Type != NodeColumn {
		t.Errorf("armed a drag of %v, want the column node under the cursor", a.dragNode.data.Type)
	}
	a.handleMouse(tcell.NewEventMouse(dragTestLabelX, y, tcell.ButtonNone, tcell.ModNone))
}
