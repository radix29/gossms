package sqlparse

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// PrefixCache's safety argument is that it answers exactly what ScanPrefix
// answers, so every test here is differential against ScanPrefix rather than
// against a written-down expectation. First the edits the resume rule is
// written for; the randomized sweep and the ScanPrefix corpus — the ones over
// edits nobody chose — are at the bottom of the file.

// compareCachedScans is compareScans plus GoStart: "field for field", which
// compareScans alone is not. compareScans formats a scan the way one golden
// line does, and GoStart is not in it — yet the completion provider reads it
// (ContainsSigil and the batch-wide tokenize both start there). The cache
// derives it from its own boundary list, independently of BatchStart, so a
// "GO" boundary recorded as a ';' yields the right batch start and a GoStart
// of 0, and any comparison stopping at the golden format would pass.
func compareCachedScans(want, got PrefixScan) string {
	if diff := compareScans(want, got); diff != "" {
		return diff
	}
	if want.GoStart != got.GoStart {
		return fmt.Sprintf("\n got: goStart=%d\nwant: goStart=%d", got.GoStart, want.GoStart)
	}
	return ""
}

// cacheDoc is a buffer identity. TextRevision.Doc is compared and never
// dereferenced, so an empty struct pointer is all a test needs.
type cacheDoc struct{ _ int }

// cacheScript is a corpus in one script: statements separated by ';' and by
// "GO", a multi-line block comment, a string literal and a bracketed
// identifier each hiding a ';' and a "GO", and a commented-out separator.
const cacheScript = `SELECT a FROM dbo.One;
SELECT b FROM dbo.Two
GO
/* a comment
   GO
   ; still the comment */
SELECT c FROM dbo.Three;
SELECT '; GO' AS lit FROM dbo.Four;
-- GO
SELECT [a; GO b] FROM dbo.Five
GO 2
SELECT f FROM dbo.Six;
SELECT g FROM dbo.Seven`

// checkCursorSweep asserts the cached scan matches ScanPrefix at every cursor
// position in lines, feeding them to one live cache in the order the cache is
// least comfortable with.
//
// The sweep runs bottom to top deliberately. Ascending, the first position
// after an edit is offset 0, which discards every boundary and turns the rest
// into a growing cold scan — so a cache that ignored the edit entirely would
// pass. Descending, the first position after an edit is the bottom of the
// script, with the cache still holding every boundary below the edit: the
// state invalidation must get right. Each step up then leaves boundaries
// recorded *past* the cursor, the state the read-time bound must get right.
func checkCursorSweep(t *testing.T, c *PrefixCache, lines [][]rune, rev TextRevision, label string) {
	t.Helper()
	buf := flattenFresh(lines)
	for row := len(lines) - 1; row >= 0; row-- {
		for col := 0; col <= len(lines[row]); col++ {
			upTo := OffsetForCursor(lines, row, col)
			want := ScanPrefix(lines, flattenFresh(lines), row, upTo)
			got := c.Scan(lines, buf, row, upTo, rev)
			if diff := compareCachedScans(want, got); diff != "" {
				t.Fatalf("%s at %d:%d:%s", label, row, col, diff)
			}
		}
	}
}

// TestPrefixCacheMatchesScanPrefixWhileTyping walks the cursor over the whole
// script after every keystroke — the case the cache exists for, a single
// setLine bumping the version by one with DirtyFrom on the edited row.
//
// The sweep after each edit makes it a real check: typing at the end only ever
// resumes at the boundary just above, while a cursor two hundred lines up must
// be answered from boundaries recorded long before the edit.
func TestPrefixCacheMatchesScanPrefixWhileTyping(t *testing.T) {
	doc := &cacheDoc{}
	lines := splitRunes(cacheScript)
	c := &PrefixCache{}
	ver := uint64(1)

	last := len(lines) - 1
	for _, r := range " WHERE g = 1;" {
		lines[last] = append(lines[last], r)
		ver++
		checkCursorSweep(t, c, lines, TextRevision{Doc: doc, Version: ver, DirtyFrom: last},
			"after typing "+string(r))
	}
}

// TestPrefixCacheColdMatchesScanPrefix runs every cursor position against a
// *fresh* cache, which is the only way to exercise the scanning path at every
// position: a warm cache answers a cursor move from what it already holds and
// never lexes at all.
//
// The positions that matter are inside a half-written separator — the cursor
// two runes into "GO 2", where the text before it reads like a bare "GO".
// Judging that line from its prefix would start a batch at the cursor and drop
// the statement being typed, which is why GO detection stops at the cursor's
// own row rather than at the cursor.
func TestPrefixCacheColdMatchesScanPrefix(t *testing.T) {
	doc := &cacheDoc{}
	lines := splitRunes(cacheScript + "\nGOOD AS a FROM dbo.Eight")
	buf := flattenFresh(lines)
	for row := range lines {
		for col := 0; col <= len(lines[row]); col++ {
			upTo := OffsetForCursor(lines, row, col)
			want := ScanPrefix(lines, flattenFresh(lines), row, upTo)
			got := (&PrefixCache{}).Scan(lines, buf, row, upTo, TextRevision{Doc: doc, Version: 1})
			if diff := compareCachedScans(want, got); diff != "" {
				t.Fatalf("cold scan at %d:%d:%s", row, col, diff)
			}
		}
	}
}

// TestPrefixCacheMatchesScanPrefixOnEditAboveCursor covers the edit the resume
// rule must be most careful about: one *above* the cursor, which invalidates
// boundaries below it. Opening a block comment on line 1 swallows every ';'
// and "GO" under it until it closes, so a cache that resumed past the edit
// would keep reporting a statement boundary that no longer exists.
func TestPrefixCacheMatchesScanPrefixOnEditAboveCursor(t *testing.T) {
	doc := &cacheDoc{}
	lines := splitRunes(cacheScript)
	c := &PrefixCache{}
	ver := uint64(1)

	// Warm the cache at the bottom of the script first, so the edits below
	// have boundaries to invalidate.
	checkCursorSweep(t, c, lines, TextRevision{Doc: doc, Version: ver}, "cold")

	for _, step := range []struct {
		row  int
		text string
	}{
		{1, "SELECT b FROM dbo.Two /*"},   // opens a comment over everything below
		{1, "SELECT b FROM dbo.Two /**/"}, // closes it again on the same line
		{0, "SELECT a FROM dbo.One"},      // drops the first ';' entirely
		{0, "GO"},                         // and turns line 0 into a separator
		{2, "-- GO"},                      // comments out the separator on line 2
		{2, "GO"},                         // puts it back
	} {
		lines[step.row] = []rune(step.text)
		ver++
		checkCursorSweep(t, c, lines, TextRevision{Doc: doc, Version: ver, DirtyFrom: step.row},
			"after rewriting line "+string(rune('0'+step.row)))
	}
}

// TestPrefixCacheMatchesScanPrefixOnLineCountChange covers the mutation
// controls.prefixStates must throw its whole array away for. Boundaries are
// keyed by offset, not by line, so a split or a join invalidates only what is
// at or below it — but only if DirtyFrom is measured against the new lines,
// the one place this could go wrong silently.
func TestPrefixCacheMatchesScanPrefixOnLineCountChange(t *testing.T) {
	doc := &cacheDoc{}
	lines := splitRunes(cacheScript)
	c := &PrefixCache{}
	ver := uint64(1)
	checkCursorSweep(t, c, lines, TextRevision{Doc: doc, Version: ver}, "cold")

	// Split line 6 in two, then paste three statements in at line 3.
	ver++
	split := append([][]rune{}, lines[:6]...)
	split = append(split, []rune("SELECT c FROM"), []rune(" dbo.Three;"))
	split = append(split, lines[7:]...)
	lines = split
	checkCursorSweep(t, c, lines, TextRevision{Doc: doc, Version: ver, DirtyFrom: 6}, "after split")

	ver++
	pasted := append([][]rune{}, lines[:3]...)
	pasted = append(pasted, splitRunes("SELECT x;\nGO\nSELECT y /* open")...)
	pasted = append(pasted, lines[3:]...)
	lines = pasted
	checkCursorSweep(t, c, lines, TextRevision{Doc: doc, Version: ver, DirtyFrom: 3}, "after paste")
}

// TestPrefixCacheFallsBackWhenItCannotResume pins the cases the cache must
// refuse to trust. Each is a revision the resume rule cannot justify, and each
// must still produce ScanPrefix's answer — by starting over.
//
// The second script is the first with an unclosed block comment opened above
// it, so every boundary the cache holds is wrong, and wrong loudly: the whole
// script is one comment, the batch starts at offset 0 and there are no tokens.
// A cache resuming from the warm script's last boundary would lex from the
// middle of that comment in LexNormal and report a batch start hundreds of
// runes down.
func TestPrefixCacheFallsBackWhenItCannotResume(t *testing.T) {
	doc, other := &cacheDoc{}, &cacheDoc{}
	warm := splitRunes(cacheScript)
	warmBuf := flattenFresh(warm)
	warmRow := len(warm) - 1
	warmUpTo := OffsetForCursor(warm, warmRow, len(warm[warmRow]))

	stale := splitRunes("/* nothing below closes this\n" + strings.ReplaceAll(cacheScript, "*/", "* /"))
	staleBuf := flattenFresh(stale)
	staleRow := len(stale) - 1
	staleUpTo := OffsetForCursor(stale, staleRow, len(stale[staleRow]))
	want := ScanPrefix(stale, flattenFresh(stale), staleRow, staleUpTo)
	if want.BatchStart != 0 || len(want.Tokens) != 0 {
		t.Fatalf("the second script should be one unclosed comment; ScanPrefix says %s", formatScan(want))
	}

	for _, tc := range []struct {
		name string
		rev  TextRevision
	}{
		{"a different document", TextRevision{Doc: other, Version: 8, DirtyFrom: staleRow}},
		{"no document identity at all", TextRevision{}},
		{"two versions ahead", TextRevision{Doc: doc, Version: 9, DirtyFrom: staleRow}},
		{"a DirtyFrom of 0", TextRevision{Doc: doc, Version: 8, DirtyFrom: 0}},
		{"a DirtyFrom past the end", TextRevision{Doc: doc, Version: 8, DirtyFrom: len(stale)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &PrefixCache{}
			c.Scan(warm, warmBuf, warmRow, warmUpTo, TextRevision{Doc: doc, Version: 7})
			if len(c.bounds) == 0 {
				t.Fatal("the warm scan recorded no boundaries, so this proves nothing")
			}
			if diff := compareCachedScans(want, c.Scan(stale, staleBuf, staleRow, staleUpTo, tc.rev)); diff != "" {
				t.Errorf("%s:%s", tc.name, diff)
			}
		})
	}
}

// TestPrefixCacheActuallyResumes is the test the others cannot be: they all
// pass just as well if Scan quietly rescans from offset 0 every time, which
// makes the change pointless rather than wrong. This asserts the resume point
// is where it should be — the boundary immediately above the edit, far down a
// long script.
func TestPrefixCacheActuallyResumes(t *testing.T) {
	doc := &cacheDoc{}
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("SELECT col FROM dbo.Tbl;\n")
		if i%10 == 9 {
			b.WriteString("GO\n")
		}
	}
	b.WriteString("SELECT ")
	lines := splitRunes(b.String())
	buf := flattenFresh(lines)
	row := len(lines) - 1
	upTo := OffsetForCursor(lines, row, len(lines[row]))

	c := &PrefixCache{}
	c.Scan(lines, buf, row, upTo, TextRevision{Doc: doc, Version: 1})
	if len(c.bounds) < 200 {
		t.Fatalf("cold scan recorded %d boundaries, want one per statement", len(c.bounds))
	}

	// One more rune on the cursor's own line, as typing produces: everything
	// above that line start is still good, so the rescan covers that line and
	// nothing else.
	lines[row] = append(lines[row], 'c')
	validTo, keep := c.validPrefix(lines, upTo+1, TextRevision{Doc: doc, Version: 2, DirtyFrom: row})
	if want := OffsetForCursor(lines, row, 0); validTo != want {
		t.Errorf("valid prefix after a keystroke = %d, want the cursor line's start, %d", validTo, want)
	}
	if keep != len(c.bounds) || c.bounds[keep-1].off > validTo {
		t.Errorf("kept %d of %d boundaries, want all of them at or below %d", keep, len(c.bounds), validTo)
	}

	// Moving the cursor without typing must not cost a rescan: every boundary
	// above it is already recorded, so the valid prefix reaches the new cursor
	// and Scan's lexSQL pass is skipped.
	up := OffsetForCursor(lines, row-3, 0)
	if validTo, _ = c.validPrefix(lines, up, TextRevision{Doc: doc, Version: 1}); validTo < up {
		t.Errorf("a cursor move with no edit left only %d of %d valid — it rescans", validTo, up)
	}
}

// ---------------------------------------------------------------------------
// The randomized sweep, and the ScanPrefix corpus driven through the cache
// ---------------------------------------------------------------------------
//
// Everything above tests the cache over the edits its resume rule was written
// for — the shape of test that agrees with a bug in the rule. What follows
// tests it over edits nobody chose: thousands of random ones, then the whole
// ScanPrefix corpus (the scripts TestScanCompletionPrefixGolden freezes), so
// the two paths share one set of expectations.

// editSeed keeps the random sweep reproducible: the same edits come out on
// every run and on every machine, so a failure can be read and re-run rather
// than chased.
const editSeed = 20260919

// editDoc models the document the way the cache is told about it: the text,
// plus the revision every mutation moves. Document bumps its version by one
// per mutation and sets dirtyFrom to the lowest line the mutation could have
// changed the meaning of; mutate is that and nothing else.
type editDoc struct {
	lines [][]rune
	rev   TextRevision
	buf   []rune // recycled across keystrokes, as QueryPanel's is
}

func (d *editDoc) mutate(from int) {
	d.rev.Version++
	d.rev.DirtyFrom = from
}

func (d *editDoc) text() string {
	var b strings.Builder
	for i, line := range d.lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(string(line))
	}
	return b.String()
}

// editFragments are what the random edits insert: constructs that can change
// where a batch starts, or swallow one that already did — an opener with no
// closer, a closer with no opener, a separator, a ';'. Ordinary SQL words are
// in there too, so the script does not degenerate into pure punctuation.
var editFragments = []string{
	"/*", "*/", "/* x */", "--", "-- GO", "'", "''", "'lit'", "[", "]", "[id]",
	"\"", "\"dq\"", ";", ";;", "GO", "go", "GO 2", "GOO", "\tGO\t",
	"SELECT ", " FROM dbo.T", " WHERE x = 1", "a.b", "f(x, y)", " ", "",
}

// deleteLineOp is also the pressure valve: the random walk inserts more than
// it removes, so the driver forces this one whenever the script grows past
// the size the sweep is meant to run at.
var deleteLineOp = editOp{"delete line", func(rng *rand.Rand, d *editDoc) string {
	if len(d.lines) < 2 {
		return ""
	}
	row := rng.Intn(len(d.lines))
	d.lines = append(d.lines[:row:row], d.lines[row+1:]...)
	d.mutate(row)
	return fmt.Sprintf("delete line %d", row)
}}

type editOp struct {
	name string
	// apply mutates d and returns a description of what it did, or "" when
	// there was nothing to do — a join with no line below, a delete from an
	// empty line. A no-op must not bump the version, hence the return.
	apply func(rng *rand.Rand, d *editDoc) string
}

var editOps = []editOp{
	{"insert", func(rng *rand.Rand, d *editDoc) string {
		row := rng.Intn(len(d.lines))
		col := rng.Intn(len(d.lines[row]) + 1)
		frag := []rune(editFragments[rng.Intn(len(editFragments))])
		line := d.lines[row]
		d.lines[row] = append(append(append([]rune{}, line[:col]...), frag...), line[col:]...)
		d.mutate(row)
		return fmt.Sprintf("insert %q at %d:%d", string(frag), row, col)
	}},
	{"delete", func(rng *rand.Rand, d *editDoc) string {
		row := rng.Intn(len(d.lines))
		line := d.lines[row]
		if len(line) == 0 {
			return ""
		}
		col := rng.Intn(len(line))
		n := 1 + rng.Intn(min(4, len(line)-col))
		d.lines[row] = append(append([]rune{}, line[:col]...), line[col+n:]...)
		d.mutate(row)
		return fmt.Sprintf("delete %d rune(s) at %d:%d", n, row, col)
	}},
	{"split", func(rng *rand.Rand, d *editDoc) string {
		row := rng.Intn(len(d.lines))
		col := rng.Intn(len(d.lines[row]) + 1)
		line := d.lines[row]
		split := append([][]rune{}, d.lines[:row]...)
		split = append(split, append([]rune{}, line[:col]...), append([]rune{}, line[col:]...))
		d.lines = append(split, d.lines[row+1:]...)
		d.mutate(row)
		return fmt.Sprintf("split %d:%d", row, col)
	}},
	{"join", func(rng *rand.Rand, d *editDoc) string {
		if len(d.lines) < 2 {
			return ""
		}
		row := rng.Intn(len(d.lines) - 1)
		joined := append(append([]rune{}, d.lines[row]...), d.lines[row+1]...)
		d.lines = append(append(append([][]rune{}, d.lines[:row]...), joined), d.lines[row+2:]...)
		d.mutate(row)
		return fmt.Sprintf("join line %d with %d", row, row+1)
	}},
	{"paste", func(rng *rand.Rand, d *editDoc) string {
		row := rng.Intn(len(d.lines))
		var b strings.Builder
		for i := 0; i < 1+rng.Intn(3); i++ {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(editFragments[rng.Intn(len(editFragments))])
			b.WriteString(editFragments[rng.Intn(len(editFragments))])
		}
		pasted := splitRunes(b.String())
		d.lines = append(append(append([][]rune{}, d.lines[:row]...), pasted...), d.lines[row:]...)
		d.mutate(row)
		return fmt.Sprintf("paste %d line(s) at %d: %q", len(pasted), row, b.String())
	}},
	// The same-length overwrite an undo produces: the text changes but nothing
	// moves, so a cache keyed on offsets alone would sail past it.
	{"overwrite", func(rng *rand.Rand, d *editDoc) string {
		row := rng.Intn(len(d.lines))
		line := d.lines[row]
		if len(line) < 2 {
			return ""
		}
		col := rng.Intn(len(line) - 1)
		over := []rune("*/;['")
		next := append([]rune{}, line...)
		next[col], next[col+1] = over[rng.Intn(len(over))], over[rng.Intn(len(over))]
		d.lines[row] = next
		d.mutate(row)
		return fmt.Sprintf("overwrite 2 runes at %d:%d with %q", row, col, string(next[col:col+2]))
	}},
	// Plain typing at the end of the script: the case the cache exists for, and
	// one the walk would otherwise produce only by accident.
	{"type", func(rng *rand.Rand, d *editDoc) string {
		row := len(d.lines) - 1
		frag := []rune(editFragments[rng.Intn(len(editFragments))])
		if len(frag) == 0 {
			return ""
		}
		r := frag[rng.Intn(len(frag))]
		d.lines[row] = append(append([]rune{}, d.lines[row]...), r)
		d.mutate(row)
		return fmt.Sprintf("type %q at end of line %d", string(r), row)
	}},
	deleteLineOp,
}

// checkSampledPositions asserts the cached scan still matches ScanPrefix,
// bottom to top, at a spread of cursor positions rather than all of them: the
// sweep runs thousands of edits, and an exhaustive check after each would
// trade edits for positions at the same cost. Descending is not a sampling
// detail — see checkCursorSweep.
func checkSampledPositions(t *testing.T, c *PrefixCache, d *editDoc, rng *rand.Rand, history []string) {
	t.Helper()
	d.buf = FlattenLinesInto(d.buf, d.lines)
	for row := len(d.lines) - 1; row >= 0; row -= 1 + rng.Intn(3) {
		cols := []int{len(d.lines[row])}
		if n := len(d.lines[row]); n > 0 {
			cols = append(cols, rng.Intn(n))
		}
		for _, col := range cols {
			upTo := OffsetForCursor(d.lines, row, col)
			want := ScanPrefix(d.lines, flattenFresh(d.lines), row, upTo)
			got := c.Scan(d.lines, d.buf, row, upTo, d.rev)
			if diff := compareCachedScans(want, got); diff != "" {
				t.Fatalf("cursor %d:%d, revision %+v, after:\n\t%s\nscript:\n%s\n%s",
					row, col, d.rev, strings.Join(history, "\n\t"), d.text(), diff)
			}
		}
	}
}

// TestPrefixCacheRandomEditSweep is what makes the cache safe to ship. A stale
// boundary is a silently *wrong* completion, not a slow one, and the tests
// above answer that only for the edits they thought to make. This applies
// thousands of edits nobody chose — insert, delete, split, join, paste,
// same-length overwrite, whole-line delete, plain typing — and asserts after
// every one that the cache answers exactly what ScanPrefix answers.
//
// The revision occasionally jumps two versions: the keystroke that first
// deletes a selection — two setLines, one DirtyFrom, and a cache that must
// refuse to resume from either.
func TestPrefixCacheRandomEditSweep(t *testing.T) {
	rng := rand.New(rand.NewSource(editSeed))
	d := &editDoc{lines: splitRunes(cacheScript), rev: TextRevision{Doc: &cacheDoc{}, Version: 1}}
	c := &PrefixCache{}

	// The last few edits only: the failure prints the script as it stands, and
	// what matters is how it got its last shape.
	var history []string
	for i := 0; i < 2000; i++ {
		op := editOps[rng.Intn(len(editOps))]
		if len(d.lines) > 60 {
			op = deleteLineOp
		}
		desc := op.apply(rng, d)
		if desc == "" {
			continue
		}
		// Every so often a second edit lands before anything is asked about the
		// text: the keystroke that first deletes a selection — two setLines and
		// one DirtyFrom, the second one's, which says nothing about the first.
		// The cache must refuse to resume from either, and the second edit is a
		// real one so that refusing matters: when it falls below the first, a
		// resume keeps boundaries the first edit already invalidated.
		if rng.Intn(20) == 0 {
			if second := editOps[rng.Intn(len(editOps))].apply(rng, d); second != "" {
				desc += ", then " + second + " (both before the next scan, as a selection delete produces)"
			}
		}
		if history = append(history, fmt.Sprintf("#%d %s", i, desc)); len(history) > 8 {
			history = history[1:]
		}
		checkSampledPositions(t, c, d, rng, history)
	}
}

// TestPrefixCacheMatchesCorpusWhileTyping drives diffCorpus — the scripts
// TestScanCompletionPrefixGolden freezes ScanPrefix's answers over — through
// the cached path, typed one rune at a time with the cursor at the end. Every
// curated shape the scan has been wrong about is in there, which is what makes
// the two paths share one set of expectations.
func TestPrefixCacheMatchesCorpusWhileTyping(t *testing.T) {
	for _, name := range sortedCorpusNames() {
		script := diffCorpus[name]
		t.Run(name, func(t *testing.T) {
			d := &editDoc{lines: [][]rune{{}}, rev: TextRevision{Doc: &cacheDoc{}, Version: 1}}
			c := &PrefixCache{}
			runes := []rune(script)
			for n := 0; ; n++ {
				row := len(d.lines) - 1
				upTo := OffsetForCursor(d.lines, row, len(d.lines[row]))
				want := ScanPrefix(d.lines, flattenFresh(d.lines), row, upTo)
				d.buf = FlattenLinesInto(d.buf, d.lines)
				if diff := compareCachedScans(want, c.Scan(d.lines, d.buf, row, upTo, d.rev)); diff != "" {
					t.Fatalf("after %d of %d runes (%q):%s", n, len(runes), d.text(), diff)
				}
				if n == len(runes) {
					return
				}
				// A newline is the split every Enter produces; the line it
				// starts is the one DirtyFrom names either way.
				if runes[n] == '\n' {
					d.lines = append(d.lines, []rune{})
				} else {
					d.lines[row] = append(d.lines[row], runes[n])
				}
				d.mutate(row)
			}
		})
	}
}

// TestPrefixCacheMatchesCorpusCursorSweep is the other half of the corpus:
// every curated script and all 400 generated ones, swept at every cursor
// position. No edits, so it exercises the read-time half of the rule —
// boundaries recorded past the cursor, and the cursor's own row, which must
// never be judged from its prefix.
//
// All of them go through *one* cache, each script its own document at version
// 1 — the second query tab, and the only check that a resume turns on the
// document's identity: every version here is equal, so a cache comparing
// versions alone would answer one script from another's boundaries.
func TestPrefixCacheMatchesCorpusCursorSweep(t *testing.T) {
	scripts := make(map[string]string, len(diffCorpus)+400)
	for name, script := range diffCorpus {
		scripts[name] = script
	}
	for i, script := range generatedScripts() {
		scripts[fmt.Sprintf("script%03d", i)] = script
	}
	c := &PrefixCache{}
	for name, script := range scripts {
		checkCursorSweep(t, c, splitRunes(script),
			TextRevision{Doc: &cacheDoc{}, Version: 1}, fmt.Sprintf("%s %q", name, script))
	}
}
