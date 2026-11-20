package tui

import (
	"slices"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/tuikit/core"
	"github.com/radix29/tuikit/dialogs"
	"github.com/radix29/tuikit/theme"
	"github.com/radix29/tuikit/widgets"
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
// The focus ring is the name, then the dropdown, then the button row
// (docs/ui-rules.md § Dialogs).
type ProfileDialog struct {
	dialogs.ModalDialog

	conn    string // the connection's generated name, shown at the top
	fName   *widgets.InputField
	ddAllow *widgets.DropDown

	// ring is the focus ring over focusable and profileButtons; its default
	// button is OK, so Enter from a field accepts.
	focusable []focusable
	ring      dialogs.FieldRing
	status    string

	// onAccept applies the name and class; an error keeps the dialog open
	// with it shown (a duplicate name, a failed check).
	onAccept func(name string, allow config.AllowClass) error
}

// NewProfileDialog creates the dialog.
func NewProfileDialog() *ProfileDialog {
	d := &ProfileDialog{}
	d.InitModal("Connection Profile", profileDialogW, profileDialogH)
	label := func(s string) string { return core.PadRight(s, profileLabelWidth) }
	d.fName = widgets.NewInputField(label("Profile Name"), profileValueWidth, false)
	classes := config.AllAllowClasses()
	items := make([]string, len(classes))
	for i, c := range classes {
		items[i] = config.AllowClassLabel(c)
	}
	d.ddAllow = widgets.NewDropDown(label("Allowed Operations"), items, profileValueWidth)
	d.focusable = []focusable{d.fName, d.ddAllow}
	d.ring = dialogs.FieldRing{
		Fields:  func() []focusable { return d.focusable },
		Buttons: func() []string { return profileButtons },
		Press:   d.doButton,
		Dismiss: d.Hide,
	}
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
	d.ModalDialog.Show()
	d.ring.Reset()
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

func (d *ProfileDialog) doButton(btn int) {
	if btn == 0 {
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
	text := theme.StyleDialog()
	dim := theme.StyleDialogDim()
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
			theme.StyleDialogError(), d.status)
	}

	d.DrawSeparator(s)
	active := -1
	if d.ring.OnButtons() {
		active = d.ring.BtnFocus
	}
	d.DrawButtons(s, profileButtons, active)
	d.ddAllow.DrawOverlay(s)
}

// HandleKey is the focus ring (dialogs.FieldRing.Key): the focused widget
// gets every key first, so an open dropdown takes Escape and the arrows, and a
// closed one opens on Enter; Enter anywhere else is OK. Editing the name
// clears a refusal shown from the last OK.
func (d *ProfileDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	name := d.fName.Value()
	d.ring.Key(ev)
	if d.fName.Value() != name {
		d.status = ""
	}
	return true
}

// HandleMouse is the focus ring's (dialogs.RingMouse).
func (d *ProfileDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	return d.ring.Mouse(ev, &d.ModalDialog)
}

// FocusClipboardTargetAt implements core.ClipboardFocuser: a right-click on the
// name field — the ring's one text field — focuses it; nothing does while the
// Allowed Operations list is open (dialogs.ClipboardFieldAt).
func (d *ProfileDialog) FocusClipboardTargetAt(x, y int) bool {
	return d.ring.FocusClipboardTargetAt(x, y)
}

// FocusedClipboardTarget implements core.ClipboardHost: the name field while
// it has focus.
func (d *ProfileDialog) FocusedClipboardTarget() core.ClipboardTarget {
	return d.ring.FocusedClipboardTarget()
}
