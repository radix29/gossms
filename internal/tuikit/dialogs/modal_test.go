package dialogs

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// sizedScreen embeds the tcell.Screen interface (nil at runtime) and
// overrides only Size() — ModalDialog.recentre only ever calls Size() on
// its screen, so this is enough to drive it without a full Screen fake.
type sizedScreen struct {
	tcell.Screen
	w, h int
}

func (s *sizedScreen) Size() (int, int) { return s.w, s.h }

func TestModalDialogClampsToSmallScreen(t *testing.T) {
	scr := &sizedScreen{w: 40, h: 10}
	d := &ModalDialog{}
	d.InitModal(scr, "Confirm", 78, 9)

	if d.rect.W != 40 {
		t.Errorf("rect.W = %d, want 40 (clamped to screen width)", d.rect.W)
	}
	if d.rect.X != 0 {
		t.Errorf("rect.X = %d, want 0", d.rect.X)
	}
}

func TestModalDialogRestoresFullSizeOnLargerScreen(t *testing.T) {
	scr := &sizedScreen{w: 40, h: 10}
	d := &ModalDialog{}
	d.InitModal(scr, "Confirm", 78, 9)
	if d.rect.W != 40 {
		t.Fatalf("rect.W = %d, want 40 (clamped)", d.rect.W)
	}

	// Terminal grows; Show (as ShowConfirm does on every call) recentres.
	scr.w, scr.h = 200, 50
	d.Show()

	if d.rect.W != 78 {
		t.Errorf("rect.W = %d, want 78 (restored to requested size, not stuck at the earlier clamp)", d.rect.W)
	}
	wantX := (200 - 78) / 2
	if d.rect.X != wantX {
		t.Errorf("rect.X = %d, want %d", d.rect.X, wantX)
	}
}

func TestModalDialogScrollbarDrag(t *testing.T) {
	scr := &sizedScreen{w: 80, h: 24}
	d := &ModalDialog{}
	d.InitModal(scr, "Test", 40, 12)

	trackX, trackY, trackH, total := d.Rect().Right()-1, d.Rect().Y+1, 10, 30
	var scroll int

	miss := tcell.NewEventMouse(d.Rect().X+2, trackY, tcell.Button1, tcell.ModNone)
	if d.ScrollbarDrag(miss, trackX, trackY, trackH, total, &scroll) {
		t.Fatal("press off the bar's column should not be handled")
	}

	press := tcell.NewEventMouse(trackX, trackY+trackH-1, tcell.Button1, tcell.ModNone)
	if !d.ScrollbarDrag(press, trackX, trackY, trackH, total, &scroll) {
		t.Fatal("press on the bar should be handled")
	}
	if !d.sbDragging {
		t.Fatal("sbDragging should be true after a qualifying press")
	}
	if scroll == 0 {
		t.Error("scroll should have jumped forward when clicking near the bottom of the track")
	}

	// ConsumeOutsideClick resets sbDragging on release, same as
	// mouseDragging — every embedding dialog calls it unconditionally.
	release := tcell.NewEventMouse(0, 0, tcell.ButtonNone, tcell.ModNone)
	d.ConsumeOutsideClick(release)
	if d.sbDragging {
		t.Error("sbDragging should reset on release via ConsumeOutsideClick")
	}
}

// A button click closes the dialog on the press, so the matching release
// never reaches ConsumeOutsideClick's latch reset — HandleMouse bails on
// !visible and the host has already stopped routing to it. Left latched,
// ButtonClicked refuses the first click of the *next* showing, and the
// reopened dialog looks frozen. Show has to start clean.
func TestModalDialogLatchDoesNotSurviveIntoTheNextShowing(t *testing.T) {
	d := &ModalDialog{}
	d.InitModal(&sizedScreen{w: 100, h: 30}, "Confirm", 60, 10)
	d.Show()

	labels := []string{"Yes", "No"}
	y := d.ButtonRowY()
	x := d.buttonRowStartX(labels) + 1
	press := func() *tcell.EventMouse {
		return tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone)
	}

	if got := d.ButtonClicked(press(), labels); got != 0 {
		t.Fatalf("first click = %d, want 0 (Yes)", got)
	}
	d.Hide() // as every button handler does, while the button is still down

	d.Show() // reopened later, with no release ever delivered in between
	if got := d.ButtonClicked(press(), labels); got != 0 {
		t.Errorf("first click of the second showing = %d, want 0 — the latch survived Show", got)
	}
}
