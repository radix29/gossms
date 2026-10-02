package propsheet

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/widgets"
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

// rightmostCell returns the largest column anything was drawn in.
func rightmostCell(s *cellScreen) int {
	right := -1
	for k := range s.runes {
		right = max(right, k[0])
	}
	return right
}

// A select row narrower than its control narrows the control rather than
// drawing it over the form's right border (Database Mail Profiles' "Account to
// add" at 80 columns, N10) — SetFitItems or not — and widens it back once there
// is room.
func TestSelectRowControlFitsTheRow(t *testing.T) {
	long := "https://account.blob.core.windows.net/container"
	cases := []struct {
		name  string
		fit   bool
		w     int
		wantW int
	}{
		{"room to spare", false, 100, selectControlWidth},
		{"exactly fits", false, LabelWidth + 3 + selectControlWidth, selectControlWidth},
		{"narrow row", false, LabelWidth + 3 + 10, 10},
		{"fit items, below the fixed width", true, LabelWidth + 3 + 15, 15},
		{"no room at all", false, LabelWidth, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Select("Account to add", []string{"short", long}, 1)
			r.SetFitItems(c.fit)
			r.Layout(0, 0, c.w)
			if got := r.dd.Width(); got != c.wantW {
				t.Fatalf("control width at row width %d = %d, want %d", c.w, got, c.wantW)
			}
			if c.wantW > 2 {
				s := newCellScreen(200, 1)
				r.Draw(s, false)
				if got, want := rightmostCell(s), min(c.w, LabelWidth+3+selectControlWidth)-1; got != want {
					t.Errorf("rightmost drawn column = %d, want %d", got, want)
				}
			}
			r.Layout(0, 0, 200)
			want := selectControlWidth
			if c.fit {
				want = len(long) + 1
			}
			if got := r.dd.Width(); got != want {
				t.Errorf("control width after widening = %d, want %d", got, want)
			}
		})
	}
}

func profilesButtons() (*ButtonsRow, *bool) {
	removed := new(bool)
	return Buttons(
		widgets.NewButton("Move Up", nil),
		widgets.NewButton("Move Down", nil),
		widgets.NewButton("Remove Account", func() { *removed = true }),
	), removed
}

// A buttons row too narrow for its buttons flows them onto further lines and
// reports the extra height, rather than drawing past the form's right border
// (Database Mail Profiles at 80 columns, N10); a wrapped button still takes a
// click. The read-only draw puts the dimmed buttons in the same cells.
func TestButtonsRowWrapsWhenNarrow(t *testing.T) {
	// One line needs 11 + 2 + 13 + 2 + 18 = 46 columns.
	cases := []struct {
		name   string
		w      int
		height int
		rows   []string
	}{
		{"one line", 46, 1, []string{"[ Move Up ]  [ Move Down ]  [ Remove Account ]"}},
		{"wraps the last", 45, 2, []string{"[ Move Up ]  [ Move Down ]", "[ Remove Account ]"}},
		{"one per line", 15, 3, []string{"[ Move Up ]", "[ Move Down ]", "[ Remove Account ]"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, readOnly := range []bool{false, true} {
				r, removed := profilesButtons()
				if got := r.Height(c.w); got != c.height {
					t.Fatalf("Height(%d) = %d, want %d", c.w, got, c.height)
				}
				r.SetDrawReadOnly(readOnly)
				r.Layout(2, 1, c.w)
				s := newCellScreen(80, 1+c.height)
				r.Draw(s, false)
				for i, want := range c.rows {
					if got := strings.TrimRight(s.row(1+i), " "); got != "  "+want {
						t.Errorf("readOnly=%v line %d = %q, want %q", readOnly, i, got, "  "+want)
					}
				}
				if readOnly {
					continue
				}
				x, y := r.pos[2].X+2, r.pos[2].Y
				r.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
				r.HandleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
				if !*removed {
					t.Errorf("a click on Remove Account at (%d,%d) did not press it", x, y)
				}
			}
		})
	}
}

// A form counts a wrapped buttons row's extra lines, so the row below it is
// laid out under the buttons rather than over their second line.
func TestFormLaysOutBelowWrappedButtons(t *testing.T) {
	b, _ := profilesButtons()
	below := Static("Below", "x")
	f := NewForm(b, below)
	f.SetBounds(0, 0, 30, 10)
	s := newCellScreen(30, 10)
	f.Draw(s)
	if got := s.row(2); !strings.HasPrefix(got, "Below") {
		t.Errorf("line 2 = %q, want the row below the two button lines", got)
	}
}
