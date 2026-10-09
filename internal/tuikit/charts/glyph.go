package charts

// Block glyph ramps, indexed by eighths filled (0..8). Vertical blocks fill a
// cell from the bottom up, horizontal from the left; both end at the full
// block.
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

// eighths converts a length in cells to whole cells plus a remainder in eighths.
// A non-zero length never yields zero cells and zero eighths: a tiny but real
// metric is promoted to one eighth so it shows a sliver. Otherwise it rounds to
// nearest, within half an eighth of the true value.
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
