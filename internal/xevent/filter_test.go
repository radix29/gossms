package xevent

import (
	"strconv"
	"strings"
	"testing"
)

// TestFoldedComparisonsMatchToLower pins containsLower, cutLowerPrefix and
// compareLower to the strings.ToLower forms they replaced, over the cases
// where lowering is not byte-for-byte: multi-byte letters, a rune whose
// lowercase is shorter or longer in UTF-8 (the Kelvin sign, dotted I),
// invalid bytes, and empty strings.
func TestFoldedComparisonsMatchToLower(t *testing.T) {
	corpus := []string{
		"", "a", "A", "abc", "ABC", "xAbCx", "PAGEIOLATCH_SH", "pageiolatch",
		"Ünïcödé", "ÜNÏCÖDÉ", "K", "k", "K", "İstanbul", "istanbul",
		"ß", "ẞ", "\xff", "a\xffb", "\xffB", "你好", "你", "Ab你好Cd",
	}
	for _, s := range corpus {
		for _, b := range corpus {
			lb := strings.ToLower(b)
			ls := strings.ToLower(s)
			if got, want := containsLower(s, lb), strings.Contains(ls, lb); got != want {
				t.Errorf("containsLower(%q, %q) = %v, want %v", s, lb, got, want)
			}
			if _, got := cutLowerPrefix(s, lb); got != strings.HasPrefix(ls, lb) {
				t.Errorf("cutLowerPrefix(%q, %q) = %v, want %v", s, lb, got, !got)
			}
			if got, want := compareLower(s, lb), strings.Compare(ls, lb); got != want {
				t.Errorf("compareLower(%q, %q) = %d, want %d", s, lb, got, want)
			}
		}
	}
}

// TestFilterMatchDoesNotAllocate pins T56's XEvent half: a filter runs over
// every value of every event in the store on each edit, and lowering each
// value into a copy made that an allocation per value per event.
func TestFilterMatchDoesNotAllocate(t *testing.T) {
	e := ev("sql_batch_completed", 1, "duration", "1500", "batch_text", "SELECT * FROM dbo.Orders WHERE Id = 7")
	e.Actions = []Value{{Name: "database_name", Value: "AppDB"}}
	for _, text := range []string{
		"no such text",
		"batch_text contains 'orders' and database_name = 'appdb'",
		"batch_text ~ 'nothing' or name > 'z'",
	} {
		f, err := ParseFilter(text)
		if err != nil {
			t.Fatalf("ParseFilter(%q): %v", text, err)
		}
		if n := testing.AllocsPerRun(100, func() { f.Match(&e) }); n != 0 {
			t.Errorf("Match with %q allocated %v times per event, want 0", text, n)
		}
	}
}

// TestFilterLoneBang pins M1: a '!' that opens neither != nor !~ is a word
// character. It made an empty word the tokenizer never advanced past, so
// ParseFilter("hello!") never returned and the filter prompt hung the UI.
func TestFilterLoneBang(t *testing.T) {
	e := ev("error_reported", 1, "message", "hello! x ! 5", "a!b", "1")
	for _, c := range []struct {
		expr     string
		freeText bool
	}{
		{"hello!", true},
		{"error!", true},
		{"x ! 5", true},
		{"!", true},
		{"message != 'nope'", false},
		{"message !~ nope", false},
		{"message~hello!", false},
		{"a!b = 1", false},
		{"message contains 'hello!'", false},
	} {
		f, err := ParseFilter(c.expr)
		if err != nil {
			t.Errorf("%q: %v", c.expr, err)
			continue
		}
		if f.IsFreeText() != c.freeText {
			t.Errorf("%q: free text %v, want %v", c.expr, f.IsFreeText(), c.freeText)
		}
		if c.expr != "error!" && c.expr != "!" && !f.Match(&e) {
			t.Errorf("%q does not match its event", c.expr)
		}
	}
	// != and !~ still split a word they touch.
	f, err := ParseFilter("message!=nope")
	if err != nil || f.IsFreeText() || !f.Match(&e) {
		t.Errorf("message!=nope: %v, %v", f, err)
	}
}

// TestParseNumberMatchesParseFloat pins parseNumber's early refusal to text
// ParseFloat would refuse anyway: every float spelling it accepts still parses.
func TestParseNumberMatchesParseFloat(t *testing.T) {
	for _, s := range []string{
		"", " ", "0", "42", " 42 ", "-1.5", "+.5", ".5", "1e9", "0x1p-2", "1_000",
		"Inf", "-INF", "+infinity", "NaN", "nan", "info", "name", "abc", "-", "+-1", "--1", "1abc", "K",
	} {
		_, want := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if _, got := parseNumber(s); got != (want == nil) {
			t.Errorf("parseNumber(%q) ok = %v, want %v", s, got, want == nil)
		}
	}
}
