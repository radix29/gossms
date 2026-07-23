package dialogs

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/core"
)

func TestFitMessageFloorsAtMinWidthForAShortMessage(t *testing.T) {
	d := &ModalDialog{}
	d.InitModal(&sizedScreen{w: 200, h: 50}, "Alert", alertDialogMinW, alertDialogBaseH)

	w, h, lines := d.fitMessage("Hi", alertDialogMinW, alertDialogBaseH)

	if w != alertDialogMinW {
		t.Errorf("w = %d, want %d (floor, message shorter than the default)", w, alertDialogMinW)
	}
	if h != alertDialogBaseH {
		t.Errorf("h = %d, want %d (single line)", h, alertDialogBaseH)
	}
	if len(lines) != 1 || lines[0] != "Hi" {
		t.Errorf("lines = %v, want [\"Hi\"]", lines)
	}
}

func TestFitMessageGrowsToFitOnOneLineUnderTheCap(t *testing.T) {
	d := &ModalDialog{}
	d.InitModal(&sizedScreen{w: 200, h: 50}, "Confirm", confirmDialogMinW, confirmDialogBaseH)

	msg := `Take "SomeVeryLongDatabaseName" offline? Existing connections to it will be rolled back.`
	wantNatural := core.DisplayWidth(msg) + messageBoxOverhead

	w, h, lines := d.fitMessage(msg, confirmDialogMinW, confirmDialogBaseH)

	if w != wantNatural {
		t.Errorf("w = %d, want %d (grown to fit the message on one line)", w, wantNatural)
	}
	if h != confirmDialogBaseH {
		t.Errorf("h = %d, want %d (still one line)", h, confirmDialogBaseH)
	}
	if len(lines) != 1 {
		t.Errorf("lines = %v, want exactly 1 (message fits under the 2/3-screen cap)", lines)
	}
}

func TestFitMessageWrapsInsteadOfExceedingTwoThirdsOfScreenWidth(t *testing.T) {
	scr := &sizedScreen{w: 120, h: 40}
	d := &ModalDialog{}
	d.InitModal(scr, "Confirm", confirmDialogMinW, confirmDialogBaseH)

	maxW := scr.w * maxMessageWidthNum / maxMessageWidthDen // 80
	words := make([]string, 40)
	for i := range words {
		words[i] = "word"
	}
	msg := strings.Join(words, " ") // long enough that one line would blow past maxW

	w, h, lines := d.fitMessage(msg, confirmDialogMinW, confirmDialogBaseH)

	if w != maxW {
		t.Errorf("w = %d, want %d (capped at 2/3 of the screen)", w, maxW)
	}
	if len(lines) <= 1 {
		t.Fatalf("lines = %v, want more than 1 (message forced to wrap)", lines)
	}
	if wantH := confirmDialogBaseH + len(lines) - 1; h != wantH {
		t.Errorf("h = %d, want %d (grown by the extra wrapped lines)", h, wantH)
	}
	contentW := w - messageBoxOverhead
	for i, line := range lines {
		if lw := core.DisplayWidth(line); lw > contentW {
			t.Errorf("line %d %q is %d columns wide, want <= %d", i, line, lw, contentW)
		}
	}
	if joined := strings.Join(lines, " "); joined != msg {
		t.Errorf("wrapped lines lost or reordered words: got %q, want %q", joined, msg)
	}
}

func TestFitMessageCapsHeightToTheScreenAndEllipsizesTheLastLine(t *testing.T) {
	// h == baseH leaves no room for any wrapped line beyond the first, so
	// truncation is guaranteed regardless of exactly how many lines the
	// message would otherwise wrap to.
	scr := &sizedScreen{w: 120, h: confirmDialogBaseH}
	d := &ModalDialog{}
	d.InitModal(scr, "Confirm", confirmDialogMinW, confirmDialogBaseH)

	words := make([]string, 40)
	for i := range words {
		words[i] = "word"
	}
	msg := strings.Join(words, " ") // wraps to multiple lines at this width

	w, h, lines := d.fitMessage(msg, confirmDialogMinW, confirmDialogBaseH)

	if h > scr.h {
		t.Errorf("h = %d, want <= %d (screen height)", h, scr.h)
	}
	if wantH := confirmDialogBaseH + len(lines) - 1; h != wantH {
		t.Errorf("h = %d, want %d (matches returned line count)", h, wantH)
	}
	last := lines[len(lines)-1]
	if !strings.HasSuffix(last, "…") {
		t.Errorf("last line = %q, want it ellipsized to signal dropped content", last)
	}
	contentW := w - messageBoxOverhead
	for i, line := range lines {
		if lw := core.DisplayWidth(line); lw > contentW {
			t.Errorf("line %d %q is %d columns wide, want <= %d", i, line, lw, contentW)
		}
	}
}

func TestFitMessageWithNilScreenSkipsTheCap(t *testing.T) {
	d := &ModalDialog{}
	d.InitModal(nil, "Alert", alertDialogMinW, alertDialogBaseH)

	msg := strings.Repeat("word ", 40)
	w, h, lines := d.fitMessage(msg, alertDialogMinW, alertDialogBaseH)

	if len(lines) != 1 {
		t.Errorf("lines = %v, want exactly 1 (no screen size to cap against)", lines)
	}
	if want := core.DisplayWidth(msg) + messageBoxOverhead; w != want {
		t.Errorf("w = %d, want %d", w, want)
	}
	if h != alertDialogBaseH {
		t.Errorf("h = %d, want %d", h, alertDialogBaseH)
	}
}
