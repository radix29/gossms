package layout

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// Splitter
// ---------------------------------------------------------------------------

// SplitterDir describes the orientation of a Splitter.
type SplitterDir int

const (
	SplitterVertical   SplitterDir = iota // column divider (left/right panes)
	SplitterHorizontal                    // row divider (top/bottom panes)
)

// Splitter is a draggable divider between two panel regions.
// It owns the geometry calculation; the caller owns the two child panels.
type Splitter struct {
	rect      core.Rect
	dir       SplitterDir
	ratio     float64 // fraction of the rect dimension given to the first pane
	minRatio  float64
	maxRatio  float64
	dragging  bool
	dragBase  int // screen coordinate where drag started
	ratioBase float64
	label     string // optional label drawn on the splitter bar
	active    bool   // see SetActive
}

// NewHorizontalSplitter creates a top/bottom splitter (editor over results).
func NewHorizontalSplitter(label string) *Splitter {
	return new(Splitter{
		dir:      SplitterHorizontal,
		ratio:    0.5,
		minRatio: 0.1,
		maxRatio: 0.9,
		label:    label,
	})
}

// NewVerticalSplitter creates a left/right splitter (explorer | panels).
func NewVerticalSplitter() *Splitter {
	return new(Splitter{
		dir:      SplitterVertical,
		ratio:    0.3,
		minRatio: 0.1,
		maxRatio: 0.8,
	})
}

// SetBounds positions the splitter in the available rect.
func (sp *Splitter) SetBounds(x, y, w, h int) {
	sp.rect = core.Rect{X: x, Y: y, W: w, H: h}
}

// SetActive marks the splitter's bar (and label, if any) as belonging to
// the region that currently holds keyboard focus, drawing it in the same
// highlighted style used elsewhere for a focused panel/tab (see theme
// "BorderActive" convention, e.g. TreeView's active border).
func (sp *Splitter) SetActive(v bool) { sp.active = v }

// Ratio returns the current split ratio.
func (sp *Splitter) Ratio() float64 { return sp.ratio }

// SetRatio explicitly sets the split ratio.
func (sp *Splitter) SetRatio(r float64) {
	sp.ratio = core.ClampF(r, sp.minRatio, sp.maxRatio)
}

// SplitPos returns the absolute screen coordinate of the divider line.
func (sp *Splitter) SplitPos() int {
	if sp.dir == SplitterHorizontal {
		return sp.rect.Y + int(float64(sp.rect.H)*sp.ratio)
	}
	return sp.rect.X + int(float64(sp.rect.W)*sp.ratio)
}

// FirstRect returns the rect for the first (top or left) pane.
func (sp *Splitter) FirstRect() core.Rect {
	pos := sp.SplitPos()
	if sp.dir == SplitterHorizontal {
		return core.Rect{X: sp.rect.X, Y: sp.rect.Y, W: sp.rect.W, H: pos - sp.rect.Y}
	}
	return core.Rect{X: sp.rect.X, Y: sp.rect.Y, W: pos - sp.rect.X, H: sp.rect.H}
}

// SecondRect returns the rect for the second (bottom or right) pane.
func (sp *Splitter) SecondRect() core.Rect {
	pos := sp.SplitPos()
	if sp.dir == SplitterHorizontal {
		afterBar := pos + 1
		return core.Rect{X: sp.rect.X, Y: afterBar, W: sp.rect.W, H: sp.rect.Y + sp.rect.H - afterBar}
	}
	afterBar := pos + 1
	return core.Rect{X: afterBar, Y: sp.rect.Y, W: sp.rect.X + sp.rect.W - afterBar, H: sp.rect.H}
}

// Draw renders the divider line.
func (sp *Splitter) Draw(s tcell.Screen) {
	p := theme.Active()
	barColor := p.Splitter
	if sp.dragging {
		barColor = p.SplitterHover
	}
	style := tcell.StyleDefault.Background(barColor).Foreground(p.TextDim)
	if sp.active {
		style = tcell.StyleDefault.Background(p.BorderActive).Foreground(tcell.ColorWhite).Bold(true)
	}
	pos := sp.SplitPos()

	if sp.dir == SplitterHorizontal {
		core.FillRect(s, core.Rect{X: sp.rect.X, Y: pos, W: sp.rect.W, H: 1}, '─', style)
		if sp.label != "" {
			core.DrawTextClipped(s, sp.rect.X+2, pos, sp.rect.W-4, style, sp.label)
		}
	} else {
		core.FillRect(s, core.Rect{X: pos, Y: sp.rect.Y, W: 1, H: sp.rect.H}, '│', style)
	}
}

// HandleKey adjusts the ratio with Ctrl+Arrow. Requires Shift to be
// unheld, so it doesn't swallow Ctrl+Shift combos reserved elsewhere (e.g.
// the query editor's Ctrl+Shift+Up/Down "move line" binding).
func (sp *Splitter) HandleKey(ev *tcell.EventKey) bool {
	const step = 0.05
	isResizeMod := ev.Modifiers()&tcell.ModCtrl != 0 && ev.Modifiers()&tcell.ModShift == 0
	if !isResizeMod {
		return false
	}
	if sp.dir == SplitterHorizontal {
		if ev.Key() == tcell.KeyUp {
			sp.ratio = core.ClampF(sp.ratio-step, sp.minRatio, sp.maxRatio)
			return true
		}
		if ev.Key() == tcell.KeyDown {
			sp.ratio = core.ClampF(sp.ratio+step, sp.minRatio, sp.maxRatio)
			return true
		}
	} else {
		if ev.Key() == tcell.KeyLeft {
			sp.ratio = core.ClampF(sp.ratio-step, sp.minRatio, sp.maxRatio)
			return true
		}
		if ev.Key() == tcell.KeyRight {
			sp.ratio = core.ClampF(sp.ratio+step, sp.minRatio, sp.maxRatio)
			return true
		}
	}
	return false
}

// HandleMouse handles drag events on the splitter bar.
// Returns true when the event was consumed (including during active drag).
func (sp *Splitter) HandleMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	pos := sp.SplitPos()

	onBar := false
	if sp.dir == SplitterHorizontal {
		onBar = my == pos && mx >= sp.rect.X && mx < sp.rect.X+sp.rect.W
	} else {
		onBar = mx == pos && my >= sp.rect.Y && my < sp.rect.Y+sp.rect.H
	}

	if !sp.dragging && ev.Buttons() == tcell.Button1 && onBar {
		sp.dragging = true
		if sp.dir == SplitterHorizontal {
			sp.dragBase = my
		} else {
			sp.dragBase = mx
		}
		sp.ratioBase = sp.ratio
		return true
	}
	if sp.dragging {
		if ev.Buttons() == tcell.ButtonNone {
			sp.dragging = false
			return true
		}
		if sp.dir == SplitterHorizontal {
			delta := float64(my-sp.dragBase) / float64(sp.rect.H)
			sp.ratio = core.ClampF(sp.ratioBase+delta, sp.minRatio, sp.maxRatio)
		} else {
			delta := float64(mx-sp.dragBase) / float64(sp.rect.W)
			sp.ratio = core.ClampF(sp.ratioBase+delta, sp.minRatio, sp.maxRatio)
		}
		return true
	}
	return false
}
