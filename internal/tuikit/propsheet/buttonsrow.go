package propsheet

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// ---------------------------------------------------------------------------
// ButtonsRow — a right-flowing row of push buttons (Add/Remove, …)
// ---------------------------------------------------------------------------

// ButtonsRow is a row of push buttons laid out rightward from the row's start,
// unlike ModalDialog's right-aligned DrawButtons: page actions like Add/Remove
// read better flush left with the form.
type ButtonsRow struct {
	buttons []*widgets.Button
	focus   int

	// drawReadOnly dims the buttons — see SetDrawReadOnly.
	drawReadOnly bool
	x, y         int
	// pos is each button's position from the last Layout, reused by Draw's read-only
	// branch so dimmed and live buttons sit in the same cells.
	pos []buttonPos
}

// buttonPos is one button's top-left cell in a ButtonsRow layout.
type buttonPos struct{ X, Y int }

// Buttons returns a row hosting the given buttons in order.
func Buttons(btns ...*widgets.Button) *ButtonsRow {
	return &ButtonsRow{buttons: btns}
}

// Height reports how many lines the buttons flow onto at width w: more than one
// when they don't fit side by side (Database Mail Profiles' Move Up/Move
// Down/Remove Account at 80 columns).
func (r *ButtonsRow) Height(w int) int {
	return r.flow(0, 0, w)[max(0, len(r.buttons)-1)].Y + 1
}

// flow places the buttons left to right from (x, y), starting a new line for one
// that would cross x+w, except the first on a line, which keeps it even when
// wider than w. Wrapping, not clipping: a clipped button can't be clicked.
func (r *ButtonsRow) flow(x, y, w int) []buttonPos {
	pos := make([]buttonPos, max(1, len(r.buttons)))
	pos[0] = buttonPos{X: x, Y: y}
	col, line := x, y
	for i, b := range r.buttons {
		if col > x && col+b.Width() > x+w {
			col, line = x, line+1
		}
		pos[i] = buttonPos{X: col, Y: line}
		col += b.Width() + 2
	}
	return pos
}

func (r *ButtonsRow) Layout(x, y, w int) {
	r.x, r.y = x, y
	r.pos = r.flow(x, y, w)
	for i, b := range r.buttons {
		b.SetBounds(r.pos[i].X, r.pos[i].Y)
	}
}
func (r *ButtonsRow) Focusable() bool { return len(r.buttons) > 0 }

// Buttons returns the row's buttons, for a host reaching one by label.
func (r *ButtonsRow) Buttons() []*widgets.Button { return r.buttons }

// SetDrawReadOnly implements ReadOnlyDrawer: the buttons draw dimmed. They are
// already inert on a read-only form (Form routes no press into a row), so what
// remains is not to look clickable. Drawn here rather than via a widgets.Button
// state no other caller needs.
func (r *ButtonsRow) SetDrawReadOnly(v bool) { r.drawReadOnly = v }

func (r *ButtonsRow) Draw(s tcell.Screen, focused bool) {
	if r.drawReadOnly {
		p := theme.Active()
		st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
		for i, b := range r.buttons {
			if i >= len(r.pos) {
				break // drawn before its first Layout
			}
			core.DrawText(s, r.pos[i].X, r.pos[i].Y, st, "[ "+b.Label()+" ]")
		}
		return
	}
	for i, b := range r.buttons {
		b.Focus(focused && i == r.focus)
		b.Draw(s)
	}
}
func (r *ButtonsRow) HandleKey(ev *tcell.EventKey) bool {
	if len(r.buttons) == 0 {
		return false
	}
	switch ev.Key() {
	case tcell.KeyLeft:
		if r.focus > 0 {
			r.focus--
			return true
		}
	case tcell.KeyRight:
		if r.focus < len(r.buttons)-1 {
			r.focus++
			return true
		}
	}
	return r.buttons[r.focus].HandleKey(ev)
}
func (r *ButtonsRow) HandleMouse(ev *tcell.EventMouse) bool {
	for i, b := range r.buttons {
		if b.HandleMouse(ev) {
			r.focus = i
			return true
		}
	}
	return false
}
