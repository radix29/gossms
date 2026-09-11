package query

import "unsafe"

// cellArena chunk sizes: small wastes less chunk tail, large saves more
// allocations. At these sizes waste is bounded to a few tens of KiB per result
// set.
const (
	arenaTextChunk = 64 << 10 // bytes of cell text per allocation
	arenaRowChunk  = 4 << 10  // cell slots per allocation
)

// cellArena packs a retained result set's cell text and row slices into a few
// large allocations instead of one or two per cell.
//
// Safe because rows are never revisited and strings never mutated; worthwhile
// because an uncapped result can hold tens of millions of cells, where
// per-allocation overhead exceeds the text (a 3-byte cell costs a 16-byte heap
// object plus a header in a separately allocated row slice).
//
// The zero value is ready. Not concurrency-safe; one arena per scanning
// goroutine.
type cellArena struct {
	text []byte   // current text chunk; handed-out strings point into it
	rows []string // current cell-slot chunk; handed-out rows are sub-slices
}

// str returns b as an arena-backed string. b is copied, so callers may reuse
// it. A nil arena returns an ordinary string (the streaming path).
func (a *cellArena) str(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if a == nil || len(b) > arenaTextChunk/8 {
		// Oversize cells get their own allocation rather than strand most of a
		// chunk.
		return string(b)
	}
	if cap(a.text)-len(a.text) < len(b) {
		a.text = make([]byte, 0, arenaTextChunk)
	}
	off := len(a.text)
	a.text = append(a.text, b...)
	// Safe only because a.text is a fixed-size, append-only chunk: later
	// appends write past off+len(b) and the chunk lives as long as any string
	// cut from it. A growing append would break both.
	return unsafe.String(&a.text[off], len(b))
}

// row returns n cell slots for one row, with capacity exactly n so appending
// can't reach the next row.
func (a *cellArena) row(n int) []string {
	if a == nil || n == 0 {
		return make([]string, n)
	}
	if cap(a.rows)-len(a.rows) < n {
		size := max(arenaRowChunk, n)
		a.rows = make([]string, 0, size)
	}
	off := len(a.rows)
	a.rows = a.rows[:off+n]
	return a.rows[off : off+n : off+n]
}
