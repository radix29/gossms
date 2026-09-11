package query

import (
	"fmt"
	"strings"
	"testing"
)

// Earlier strings must survive after later cells fill their chunk and start
// more. A single growing buffer would corrupt them on reallocation, and a
// missing copy would show because the scratch buffer is reused.
func TestCellArenaStrSurvivesChunkReuse(t *testing.T) {
	a := &cellArena{}
	const n = 20000

	got := make([]string, n)
	scratch := make([]byte, 0, 64)
	for i := range n {
		scratch = fmt.Appendf(scratch[:0], "cell-%d", i)
		got[i] = a.str(scratch)
	}
	for i := range n {
		if want := fmt.Sprintf("cell-%d", i); got[i] != want {
			t.Fatalf("cell %d = %q, want %q", i, got[i], want)
		}
	}
}

// An oversize cell round-trips via its own allocation.
func TestCellArenaStrOversizeValue(t *testing.T) {
	a := &cellArena{}
	big := strings.Repeat("x", arenaTextChunk*2)
	if got := a.str([]byte(big)); got != big {
		t.Errorf("oversize cell round-tripped to %d bytes, want %d", len(got), len(big))
	}
	// The oversize value mustn't disturb packing.
	if got := a.str([]byte("after")); got != "after" {
		t.Errorf("after oversize cell, str = %q, want %q", got, "after")
	}
}

func TestCellArenaStrEmpty(t *testing.T) {
	a := &cellArena{}
	if got := a.str(nil); got != "" {
		t.Errorf("str(nil) = %q, want empty", got)
	}
	if got := a.str([]byte{}); got != "" {
		t.Errorf("str(empty) = %q, want empty", got)
	}
}

// Rows from one chunk don't alias: capacity equals length, so appends and
// writes stay in their row.
func TestCellArenaRowsAreIndependent(t *testing.T) {
	a := &cellArena{}
	const cols = 3
	rows := make([][]string, 5000)
	for i := range rows {
		rows[i] = a.row(cols)
		if len(rows[i]) != cols || cap(rows[i]) != cols {
			t.Fatalf("row %d: len/cap = %d/%d, want %d/%d", i, len(rows[i]), cap(rows[i]), cols, cols)
		}
		for c := range cols {
			rows[i][c] = fmt.Sprintf("%d.%d", i, c)
		}
	}
	// Appending past a row's length must reallocate.
	spill := append(rows[0], "spilled") //nolint:gocritic // deliberate: appendAssign is the point
	_ = spill
	for i := range rows {
		for c := range cols {
			if want := fmt.Sprintf("%d.%d", i, c); rows[i][c] != want {
				t.Fatalf("row %d col %d = %q, want %q", i, c, rows[i][c], want)
			}
		}
	}
}

// A nil arena (streaming path) returns the same values, unpacked.
func TestCellArenaNilBehavesUnpacked(t *testing.T) {
	var a *cellArena
	if got := a.str([]byte("hello")); got != "hello" {
		t.Errorf("nil arena str = %q, want %q", got, "hello")
	}
	if got := a.row(4); len(got) != 4 || cap(got) != 4 {
		t.Errorf("nil arena row(4) len/cap = %d/%d, want 4/4", len(got), cap(got))
	}
}
