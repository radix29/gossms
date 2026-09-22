package sqlparse

import "sort"

// TextRevision identifies the revision of the text a scan is made against, so
// PrefixCache can tell whether it may resume from the previous one.
//
// It mirrors controls.TextRevision field for field; the query panel converts
// at the one call site. A lexer over runes has no business importing a widget
// package to name a cache key.
type TextRevision struct {
	// Doc identifies the buffer. Compared, never dereferenced, so it must be
	// comparable — a pointer, in practice. A zero Doc means "no identity" and
	// never resumes.
	Doc any
	// Version is the buffer's mutation counter.
	Version uint64
	// DirtyFrom is the lowest line index the last mutation could have changed
	// the meaning of. Meaningful only when Version is exactly one ahead of the
	// cached one for the same Doc.
	DirtyFrom int
}

// boundary is an offset a scan may resume at in LexNormal state, and which
// construct established it: the offset after a ';', or the start of the line
// after a GO separator. Both are LexNormal by construction — the lexer
// recognises either only in normal state, and a separator line leaves nothing
// open past it.
type boundary struct {
	off  int
	isGo bool
}

// PrefixCache is ScanPrefix with its first pass made incremental.
//
// ScanPrefix lexes the whole prefix on every keystroke just to locate two
// boundaries — the offset after the last top-level ';' and the line after the
// last bare "GO" above the cursor — then tokenizes from the later one. That
// pass is O(script): 0.57 ms on a 100-statement script, 5.9 ms on 1000, on the
// UI goroutine, per keystroke while the popup is open.
//
// This keeps every boundary the previous pass crossed and restarts from the
// last one *before* the edit, making the pass O(statement) for ordinary
// typing. Three facts make that sound:
//
//   - Every boundary is a LexNormal position, so resuming needs no saved lexer
//     state (see boundary).
//   - A boundary before the edit keeps its offset: only text at or after the
//     edit point moves. Unlike controls.prefixStates, which is indexed by line
//     and must start over whenever the line count changes.
//   - Restarting mid-buffer cannot miss a "GO" line. A separator must start a
//     line; the line holding the restart offset began before it and was
//     already scanned, and every later line start is still walked.
//
// The zero value is an empty cache and every method falls back to a full scan
// when it cannot justify a resume, so a caller never resets it — a different
// document, or a revision more than one version ahead, costs one cold scan.
// That fallback is the kill switch: make validPrefix return (0, 0)
// unconditionally and this is ScanPrefix again.
//
// Not safe for concurrent use; it belongs to the UI goroutine.
type PrefixCache struct {
	// doc and version are the revision bounds describes, valid only while
	// valid is set. A zero Doc is never stored as valid: the completion
	// provider's tests pass a zero TextRevision, and treating that as an
	// identity would let one test's text answer from another's boundaries.
	doc     any
	version uint64
	valid   bool

	// scannedTo is the offset the last scan reached. bounds is complete over
	// [0, scannedTo) with one gap that cannot matter: "GO" detection stops at
	// the cursor's own row, since a separator the cursor sits inside is half
	// written and must not be judged from its prefix. That row is the only
	// line start in [rowStart, scannedTo), and a real separator line holds no
	// ';', so no boundary is recorded above rowStart when that row is a
	// separator. A later scan with the cursor further down reaches past
	// scannedTo, resumes at or below that row, and re-examines it.
	scannedTo int

	// bounds is every boundary crossed below scannedTo, ascending by offset.
	// "GO" boundaries are stored without reference to the cursor row and
	// filtered at read time, so moving the cursor within a statement costs no
	// rescan.
	bounds []boundary
}

// Scan is ScanPrefix, answered from the cache where it can be.
//
// The arguments are ScanPrefix's plus the revision lines belongs to, and the
// result is field-for-field what ScanPrefix returns for the same inputs —
// prefix_cache_test.go asserts that after every edit, since a stale boundary
// is a silently *wrong* completion, not a slow one.
func (c *PrefixCache) Scan(lines [][]rune, buf []rune, cursorRow, upTo int, rev TextRevision) PrefixScan {
	// Only "GO" lines strictly above the cursor's row separate the statement
	// the cursor is in — hence the bound at that row's start, as in
	// ScanPrefix. Judging a half-written separator from its prefix would move
	// the batch start under the word being typed.
	rowStart := OffsetForCursor(lines, cursorRow, 0)

	validTo, keep := c.validPrefix(lines, upTo, rev)
	// Everything past the valid prefix is stale or was never established;
	// dropping it keeps bounds ascending and complete over [0, scannedTo)
	// rather than a merge of two scans' offsets.
	c.bounds = c.bounds[:keep]
	if validTo < upTo {
		// Resume at the last surviving boundary — a LexNormal position — and
		// record what the walk crosses.
		resume := 0
		if keep > 0 {
			resume = c.bounds[keep-1].off
		}
		lexSQL(buf, resume, upTo, false, LexNormal, nil, goScan{lo: 0, hi: rowStart},
			func(off int, isGo bool) { c.bounds = append(c.bounds, boundary{off: off, isGo: isGo}) })
		validTo = upTo
	}
	c.doc, c.version, c.scannedTo, c.valid = rev.Doc, rev.Version, validTo, rev.Doc != nil

	// bounds may reach past the cursor — a cursor moved up leaves the
	// boundaries below it recorded and still true — so both reads stop at
	// upTo. For a "GO" boundary that bound *is* ScanPrefix's "strictly above
	// the cursor's row" rule: the boundary is the start of the line after the
	// separator, and the only line start at or below upTo and above rowStart
	// would have to be a newline inside the cursor's own row.
	lastSemi, lastGo := 0, 0
	for _, b := range c.bounds {
		if b.off > upTo {
			break
		}
		if b.isGo {
			lastGo = b.off
		} else {
			lastSemi = b.off
		}
	}

	// The statement starts at whichever boundary is later. The scan resumes in
	// LexNormal unconditionally because both are normal-state positions, and
	// it is also where State and QuoteStart come from: a prefix scan from
	// offset 0 reaches batchStart in LexNormal too, so the two agree on the
	// state at upTo, and a quote still open there was opened after batchStart.
	batchStart := max(lastSemi, lastGo)
	tokens, state, _, quoteStart := TokenizeRangeFrom(buf, batchStart, upTo, false, LexNormal)
	return PrefixScan{
		Tokens:     tokens,
		State:      state,
		BatchStart: batchStart,
		QuoteStart: quoteStart,
		GoStart:    lastGo,
	}
}

// validPrefix reports how far bounds is still complete and true for rev, and
// how many of its entries reach that far — (0, 0) when the cache cannot
// justify a resume, which starts the next scan from offset 0.
//
// The invalidation rule is controls.prefixStates': the document's identity as
// well as its version, because two documents number their versions
// independently from zero, and a version exactly one ahead, because DirtyFrom
// describes one mutation only. Typing is a single setLine and so hits; a
// keystroke that first deletes a selection bumps the version twice and costs
// one cold scan.
func (c *PrefixCache) validPrefix(lines [][]rune, upTo int, rev TextRevision) (validTo, keep int) {
	if !c.valid || rev.Doc == nil || c.doc != rev.Doc {
		return 0, 0
	}
	// Nothing past the previous scan's reach was ever established.
	validTo = c.scannedTo
	switch {
	case c.version == rev.Version:
		// No mutation since: the cursor moved, or the same revision twice.
	case c.version+1 == rev.Version && rev.DirtyFrom > 0 && rev.DirtyFrom < len(lines):
		// Exactly one mutation, and it left every line above DirtyFrom alone.
		// That line's start offset is the same in both revisions because the
		// lines above it did not move, so it can be measured against the *new*
		// lines even when the edit changed the line count — the mutation
		// prefixStates must throw its whole array away for.
		validTo = min(validTo, OffsetForCursor(lines, rev.DirtyFrom, 0))
	default:
		return 0, 0
	}
	// bounds is ascending, so this is the count of entries at or below
	// validTo. An entry exactly at validTo is still good: the ';' or "GO" that
	// established it lies strictly above, untouched.
	return validTo, sort.Search(len(c.bounds), func(i int) bool { return c.bounds[i].off > validTo })
}
