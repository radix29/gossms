package controls

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// referenceVisualLines is buildVisualLines' pre-optimisation form: everything
// built from a fresh nil slice, no buffer shared with anything. It exists to
// be differentially compared against the real one, whose result aliases a
// buffer reused across calls — the failure that buys is subtle (a later call
// silently rewriting an earlier call's result) and would not show up as a
// crash.
func referenceVisualLines(lines [][]rune, w int) []visualLine {
	if w < 1 {
		w = 1
	}
	var out []visualLine
	for li, line := range lines {
		n := len(line)
		if n == 0 {
			out = append(out, visualLine{row: li, start: 0, end: 0})
			continue
		}
		start := 0
		for start < n {
			end := start + w
			if end >= n {
				out = append(out, visualLine{row: li, start: start, end: n})
				break
			}
			breakAt := end
			lastSpace := -1
			for i := start; i < end; i++ {
				if line[i] == ' ' || line[i] == '\t' {
					lastSpace = i
				}
			}
			if lastSpace >= start {
				breakAt = lastSpace + 1
			}
			out = append(out, visualLine{row: li, start: start, end: breakAt})
			start = breakAt
		}
	}
	return out
}

func wrapTestDocs() map[string]string {
	return map[string]string{
		"empty":           "",
		"blank lines":     "\n\n\n",
		"short":           "hello",
		"exact width":     "abcdefghij",
		"one long word":   strings.Repeat("x", 95),
		"spaces":          "the quick brown fox jumps over the lazy dog",
		"trailing space":  "alpha beta ",
		"mixed":           "short\n" + strings.Repeat("y", 47) + "\n\na b c d e f g h i j k l",
		"leading spaces":  "    indented line that goes on for a while",
		"tabs and spaces": "a\tb c\td e\tf g h i j k l m n o p",
	}
}

func TestBuildVisualLinesMatchesTheUnbufferedForm(t *testing.T) {
	for name, doc := range wrapTestDocs() {
		for _, w := range []int{1, 2, 3, 7, 10, 40, 200} {
			e := NewEditor(nil)
			e.SetText(doc)
			got := e.buildVisualLines(w)
			want := referenceVisualLines(e.doc.all(), w)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s @ w=%d: buildVisualLines = %v, want %v", name, w, got, want)
			}
		}
	}
}

// TestBuildVisualLinesSurvivesRepeatedCalls is the reuse-specific half: the
// same Editor is asked over and over, at changing widths and after edits, and
// every answer must still match the unbuffered form. A buffer that isn't
// truncated correctly between calls shows up here as a stale tail, which the
// single-call test above cannot see.
func TestBuildVisualLinesSurvivesRepeatedCalls(t *testing.T) {
	e := NewEditor(nil)
	docs := []string{
		strings.Repeat("z", 300),        // one very long line: many segments
		"tiny",                          // then far fewer, exposing a missing truncate
		"a b c\nd e f\ng h i",           //
		"",                              // empty document
		strings.Repeat("word ", 60),     // many short segments
		"back to something\nshort here", //
	}
	for round, doc := range docs {
		e.SetText(doc)
		for _, w := range []int{80, 5, 33, 1, 120} {
			got := e.buildVisualLines(w)
			want := referenceVisualLines(e.doc.all(), w)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round %d @ w=%d: buildVisualLines = %v, want %v", round, w, got, want)
			}
		}
	}
}

// TestBuildVisualLinesReusesItsBuffer is the point of the change: a steady
// state document must stop allocating a fresh slice on every call, since
// Draw calls this on every event the app processes.
func TestBuildVisualLinesReusesItsBuffer(t *testing.T) {
	e := NewEditor(nil)
	e.SetText(strings.Repeat("some words to wrap ", 200))

	first := e.buildVisualLines(40)
	firstCap := cap(first)
	if firstCap == 0 {
		t.Fatal("buildVisualLines returned an empty slice, nothing to check")
	}
	for i := range 5 {
		if got := cap(e.buildVisualLines(40)); got != firstCap {
			t.Fatalf("call %d reallocated: cap = %d, want the first call's %d", i+2, got, firstCap)
		}
	}
}

// TestBuildVisualLinesTracksEditsIncrementally drives random edits through the
// paths that reach the document — typing, Enter (with its auto-indent, several
// mutations before the next look), joining Backspace and Delete, deleting a
// multi-line selection, multi-line paste, undo and redo, and an edit-based
// line operation that forces a rebuild — and checks the wrap cache against a
// from-scratch flattening every few steps. The incremental splice
// (rewrapSpan) going wrong shows up here as a row numbered for the old line
// count or a stale segment of an edited line.
func TestBuildVisualLinesTracksEditsIncrementally(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	e := NewEditor(nil)
	e.SetWrapMode(true)
	e.SetGutterVisible(false)
	e.SetBounds(0, 0, 12, 6)
	e.SetText("select a, b, c from t\n    where x = 1\n\nand the rest of a long line here\nend")
	const w = 12
	words := []string{"x", "go ", "alpha beta gamma", "\n", "a\nbb\nccc ddd eee\n", "    "}

	for step := range 3000 {
		e.cursorRow = rng.IntN(e.doc.Len())
		e.cursorCol = rng.IntN(len(e.doc.Line(e.cursorRow)) + 1)
		e.selecting = false
		switch rng.IntN(10) {
		case 0, 1:
			e.HandleKey(runeKey(rune('a'+rng.IntN(26)), tcell.ModNone))
		case 2:
			e.HandleKey(runeKey(' ', tcell.ModNone))
		case 3:
			e.HandleKey(key(tcell.KeyEnter, tcell.ModNone))
		case 4:
			e.HandleKey(key(tcell.KeyBackspace2, tcell.ModNone))
		case 5:
			e.HandleKey(key(tcell.KeyDelete, tcell.ModNone))
		case 6:
			e.Paste(words[rng.IntN(len(words))])
		case 7:
			e.selecting = true
			e.selAnchorRow = rng.IntN(e.doc.Len())
			e.selAnchorCol = rng.IntN(len(e.doc.Line(e.selAnchorRow)) + 1)
			e.HandleKey(key(tcell.KeyDelete, tcell.ModNone))
		case 8:
			if rng.IntN(2) == 0 {
				e.undo()
			} else {
				e.redo()
			}
		case 9:
			if rng.IntN(4) == 0 {
				e.DuplicateLines()
			} else {
				e.HandleKey(runeKey('y', tcell.ModNone))
			}
		}
		if rng.IntN(3) > 0 {
			continue // let several edits pile up between looks
		}
		got := e.buildVisualLines(w)
		if want := referenceVisualLines(e.doc.all(), w); !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d: buildVisualLines = %v, want %v", step, got, want)
		}
	}
}

// TestBuildVisualLinesLineCountEdits pins the cases the plan named: inserting
// and deleting lines (so every later visual row is renumbered) and a
// multi-line paste, each checked right after the edit with the cache warm
// from before it.
func TestBuildVisualLinesLineCountEdits(t *testing.T) {
	const w = 10
	edits := map[string]func(e *Editor){
		"enter mid-line":  func(e *Editor) { e.cursorRow, e.cursorCol = 1, 4; e.HandleKey(key(tcell.KeyEnter, tcell.ModNone)) },
		"backspace joins": func(e *Editor) { e.cursorRow, e.cursorCol = 2, 0; e.HandleKey(key(tcell.KeyBackspace2, tcell.ModNone)) },
		"delete joins": func(e *Editor) {
			e.cursorRow, e.cursorCol = 0, len(e.doc.Line(0))
			e.HandleKey(key(tcell.KeyDelete, tcell.ModNone))
		},
		"multi-line paste": func(e *Editor) {
			e.cursorRow, e.cursorCol = 1, 2
			e.Paste("one two three\nfour\n\nfive six seven eight")
		},
		"delete a selection": func(e *Editor) {
			e.selecting, e.selAnchorRow, e.selAnchorCol, e.cursorRow, e.cursorCol = true, 0, 3, 2, 1
			e.deleteSelection()
		},
		"undo a line insert": func(e *Editor) {
			e.cursorRow, e.cursorCol = 1, 0
			e.HandleKey(key(tcell.KeyEnter, tcell.ModNone))
			e.buildVisualLines(w)
			e.undo()
		},
		"edit at the last line": func(e *Editor) { e.cursorRow, e.cursorCol = 3, 1; e.HandleKey(key(tcell.KeyEnter, tcell.ModNone)) },
	}
	for name, edit := range edits {
		e := NewEditor(nil)
		e.SetText("first line of text\nsecond line wraps here\nthird\nlast one wraps too")
		e.buildVisualLines(w)
		edit(e)
		got := e.buildVisualLines(w)
		if want := referenceVisualLines(e.doc.all(), w); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: buildVisualLines = %v, want %v", name, got, want)
		}
	}
}
