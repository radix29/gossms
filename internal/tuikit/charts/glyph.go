package charts

// Block glyph ramps. Index is the number of eighths filled, 0..8, so a
// value can be looked up directly by its filled fraction.
//
// Vertical blocks fill a cell from the bottom up, horizontal ones from the
// left. Both ramps end at the full block, which is why a fully filled cell
// and a fully filled eighth are the same glyph.
var (
	vBlocks = [9]rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	hBlocks = [9]rune{' ', '▏', '▎', '▍', '▌', '▋', '▊', '▉', '█'}
)

const (
	// FullBlock fills a whole cell — the interior of any bar or stack
	// segment large enough not to need a partial glyph.
	FullBlock = '█'
	// LegendSquare precedes every legend label.
	LegendSquare = '■'
	// GridDot is the lightweight dot grid drawn across a plot area.
	GridDot = '·'
	// GridDivider is the dotted vertical rule marking a time division.
	GridDivider = '┆'
)

// VBlock returns the vertical block glyph for eighths eighths of a cell,
// clamped to 0..8.
func VBlock(eighths int) rune { return vBlocks[clampEighths(eighths)] }

// HBlock returns the horizontal block glyph for eighths eighths of a cell,
// clamped to 0..8.
func HBlock(eighths int) rune { return hBlocks[clampEighths(eighths)] }

func clampEighths(e int) int {
	switch {
	case e < 0:
		return 0
	case e > 8:
		return 8
	}
	return e
}

// eighths converts a length in cells to whole cells plus a remainder in
// eighths of a cell.
//
// A non-zero length never yields zero cells and zero eighths: a value too
// small to fill an eighth is promoted to one, so a real but tiny metric
// still shows a sliver instead of vanishing into the axis. Rounding
// otherwise is to nearest, which keeps a bar's drawn length within half an
// eighth of its true value.
func eighths(cells float64) (whole, rem int) {
	if cells <= 0 {
		return 0, 0
	}
	total := int(cells*8 + 0.5)
	if total == 0 {
		total = 1
	}
	return total / 8, total % 8
}
