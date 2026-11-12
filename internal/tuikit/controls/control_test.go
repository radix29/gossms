package controls

import (
	"testing"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// Which of core's control interfaces each control implements; see the widgets
// package's control_test.go for why these are pinned.
var (
	_ interface {
		core.Focusable
		core.Bounded
		core.KeyHandler
		core.MouseHandler
	} = (*ListBox)(nil)
	_ interface {
		core.Focusable
		core.Bounded
		core.KeyHandler
		core.MouseHandler
		core.Overlay
	} = (*TreeView)(nil)
	_ interface {
		core.Focusable
		core.Bounded
		core.KeyHandler
		core.MouseHandler
		core.Overlay
		core.ClipboardTarget
	} = (*DataGrid)(nil)
	_ interface {
		core.Focusable
		core.Bounded
		core.KeyHandler
		core.MouseHandler
		core.Overlay
		core.ClipboardTarget
	} = (*Editor)(nil)
	_ interface {
		core.Bounded
		core.KeyHandler
		core.MouseHandler
		core.Overlay
	} = (*MenuBar)(nil)
	_ interface {
		core.KeyHandler
		core.MouseHandler
		core.Overlay
	} = (*ContextMenu)(nil)
	_ interface {
		core.Bounded
		core.MouseHandler
		core.Overlay
	} = (*Toolbar)(nil)
	_ interface {
		core.KeyHandler
		core.MouseHandler
		core.Overlay
		core.ClipboardTarget
	} = (*ValueViewer)(nil)
)

func TestControlsReportFocus(t *testing.T) {
	for _, c := range []core.Focusable{NewListBox(), NewTreeView(), NewDataGrid(), NewEditor(nil)} {
		c.Focus(true)
		if !c.Focused() {
			t.Errorf("%T: Focused() = false after Focus(true)", c)
		}
		c.Focus(false)
		if c.Focused() {
			t.Errorf("%T: Focused() = true after Focus(false)", c)
		}
	}
}

func TestControlsReportTheBoundsTheyWereGiven(t *testing.T) {
	want := core.Rect{X: 2, Y: 3, W: 20, H: 7}
	for _, c := range []interface {
		core.Bounded
		SetBounds(x, y, w, h int)
	}{NewListBox(), NewTreeView(), NewDataGrid(), NewEditor(nil)} {
		c.SetBounds(want.X, want.Y, want.W, want.H)
		if got := c.Bounds(); got != want {
			t.Errorf("%T: Bounds() = %+v, want %+v", c, got, want)
		}
	}
}
