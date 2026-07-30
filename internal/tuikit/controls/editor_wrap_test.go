package controls

import (
	"reflect"
	"strings"
	"testing"
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
			want := referenceVisualLines(e.lines, w)
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
			want := referenceVisualLines(e.lines, w)
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
	for i := 0; i < 5; i++ {
		if got := cap(e.buildVisualLines(40)); got != firstCap {
			t.Fatalf("call %d reallocated: cap = %d, want the first call's %d", i+2, got, firstCap)
		}
	}
}
