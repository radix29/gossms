package controls

import (
	"strings"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// LineBuffer is an Editor document built somewhere other than the UI
// goroutine, for Editor.SetLineBuffer to install in O(1).
//
// SetText does three passes over its text on the UI goroutine — expand tabs,
// split, convert each line to runes — and the first Draw after it does a
// fourth, measuring every line for the horizontal scrollbar. On a million
// lines that was 1.1 s in SetText and 370 ms in the Draw, with the input loop
// frozen for both. A LineBuffer does all four as lines are appended, on
// whichever goroutine is building it, and carries the widths along so the
// Draw has nothing left to measure.
//
// A LineBuffer is not safe for concurrent use: one goroutine builds it, then
// hands it over. Once installed it is shared with the editor, not copied, so
// an editor that then edits its text writes through it; install one buffer
// more than once only into a read-only editor.
type LineBuffer struct {
	tab   string
	lines [][]rune
	lineW []int
	maxW  int
}

// NewLineBuffer returns an empty buffer that expands a tab to tabWidth
// spaces, as the receiving editor's SetText would at its indent width.
func NewLineBuffer(tabWidth int) *LineBuffer {
	return &LineBuffer{tab: strings.Repeat(" ", max(tabWidth, 0))}
}

// AppendText appends s, which may itself span lines: it is split on "\n"
// (a "\r\n" counts as one) exactly as SetText splits, so a value with an
// embedded newline lands as the same lines either way.
func (b *LineBuffer) AppendText(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			b.appendLine(s)
			return
		}
		b.appendLine(s[:i])
		s = s[i+1:]
	}
}

func (b *LineBuffer) appendLine(s string) {
	if strings.IndexByte(s, '\t') >= 0 {
		s = strings.ReplaceAll(s, "\t", b.tab)
	}
	line := []rune(s)
	w := core.RunesWidth(line)
	b.lines = append(b.lines, line)
	b.lineW = append(b.lineW, w)
	b.maxW = max(b.maxW, w)
}

// Len is the number of lines appended so far.
func (b *LineBuffer) Len() int { return len(b.lines) }
