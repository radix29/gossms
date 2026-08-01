package query

import (
	"fmt"
	"strings"
	"testing"
)

// TestCellArenaStrSurvivesChunkReuse is the whole safety claim of the arena
// in one test: a string handed out earlier must still read back correctly
// after enough later cells to fill the chunk it lives in and start several
// more. A packing scheme that grew one buffer with append (instead of
// starting a fresh fixed-size chunk) would corrupt every earlier string the
// moment the buffer reallocated, and the scratch buffer str copies from is
// reused for every cell, so a missing copy shows up here too.
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

// TestCellArenaStrOversizeValue confirms a cell too large to pack still
// round-trips — it takes its own allocation rather than stranding a chunk.
func TestCellArenaStrOversizeValue(t *testing.T) {
	a := &cellArena{}
	big := strings.Repeat("x", arenaTextChunk*2)
	if got := a.str([]byte(big)); got != big {
		t.Errorf("oversize cell round-tripped to %d bytes, want %d", len(got), len(big))
	}
	// The oversize value must not have disturbed ordinary packing.
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

// TestCellArenaRowsAreIndependent confirms rows carved from one chunk don't
// alias: each has capacity exactly its own length, so appending to one can't
// overwrite the next row's cells, and writing a cell in one row is invisible
// in every other.
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
	// An append past a row's length must reallocate rather than reach into
	// the row after it.
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

// TestCellArenaNilBehavesUnpacked pins the streaming path's use of a nil
// arena: same values out, just not packed.
func TestCellArenaNilBehavesUnpacked(t *testing.T) {
	var a *cellArena
	if got := a.str([]byte("hello")); got != "hello" {
		t.Errorf("nil arena str = %q, want %q", got, "hello")
	}
	if got := a.row(4); len(got) != 4 || cap(got) != 4 {
		t.Errorf("nil arena row(4) len/cap = %d/%d, want 4/4", len(got), cap(got))
	}
}
