package widgets

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// DropDown is a selector control that shows a pop-up list when open.
type DropDown struct {
	rect     core.Rect
	items    []string
	selected int
	focused  bool
	label    string
	open     bool

	// disabled dropdowns refuse input and draw greyed out, as InputField's do.
	// The zero value is enabled.
	disabled bool

	// mouseDragging distinguishes a fresh Button1 press from a continued
	// hold — mirrors Toolbar's/TreeView's/MenuBar's field of the same name
	// and purpose. Without it, tcell's all-motion mouse tracking resends
	// Buttons()==Button1 on every motion event while the button stays
	// down, so a click that so much as twitches before release would
	// re-toggle open/closed (or re-pick whatever item the pointer drifted
	// onto) on every resent event instead of once per physical click.
	mouseDragging bool

	// top is the first item the open list shows, once the list is taller than
	// the screen leaves room for; screenW/screenH are the screen size the last
	// Draw saw. HandleMouse has no screen, so it hit-tests through listGeometry
	// from these, the same geometry DrawOverlay paints — an unclamped list ran
	// off the bottom with 200 logins and the selection went with it.
	top              int
	screenW, screenH int

	// sbDragging latches a press on the list's scrollbar for the whole gesture
	// (core.HandleScrollbarDrag), so a drag off the bar keeps scrolling rather
	// than picking whatever item the pointer crosses.
	sbDragging bool
}

// NewDropDown creates a DropDown.
func NewDropDown(label string, items []string, w int) *DropDown {
	return new(DropDown{label: label, items: items, rect: core.Rect{W: w}})
}

// SetItems replaces the item list — for a dropdown whose choices depend on
// another control (the schemas of the database a grid row just selected).
// The selection resets to the first item rather than being carried over by
// index: the new list is a different set, so the old index names something
// unrelated. It also closes the list, since an open one is drawn from the
// items it was opened over.
func (d *DropDown) SetItems(items []string) {
	d.items = items
	d.selected = 0
	d.top = 0
	d.open = false
}

// Items returns the current item list.
func (d *DropDown) Items() []string { return d.items }

// Label returns the inline label the dropdown was created with, padded as
// the caller padded it. Read-only: the label is fixed at construction.
func (d *DropDown) Label() string { return d.label }

func (d *DropDown) SetBounds(x, y int) { d.rect.X, d.rect.Y = x, y }

// RectX and Width report the widget's position and the visible width of its
// value area (excluding label and brackets) — the pair InputField exposes, for
// a caller laying a dropdown out in a row alongside other widgets.
func (d *DropDown) RectX() int    { return d.rect.X }
func (d *DropDown) Width() int    { return d.rect.W }
func (d *DropDown) Selected() int { return d.selected }
func (d *DropDown) SetSelected(i int) {
	if i >= 0 && i < len(d.items) {
		d.selected = i
	}
}

// SetWidth changes the value area's width — for a host that sizes the control
// to its items rather than at construction (propsheet.SelectRow.SetFitItems).
func (d *DropDown) SetWidth(w int) { d.rect.W = w }

func (d *DropDown) Focus(v bool) {
	d.focused = v
	if !v {
		d.setOpen(false)
	}
}
func (d *DropDown) Value() string {
	if d.selected >= 0 && d.selected < len(d.items) {
		return d.items[d.selected]
	}
	return ""
}
func (d *DropDown) IsOpen() bool { return d.open }

// SetEnabled toggles whether the selection can be changed, with InputField's
// contract: a disabled dropdown draws greyed out *and* refuses keys and clicks,
// keeps its place in the caller's focus ring, and SetSelected/SetItems still
// work. Disabling closes an open list, which nothing would route events to.
func (d *DropDown) SetEnabled(v bool) {
	d.disabled = !v
	if !v {
		d.setOpen(false)
	}
}

// Enabled reports whether the selection can be changed.
func (d *DropDown) Enabled() bool { return !d.disabled }

// setOpen opens or closes the list. Opening scrolls the selection into view:
// the list may have been scrolled away from it by the wheel last time, or the
// selection set since.
func (d *DropDown) setOpen(v bool) {
	d.open = v
	d.sbDragging = false
	if v {
		d.ensureVisible()
	}
}

// listGeometry returns where the open list is drawn: its first screen row and
// how many items it shows. Below the control when every item fits there, else
// on whichever side has more room (SSMS's combo flips the same way), clamped to
// it. Before the first Draw the screen size is unknown and the list is not
// clamped.
func (d *DropDown) listGeometry() (y, rows int) {
	n := len(d.items)
	below := d.screenH - (d.rect.Y + 1)
	if d.screenH <= 0 || n <= below {
		return d.rect.Y + 1, n
	}
	if above := d.rect.Y; above > below {
		rows = min(n, above)
		return d.rect.Y - rows, rows
	}
	return d.rect.Y + 1, max(below, 1)
}

// scrolled reports whether the open list shows only part of its items, and so
// gives its last column to a scrollbar.
func (d *DropDown) scrolled() bool {
	_, rows := d.listGeometry()
	return rows < len(d.items)
}

// firstShown is top clamped to the current geometry. Read, never written back
// from Draw: a resize changes the geometry, and the handlers own d.top.
func (d *DropDown) firstShown() int {
	_, rows := d.listGeometry()
	return core.Clamp(d.top, 0, max(len(d.items)-rows, 0))
}

// ensureVisible scrolls the list just far enough to show the selection.
func (d *DropDown) ensureVisible() {
	_, rows := d.listGeometry()
	top := d.firstShown()
	if d.selected < top {
		top = d.selected
	} else if rows > 0 && d.selected >= top+rows {
		top = d.selected - rows + 1
	}
	d.top = max(top, 0)
}

// moveTo selects item i, clamped to the list, and scrolls it into view.
func (d *DropDown) moveTo(i int) {
	if len(d.items) == 0 {
		return
	}
	d.selected = core.Clamp(i, 0, len(d.items)-1)
	d.ensureVisible()
}

// jumpToLetter selects the next item after the selection starting with the
// typed letter, case-insensitively, wrapping round — a keyboard user's way
// down a 200-login list. Reports whether any item matched.
func (d *DropDown) jumpToLetter(str string) bool {
	r, _ := utf8.DecodeRuneInString(str)
	if r == utf8.RuneError || !unicode.IsPrint(r) || unicode.IsSpace(r) {
		return false
	}
	want := strings.ToLower(string(r))
	n := len(d.items)
	for k := 1; k <= n; k++ {
		i := (d.selected + k) % n
		if strings.HasPrefix(strings.ToLower(d.items[i]), want) {
			d.moveTo(i)
			return true
		}
	}
	return false
}

func (d *DropDown) inputX() int {
	if d.label != "" {
		return d.rect.X + core.DisplayWidth(d.label) + 1
	}
	return d.rect.X
}

// Draw renders the closed widget box (label, value, arrow). If the list is
// open, call DrawOverlay afterward — once every other widget in the same
// dialog has been drawn — so the open list isn't painted over by fields
// positioned below this one.
func (d *DropDown) Draw(s tcell.Screen) {
	d.screenW, d.screenH = s.Size()
	p := theme.Active()
	if d.label != "" {
		labelFg := p.Text
		if d.disabled {
			labelFg = p.TextDim
		}
		core.DrawText(s, d.rect.X, d.rect.Y, tcell.StyleDefault.Background(p.DialogBg).Foreground(labelFg), d.label)
	}
	ix := d.inputX()
	borderColor := p.InputBorder
	if d.focused && !d.disabled {
		borderColor = p.InputFocused
	}
	borderStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(borderColor)
	core.PutRune(s, ix, d.rect.Y, '[', borderStyle)
	core.PutRune(s, ix+d.rect.W+1, d.rect.Y, ']', borderStyle)
	core.PutRune(s, ix+d.rect.W, d.rect.Y, 'v', borderStyle)

	inputStyle := theme.StyleInput()
	if d.disabled {
		inputStyle = theme.StyleInputDisabled()
	}
	core.FillRect(s, core.Rect{X: ix + 1, Y: d.rect.Y, W: d.rect.W - 1, H: 1}, ' ', inputStyle)
	core.DrawTextClipped(s, ix+1, d.rect.Y, d.rect.W-1, inputStyle, d.Value())
}

// DrawOverlay renders the open item list, if open. Must be called after
// every other widget in the same dialog has drawn, so nothing below this
// dropdown paints over the list.
func (d *DropDown) DrawOverlay(s tcell.Screen) {
	if !d.open {
		return
	}
	d.screenW, d.screenH = s.Size()
	p := theme.Active()
	ix := d.inputX()
	listStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Text)
	selStyle := theme.StyleSelected()
	y0, rows := d.listGeometry()
	top := d.firstShown()
	textW := d.rect.W
	if d.scrolled() {
		textW-- // the last column is the scrollbar's
	}
	for r := range rows {
		i := top + r
		st := listStyle
		if i == d.selected {
			st = selStyle
		}
		core.FillRect(s, core.Rect{X: ix + 1, Y: y0 + r, W: textW, H: 1}, ' ', st)
		core.DrawTextClipped(s, ix+1, y0+r, textW, st, d.items[i])
	}
	if d.scrolled() {
		core.DrawScrollbar(s, ix+d.rect.W, y0, rows, len(d.items), rows, top, listStyle, listStyle)
	}
}

// HandleKey processes keyboard input. Returns false for a key it doesn't
// act on — in particular Up/Down/Escape while closed fall through instead
// of being swallowed, so a caller like propsheet.Form can move focus to
// the next row instead of a closed dropdown eating arrow navigation.
func (d *DropDown) HandleKey(ev *tcell.EventKey) bool {
	if !d.focused || d.disabled {
		return false
	}
	if ev.Key() == tcell.KeyEnter || ev.Key() == tcell.KeyF4 {
		d.setOpen(!d.open)
		return true
	}
	if !d.open {
		return false
	}
	_, rows := d.listGeometry()
	page := max(rows-1, 1)
	switch ev.Key() {
	case tcell.KeyUp:
		d.moveTo(d.selected - 1)
	case tcell.KeyDown:
		d.moveTo(d.selected + 1)
	case tcell.KeyPgUp:
		d.moveTo(d.selected - page)
	case tcell.KeyPgDn:
		d.moveTo(d.selected + page)
	case tcell.KeyHome:
		d.moveTo(0)
	case tcell.KeyEnd:
		d.moveTo(len(d.items) - 1)
	case tcell.KeyEscape:
		d.setOpen(false)
	case tcell.KeyRune:
		if ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0 {
			return false
		}
		d.jumpToLetter(ev.Str())
	default:
		return false
	}
	return true
}

// HandleMouse processes mouse events.
func (d *DropDown) HandleMouse(ev *tcell.EventMouse) bool {
	if ev.Buttons() == tcell.ButtonNone {
		d.mouseDragging = false
		d.sbDragging = false
		return false
	}
	if d.disabled {
		return false
	}
	mx, my := ev.Position()
	ix := d.inputX()
	y0, rows := d.listGeometry()
	inList := d.open && my >= y0 && my < y0+rows && mx >= ix+1 && mx <= ix+d.rect.W
	if b := ev.Buttons(); b&(tcell.WheelUp|tcell.WheelDown) != 0 {
		if !inList {
			return false
		}
		top := d.firstShown()
		if b&tcell.WheelUp != 0 {
			top -= 3
		} else {
			top += 3
		}
		d.top = core.Clamp(top, 0, max(len(d.items)-rows, 0))
		return true
	}
	if ev.Buttons() != tcell.Button1 {
		return false
	}
	if d.open && d.scrolled() && (d.sbDragging || !d.mouseDragging) {
		top := d.firstShown()
		if core.HandleScrollbarDrag(ev, ix+d.rect.W, y0, rows, len(d.items), &d.sbDragging, &top) {
			d.mouseDragging = true
			d.top = top
			return true
		}
	}
	if my == d.rect.Y && mx >= ix && mx <= ix+d.rect.W+1 {
		if !d.mouseDragging {
			d.mouseDragging = true
			d.setOpen(!d.open)
		}
		return true
	}
	if inList {
		if !d.mouseDragging {
			d.mouseDragging = true
			d.selected = d.firstShown() + my - y0
			d.setOpen(false)
		}
		return true
	}
	// A click outside an open list closes it and returns false, so the same
	// click also reaches whatever it landed on. That is the convention across
	// this codebase's overlays — layout.PanelManager's panel combo does the
	// same — not an oversight: the click is aimed at the thing underneath, and
	// swallowing it costs the user a second one. Change both together or the
	// two dropdowns stop behaving alike.
	if d.open {
		d.setOpen(false)
	}
	return false
}
