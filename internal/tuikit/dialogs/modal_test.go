package dialogs

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
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

// A dialog that stays open across a terminal resize must re-fit: it kept
// its old rect and drew its right border and whole button row past the new
// screen edge, while still swallowing every key.
func TestModalDialogRelayoutFollowsAShrinkingScreen(t *testing.T) {
	scr := &sizedScreen{w: 120, h: 34}
	d := &ModalDialog{}
	d.InitModal(scr, "Help", 78, 20)
	d.Show()

	scr.w, scr.h = 60, 12
	d.Relayout()

	if d.rect.W != 60 || d.rect.X != 0 {
		t.Errorf("rect = %dx%d at (%d,%d), want width 60 at x=0 (clamped to the new screen)",
			d.rect.W, d.rect.H, d.rect.X, d.rect.Y)
	}
	if d.rect.Right() > scr.w || d.rect.Bottom() > scr.h {
		t.Errorf("rect %+v extends past the %dx%d screen", d.rect, scr.w, scr.h)
	}
	// The button row is the part that went missing first.
	if d.ButtonRowY() >= scr.h {
		t.Errorf("ButtonRowY() = %d, off a %d-row screen", d.ButtonRowY(), scr.h)
	}
}

func TestModalDialogRelayoutRestoresRequestedSizeOnAGrowingScreen(t *testing.T) {
	scr := &sizedScreen{w: 40, h: 10}
	d := &ModalDialog{}
	d.InitModal(scr, "Help", 78, 20)
	d.Show()

	scr.w, scr.h = 200, 50
	d.Relayout()

	if d.rect.W != 78 || d.rect.H != 20 {
		t.Errorf("rect = %dx%d, want 78x20 (the requested size, not the earlier clamp)", d.rect.W, d.rect.H)
	}
	if wantX := (200 - 78) / 2; d.rect.X != wantX {
		t.Errorf("rect.X = %d, want %d (recentred)", d.rect.X, wantX)
	}
}

// AlertDialog sizes itself to its message and a fraction of the screen
// width, so following a resize means re-wrapping, not only recentring.
func TestAlertDialogRelayoutRewrapsMessage(t *testing.T) {
	scr := &sizedScreen{w: 200, h: 50}
	d := NewAlertDialog(scr)
	msg := strings.Repeat("word ", 30)
	d.ShowAlert("Alert", msg)
	wide := len(d.msgLines)

	scr.w, scr.h = 60, 24
	d.Relayout()

	if len(d.msgLines) <= wide {
		t.Errorf("msgLines = %d after shrinking to 60 cols, want more than the %d it wrapped to at 200",
			len(d.msgLines), wide)
	}
	if d.rect.Right() > scr.w || d.rect.Bottom() > scr.h {
		t.Errorf("rect %+v extends past the %dx%d screen", d.rect, scr.w, scr.h)
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

// drawScreen is a screen fake with enough surface for DrawBase: Size, the
// Get/Put pair DimArea walks with, and SetContent, recording every cell.
type drawScreen struct {
	tcell.Screen
	w, h  int
	cells map[[2]int]rune
}

func newDrawScreen(w, h int) *drawScreen {
	return &drawScreen{w: w, h: h, cells: map[[2]int]rune{}}
}

func (s *drawScreen) Size() (int, int) { return s.w, s.h }

func (s *drawScreen) SetContent(x, y int, primary rune, _ []rune, _ tcell.Style) {
	s.cells[[2]int{x, y}] = primary
}

func (s *drawScreen) Get(int, int) (string, tcell.Style, int) {
	return " ", tcell.StyleDefault, 1
}

func (s *drawScreen) Put(x, y int, str string, style tcell.Style) (string, int) {
	if str == "" {
		return "", 0
	}
	r := []rune(str)
	s.SetContent(x, y, r[0], nil, style)
	return string(r[1:]), 1
}

func (s *drawScreen) at(x, y int) rune { return s.cells[[2]int{x, y}] }

// A dialog laid out for 78x20 and clamped onto a 30x8 terminal draws its
// rows at the offsets it asked for, so they run past the right border and
// past the button row. DrawBase confines them to the box.
func TestDrawBaseClipsContentOnAClampedRect(t *testing.T) {
	rec := newDrawScreen(30, 8)
	d := &ModalDialog{}
	d.InitModal(rec, "Connect", 78, 20)
	d.Show()

	cs := core.NewClipScreen(rec)
	d.DrawBase(cs)

	r := d.Rect()
	// A field row laid out for 78 columns, and a row laid out for row 15 of
	// a 20-row dialog — neither fits.
	core.DrawText(cs, r.X+2, r.Y+2, tcell.StyleDefault, strings.Repeat("x", 70))
	core.DrawText(cs, r.X+2, r.Y+15, tcell.StyleDefault, "overflow")

	if got := rec.at(r.X+2, r.Y+2); got != 'x' {
		t.Errorf("first content column = %q, want 'x' — the clip must not eat what fits", got)
	}
	if got := rec.at(r.Right(), r.Y+2); got == 'x' {
		t.Error("content ran past the dialog's right edge")
	}
	if got := rec.at(r.X+2, r.Y+15); got == 'o' {
		t.Error("a row laid out below the dialog's clamped height still drew")
	}
	if got := rec.at(r.Right()-1, r.Y+2); got == 'x' {
		t.Error("content overwrote the right border column")
	}
	// The scrollbar sits on that border column, outside the content clip —
	// DrawContentScrollbar is what still gets through.
	before := rec.at(r.Right()-1, r.Y+1)
	d.DrawContentScrollbar(cs, r.Y+1, 3, 30, 0)
	if rec.at(r.Right()-1, r.Y+1) == before {
		t.Error("scrollbar on the right border column was clipped away")
	}
}

// At its requested size a dialog draws exactly as it always has — an
// overlay it opens (a dropdown, a completion list) may legitimately extend
// past the box, and clipping that would be the regression.
func TestDrawBaseDoesNotClipAtTheRequestedSize(t *testing.T) {
	rec := newDrawScreen(100, 40)
	d := &ModalDialog{}
	d.InitModal(rec, "Connect", 78, 20)
	d.Show()

	cs := core.NewClipScreen(rec)
	d.DrawBase(cs)

	r := d.Rect()
	core.DrawText(cs, r.Right()+1, r.Bottom()+1, tcell.StyleDefault, "z")
	if rec.at(r.Right()+1, r.Bottom()+1) != 'z' {
		t.Error("an unclamped dialog must not clip anything")
	}
}

// Right-aligned buttons over a content row that reached the button row
// leave the row's left half showing through. Nothing else draws there.
func TestDrawButtonsClearsTheRowOnAClampedRect(t *testing.T) {
	rec := newDrawScreen(30, 8)
	d := &ModalDialog{}
	d.InitModal(rec, "Connect", 78, 20)
	d.Show()

	cs := core.NewClipScreen(rec)
	d.DrawBase(cs)
	r := d.Rect()
	core.DrawText(cs, r.X+1, d.ButtonRowY(), tcell.StyleDefault, strings.Repeat("x", 10))
	d.DrawButtons(cs, []string{"OK"}, 0)

	if got := rec.at(r.X+1, d.ButtonRowY()); got == 'x' {
		t.Error("content left of the buttons survived on the button row")
	}
	if got := rec.at(d.buttonRowStartX([]string{"OK"}), d.ButtonRowY()); got != '[' {
		t.Errorf("button row starts with %q, want '['", got)
	}
}
