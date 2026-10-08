package sqlparse

import "github.com/radix29/gossms/internal/tuikit/sqltext"

// ---------------------------------------------------------------------------
// Statement bounds: where the DML statement the cursor is in begins and ends
// ---------------------------------------------------------------------------

// DMLStatementStarts scans tokens (already correctly depth-tracked from its own
// start, since a ';'/GO textual boundary falls outside any paren in valid SQL)
// and returns, in ascending order, the offset of every top-level WITH or
// sqltext.IsDMLLeader keyword that begins a new statement rather than
// continuing the current one:
//   - a SELECT chained onto the previous top-level clause by
//     UNION[ ALL]/EXCEPT/INTERSECT is the same statement, not a new one
//   - the first top-level SELECT after WITH or after an INSERT with no
//     intervening VALUES or EXEC is that statement's own main query/source
//     (CTE's SELECT, INSERT ... SELECT), not a new one; only WITH/INSERT itself
//     is the boundary. An INSERT ... VALUES has no such SELECT to suppress, so
//     a later, separate SELECT stacked right after it with no ';' is (rarely)
//     missed (known limitation)
//   - an INSERT, UPDATE or DELETE right after THEN is a MERGE action, part
//     of the MERGE, and a MERGE after INNER/OUTER/LEFT/RIGHT/FULL is a join
//     hint
//   - a WITH directly followed by '(' is a table hint ("t WITH (NOLOCK)") or a
//     rowset function's column list ("OPENJSON(@j) WITH (a int)"), never a CTE,
//     which names itself first; a WITH that is the last token is not decided
//     either way, and is not reported
//
// Combined with the ';'/GO boundaries PrefixCache and NarrowStatementForward
// apply, this narrows FROM-scope/clause analysis to the statement under the
// cursor even when several statements sit back to back with no ';'.
func DMLStatementStarts(tokens []Token) []int {
	var starts []int
	var s dmlSplitter
	for _, t := range tokens {
		if st, ok := s.feed(t); ok {
			starts = append(starts, st.Start)
		}
	}
	return starts
}

// dmlSplitter is DMLStatementStarts' state machine, fed one token at a time so
// a forward scan can stop at the first statement start instead of tokenizing
// everything after the cursor (see NarrowStatementForward).
type dmlSplitter struct {
	depth                   int
	prevKeyword, prevPrevKw string
	pendingMainSelect       bool

	// with is a top-level WITH whose role the token after it decides: a '('
	// makes it a hint or a WITH column list, anything else a CTE clause.
	with        Token
	withPending bool
}

// feed advances the splitter past t and reports the token that begins a new
// statement, if one does. That is usually t itself, but a WITH is reported
// one token late, when the token after it shows what it is. A token that would
// itself start a statement right after a WITH reported that way — no valid
// script has one — continues the WITH's statement.
func (s *dmlSplitter) feed(t Token) (Token, bool) {
	if s.withPending {
		s.withPending = false
		if t.Kind != TokenParenOpen {
			// advance may overwrite s.with — t can be another WITH — and
			// may set a WITH pending; t continues this statement either way.
			with := s.with
			s.pendingMainSelect = true
			s.advance(t)
			s.withPending = false
			return with, true
		}
	}
	if s.advance(t) {
		return t, true
	}
	return Token{}, false
}

// joinHintPrefixes are the keywords a MERGE join hint follows ("INNER MERGE
// JOIN", "LEFT OUTER MERGE JOIN", "FULL MERGE JOIN").
var joinHintPrefixes = map[string]bool{"INNER": true, "OUTER": true, "LEFT": true, "RIGHT": true, "FULL": true}

// advance is feed for every token but the one after a pending WITH's verdict.
func (s *dmlSplitter) advance(t Token) bool {
	switch t.Kind {
	case TokenParenOpen:
		s.depth++
		return false
	case TokenParenClose:
		if s.depth > 0 {
			s.depth--
		}
		return false
	}
	if s.depth != 0 || t.Kind != TokenKeyword {
		return false
	}
	starts := false
	switch {
	case t.Text == "VALUES", t.Text == "EXEC", t.Text == "EXECUTE":
		// INSERT ... VALUES and INSERT ... EXEC have no main SELECT to
		// wait for, so the next top-level SELECT is a statement of its own.
		s.pendingMainSelect = false
	case t.Text == "WITH":
		s.with, s.withPending = t, true
	case sqltext.IsDMLLeader(t.Text):
		continuesUnion := s.prevKeyword == "UNION" || s.prevKeyword == "EXCEPT" || s.prevKeyword == "INTERSECT" ||
			(s.prevKeyword == "ALL" && s.prevPrevKw == "UNION")
		switch {
		case t.Text == "SELECT" && s.pendingMainSelect:
			s.pendingMainSelect = false
		case t.Text == "SELECT" && continuesUnion:
			// UNION-chain continuation of the same statement.
		case s.prevKeyword == "THEN":
			// MERGE's "WHEN MATCHED THEN UPDATE SET ..." action, part of the MERGE
			// (sqltext's splitter makes the same call). A CASE's THEN is followed by
			// an expression, where a leader can only sit inside parentheses.
		case t.Text == "MERGE" && joinHintPrefixes[s.prevKeyword]:
			// "INNER MERGE JOIN": a join hint, not a MERGE statement.
		default:
			starts = true
			s.pendingMainSelect = t.Text == "INSERT"
		}
	}
	s.prevPrevKw = s.prevKeyword
	s.prevKeyword = t.Text
	return starts
}

// NarrowToDMLStatement tightens [batchStart, batchEnd) — the ';'/GO-
// delimited boundaries ScanPrefix/statementEndOffset already
// computed — to the actual DML statement containing upTo, using
// DMLStatementStarts on tokens (which must already span the same
// [batchStart, batchEnd) range so its depth tracking starts at 0).
//
// Only tests call it: it is the oracle NarrowStatementForward's one bounded
// pass is checked against (statement_forward_test.go), and the obvious
// composition internal/tui's completion tests narrow a statement with.
func NarrowToDMLStatement(tokens []Token, batchStart, batchEnd, upTo int) (start, end int) {
	start, end = batchStart, batchEnd
	for _, off := range DMLStatementStarts(tokens) {
		switch {
		case off <= upTo && off > start:
			start = off
		case off > upTo && off < end:
			return start, off // ascending order: first hit is the closest
		}
	}
	return start, end
}

// NarrowStatementForward does what a ';'/GO end scan, TokenizeRange and
// NarrowToDMLStatement did in three passes (the reference composition in
// statement_forward_test.go), in one bounded pass. prefix holds the tokens of
// [batchStart, upTo) as ScanPrefix returns them; the forward half is lexed from
// from (upTo, or just past a bracket identifier the cursor sits in) and stops
// at the first top-level ';', the next "GO" line below cursorRow, or the first
// keyword after upTo that starts a new statement, whichever comes first. It
// returns the cursor's statement bounds and the forward tokens in [from, end).
//
// Stopping at the next statement is the point: in a script that ends no
// statement with ';', a ';'/GO scan lexes everything below the cursor (200-250
// ms and 53 MB per keystroke on a 20k-line script, B11). The leader test is
// DMLStatementStarts' own, seeded with the prefix, so a SELECT that continues
// an INSERT, WITH or UNION before the cursor still continues it.
func NarrowStatementForward(lines [][]rune, buf []rune, cursorRow, batchStart, from, upTo int, prefix []Token) (start, end int, tail []Token) {
	start, end = batchStart, len(buf)
	var s dmlSplitter
	for _, t := range prefix {
		if st, ok := s.feed(t); ok && st.Start <= upTo && st.Start > start {
			start = st.Start
		}
	}
	tail = make([]Token, 0, 64)
	seen := 0
	found := false
	// scan feeds the tokens lexed since the last call, stopping at the first
	// one that ends the cursor's statement.
	scan := func() {
		for ; seen < len(tail) && !found; seen++ {
			t := tail[seen]
			st, leader := s.feed(t)
			switch {
			case leader && st.Start <= upTo:
				if st.Start > start {
					start = st.Start
				}
			case leader:
				end, found = st.Start, true
			case t.Start > upTo && s.depth == 0 && t.Kind == TokenKeyword && sqltext.EndsDML(t.Text):
				end, found = t.Start, true
			}
		}
	}
	r := lexSQL(buf, from, len(buf), true, &tail,
		goScan{lo: OffsetForCursor(lines, cursorRow+1, 0), hi: len(buf)}, nil,
		func(_, goNext int) bool {
			scan()
			return found || goNext >= 0
		})
	scan()
	if !found {
		end = r.boundary
		if r.firstGo >= 0 && r.firstGo < end {
			end = r.firstGo
		}
	}
	for i, t := range tail {
		if t.Start >= end {
			tail = tail[:i]
			break
		}
	}
	return start, end, tail
}
