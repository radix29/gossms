package format

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/*/*.out from the current formatters")

// TestGolden formats every testdata/<lang>/*.in in each of the language's
// styles at the default indent and compares with the matching .out — <name>.out
// for the default style, <name>.<style ID>.out for any other. Run with
// -update to regenerate the .out files, then read the diff.
func TestGolden(t *testing.T) {
	for _, l := range []Language{LangSQL, LangXML, LangJSON} {
		dir := filepath.Join("testdata", strings.ToLower(l.String()))
		ins, _ := filepath.Glob(filepath.Join(dir, "*.in"))
		if len(ins) == 0 {
			t.Fatalf("no golden inputs in %s", dir)
		}
		for k, st := range Styles(l) {
			for _, in := range ins {
				outPath := strings.TrimSuffix(in, ".in") + ".out"
				if k > 0 {
					outPath = strings.TrimSuffix(in, ".in") + "." + st.ID + ".out"
				}
				t.Run(filepath.Base(outPath), func(t *testing.T) { golden(t, l, st.ID, in, outPath) })
			}
		}
	}
}

func golden(t *testing.T, l Language, styleID, in, outPath string) {
	src, err := os.ReadFile(in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Format(l, styleID, string(src), Options{})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if *update {
		if err := os.WriteFile(outPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	again, err := Format(l, styleID, got, Options{})
	if err != nil || again != got {
		t.Errorf("not idempotent (err %v):\n%s", err, again)
	}
}

func TestFormatJSONIndentWidth(t *testing.T) {
	got, err := Format(LangJSON, "", `{"a":[1]}`, Options{IndentWidth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"a\": [\n    1\n  ]\n}"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// esc spells a JSON \u escape as %u, so the tests' raw strings stay
// readable without Go interpreting the backslash.
func esc(s string) string { return strings.ReplaceAll(s, "%u", "\x5cu") }

// Tokens json.Indent copies verbatim: anything decoding them would change.
func TestFormatJSONKeepsTokenSpelling(t *testing.T) {
	keep := []string{
		`1.0`, `1e5`, `-0.5E-3`, `12345678901234567890123`, `-0`,
		esc(`"%u00e9"`), esc(`"%uD83D%uDE00"`), `"\/"`,
		`"<&>"`, `"` + string(rune(0x2028)) + `"`,
		`"zeta"`, `"alpha"`, // key order: zeta stays before alpha
	}
	src := esc(`{"zeta":[1.0,1e5,-0.5E-3,12345678901234567890123,-0],"alpha":["%u00e9","%uD83D%uDE00","\/","<&>","` + string(rune(0x2028)) + `"]}`)
	got, err := Format(LangJSON, "", src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keep {
		if !strings.Contains(got, k) {
			t.Errorf("output lost %s:\n%s", k, got)
		}
	}
	if strings.Index(got, `"zeta"`) > strings.Index(got, `"alpha"`) {
		t.Errorf("keys reordered:\n%s", got)
	}
}

func TestFormatJSONTrailingNewline(t *testing.T) {
	for src, want := range map[string]string{
		`[]`:              `[]`,
		"[]\n":            "[]\n",
		"[]\n\n \t\n":     "[]\n",
		"[]  ":            "[]",
		"\n\n  {\"a\":1}": "{\n    \"a\": 1\n}",
	} {
		got, err := Format(LangJSON, "", src, Options{})
		if err != nil || got != want {
			t.Errorf("Format(%q) = %q, %v; want %q", src, got, err, want)
		}
	}
}

func TestFormatJSONSyntaxErrorPosition(t *testing.T) {
	for _, tc := range []struct {
		src       string
		line, col int
	}{
		{"{\n  \"a\": 1,\n  \"b\" 2\n}", 3, 7}, // the 2 where ':' belongs
		{`[1,2,]`, 1, 6},                       // the ]
		{"{\"é😀\": x}", 1, 8},                  // columns count runes, not bytes
		{"[1,\n2", 2, 2},                       // end of input: just past the 2
		{"{\"a\":1}\n{\"b\":2}\n", 2, 1},       // a second top-level value
		{"\n\n   ", 1, 1},                      // nothing but whitespace
		{"[\"unterminated\n", 1, 15},           // raw newline inside a string
		{strings.Repeat("[", 10001), 1, 10001}, // json's nesting limit
	} {
		_, err := Format(LangJSON, "", tc.src, Options{})
		se, ok := errors.AsType[*SyntaxError](err)
		if !ok {
			t.Errorf("Format(%.20q): err %v, want *SyntaxError", tc.src, err)
			continue
		}
		if se.Lang != LangJSON || se.Line != tc.line || se.Col != tc.col {
			t.Errorf("Format(%.20q): %v; want line %d col %d", tc.src, se, tc.line, tc.col)
		}
	}
}

func TestSyntaxErrorMessage(t *testing.T) {
	err := &SyntaxError{Lang: LangJSON, Line: 12, Col: 4, Msg: "invalid character 'x'"}
	if got, want := err.Error(), "JSON line 12 col 4: invalid character 'x'"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStyleRegistry(t *testing.T) {
	if s := Styles(LangJSON); len(s) != 1 || s[0].ID != "standard" || s[0].Name != "Standard" {
		t.Errorf("Styles(JSON) = %+v", s)
	}
	for _, id := range []string{"", "standard", "no-such-style"} {
		if st, ok := StyleByID(LangJSON, id); !ok || st.ID != "standard" {
			t.Errorf("StyleByID(JSON, %q) = %+v, %v; want the default", id, st, ok)
		}
	}
	// Styles hands out a copy: a caller can't edit the registry.
	Styles(LangJSON)[0].ID = "mutated"
	if Styles(LangJSON)[0].ID != "standard" {
		t.Error("Styles exposed the registry")
	}
	if _, ok := StyleByID(LangText, ""); ok {
		t.Error("StyleByID(Text) found a style; plain text has no formatter")
	}
	if _, err := Format(LangText, "", "x", Options{}); !errors.Is(err, ErrNoFormatter) {
		t.Errorf("Format(Text) err %v, want ErrNoFormatter", err)
	}
	for _, l := range []Language{LangXML, LangJSON} {
		if s := Styles(l); len(s) != 1 || s[0].ID != "standard" {
			t.Errorf("Styles(%s) = %+v", l, s)
		}
	}
	// SQL: Standard stays the default, so an empty or stale config value
	// formats as it always has.
	if s := Styles(LangSQL); len(s) != 2 || s[0].ID != "standard" || s[1].ID != "lowercase" || s[1].Name != "Lowercase" {
		t.Errorf("Styles(SQL) = %+v", s)
	}
	for id, want := range map[string]string{"": "standard", "no-such-style": "standard", "lowercase": "lowercase"} {
		if st, ok := StyleByID(LangSQL, id); !ok || st.ID != want {
			t.Errorf("StyleByID(SQL, %q) = %+v, %v; want %s", id, st, ok, want)
		}
	}
}

// The guard refuses output that differs from the input in more than
// whitespace, whatever the formatter claims.
func TestGuardRefusesChangedOutput(t *testing.T) {
	for name, fn := range map[string]func(string, Options) (string, error){
		"drops a token":      func(string, Options) (string, error) { return `{"a":1}`, nil },
		"edits a string":     func(string, Options) (string, error) { return `{"a":1,"b":"x y"}`, nil },
		"reorders keys":      func(string, Options) (string, error) { return `{"b":"xy","a":1}`, nil },
		"rewrites a number":  func(string, Options) (string, error) { return `{"a":1.0,"b":"xy"}`, nil },
		"unescapes a string": func(string, Options) (string, error) { return esc(`{"a":1,"b":"%u0078y"}`), nil },
	} {
		st := Style{ID: "broken", fn: plain(fn)}
		out, err := run(LangJSON, st, `{"a":1,"b":"xy"}`, Options{})
		if !errors.Is(err, ErrGuard) || out != "" {
			t.Errorf("%s: got %q, %v; want ErrGuard and no output", name, out, err)
		}
	}
	ok := Style{ID: "spaces", fn: plain(func(string, Options) (string, error) { return "{ \"a\" :\n\t1 , \"b\":\"xy\" }", nil })}
	if _, err := run(LangJSON, ok, `{"a":1,"b":"xy"}`, Options{}); err != nil {
		t.Errorf("whitespace-only change refused: %v", err)
	}
	// Whitespace inside a string is a token, not layout.
	if err := verify(LangJSON, `["a b"]`, `["ab"]`); !errors.Is(err, ErrGuard) {
		t.Errorf("verify missed a space removed from a string: %v", err)
	}
	if err := verify(LangJSON, `["a\"b", 1]`, "[\n\"a\\\"b\",\n1]"); err != nil {
		t.Errorf("verify tripped on an escaped quote: %v", err)
	}
}

// A language with a formatter but no verify case fails closed.
func TestGuardFailsClosedWithoutACheck(t *testing.T) {
	if err := verify(LangText, "x", "x"); !errors.Is(err, ErrGuard) {
		t.Errorf("verify(Text) = %v, want ErrGuard: Text has no check", err)
	}
}

func TestPosition(t *testing.T) {
	src := "ab\ncé😀d"
	for _, tc := range []struct{ off, line, col int }{
		{0, 1, 1}, {2, 1, 3}, {3, 2, 1}, {4, 2, 2}, {6, 2, 3}, {10, 2, 4}, {len(src), 2, 5}, {99, 2, 5}, {-1, 1, 1},
	} {
		if l, c := position(src, tc.off); l != tc.line || c != tc.col {
			t.Errorf("position(%d) = %d,%d; want %d,%d", tc.off, l, c, tc.line, tc.col)
		}
	}
}

// FuzzFormatJSON checks Format against encoding/json as an independent
// oracle: output exists exactly when the input is valid, compacts to the same
// bytes as the input, and formats to itself.
func FuzzFormatJSON(f *testing.F) {
	ins, _ := filepath.Glob(filepath.Join("testdata", "json", "*.in"))
	for _, in := range ins {
		if b, err := os.ReadFile(in); err == nil {
			f.Add(string(b))
		}
	}
	f.Add(`{"a":[1,{"b":"c\"d"}],"e":null}`)
	f.Add(`[1,2,]`)
	f.Fuzz(func(t *testing.T, src string) {
		got, err := Format(LangJSON, "", src, Options{IndentWidth: 2})
		if valid := json.Valid([]byte(src)); valid != (err == nil) {
			t.Fatalf("json.Valid %v but Format err %v", valid, err)
		}
		if err != nil {
			if errors.Is(err, ErrGuard) {
				t.Fatalf("guard tripped on valid formatter output: %v", err)
			}
			return
		}
		var a, b bytes.Buffer
		if json.Compact(&a, []byte(src)) != nil || json.Compact(&b, []byte(got)) != nil || a.String() != b.String() {
			t.Fatalf("tokens changed:\n%q\n%q", src, got)
		}
		if again, err := Format(LangJSON, "", got, Options{IndentWidth: 2}); err != nil || again != got {
			t.Fatalf("not idempotent: %q -> %q (%v)", got, again, err)
		}
	})
}
