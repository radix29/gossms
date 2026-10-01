package sqlparse

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// BatchEndOffset is where the cursor's GO-delimited batch ends: the start of
// the next bare "GO" line below the cursor's own row, or len(buf), lexed
// forward from upTo. It is the forward scan BatchCache replaced, kept as a
// second reference: wherever the cursor's row starts in LexNormal it must agree
// with the cache, so the cache cannot drift from what shipped before it.
func BatchEndOffset(lines [][]rune, buf []rune, cursorRow, upTo int) int {
	r := lexSQL(buf, upTo, len(buf), false, LexNormal, nil,
		goScan{lo: OffsetForCursor(lines, cursorRow+1, 0), hi: len(buf)}, nil, nil)
	if r.firstGo >= 0 {
		return r.firstGo
	}
	return len(buf)
}

// referenceBatch is BatchCache.batch from scratch: one lex of the whole buffer
// collecting its marks, then the last "GO" line strictly above the cursor's
// row and the first strictly below it. It also reports whether the row itself
// starts in LexNormal, the condition under which BatchEndOffset must agree.
func referenceBatch(lines [][]rune, buf []rune, row int) (from, to int, rowIsMark bool) {
	rowStart := OffsetForCursor(lines, row, 0)
	nextRow := len(buf) + 1
	if row+1 < len(lines) {
		nextRow = OffsetForCursor(lines, row+1, 0)
	}
	to = len(buf)
	lexSQL(buf, 0, len(buf), false, LexNormal, nil, allLines(buf), nil, func(start, goNext int) bool {
		switch {
		case start == rowStart:
			rowIsMark = true
		case goNext >= 0 && start < rowStart:
			from = goNext
		case goNext >= 0 && start >= nextRow:
			to = start
			return true
		}
		return false
	})
	return from, to, rowIsMark
}

// referenceCarried is BatchCache.carryTo from scratch: CarryTempBindings
// folded over every batch above from, each tokenized on its own.
func referenceCarried(buf []rune, from int) []Binding {
	var carried []Binding
	start := 0
	lexSQL(buf, 0, len(buf), false, LexNormal, nil, allLines(buf), nil, func(lineStart, goNext int) bool {
		if lineStart >= from {
			return true
		}
		if goNext >= 0 {
			tokens, _, _, _ := TokenizeRange(buf, start, lineStart, false)
			carried = CarryTempBindings(carried, tokens)
			start = goNext
		}
		return false
	})
	return carried
}

// checkBatch asserts the cache answers the batch holding row exactly as a
// from-scratch scan does: its bounds, its tokens, and the bindings — and that
// the bounds are the ones PrefixScan.GoStart and BatchEndOffset found.
func checkBatch(t *testing.T, c *BatchCache, lines [][]rune, buf []rune, row int, history []string) {
	t.Helper()
	fresh := flattenFresh(lines)
	from, to, rowIsMark := referenceBatch(lines, fresh, row)
	wantTokens, _, _, _ := TokenizeRange(fresh, from, to, false)
	gotBindings := c.Bindings(lines, buf, row)
	fail := func(format string, args ...any) {
		t.Helper()
		t.Fatalf("row %d, after:\n\t%s\nscript:\n%s\n%s", row, strings.Join(history, "\n\t"),
			string(fresh), fmt.Sprintf(format, args...))
	}
	if c.bindFrom != from || c.bindTo != to {
		fail("batch [%d, %d), want [%d, %d)", c.bindFrom, c.bindTo, from, to)
	}
	if got := c.tokensIn(from, to); !reflect.DeepEqual(got, wantTokens) && (len(got) > 0 || len(wantTokens) > 0) {
		fail("tokens differ:\n got %v\nwant %v", got, wantTokens)
	}
	if want := slices.Concat(referenceCarried(fresh, from), ScanBindings(wantTokens)); !reflect.DeepEqual(gotBindings, want) {
		fail("bindings %+v, want %+v", gotBindings, want)
	}
	if goStart := ScanPrefix(lines, fresh, row, OffsetForCursor(lines, row, len(lines[row]))).GoStart; goStart != from {
		fail("batch starts at %d, PrefixScan.GoStart is %d", from, goStart)
	}
	if rowStart := OffsetForCursor(lines, row, 0); rowIsMark {
		if end := BatchEndOffset(lines, fresh, row, rowStart); end != to {
			fail("batch ends at %d, BatchEndOffset is %d", to, end)
		}
	}
}

// batchScript is cacheScript with the declarations ScanBindings reads spread
// across its batches, so the sweep compares bindings that exist.
const batchScript = `CREATE TABLE #t (a int NOT NULL, b nvarchar(50));
DECLARE @v TABLE (x int);
` + cacheScript + `
SELECT a, b INTO #u FROM dbo.One
GO
DECLARE @w TABLE (y decimal(18, 2))
SELECT * FROM @w`

// TestBatchCacheRandomEditSweep is TestPrefixCacheRandomEditSweep for the batch
// cache: thousands of edits nobody chose, and after each one the batch checked
// at rows in random order. Random, not descending, because lexing is lazy: a
// row further down than any asked about before makes the cache lex on, and an
// edit below what it has lexed must be handled as well as one inside it.
func TestBatchCacheRandomEditSweep(t *testing.T) {
	rng := rand.New(rand.NewSource(editSeed))
	d := &editDoc{lines: splitRunes(batchScript)}
	c := &BatchCache{}

	var history []string
	for i := 0; i < 3000; i++ {
		op := editOps[rng.Intn(len(editOps))]
		if len(d.lines) > 60 {
			op = deleteLineOp
		}
		desc := op.apply(rng, d)
		if desc == "" {
			continue
		}
		if history = append(history, fmt.Sprintf("#%d %s", i, desc)); len(history) > 8 {
			history = history[1:]
		}
		d.buf = FlattenLinesInto(d.buf, d.lines)
		for range 1 + rng.Intn(3) {
			checkBatch(t, c, d.lines, d.buf, rng.Intn(len(d.lines)), history)
		}
	}
}

// TestBatchCacheMatchesCorpusWhileTyping types every curated script one rune
// at a time, checking the batch at the cursor's row and at the first row after
// each: the end of the text is where an edit leaves nothing to resynchronise
// with, and the top is where the batch reaches furthest down.
func TestBatchCacheMatchesCorpusWhileTyping(t *testing.T) {
	for _, name := range sortedCorpusNames() {
		script := diffCorpus[name]
		t.Run(name, func(t *testing.T) {
			lines := [][]rune{{}}
			var buf []rune
			c := &BatchCache{}
			runes := []rune(script)
			for n := 0; n <= len(runes); n++ {
				buf = FlattenLinesInto(buf, lines)
				history := []string{fmt.Sprintf("%d of %d runes typed", n, len(runes))}
				checkBatch(t, c, lines, buf, len(lines)-1, history)
				checkBatch(t, c, lines, buf, 0, history)
				if n == len(runes) {
					return
				}
				if runes[n] == '\n' {
					lines = append(lines, []rune{})
				} else {
					lines[len(lines)-1] = append(lines[len(lines)-1], runes[n])
				}
			}
		})
	}
}

// TestBatchCacheResyncsAfterOneLine pins the property the cache exists for. A
// rune typed near the top of a long batch must be answered by re-lexing that
// line, not the batch — the sweep above would pass just as well against a
// cache that re-lexed everything on every call.
func TestBatchCacheResyncsAfterOneLine(t *testing.T) {
	lines := benchScript(400)
	var buf []rune
	c := &BatchCache{}
	buf = FlattenLinesInto(buf, lines)
	c.Bindings(lines, buf, 2)

	lines[2] = append(lines[2], 'x')
	buf = FlattenLinesInto(buf, lines)
	checkBatch(t, c, lines, buf, 2, []string{"typed 'x' at the end of line 2"})
	if n := len(c.scratchTokens); n > 20 {
		t.Errorf("a one-rune edit re-lexed %d tokens; it should stop at the next line", n)
	}

	// Opening a comment re-lexes to wherever it closes — here, nowhere — and
	// closing it again must come back to one line.
	lines[2] = append(lines[2], '/', '*')
	buf = FlattenLinesInto(buf, lines)
	checkBatch(t, c, lines, buf, 2, []string{"typed '/*' at the end of line 2"})
	lines[2] = lines[2][:len(lines[2])-2]
	buf = FlattenLinesInto(buf, lines)
	checkBatch(t, c, lines, buf, 2, []string{"removed the '/*' again"})
	lines[2] = append(lines[2], 'y')
	buf = FlattenLinesInto(buf, lines)
	checkBatch(t, c, lines, buf, 2, []string{"typed 'y' at the end of line 2"})
	if n := len(c.scratchTokens); n > 20 {
		t.Errorf("after the comment closed, a one-rune edit re-lexed %d tokens", n)
	}
}

// TestBatchCacheLexesOnlyToTheBatchEnd: the first call on a long script pays
// for the text down to the batch end below the cursor, not for the rest.
func TestBatchCacheLexesOnlyToTheBatchEnd(t *testing.T) {
	lines := benchScript(400) // a GO line every 20 statements
	buf := flattenFresh(lines)
	c := &BatchCache{}
	c.Bindings(lines, buf, 2)
	_, to, _ := referenceBatch(lines, buf, 2)
	if to == len(buf) {
		t.Fatal("benchScript has no GO below row 2; the test proves nothing")
	}
	if c.lexedTo != to {
		t.Errorf("lexed to %d; the batch ends at %d, of %d", c.lexedTo, to, len(buf))
	}
}

// TestBatchCacheReDecidesTheSeparatorItStoppedAt: lazy lexing stops on the
// "GO" line ending the batch, and an edit to that very line — below the
// restart, past nothing else lexed — must take the separator decision again
// rather than keep the one the stop recorded.
func TestBatchCacheReDecidesTheSeparatorItStoppedAt(t *testing.T) {
	lines := splitRunes("SELECT #a\nSELECT 1\nGO\nSELECT 2\nGO\nSELECT 3")
	buf := flattenFresh(lines)
	c := &BatchCache{}
	checkBatch(t, c, lines, buf, 0, []string{"cold"})

	lines[2] = append(lines[2], 'X') // "GOX" separates nothing
	buf = FlattenLinesInto(buf, lines)
	checkBatch(t, c, lines, buf, 0, []string{"GO on line 2 became GOX"})

	lines[2] = lines[2][:2]
	buf = FlattenLinesInto(buf, lines)
	checkBatch(t, c, lines, buf, 0, []string{"and back to GO"})
}

// TestBatchCacheStaysLazyAfterAnEdit: an edit whose re-lex outruns everything
// the previous pass lexed stops at the next line it can resume from, instead
// of running on to the end of the script. Opening a "/*" above the GO that
// ended the batch swallows it, and the old pass had nothing lexed past that
// GO to resynchronise with.
func TestBatchCacheStaysLazyAfterAnEdit(t *testing.T) {
	var b strings.Builder
	b.WriteString("SELECT #a\n\n")
	for i := range 50 {
		fmt.Fprintf(&b, "SELECT %d\n", i)
		if i == 20 {
			b.WriteString("GO\n")
		}
	}
	b.WriteString("*/\nGO\n")
	for i := range 200 {
		fmt.Fprintf(&b, "SELECT %d\nGO\n", i)
	}
	lines := splitRunes(b.String())
	buf := flattenFresh(lines)
	c := &BatchCache{}
	checkBatch(t, c, lines, buf, 0, []string{"cold"})

	lines[1] = []rune("/*")
	buf = FlattenLinesInto(buf, lines)
	checkBatch(t, c, lines, buf, 0, []string{"opened a /* on line 1"})
	if c.lexedTo > len(buf)/2 {
		t.Errorf("lexed to %d of %d after the edit; the batch ends near the top", c.lexedTo, len(buf))
	}
}

// TestBatchCacheRecarriesAfterAnEditAbove: an edit above the cursor's batch
// that leaves its start where it was must still redo what that batch carries
// in. The random sweep rarely makes one: its edits change lengths.
func TestBatchCacheRecarriesAfterAnEditAbove(t *testing.T) {
	lines := splitRunes("CREATE TABLE #t (a int)\nGO\nSELECT * FROM #t")
	buf := flattenFresh(lines)
	c := &BatchCache{}
	checkBatch(t, c, lines, buf, 2, []string{"cold"})

	lines[0][17] = 'b' // "(a int)" -> "(b int)": same length
	buf = FlattenLinesInto(buf, lines)
	checkBatch(t, c, lines, buf, 2, []string{"renamed column a to b above the GO"})
	if got := c.Bindings(lines, buf, 2); len(got) != 1 || got[0].Columns[0].Name != "b" {
		t.Errorf("carried %+v, want #t with column b", got)
	}
}
