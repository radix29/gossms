package propsheet

import (
	"strings"
	"testing"
)

// A text row narrower than its field's asked-for width narrows the field to
// fit, rather than drawing it over the form's right border (Send Test E-Mail
// at 80 columns, N10), and widens it back to the asked-for width, no further,
// once there is room again.
func TestTextRowFieldFitsTheRow(t *testing.T) {
	cases := []struct {
		name  string
		row   *TextRow
		w     int
		wantW int
	}{
		{"room to spare", Text("To", "", 46), 100, 46},
		{"exactly fits", Text("To", "", 46), LabelWidth + 3 + 46, 46},
		{"narrow row", Text("To", "", 46), 60, 60 - LabelWidth - 3},
		{"password", Password("Password", 46), 60, 60 - LabelWidth - 3},
		{"unit takes its own room", Int("Size", 0, 0, 9, "MB"), LabelWidth + 3 + 8, 8 - len("MB") - 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.row.Layout(0, 0, c.w)
			if got := c.row.field.Width(); got != c.wantW {
				t.Fatalf("field width at row width %d = %d, want %d", c.w, got, c.wantW)
			}
			c.row.Layout(0, 0, 200)
			if got, want := c.row.field.Width(), c.row.width; got != want {
				t.Errorf("field width after widening = %d, want the asked-for %d", got, want)
			}
		})
	}
}

// Narrowing a field in layout must not scroll it: Layout runs every frame, so
// a field shown from its start (ShowFromStart — Send Test E-Mail's pre-filled
// Body) would otherwise always show its tail once the row is too narrow.
func TestTextRowNarrowingKeepsShowFromStart(t *testing.T) {
	r := Text("Body", "This is a test e-mail sent from Database Mail on srv.", 46)
	r.ShowFromStart()
	r.Layout(0, 0, LabelWidth+3+10)
	if got := r.field.Width(); got != 10 {
		t.Fatalf("field width = %d, want 10", got)
	}
	s := newCellScreen(80, 1)
	r.Draw(s, false)
	if got, want := s.row(0), "[This is a "; !strings.Contains(got, want) {
		t.Errorf("row = %q, want it to show the value from its start, %q", got, want)
	}
}
