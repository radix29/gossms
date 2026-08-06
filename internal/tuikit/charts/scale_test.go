package charts

import (
	"math"
	"testing"
)

func TestAutoScaleRoundsUpToNiceMaximum(t *testing.T) {
	cases := []struct {
		max  float64
		want float64
	}{
		{max: 1, want: 1},
		{max: 1.1, want: 1.5},
		{max: 23, want: 25},
		{max: 28000, want: 30000},
		{max: 11174, want: 15000},
		{max: 0.9, want: 1},
		{max: 0.42, want: 0.5},
		{max: 250, want: 250},
		{max: 251, want: 300},
	}
	for _, c := range cases {
		if got := AutoScale(c.max).Max; got != c.want {
			t.Errorf("AutoScale(%v).Max = %v, want %v", c.max, got, c.want)
		}
	}
}

func TestAutoScaleHandlesDegenerateInput(t *testing.T) {
	for _, v := range []float64{0, -5, math.NaN(), math.Inf(1)} {
		got := AutoScale(v)
		if got.Min != 0 || got.Max != 1 {
			t.Errorf("AutoScale(%v) = %+v, want {0 1}", v, got)
		}
	}
}

func TestScaleCellsClampsToPlot(t *testing.T) {
	sc := Scale{Min: 0, Max: 100}
	cases := []struct {
		v    float64
		want float64
	}{
		{v: -10, want: 0},
		{v: 0, want: 0},
		{v: 50, want: 5},
		{v: 100, want: 10},
		{v: 900, want: 10},
	}
	for _, c := range cases {
		if got := sc.Cells(c.v, 10); got != c.want {
			t.Errorf("Cells(%v, 10) = %v, want %v", c.v, got, c.want)
		}
	}
	if got := sc.Cells(50, 0); got != 0 {
		t.Errorf("Cells into a zero-height plot = %v, want 0", got)
	}
}

func TestScaleSpanNeverZero(t *testing.T) {
	if got := (Scale{Min: 5, Max: 5}).Span(); got != 1 {
		t.Errorf("degenerate Span() = %v, want 1", got)
	}
}

func TestScaleTicksDescendFromMax(t *testing.T) {
	got := Scale{Min: 0, Max: 12000}.Ticks(5)
	want := []float64{12000, 9000, 6000, 3000, 0}
	if len(got) != len(want) {
		t.Fatalf("Ticks(5) returned %d levels, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tick %d = %v, want %v", i, got[i], want[i])
		}
	}
	single := Scale{Min: 0, Max: 7}.Ticks(1)
	if len(single) != 1 || single[0] != 7 {
		t.Errorf("Ticks(1) = %v, want [7]", single)
	}
}

func TestFormatValue(t *testing.T) {
	cases := []struct {
		v    float64
		want string
	}{
		{v: 0, want: "0.0"},
		{v: 1.5, want: "1.5"},
		{v: 0.42, want: "0.42"},
		{v: 42, want: "42"},
		{v: 356, want: "356"},
		{v: 7500, want: "7.5K"},
		{v: 105000, want: "105K"},
		{v: 2.5e6, want: "2.5M"},
		{v: 4e9, want: "4.0G"},
		{v: -7500, want: "-7.5K"},
		{v: math.NaN(), want: "n/a"},
	}
	for _, c := range cases {
		if got := FormatValue(c.v); got != c.want {
			t.Errorf("FormatValue(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}
