package controls

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/theme"
)

// jsonHighlightRuns runs JSONHighlighter over a document and returns line
// idx's colored substrings, with a timeout guard so a scanner bug that fails
// to advance shows up as a clear failure instead of a hung test binary — same
// approach as xml_highlighter_test.go.
func jsonHighlightRuns(t *testing.T, lines [][]rune, idx int) []string {
	t.Helper()
	hl := JSONHighlighter(&theme.Default)
	done := make(chan []ColorRun, 1)
	go func() { done <- hl(docOf(lines), idx) }()
	select {
	case runs := <-done:
		line := lines[idx]
		out := make([]string, len(runs))
		for i, run := range runs {
			out[i] = string(line[run.Start : run.Start+run.Len])
		}
		return out
	case <-time.After(2 * time.Second):
		t.Fatal("JSONHighlighter did not return — infinite loop?")
		return nil
	}
}

func jsonHighlightWords(t *testing.T, line string) []string {
	t.Helper()
	return jsonHighlightRuns(t, [][]rune{[]rune(line)}, 0)
}

// The whole token stream of an ordinary object, in order: keys, string
// values, numbers, and literals each get their own run, and the structural
// punctuation between them gets none.
func TestJSONHighlighterTokenizesObject(t *testing.T) {
	got := jsonHighlightWords(t, `{"name": "Ann", "age": 41, "ok": true, "note": null}`)
	want := []string{`"name"`, `"Ann"`, `"age"`, "41", `"ok"`, "true", `"note"`, "null"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("runs = %v, want %v", got, want)
	}
}

// A key is a string followed by ':', a value is a string that isn't — the
// only thing separating them, so both must be found in the same line.
func TestJSONHighlighterDistinguishesKeysFromStringValues(t *testing.T) {
	hl := JSONHighlighter(&theme.Default)
	line := []rune(`{"k": "v"}`)
	runs := hl(docOf([][]rune{line}), 0)
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2: %v", len(runs), jsonHighlightWords(t, string(line)))
	}
	if runs[0].Style == runs[1].Style {
		t.Error("a key and a string value must not share a style — they are what tells an object apart")
	}
}

// A backslash escapes the next character, so an escaped quote does not end
// the string. Getting this wrong colors the rest of the document as string.
func TestJSONHighlighterHandlesEscapes(t *testing.T) {
	got := jsonHighlightWords(t, `{"path": "C:\\x", "quote": "a\"b", "n": 1}`)
	want := []string{`"path"`, `"C:\\x"`, `"quote"`, `"a\"b"`, `"n"`, "1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("runs = %v, want %v", got, want)
	}
}

// An unterminated string means a truncated or malformed value, which is
// exactly what someone opens the panel to look at — it must not hang or
// swallow the line silently.
func TestJSONHighlighterUnterminatedString(t *testing.T) {
	got := jsonHighlightWords(t, `{"a": "never closed`)
	want := []string{`"a"`, `"never closed`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("runs = %v, want %v", got, want)
	}
}

// "nullable"/"truest" are not the literals null/true — styling their prefix
// would mislabel an ordinary bare word as a JSON literal.
func TestJSONHighlighterRejectsLiteralPrefixes(t *testing.T) {
	for _, line := range []string{`{"a": nullable}`, `{"a": truest}`, `{"a": falsey}`} {
		if got := jsonHighlightWords(t, line); !reflect.DeepEqual(got, []string{`"a"`}) {
			t.Errorf("%s: runs = %v, want only the key", line, got)
		}
	}
}

func TestJSONHighlighterNumbers(t *testing.T) {
	got := jsonHighlightWords(t, `[-1, 2.5, 3e10, 4.2E-3]`)
	want := []string{"-1", "2.5", "3e10", "4.2E-3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("runs = %v, want %v", got, want)
	}
}

// Each line stands alone — no JSON token can span one (see JSONHighlighter),
// so a pretty-printed document highlights the same per line as it would
// alone, with no prior-line replay involved.
func TestJSONHighlighterIsPerLine(t *testing.T) {
	lines := [][]rune{
		[]rune("{"),
		[]rune(`  "a": 1,`),
		[]rune(`  "b": "two"`),
		[]rune("}"),
	}
	if got := jsonHighlightRuns(t, lines, 2); !reflect.DeepEqual(got, []string{`"b"`, `"two"`}) {
		t.Errorf("line 2 runs = %v, want the key and its value", got)
	}
	// The same line on its own must produce the same answer.
	if got := jsonHighlightWords(t, `  "b": "two"`); !reflect.DeepEqual(got, []string{`"b"`, `"two"`}) {
		t.Errorf("standalone runs = %v, want the same as in context", got)
	}
}

// Runs must stay inside the line and never overlap — Editor slices the line
// by them when it draws, so a bad bound is a panic in the draw path.
func TestJSONHighlighterRunsAreWellFormed(t *testing.T) {
	docs := []string{
		`{"a":1,"b":[true,null,-2.5],"c":{"d":"e"}}`,
		`[{"x":"\"quoted\""},{"y":"unterminated`,
		`not json at all`,
		`{{{[[[,,,:::}}}`,
		``,
		strings.Repeat(`{"k":"v"},`, 500),
	}
	hl := JSONHighlighter(&theme.Default)
	for _, d := range docs {
		line := []rune(d)
		prevEnd := 0
		for _, run := range hl(docOf([][]rune{line}), 0) {
			if run.Start < prevEnd {
				t.Errorf("%q: run at %d overlaps the previous one ending at %d", d, run.Start, prevEnd)
			}
			if run.Len <= 0 {
				t.Errorf("%q: run at %d has length %d", d, run.Start, run.Len)
			}
			if run.Start+run.Len > len(line) {
				t.Errorf("%q: run [%d,%d) runs past the line's %d runes", d, run.Start, run.Start+run.Len, len(line))
			}
			prevEnd = run.Start + run.Len
		}
	}
}
