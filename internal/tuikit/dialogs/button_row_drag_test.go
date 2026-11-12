package dialogs

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// A dialog's button row acts only on the first Button1 of a gesture. tcell's
// all-motion tracking resends the held button on every motion, so a drag that
// began elsewhere (the message, a list, a scrollbar, a checkbox, or outside
// the box) arrives on the button row as a Button1 that is not a resend of a
// press the row saw. A latch set only by a button hit let it through: dragging
// from an AlertDialog's text onto [ OK ] closed it.
//
// Each case presses at from, drags to the first button's centre and checks
// nothing fired; then releases and clicks the button fresh, which must fire
// (else the test passes against a dead button row).
func TestADragOntoTheButtonRowDoesNotPressAButton(t *testing.T) {
	type rig struct {
		d       interface{ HandleMouse(*tcell.EventMouse) bool }
		from    [2]int // where the gesture's press lands
		to      [2]int // a point on the button under test
		pressed func() bool
	}
	scr := func() tcell.Screen { return &sizedScreen{w: 100, h: 30} }
	buttonAt := func(m *ModalDialog, labels []string) [2]int {
		return [2]int{m.ButtonRowStartX(labels) + 2, m.ButtonRowY()}
	}
	// lastButtonAt is the rightmost button (Cancel), for a dialog whose first
	// button is gated or does something other than close.
	lastButtonAt := func(m *ModalDialog) [2]int {
		return [2]int{m.Rect().Right() - 4, m.ButtonRowY()}
	}

	cases := map[string]func(t *testing.T) rig{
		"alert, from the message": func(t *testing.T) rig {
			d := NewAlertDialog(scr())
			d.ShowAlert("Error", "Something went wrong.")
			in := d.InnerRect()
			return rig{d, [2]int{in.X + 2, in.Y + 1}, buttonAt(&d.ModalDialog, []string{"OK"}),
				func() bool { return !d.Visible() }}
		},
		"alert, from outside the box": func(t *testing.T) rig {
			d := NewAlertDialog(scr())
			d.ShowAlert("Error", "Something went wrong.")
			return rig{d, [2]int{d.Rect().X - 2, d.Rect().Y - 2}, buttonAt(&d.ModalDialog, []string{"OK"}),
				func() bool { return !d.Visible() }}
		},
		"confirm, from the option checkbox": func(t *testing.T) rig {
			d := NewConfirmDialog(scr())
			answered := false
			d.ShowConfirmOption("Delete Table", "Delete dbo.Orders?", "Cascade", false,
				func(bool, bool) { answered = true })
			// Where Draw puts it, as TestConfirmOptionReleaseOutsideDoesNotStrandTheLatch.
			in := d.InnerRect()
			d.option.SetBounds(in.X+1, in.Y+2+len(d.msgLines)+1)
			return rig{d, [2]int{d.option.RectX() + 1, d.option.RectY()}, buttonAt(&d.ModalDialog, d.buttons),
				func() bool { return answered }}
		},
		"prompt, from the message": func(t *testing.T) rig {
			d := NewPromptDialog(scr())
			accepted := false
			d.ShowPrompt("Rename", "New name:", "Name", "orders", func(string) { accepted = true })
			in := d.InnerRect()
			return rig{d, [2]int{in.X + 1, in.Y}, buttonAt(&d.ModalDialog, d.buttons()),
				func() bool { return accepted }}
		},
		"typed confirm, from the message": func(t *testing.T) rig {
			d := NewTypedConfirmDialog(scr())
			answered := false
			d.ShowTypedConfirm("Drop Database", "Type the name to confirm.", "sales", func(bool) { answered = true })
			in := d.InnerRect()
			// The confirming button is gated until the name is typed.
			return rig{d, [2]int{in.X + 1, in.Y}, lastButtonAt(&d.ModalDialog),
				func() bool { return answered }}
		},
		"progress, from the message": func(t *testing.T) rig {
			d := NewProgressDialog(scr())
			d.RevealDelay = 0
			cancelled := false
			d.ShowProgress("Delete Table", "Deleting...", func() { cancelled = true })
			in := d.InnerRect()
			return rig{d, [2]int{in.X + 1, in.Y}, buttonAt(&d.ModalDialog, progressButtons),
				func() bool { return cancelled }}
		},
		"properties, from the scrollbar": func(t *testing.T) rig {
			d := NewPropertiesDialog(scr())
			rows := make([]PropertyRow, 100)
			for i := range rows {
				rows[i] = PropertyRow{Key: "k", Value: "v"}
			}
			d.ShowProperties("Properties", rows)
			in := d.InnerRect()
			return rig{d, [2]int{d.Rect().Right() - 1, in.Y + 3}, buttonAt(&d.ModalDialog, []string{"Close"}),
				func() bool { return !d.Visible() }}
		},
		"file dialog, from the list": func(t *testing.T) rig {
			d, dir := newTestFileDialog(t)
			d.ShowOpen("Open File", dir+"/", func(string) {})
			d.rect = core.Rect{X: 0, Y: 0, W: 60, H: 20} // nil screen: placed by hand, as the other FileDialog tests do
			lr := d.listRect()
			// A row other than the selected one, so the press only selects. Cancel,
			// since Open on a directory navigates rather than closing.
			row := (d.sel + 1 - d.scroll) % lr.H
			return rig{d, [2]int{lr.X + 1, lr.Y + row}, lastButtonAt(&d.ModalDialog),
				func() bool { return !d.Visible() }}
		},
	}

	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			r := build(t)
			ev := func(p [2]int, b tcell.ButtonMask) *tcell.EventMouse {
				return tcell.NewEventMouse(p[0], p[1], b, tcell.ModNone)
			}
			r.d.HandleMouse(ev(r.from, tcell.Button1))
			if r.pressed() {
				t.Fatal("the press at the gesture's start already pressed the button; the rig is wrong")
			}
			r.d.HandleMouse(ev(r.to, tcell.Button1)) // held, dragged onto the button
			if r.pressed() {
				t.Fatal("a drag that began elsewhere pressed the button when it reached the button row")
			}
			r.d.HandleMouse(ev(r.to, tcell.ButtonNone))
			r.d.HandleMouse(ev(r.to, tcell.Button1))
			if !r.pressed() {
				t.Fatal("a fresh click on the button after the release did not press it")
			}
		})
	}
}

// The resends of a press an open drop-down list consumed never reach
// ConsumeOutsideClick while the list is open; once the pick closes it, they do,
// and look like a first press. A dialog routing a list ahead of
// ConsumeOutsideClick calls TrackPress first, so the press is seen. This pins
// TrackPress's own contract: a repeat call for the same event is a no-op, so
// ConsumeOutsideClick and ButtonClicked calling it again change nothing.
func TestTrackPressSeesAPressBeforeConsumeOutsideClick(t *testing.T) {
	d := &ModalDialog{}
	d.InitModal(&sizedScreen{w: 100, h: 30}, "Backup", 60, 12)
	d.Show()
	labels := []string{"OK", "Cancel"}
	x, y := d.ButtonRowStartX(labels)+2, d.ButtonRowY()

	// A pick in a list over the form: tracked, then consumed by the list.
	d.TrackPress(tcell.NewEventMouse(x, y-4, tcell.Button1, tcell.ModNone))
	// Its resend, dragged onto OK, now goes the whole way through.
	drag := tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone)
	d.TrackPress(drag)
	if d.ConsumeOutsideClick(drag) {
		t.Fatal("a point on the button row read as outside")
	}
	if got := d.ButtonClicked(drag, labels); got != -1 {
		t.Fatalf("the resend of a list pick pressed button %d", got)
	}

	d.TrackPress(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
	click := tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone)
	d.TrackPress(click)
	d.ConsumeOutsideClick(click)
	if got := d.ButtonClicked(click, labels); got != 0 {
		t.Fatalf("a fresh click on OK = %d, want 0 — a repeat TrackPress for one event is not a no-op", got)
	}
	if got := d.ButtonClicked(click, labels); got != -1 {
		t.Fatalf("one press clicked twice (%d)", got)
	}
}
