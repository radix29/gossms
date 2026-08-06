package charts

import (
	"fmt"
	"math"
)

// Scale is a linear value range mapped onto a chart's plot area.
type Scale struct {
	Min, Max float64
}

// IsZero reports whether s carries no range at all — the zero Scale, which
// every chart treats as "derive the range from the data".
func (s Scale) IsZero() bool { return s.Min == 0 && s.Max == 0 }

// niceSteps are the mantissas a rounded-up axis maximum is allowed to land
// on. 2.5 and 7.5 are included because a five-level axis over them divides
// into readable quarters (7.5K → 5.6K/3.8K/1.9K reads worse than 7.5K →
// 5K/2.5K, which is what including them buys).
var niceSteps = [...]float64{1, 1.5, 2, 2.5, 3, 4, 5, 7.5, 10}

// AutoScale returns a 0-based scale whose maximum is max rounded up to the
// next "nice" number, so axis labels land on readable values instead of
// whatever the largest sample happened to be. A non-positive max yields a
// 0..1 scale, which renders an empty plot with a sane axis rather than
// dividing by zero.
func AutoScale(max float64) Scale {
	if max <= 0 || math.IsNaN(max) || math.IsInf(max, 0) {
		return Scale{Min: 0, Max: 1}
	}
	mag := math.Pow(10, math.Floor(math.Log10(max)))
	norm := max / mag
	for _, step := range niceSteps {
		if norm <= step+1e-9 {
			return Scale{Min: 0, Max: step * mag}
		}
	}
	return Scale{Min: 0, Max: 10 * mag}
}

// Span is the scale's value range, never zero — a degenerate scale (Max at
// or below Min) reports 1 so callers can divide by it unconditionally.
func (s Scale) Span() float64 {
	span := s.Max - s.Min
	if span <= 0 {
		return 1
	}
	return span
}

// Cells maps a value to a length in cells within a plot of the given size,
// clamped to 0..cells. The result is fractional: the caller decides how
// much of the remainder to render with a partial block glyph.
func (s Scale) Cells(v float64, cells int) float64 {
	if cells <= 0 {
		return 0
	}
	frac := (v - s.Min) / s.Span()
	switch {
	case frac <= 0 || math.IsNaN(frac):
		return 0
	case frac >= 1:
		return float64(cells)
	}
	return frac * float64(cells)
}

// Ticks returns levels evenly spaced values from Min to Max inclusive,
// highest first — the order Y-axis labels are drawn in, top to bottom.
// Fewer than two levels yields just the maximum.
func (s Scale) Ticks(levels int) []float64 {
	if levels < 2 {
		return []float64{s.Max}
	}
	out := make([]float64, levels)
	for i := range out {
		out[i] = s.Max - (s.Max-s.Min)*float64(i)/float64(levels-1)
	}
	return out
}

// FormatValue renders an axis label or a numeric readout compactly: exact
// for small values, K/M/G suffixed above a thousand. Zero is "0.0" rather
// than "0" so a zero baseline lines up with the decimal labels above it.
func FormatValue(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "n/a"
	}
	neg := ""
	if v < 0 {
		neg, v = "-", -v
	}
	switch {
	case v == 0:
		return "0.0"
	case v >= 1e9:
		return neg + trimUnit(v/1e9, "G")
	case v >= 1e6:
		return neg + trimUnit(v/1e6, "M")
	case v >= 1e3:
		return neg + trimUnit(v/1e3, "K")
	case v >= 10:
		return neg + fmt.Sprintf("%.0f", v)
	case v >= 1:
		return neg + fmt.Sprintf("%.1f", v)
	}
	return neg + fmt.Sprintf("%.2f", v)
}

// trimUnit formats a suffixed magnitude with one decimal, dropping the
// decimal once the value is big enough that it adds width without adding
// information ("105K", not "105.0K").
func trimUnit(v float64, unit string) string {
	if v >= 100 {
		return fmt.Sprintf("%.0f%s", v, unit)
	}
	return fmt.Sprintf("%.1f%s", v, unit)
}
