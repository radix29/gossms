package dialogs

import (
	"slices"
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
	if want := core.DisplayWidth(strings.TrimSpace(msg)) + messageBoxOverhead; w != want {
		t.Errorf("w = %d, want %d", w, want)
	}
	if h != alertDialogBaseH {
		t.Errorf("h = %d, want %d", h, alertDialogBaseH)
	}
}

// TestMessageDialogsKeepLineBreaks pins B8: every message-driven dialog draws
// a "\n\n" as a paragraph gap, sized to its widest paragraph rather than the
// whole message. WrapText's strings.Fields folded the breaks into spaces, so
// cycleLogMessage's question and consequence ran together on one line.
func TestMessageDialogsKeepLineBreaks(t *testing.T) {
	const question = "Close the current log and start a new one?"
	const consequence = "Each archive is renumbered."
	msg := question + "\n\n" + consequence + "\r\n"
	want := []string{question, "", consequence}

	scr := &sizedScreen{w: 200, h: 50}
	cases := map[string]func() []string{
		"Alert": func() []string {
			d := NewAlertDialog(scr)
			d.ShowAlert("t", msg)
			return d.msgLines
		},
		"Confirm": func() []string {
			d := NewConfirmDialog(scr)
			d.ShowConfirm("t", msg, func(bool) {})
			return d.msgLines
		},
		"TypedConfirm": func() []string {
			d := NewTypedConfirmDialog(scr)
			d.ShowTypedConfirm("t", msg, "DROP", func(bool) {})
			return d.msgLines
		},
		"Prompt": func() []string {
			d := NewPromptDialog(scr)
			d.ShowPrompt("t", msg, "Name:", "", func(string) {})
			return d.msgLines
		},
	}
	for name, show := range cases {
		if got := show(); !slices.Equal(got, want) {
			t.Errorf("%s: lines = %q, want %q", name, got, want)
		}
	}
}

// The width is the widest paragraph's, so a message whose paragraphs each fit
// is not wrapped, and is not sized to the sum of them.
func TestFitMessageSizesToTheWidestParagraph(t *testing.T) {
	d := &ModalDialog{}
	d.InitModal(&sizedScreen{w: 200, h: 50}, "Confirm", confirmDialogMinW, confirmDialogBaseH)

	long := strings.Repeat("x", 100) // wider than confirmDialogMinW, under 2/3 of 200
	w, h, lines := d.fitMessage("short\n"+long, confirmDialogMinW, confirmDialogBaseH)

	if want := len(long) + messageBoxOverhead; w != want {
		t.Errorf("w = %d, want %d (the longer paragraph's width)", w, want)
	}
	if len(lines) != 2 || h != confirmDialogBaseH+1 {
		t.Errorf("lines = %q, h = %d, want 2 lines and h %d", lines, h, confirmDialogBaseH+1)
	}
}

// The height cap applies across paragraphs: the surplus, gap included, folds
// into the last line kept and is ellipsized there.
func TestFitMessageCapsParagraphsToTheScreen(t *testing.T) {
	scr := &sizedScreen{w: 120, h: confirmDialogBaseH + 1} // room for 2 lines
	d := &ModalDialog{}
	d.InitModal(scr, "Confirm", confirmDialogMinW, confirmDialogBaseH)

	_, _, lines := d.fitMessage("one\n\nthree\nfour", confirmDialogMinW, confirmDialogBaseH)

	if len(lines) != 2 || lines[0] != "one" {
		t.Fatalf("lines = %q, want 2 lines starting with \"one\"", lines)
	}
	if lines[1] != "three four" {
		t.Errorf("last line = %q, want the gap folded away: \"three four\"", lines[1])
	}
}
