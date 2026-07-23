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
