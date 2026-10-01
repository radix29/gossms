package propsheet

import (
	"strings"
	"testing"
)

func TestHintRowStartsBlank(t *testing.T) {
	r := Hint()
	if got := r.Text(); got != "" {
		t.Errorf("Hint().Text() = %q, want \"\"", got)
	}
}

func TestHintRowSetAndClear(t *testing.T) {
	r := Hint()

	r.Set("already listed")
	if got := r.Text(); got != "already listed" {
		t.Errorf("after Set: Text() = %q, want %q", got, "already listed")
	}
	if r.isError {
		t.Error("Set marked the hint as an error; want advisory")
	}

	r.SetError("failed")
	if got := r.Text(); got != "failed" {
		t.Errorf("after SetError: Text() = %q, want %q", got, "failed")
	}
	if !r.isError {
		t.Error("SetError did not mark the hint as an error")
	}

	r.Clear()
	if got := r.Text(); got != "" {
		t.Errorf("after Clear: Text() = %q, want \"\"", got)
	}
	if r.isError {
		t.Error("Clear left the error flag set")
	}
}

// A hint appearing or disappearing must not reflow the rows around it, so its
// height is 1 whether or not it currently has text.
func TestHintRowReservesItsLineWhenBlank(t *testing.T) {
	r := Hint()
	if got := r.Height(40); got != 1 {
		t.Errorf("blank Height(40) = %d, want 1", got)
	}
	r.Set("something")
	if got := r.Height(40); got != 1 {
		t.Errorf("populated Height(40) = %d, want 1", got)
	}
}

// A hint wider than its row wraps to a second line rather than losing its
// second half (N10: Accounts' delete hint at 80 columns), and one too long for
// two lines ends in an ellipsis, so a clipped hint never reads as complete.
func TestHintRowWrapsToTwoLines(t *testing.T) {
	const w = 30
	cases := []struct {
		name      string
		text      string
		wantLines []string
	}{
		{"fits", "short hint", []string{"short hint"}},
		{"wraps", "Mailer is deleted on Apply, and leaves every profile that uses it.",
			[]string{"Mailer is deleted on Apply,", "and leaves every profile that"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Hint()
			r.Set(c.text)
			if got := r.Height(w); got != len(c.wantLines) {
				t.Fatalf("Height(%d) = %d, want %d", w, got, len(c.wantLines))
			}
			r.Layout(0, 0, w)
			s := newCellScreen(w, 3)
			r.Draw(s, false)
			for i, want := range c.wantLines {
				if got := strings.TrimRight(s.row(i), " "); !strings.HasPrefix(got, want) {
					t.Errorf("line %d = %q, want it to start %q", i, got, want)
				}
			}
			if got := strings.TrimSpace(s.row(len(c.wantLines))); got != "" {
				t.Errorf("line %d = %q, want blank", len(c.wantLines), got)
			}
		})
	}

	r := Hint()
	r.Set(strings.Repeat("word ", 30))
	if got := r.Height(w); got != 2 {
		t.Fatalf("a hint needing many lines has Height %d, want the cap of 2", got)
	}
	r.Layout(0, 0, w)
	s := newCellScreen(w, 3)
	r.Draw(s, false)
	if got := strings.TrimRight(s.row(1), " "); !strings.HasSuffix(got, "…") {
		t.Errorf("second line = %q, want it to end in an ellipsis", got)
	}
}

// The hint is advisory text, not a control: it must never take a Tab stop,
// or every page carrying one would gain a dead focus position.
func TestHintRowIsNotFocusable(t *testing.T) {
	if Hint().Focusable() {
		t.Error("HintRow.Focusable() = true, want false")
	}
}

// A DynamicNote re-wraps what SetText gives it: the height the form lays it
// out at follows the new text, not the one it was built with.
func TestDynamicNoteHeightFollowsSetText(t *testing.T) {
	r := DynamicNote("short")
	if h := r.Height(20); h != 1 {
		t.Fatalf("one word takes %d lines", h)
	}
	r.SetText("a description long enough to need three lines at this width")
	if h := r.Height(20); h < 3 {
		t.Errorf("after SetText the note takes %d lines", h)
	}
	if r.Text() != "a description long enough to need three lines at this width" {
		t.Errorf("Text = %q", r.Text())
	}
}

// A hint set below the fold is scrolled into view on the next Draw (N10d:
// Accounts' "is deleted on Apply" sat under the bottom edge, below the focused
// Remove button), once — a later scroll away from it is left alone.
func TestHintRowSetScrollsItIntoView(t *testing.T) {
	rows := make([]Row, 0, 12)
	for range 10 {
		rows = append(rows, Static("Row", "v"))
	}
	hint := Hint()
	rows = append(rows, hint, Static("Below", "v"))
	f := NewForm(rows...)
	f.SetBounds(0, 0, 60, 10) // rows 0–9 fit; the hint, row 10, does not
	f.FocusLast()
	f.FocusPrev() // row 9, the last on screen at scroll 0
	f.scroll = 0
	scr := &fakeScreen{w: 60, h: 10}

	f.Draw(scr)
	if f.onScreen(10) {
		t.Fatal("the hint is on screen before it is set; the test needs it below the fold")
	}

	hint.Set("x is deleted on Apply")
	f.Draw(scr)
	if !f.onScreen(10) {
		t.Fatalf("hint not on screen after Set (scroll %d)", f.scroll)
	}
	if !f.onScreen(f.focus) {
		t.Error("revealing the hint scrolled the focused row out of view")
	}

	f.scroll = 0
	f.Draw(scr)
	if f.scroll != 0 {
		t.Errorf("scroll = %d on a later Draw; the reveal must fire once per Set", f.scroll)
	}

	hint.Clear()
	f.Draw(scr)
	if f.scroll != 0 {
		t.Errorf("Clear scrolled the form to %d; only a hint with text asks to be seen", f.scroll)
	}
}
