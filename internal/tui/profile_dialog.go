package tui

import (
	"slices"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// Profile dialog geometry. The labels are padded to profileLabelWidth so the
// two value boxes line up.
const (
	profileDialogW    = 64
	profileDialogH    = 14
	profileLabelWidth = 19
	profileValueWidth = 36
)

var profileButtons = []string{"OK", "Cancel"}

// ProfileDialog names a saved connection for the command line (`gossms profile
// test <name>`) and sets the highest operation class the command line may run
// through it — the Connect dialog's Profile... button, on the History row
// selected. An empty name turns the entry back into a plain recent connection.
//
// The focus ring is the name, then the dropdown; past either end is the button
// row (docs/ui-rules.md § Dialogs).
type ProfileDialog struct {
	dialogs.ModalDialog

	conn    string // the connection's generated name, shown at the top
	fName   *widgets.InputField
	ddAllow *widgets.DropDown

	focusIdx  int
	focusable []focusable
	onButtons bool
	btnFocus  int
	status    string

	// onAccept applies the name and class; an error keeps the dialog open
	// with it shown (a duplicate name, a failed check).
	onAccept func(name string, allow config.AllowClass) error

	drag dialogs.FieldGesture
}

// NewProfileDialog creates the dialog.
func NewProfileDialog(s tcell.Screen) *ProfileDialog {
	d := &ProfileDialog{}
	d.InitModal(s, "Connection Profile", profileDialogW, profileDialogH)
	label := func(s string) string { return core.PadRight(s, profileLabelWidth) }
	d.fName = widgets.NewInputField(label("Profile Name"), profileValueWidth, false)
	classes := config.AllAllowClasses()
	items := make([]string, len(classes))
	for i, c := range classes {
		items[i] = config.AllowClassLabel(c)
	}
	d.ddAllow = widgets.NewDropDown(label("Allowed Operations"), items, profileValueWidth)
	d.focusable = []focusable{d.fName, d.ddAllow}
	return d
}

// ShowProfile opens the dialog on conn, pre-filled with its current name and
// ceiling (Admin for an unnamed one, the config default). onAccept runs on OK.
func (d *ProfileDialog) ShowProfile(conn config.Connection, onAccept func(string, config.AllowClass) error) {
	d.conn = conn.GeneratedName()
	d.onAccept = onAccept
	d.status = ""
	d.fName.SetValue(conn.Profile)
	d.fName.SelectAll()
	d.ddAllow.SetSelected(max(slices.Index(config.AllAllowClasses(), conn.Ceiling()), 0))
	d.btnFocus = 0
	d.drag.Clear()
	d.ModalDialog.Show()
	d.setFocus(0)
}

func (d *ProfileDialog) setFocus(i int) {
	d.onButtons = false
	d.focusIdx = setFocusIn(d.focusable, i, d.focusIdx)
}

func (d *ProfileDialog) focused() focusable {
	if d.onButtons || d.focusIdx < 0 || d.focusIdx >= len(d.focusable) {
		return nil
	}
	return d.focusable[d.focusIdx]
}

// step moves focus dir along the ring; past either end is the button row.
func (d *ProfileDialog) step(dir int) {
	if i := d.focusIdx + dir; i >= 0 && i < len(d.focusable) {
		d.setFocus(i)
		return
	}
	d.onButtons = true
	setFocusIn(d.focusable, -1, d.focusIdx)
}

// leaveButtons is Tab/Backtab off the button row, back onto the first or last
// field; the highlight returns to OK, which Enter in a field fires.
func (d *ProfileDialog) leaveButtons(dir int) {
	d.btnFocus = 0
	if dir > 0 {
		d.setFocus(0)
	} else {
		d.setFocus(len(d.focusable) - 1)
	}
}

func (d *ProfileDialog) allow() config.AllowClass {
	return config.AllAllowClasses()[d.ddAllow.Selected()]
}

// accept validates and applies. A refusal stays open with the reason.
func (d *ProfileDialog) accept() {
	name := strings.TrimSpace(d.fName.Value())
	if err := config.ValidateProfileName(name); err != nil {
		d.status = err.Error()
		return
	}
	if d.onAccept != nil {
		if err := d.onAccept(name, d.allow()); err != nil {
			d.status = err.Error()
			return
		}
	}
	d.Hide()
}

func (d *ProfileDialog) doButton() {
	if d.btnFocus == 0 {
		d.accept()
		return
	}
	d.Hide()
}

// Draw renders the dialog.
func (d *ProfileDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	p := theme.Active()
	text := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	dim := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
	inner := d.InnerRect()
	x, w := inner.X+1, inner.W-2

	core.DrawTextClipped(s, x, inner.Y+1, w, text.Bold(true), core.Truncate(d.conn, w))
	d.fName.SetBounds(x, inner.Y+3)
	d.ddAllow.SetBounds(x, inner.Y+4)
	d.fName.Draw(s)
	d.ddAllow.Draw(s)
	core.DrawTextClipped(s, x, inner.Y+6, w, dim, "The command line uses it: gossms profile test <name>")
	core.DrawTextClipped(s, x, inner.Y+7, w, dim, "An empty name makes it a plain recent connection.")
	if d.status != "" {
		core.DrawTextClipped(s, x, d.ButtonRowY()-2, w,
			tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Error), d.status)
	}

	d.DrawSeparator(s)
	active := -1
	if d.onButtons {
		active = d.btnFocus
	}
	d.DrawButtons(s, profileButtons, active)
	d.ddAllow.DrawOverlay(s)
}

// HandleKey routes keyboard events. Enter in a field is OK, except on an open
// dropdown, where it picks the item.
func (d *ProfileDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	if d.onButtons {
		return buttonRowKey(ev, &d.btnFocus, len(profileButtons), nil, d.doButton, d.Hide, d.leaveButtons)
	}
	if d.ddAllow.IsOpen() {
		if ev.Key() == tcell.KeyEscape {
			// Close the list, not the dialog.
			d.ddAllow.HandleKey(ev)
			return true
		}
		d.ddAllow.HandleKey(ev)
		return true
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		d.Hide()
		return true
	case tcell.KeyTab:
		d.step(+1)
		return true
	case tcell.KeyBacktab:
		d.step(-1)
		return true
	case tcell.KeyEnter:
		d.btnFocus = 0
		d.accept()
		return true
	}
	switch w := d.focused().(type) {
	case *widgets.InputField:
		d.status = ""
		return w.HandleKey(ev)
	case *widgets.DropDown:
		return w.HandleKey(ev)
	}
	return true
}

// HandleMouse routes mouse events.
func (d *ProfileDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	if ev.Buttons() == tcell.ButtonNone {
		d.ddAllow.HandleMouse(ev)
		d.drag.Release(ev)
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if forwardReleaseToFocusedField(ev, d.focused()) {
		return true
	}
	if d.drag.Replay(ev) {
		return true
	}
	if ev.Buttons() != tcell.Button1 {
		return true
	}
	// The open list overlays the rows below it, the button row included.
	if d.ddAllow.HandleMouse(ev) {
		d.setFocus(indexOfFocusable(d.focusable, d.ddAllow))
		return true
	}
	if i := d.ButtonClicked(ev, profileButtons); i >= 0 {
		d.btnFocus = i
		d.doButton()
		return true
	}
	if mx, my := ev.Position(); d.fName.HitTest(mx, my) {
		d.setFocus(indexOfFocusable(d.focusable, d.fName))
		d.drag.Claim(d.fName, ev)
	}
	return true
}

// FocusedClipboardTarget implements core.ClipboardHost: the name field while
// it has focus.
func (d *ProfileDialog) FocusedClipboardTarget() core.ClipboardTarget {
	if d.focused() == focusable(d.fName) {
		return d.fName
	}
	return nil
}
