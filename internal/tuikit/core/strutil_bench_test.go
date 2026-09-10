package core

import (
	"strings"
	"testing"
)

// The hard-break path is the one that used to go quadratic: each split
// re-measured the whole remainder, so one unbroken token of n bytes cost
// O(n²/w) — a base64 blob or a long path in a log entry or an alert.
// The CJK case keeps displaywidth off its printable-ASCII fast path, which is
// where a re-measure is dearest.
func BenchmarkWrapTextLongToken64K(b *testing.B) {
	benchmarkWrap(b, strings.Repeat("QUJD", 16<<10), 100)
}

func BenchmarkWrapTextLongTokenCJK16K(b *testing.B) {
	benchmarkWrap(b, strings.Repeat("你好吗朋", 4<<10), 100)
}

// Ordinary prose, so a fix for the token case is seen not to cost the common
// one anything.
func BenchmarkWrapTextProse(b *testing.B) {
	benchmarkWrap(b, strings.Repeat("The quick brown fox jumps over the lazy dog. ", 200), 100)
}

func benchmarkWrap(b *testing.B, text string, w int) {
	b.SetBytes(int64(len(text)))
	for b.Loop() {
		WrapText(text, w)
	}
}
