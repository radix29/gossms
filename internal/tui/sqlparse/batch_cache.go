package sqlparse

import (
	"slices"
	"sort"
)

// lineMark is a line start a scan reached in LexNormal state: a position a
// later scan may resume at with no saved lexer state, and the one kind of
// position a "GO" separator can occupy. goNext is the start of the line after
// it when the line is a separator, or -1.
type lineMark struct {
	start, goNext int
}

// BatchCache answers ScanBindings for the cursor's GO-delimited batch without
// re-lexing the batch on every keystroke, preceded by the temp tables the
// batches above it carry in (CarryTempBindings).
//
// The batch scan runs only when a temp-table or table-variable sigil is in
// play, but then covered the whole batch in both directions: lex forward to the
// next "GO", then tokenize from the last one. On a 20,000-line batch that was
// 23 ms for the forward lex and 90 ms with the tokens, per keystroke on the UI
// goroutine (N3), past a frame at about 3,000 lines.
//
// This keeps the token stream of everything lexed, plus every LexNormal line
// start (lineMark), and on the next call diffs the buffer against the copy it
// described. Only the changed window is re-lexed: the walk restarts at the last
// mark at or before the first changed rune, and stops at the first mark past
// the window that the previous pass also reached as a mark at the same distance
// from the buffer end. From there the old tokens and marks are true again,
// shifted by the length change. Three facts make that sound:
//
//   - A mark needs no saved state: LexNormal, block-comment depth 0, nothing
//     open. Resuming at one is exact.
//   - Nothing lexed before a mark depends on text after it: the rune before a
//     line start is '\n', which ends every word, and a quoted identifier open
//     across it would have left the line start out of LexNormal.
//   - Two scans in LexNormal at a line start, over identical text from there to
//     the end, produce identical tokens and separator decisions
//     (sqltext.GoSeparatorAt reads only its own line).
//
// Typing changes one line, so an ordinary keystroke re-lexes that line and
// resynchronises at the next; one that opens a comment or literal re-lexes
// until it closes. The diff and shift are O(buffer) but carry no lexing:
// BenchmarkBatchBindingsTypingFirstLine20k measures 11 ms against its
// reference's 95, ScanBindings itself about half of that.
//
// Lexing is lazy: only far enough to find the batch end below the cursor, so
// the first call on a long script pays for the text above and in the batch, not
// after it.
//
// Every scan is a whole-buffer lex resumed in pieces, so the batch's bounds are
// the GO lines that lex finds: the last strictly above the cursor's row and the
// first strictly below it. That is where PrefixScan.GoStart puts the start, and
// where the forward scan from the cursor put the end, except with the cursor
// between the two runes of a "--" or "/*", which a scan starting there read as
// no comment at all.
//
// The zero value is ready to use. Not safe for concurrent use: it belongs to
// the UI goroutine.
type BatchCache struct {
	// text is the buffer tokens and marks describe, copied: the caller's
	// buffer is reused across keystrokes (see FlattenLinesInto), so it cannot
	// be the old side of the next diff.
	text []rune
	// known is false until text has been taken: the zero value.
	known bool

	// tokens is every token whose start is below lexedTo, ascending. marks is
	// every LexNormal line start below lexedTo, ascending, and may also hold
	// one exactly at it.
	tokens []Token
	marks  []lineMark
	// lexedTo is len(text) once the whole buffer is lexed, and otherwise a
	// LexNormal line start the next lex resumes at.
	lexedTo int

	// bindings is ScanBindings over [bindFrom, bindTo) of text, reused while
	// neither the text nor the batch changes — a cursor moving within one.
	bindings         []Binding
	bindFrom, bindTo int
	bindOK           bool

	// carried is CarryTempBindings folded over every batch in [0, carriedTo),
	// a batch start. sync clears carriedOK when an edit lands above carriedTo;
	// a cursor moving down folds on from there, one moving up starts over.
	carried   []Binding
	carriedTo int
	carriedOK bool

	// scratch holds the re-lexed window while sync decides where it goes.
	scratchTokens []Token
	scratchMarks  []lineMark
}

// Bindings is the temp tables carried into the GO-delimited batch holding
// cursorRow, followed by ScanBindings over that batch, for the text lines and
// buf (FlattenLinesInto's output for lines) hold.
func (c *BatchCache) Bindings(lines [][]rune, buf []rune, cursorRow int) []Binding {
	changed := c.sync(buf)
	from, to := c.batch(lines, buf, cursorRow)
	if changed || !c.bindOK || from != c.bindFrom || to != c.bindTo {
		c.carryTo(from)
		c.bindings = slices.Concat(c.carried, ScanBindings(c.tokensIn(from, to)))
		c.bindFrom, c.bindTo, c.bindOK = from, to, true
	}
	return c.bindings
}

// carryTo brings carried up to the batch starting at from. Every mark above
// from is known: batch lexed past the cursor's row to find it.
func (c *BatchCache) carryTo(from int) {
	if c.carriedOK && c.carriedTo == from {
		return
	}
	start := c.carriedTo
	if !c.carriedOK || from < c.carriedTo {
		c.carried, start = nil, 0
	}
	for _, m := range c.marks[c.markAtOrAfter(start):] {
		if m.start >= from {
			break
		}
		if m.goNext >= 0 {
			c.carried = CarryTempBindings(c.carried, c.tokensIn(start, m.start))
			start = m.goNext
		}
	}
	c.carriedTo, c.carriedOK = from, true
}

// batch returns the bounds of the batch holding cursorRow — the line after
// the last "GO" strictly above that row, or 0, and the first "GO" line
// strictly below it, or len(buf) — lexing as far as it must to know them.
func (c *BatchCache) batch(lines [][]rune, buf []rune, cursorRow int) (from, to int) {
	rowStart := OffsetForCursor(lines, cursorRow, 0)
	nextRow := len(buf) + 1 // no line below: no mark can qualify
	if cursorRow+1 < len(lines) {
		nextRow = OffsetForCursor(lines, cursorRow+1, 0)
	}
	to = -1
	for to < 0 {
		for _, m := range c.marks[c.markAtOrAfter(nextRow):] {
			if m.goNext >= 0 {
				to = m.start
				break
			}
		}
		if to >= 0 {
			break
		}
		if c.lexedTo >= len(buf) {
			to = len(buf)
			break
		}
		c.extend(buf, nextRow)
	}
	// Every mark below rowStart is known: the lex reached nextRow or the end.
	for i := c.markAtOrAfter(rowStart) - 1; i >= 0; i-- {
		if c.marks[i].goNext >= 0 {
			return c.marks[i].goNext, to
		}
	}
	return 0, to
}

// extend lexes on from lexedTo until it has marked a "GO" line at or below
// nextRow, or reached the end of buf.
func (c *BatchCache) extend(buf []rune, nextRow int) {
	stop := -1
	lexSQL(buf, c.lexedTo, len(buf), false, &c.tokens, allLines(buf), nil,
		func(start, goNext int) bool {
			// lexedTo itself is reported again; sync may have kept its mark.
			if n := len(c.marks); n > 0 && c.marks[n-1].start == start {
				return false
			}
			c.marks = append(c.marks, lineMark{start, goNext})
			if goNext >= 0 && start >= nextRow {
				stop = start
				return true
			}
			return false
		})
	c.lexedTo = len(buf)
	if stop >= 0 {
		c.lexedTo = stop
	}
}

// sync brings tokens and marks from describing c.text to describing buf,
// re-lexing only the window that changed, and reports whether buf differs.
func (c *BatchCache) sync(buf []rune) bool {
	if !c.known {
		c.text = append(c.text[:0], buf...)
		c.tokens, c.marks, c.lexedTo, c.known = c.tokens[:0], c.marks[:0], 0, true
		c.carriedOK = false
		return true
	}
	old := c.text
	n := min(len(old), len(buf))
	p := 0
	for p < n && old[p] == buf[p] {
		p++
	}
	if p == len(old) && p == len(buf) {
		return false
	}
	if p < c.carriedTo {
		c.carriedOK = false
	}
	s := 0
	for s < n-p && old[len(old)-1-s] == buf[len(buf)-1-s] {
		s++
	}
	delta := len(buf) - len(old)
	oldWindowEnd, newWindowEnd := len(old)-s, len(buf)-s
	oldLexedTo := c.lexedTo

	// Restart at the last mark at or before the first changed rune: the text above
	// it is the same, so its state is. Its own line may have changed, so its
	// separator decision is retaken. marks[0] is offset 0, so mi is valid whenever
	// anything was lexed, and no mark lies past lexedTo, so neither does the
	// restart.
	mi := sort.Search(len(c.marks), func(i int) bool { return c.marks[i].start > p }) - 1
	if mi < 0 {
		c.text = append(c.text[:0], buf...)
		return true
	}
	restart := c.marks[mi].start
	cut := sort.Search(len(c.tokens), func(i int) bool { return c.tokens[i].Start >= restart })

	if oldLexedTo < oldWindowEnd {
		// Nothing lexed past the window, so nothing to resynchronise with:
		// keep what is above the restart and let batch lex on from there.
		c.tokens, c.marks, c.lexedTo = c.tokens[:cut], c.marks[:mi], restart
		c.text = append(c.text[:0], buf...)
		return true
	}

	// Re-lex the window. A line start at or past its end whose counterpart in
	// the old text — same distance from the end — was also a mark ends the
	// walk: from there both scans see the same text from the same state.
	resync, stop := -1, -1
	c.scratchTokens, c.scratchMarks = c.scratchTokens[:0], c.scratchMarks[:0]
	lexSQL(buf, restart, len(buf), false, &c.scratchTokens, allLines(buf), nil,
		func(start, goNext int) bool {
			if start >= newWindowEnd {
				o := start - delta
				if o > oldLexedTo {
					// Past what the old pass reached: no resync left to find.
					stop = start
					return true
				}
				if k, ok := slices.BinarySearchFunc(c.marks[mi:], o, func(m lineMark, off int) int { return m.start - off }); ok {
					resync, stop = mi+k, start
					return true
				}
			}
			c.scratchMarks = append(c.scratchMarks, lineMark{start, goNext})
			return false
		})

	if resync >= 0 {
		tail := sort.Search(len(c.tokens), func(i int) bool { return c.tokens[i].Start >= stop-delta })
		c.tokens = slices.Replace(c.tokens, cut, tail, c.scratchTokens...)
		for i := cut + len(c.scratchTokens); i < len(c.tokens); i++ {
			c.tokens[i].Start += delta
		}
		c.marks = slices.Replace(c.marks, mi, resync, c.scratchMarks...)
		for i := mi + len(c.scratchMarks); i < len(c.marks); i++ {
			c.marks[i].start += delta
			if c.marks[i].goNext >= 0 {
				c.marks[i].goNext += delta
			}
		}
		c.lexedTo = oldLexedTo + delta
	} else {
		c.tokens = append(c.tokens[:cut], c.scratchTokens...)
		c.marks = append(c.marks[:mi], c.scratchMarks...)
		c.lexedTo = len(buf)
		if stop >= 0 {
			c.lexedTo = stop
		}
	}
	c.text = append(c.text[:0], buf...)
	return true
}

// markAtOrAfter is the index of the first mark starting at or after off.
func (c *BatchCache) markAtOrAfter(off int) int {
	return sort.Search(len(c.marks), func(i int) bool { return c.marks[i].start >= off })
}

// tokensIn is the cached tokens starting in [from, to). Both bounds are
// LexNormal line starts (or the buffer's ends), so this is exactly what
// TokenizeRange(buf, from, to, false) returns: no token straddles either.
func (c *BatchCache) tokensIn(from, to int) []Token {
	a := sort.Search(len(c.tokens), func(i int) bool { return c.tokens[i].Start >= from })
	b := sort.Search(len(c.tokens), func(i int) bool { return c.tokens[i].Start >= to })
	return c.tokens[a:b]
}

// allLines makes every line start of buf a separator candidate. The upper
// bound is past len(buf) so a buffer ending in '\n' has its empty last line
// considered too, as every other line start is.
func allLines(buf []rune) goScan { return goScan{lo: 0, hi: len(buf) + 1} }
