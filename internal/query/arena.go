package query

import "unsafe"

// Chunk sizes for cellArena. Both are a compromise between wasting the tail
// of a chunk (small values are better) and the per-allocation overhead the
// arena exists to avoid (large values are better); at these sizes the waste
// is bounded by a few tens of KiB per result set no matter how many rows it
// holds.
const (
	arenaTextChunk = 64 << 10 // bytes of cell text per allocation
	arenaRowChunk  = 4 << 10  // cell slots per allocation
)

// cellArena packs a retained result set's cell text and per-row slices into
// a few large allocations instead of one (or two) per cell.
//
// Rows are never revisited once scanned, and the strings are never mutated,
// which is what makes the packing safe — and worth it: a result set with no
// row cap can hold tens of millions of cells, where the runtime's
// per-allocation rounding and GC metadata cost more than the text itself. A
// 3-byte cell is a 16-byte heap object plus a 16-byte string header in a
// per-row slice that is itself a separate heap object; packed, it is 3 bytes
// plus the header.
//
// The zero value is ready to use. Not safe for concurrent use — one arena
// belongs to the one goroutine scanning one result set.
type cellArena struct {
	text []byte   // current text chunk; handed-out strings point into it
	rows []string // current cell-slot chunk; handed-out rows are sub-slices
}

// str returns b as a string backed by the arena. The bytes are copied, so b
// may be (and is) a scratch buffer the caller reuses for the next cell.
//
// A nil arena returns an ordinary string instead, for the streaming path
// that retains nothing.
func (a *cellArena) str(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if a == nil || len(b) > arenaTextChunk/8 {
		// A cell too big to pack gets its own allocation: sharing a chunk
		// with it would strand most of the chunk, and the per-allocation
		// overhead is already negligible next to the value itself.
		return string(b)
	}
	if cap(a.text)-len(a.text) < len(b) {
		a.text = make([]byte, 0, arenaTextChunk)
	}
	off := len(a.text)
	a.text = append(a.text, b...)
	// Safe only because a.text is append-only and never regrown in place:
	// every later append writes at indices past off+len(b), so the bytes
	// under this string can't change, and the chunk stays alive as long as
	// any string cut from it does. Replacing the fixed-size chunk with a
	// plain growing append would break both halves of that.
	return unsafe.String(&a.text[off], len(b))
}

// row returns a slice of n cell slots for one row, carved out of the arena.
// Its capacity is exactly n, so appending to it can't reach into the next
// row's slots.
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
