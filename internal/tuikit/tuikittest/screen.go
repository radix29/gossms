package tuikittest

import (
	"strings"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// Screen is a core.Buffer, and so a tcell.Screen a control can Draw into,
// with helpers for reading what it drew. Every cell starts as a space in
// tcell.StyleDefault; RuneAt reads 0 only for the trailing cell of a wide
// glyph or off the screen.
type Screen struct {
	*core.Buffer
}

// NewScreen returns a w×h screen of spaces.
func NewScreen(w, h int) *Screen { return &Screen{core.NewBuffer(w, h)} }

// SetSize replaces the screen with a blank w×h one, as a terminal resize
// would (tcell.Screen); whatever holds the Screen sees the new Size.
func (s *Screen) SetSize(w, h int) { s.Buffer = core.NewBuffer(w, h) }

// Snapshot returns every row, right-trimmed, joined by newlines: a compact
// golden for a whole draw.
func (s *Screen) Snapshot() string {
	rows := s.Rows()
	for i, r := range rows {
		rows[i] = strings.TrimRight(r, " ")
	}
	return strings.Join(rows, "\n")
}

// Find returns the column and row where sub first appears, scanning rows top
// to bottom. The column is a screen column (wide glyphs count two), so it can
// be fed straight back as a click position.
func (s *Screen) Find(sub string) (x, y int, ok bool) {
	w, h := s.Size()
	for y := range h {
		row := s.Row(y)
		i := strings.Index(row, sub)
		if i < 0 {
			continue
		}
		return s.column(row[:i], w, y), y, true
	}
	return 0, 0, false
}

// RowOf returns the first row containing sub, or -1.
func (s *Screen) RowOf(sub string) int {
	if _, y, ok := s.Find(sub); ok {
		return y
	}
	return -1
}

// column maps the leading text of row y (as Row returned it) to the screen
// column where the text after it starts.
func (s *Screen) column(prefix string, w, y int) int {
	x := 0
	for x < w && len(s.Text(0, y, x)) < len(prefix) {
		x++
	}
	for x < w && s.RuneAt(x, y) == 0 {
		x++ // skip a wide glyph's trailing cell
	}
	return x
}
