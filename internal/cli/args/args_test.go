package args

import (
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/cli/spec"
)

// testTree has every shape the parser handles: depth-1 leaves, a group with
// depth-2 leaves, each option type, required/optional/repeating arguments and
// an environment variable.
func testTree() *spec.Tree {
	return &spec.Tree{
		Globals: []spec.Option{
			{Name: "output", Short: "o", Type: spec.TypeEnum, Enum: []string{"text", "json"}, Default: "text", Help: "h", Global: true},
			{Name: "quiet", Short: "q", Type: spec.TypeBool, Help: "h", Global: true},
			{Name: "no-color", Type: spec.TypeBool, Help: "h", Global: true},
			{Name: "help", Short: "h", Type: spec.TypeBool, Help: "h", Global: true},
		},
		Commands: []*spec.Command{
			{Path: []string{"version"}},
			{Path: []string{"help"}, Args: []spec.Arg{{Name: "group"}, {Name: "command"}}},
			{Path: []string{"object"}},
			{Path: []string{"object", "list"}, Options: []spec.Option{
				{Name: "type", Type: spec.TypeList, Help: "h"},
				{Name: "limit", Type: spec.TypeInt, Default: "50", Help: "h"},
				{Name: "database", Short: "d", Type: spec.TypeString, Env: "GOSSMS_DATABASE", Help: "h"},
				{Name: "system", Type: spec.TypeBool, Default: "true", Help: "h"},
			}},
			{Path: []string{"object", "show"}, Args: []spec.Arg{
				{Name: "name", Required: true},
				{Name: "more", Repeat: true},
			}, Options: []spec.Option{
				{Name: "file", Type: spec.TypeString, Help: "h"},
				{Name: "param", Type: spec.TypeRepeat, Help: "h"},
			}},
		},
	}
}

var files = map[string]string{
	"args.txt":  "\ufeffobject\r\nlist\r\n--type table, view\r\n\r\n-d  My Db \r\n--limit=5\n@nested.txt\n",
	"empty.txt": "",
}

func readFile(path string) ([]byte, error) {
	if s, ok := files[path]; ok {
		return []byte(s), nil
	}
	return nil, fs.ErrNotExist
}

func TestParse(t *testing.T) {
	env := map[string]string{"GOSSMS_DATABASE": "envdb"}
	opts := Options{ReadFile: readFile, Getenv: func(k string) string { return env[k] }}
	listDefaults := func(over map[string]any) map[string]any {
		m := map[string]any{"output": "text", "quiet": false, "no-color": false, "help": false,
			"type": []string(nil), "limit": 50, "database": "envdb", "system": true}
		for k, v := range over {
			m[k] = v
		}
		return m
	}
	globals := func(over map[string]any) map[string]any {
		m := map[string]any{"output": "text", "quiet": false, "no-color": false, "help": false}
		for k, v := range over {
			m[k] = v
		}
		return m
	}
	showDefaults := func(over map[string]any) map[string]any {
		m := globals(over)
		if _, ok := m["file"]; !ok {
			m["file"] = ""
		}
		if _, ok := m["param"]; !ok {
			m["param"] = []string(nil)
		}
		return m
	}

	tests := []struct {
		argv   []string
		path   string
		args   []string
		values map[string]any
	}{
		{[]string{"version"}, "version", nil, globals(nil)},
		{[]string{"version", "-o", "json"}, "version", nil, globals(map[string]any{"output": "json"})},
		{[]string{"version", "--output=json", "-q"}, "version", nil, globals(map[string]any{"output": "json", "quiet": true})},
		{[]string{"-o", "json", "version"}, "version", nil, globals(map[string]any{"output": "json"})},
		{[]string{"-o=json", "--quiet", "version"}, "version", nil, globals(map[string]any{"output": "json", "quiet": true})},
		// "--no-color" is an option's own name, not the negation of "color".
		{[]string{"version", "--no-color"}, "version", nil, globals(map[string]any{"no-color": true})},
		{[]string{"version", "--no-quiet"}, "version", nil, globals(nil)},
		{[]string{"help"}, "help", nil, globals(nil)},
		{[]string{"help", "object", "list"}, "help", []string{"object", "list"}, globals(nil)},
		// A group node alone parses; Main answers it.
		{[]string{"object"}, "object", nil, globals(nil)},
		{[]string{"object", "--help"}, "object", nil, globals(map[string]any{"help": true})},
		{[]string{"object", "list"}, "object list", nil, listDefaults(nil)},
		{[]string{"-q", "object", "-o", "json", "list"}, "object list", nil,
			listDefaults(map[string]any{"quiet": true, "output": "json"})},
		{[]string{"object", "list", "--type", "table,view", "--type", "proc"}, "object list", nil,
			listDefaults(map[string]any{"type": []string{"table", "view", "proc"}})},
		{[]string{"object", "list", "--type= table , view"}, "object list", nil,
			listDefaults(map[string]any{"type": []string{"table", "view"}})},
		{[]string{"object", "list", "--limit", "-1", "-d", "x"}, "object list", nil,
			listDefaults(map[string]any{"limit": -1, "database": "x"})},
		{[]string{"object", "list", "--no-system"}, "object list", nil, listDefaults(map[string]any{"system": false})},
		{[]string{"object", "show", "a"}, "object show", []string{"a"}, showDefaults(nil)},
		{[]string{"object", "show", "a", "b", "c"}, "object show", []string{"a", "b", "c"}, showDefaults(nil)},
		// A repeat option keeps each value whole: no comma split, no trim.
		{[]string{"object", "show", "a", "--param", "p=1,5:decimal(9,2)", "--param= x , y"}, "object show", []string{"a"},
			showDefaults(map[string]any{"param": []string{"p=1,5:decimal(9,2)", " x , y"}})},
		// "-" is a value and a positional, never an option.
		{[]string{"object", "show", "-", "--file", "-"}, "object show", []string{"-"}, showDefaults(map[string]any{"file": "-"})},
		// "--" ends the options.
		{[]string{"object", "show", "--", "--file", "-q"}, "object show", []string{"--file", "-q"}, showDefaults(nil)},
		{[]string{"object", "--", "show", "x"}, "object show", []string{"x"}, showDefaults(nil)},
		// A value may begin with a single dash; --x=--y spells a "--" value.
		{[]string{"object", "show", "a", "--file", "-x"}, "object show", []string{"a"}, showDefaults(map[string]any{"file": "-x"})},
		{[]string{"object", "show", "a", "--file=--x"}, "object show", []string{"a"}, showDefaults(map[string]any{"file": "--x"})},
		// --help skips the argument checks.
		{[]string{"object", "show", "--help"}, "object show", nil, showDefaults(map[string]any{"help": true})},
		{[]string{"object", "show", "-h"}, "object show", nil, showDefaults(map[string]any{"help": true})},
		// Response files: TestResponseFile; an empty one adds nothing.
		{[]string{"-o", "json", "@empty.txt", "version"}, "version", nil, globals(map[string]any{"output": "json"})},
		// After --, @file is positional.
		{[]string{"object", "show", "--", "@args.txt"}, "object show", []string{"@args.txt"}, showDefaults(nil)},
		// @ alone is not a response file.
		{[]string{"object", "show", "@"}, "object show", []string{"@"}, showDefaults(nil)},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			inv, err := Parse(testTree(), tc.argv, opts)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if got := strings.Join(inv.Command.Path, " "); got != tc.path {
				t.Errorf("command %q, want %q", got, tc.path)
			}
			if !reflect.DeepEqual(inv.Args, tc.args) {
				t.Errorf("args %q, want %q", inv.Args, tc.args)
			}
			if tc.values != nil && !reflect.DeepEqual(inv.Values, tc.values) {
				t.Errorf("values\n got %v\nwant %v", inv.Values, tc.values)
			}
		})
	}
}

// A value given as "--file @args.txt" stays a value: the file is not read.
func TestResponseFileNotExpandedAsValue(t *testing.T) {
	inv, err := Parse(testTree(), []string{"object", "show", "a", "--file", "@args.txt"}, Options{ReadFile: readFile})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Values["file"] != "@args.txt" {
		t.Errorf("file = %q", inv.Values["file"])
	}
}

func TestResponseFile(t *testing.T) {
	files["ok.txt"] = "object\nlist\n--type table, view\n\n-d  My Db \r\n--limit=5\r\n-q\n"
	defer delete(files, "ok.txt")
	inv, err := Parse(testTree(), []string{"@ok.txt", "-o", "json"}, Options{ReadFile: readFile})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"output": "json", "quiet": true, "no-color": false, "help": false,
		"type": []string{"table", "view"}, "limit": 5, "database": "My Db", "system": true}
	if strings.Join(inv.Command.Path, " ") != "object list" || !reflect.DeepEqual(inv.Values, want) {
		t.Errorf("got %v %v\nwant %v", inv.Command.Path, inv.Values, want)
	}
}

func TestParseErrors(t *testing.T) {
	opts := Options{ReadFile: readFile}
	tests := []struct {
		argv []string
		kind Kind
		msg  string // substring of Message
		hint string // substring of Hint
	}{
		{nil, KindNoCommand, "no command", "Commands: version, help, object"},
		{[]string{"--quiet"}, KindNoCommand, "no command", "gossms help"},
		// -p does not exist: before the command, an unknown option is assumed
		// to take a value, so "x" is not read as a command.
		{[]string{"-p", "x"}, KindNoCommand, "no command", ""},
		{[]string{"-o", "json"}, KindNoCommand, "no command", ""},
		{[]string{"nope"}, KindUnknownCommand, `unknown command "nope"`, "gossms help"},
		{[]string{"verison"}, KindUnknownCommand, `"verison"`, "Did you mean `gossms version`?"},
		{[]string{"object", "lsit"}, KindUnknownCommand, `"object lsit"`, "Did you mean `gossms object list`?"},
		{[]string{"object", "zzzzz"}, KindUnknownCommand, `"object zzzzz"`, "gossms help object"},
		// An unknown option followed by a command word does not swallow it.
		{[]string{"-p", "version"}, KindUnknownOption, "unknown option -p", "gossms help version"},
		{[]string{"-p", "x", "version"}, KindUnknownOption, "unknown option -p", ""},
		{[]string{"version", "--bogus"}, KindUnknownOption, "unknown option --bogus", "gossms help version"},
		{[]string{"version", "--qiuet"}, KindUnknownOption, "", "Did you mean --quiet?"},
		{[]string{"version", "-x"}, KindUnknownOption, "unknown option -x", ""},
		{[]string{"version", "--no-output"}, KindUnknownOption, "--no-output", ""},
		{[]string{"version", "--type", "t"}, KindUnknownOption, "--type", ""}, // another command's option
		{[]string{"version", "-o"}, KindMissingValue, "-o needs a value", "-o <enum>"},
		{[]string{"version", "-o", "--quiet"}, KindMissingValue, "-o needs a value", ""},
		{[]string{"object", "--type", "list"}, KindMissingValue, "--type needs a value", ""},
		{[]string{"version", "-o", "jsno"}, KindBadValue, `"jsno"`, "Did you mean json? One of: text, json."},
		{[]string{"version", "-o", "xml"}, KindBadValue, `"xml"`, "One of: text, json."},
		{[]string{"version", "--quiet=true"}, KindBadValue, "--quiet takes no value", ""},
		{[]string{"object", "list", "--limit", "ten"}, KindBadValue, `"ten" is not an integer`, ""},
		{[]string{"object", "list", "--type", "a,,b"}, KindBadValue, "empty item", ""},
		{[]string{"version", "-o", "json", "--output", "text"}, KindRepeated, "--output given more than once", ""},
		{[]string{"version", "-q", "--no-quiet"}, KindRepeated, "--quiet", ""},
		{[]string{"object", "show"}, KindMissingArg, "missing argument <name>", "gossms help object show"},
		{[]string{"version", "extra"}, KindExtraArg, `unexpected argument "extra"`, ""},
		{[]string{"help", "a", "b", "c"}, KindExtraArg, `"c"`, ""},
		{[]string{"@missing.txt"}, KindResponseFile, "response file missing.txt", ""},
		// The nested @nested.txt in args.txt stays literal: object list takes
		// no positional, so it is an extra argument rather than a file read.
		{[]string{"@args.txt"}, KindExtraArg, `"@nested.txt"`, ""},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			_, err := Parse(testTree(), tc.argv, opts)
			e, ok := errors.AsType[*Error](err)
			if !ok {
				t.Fatalf("err = %v, want *Error", err)
			}
			if e.Kind != tc.kind || !strings.Contains(e.Message, tc.msg) || !strings.Contains(e.Hint, tc.hint) {
				t.Errorf("got kind %d %q hint %q, want kind %d %q hint %q", e.Kind, e.Message, e.Hint, tc.kind, tc.msg, tc.hint)
			}
		})
	}
}

func TestResponseFileWithoutReader(t *testing.T) {
	_, err := Parse(testTree(), []string{"@args.txt"}, Options{})
	if e, ok := errors.AsType[*Error](err); !ok || e.Kind != KindResponseFile {
		t.Errorf("err = %v", err)
	}
}

func TestEnvironmentAndDefaults(t *testing.T) {
	tree := testTree()
	tree.Commands[3].Options[1].Env = "LIMIT" // --limit, default 50
	cases := []struct {
		env     map[string]string
		argv    []string
		limit   any
		wantErr string
	}{
		{nil, []string{"object", "list"}, 50, ""},
		{map[string]string{"LIMIT": "7"}, []string{"object", "list"}, 7, ""},
		{map[string]string{"LIMIT": "7"}, []string{"object", "list", "--limit", "3"}, 3, ""},
		{map[string]string{"LIMIT": "x"}, []string{"object", "list"}, nil, `LIMIT: "x" is not an integer`},
	}
	for _, c := range cases {
		inv, err := Parse(tree, c.argv, Options{Getenv: func(k string) string { return c.env[k] }})
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%v: err = %v, want %q", c.env, err, c.wantErr)
			}
			continue
		}
		if err != nil || inv.Values["limit"] != c.limit {
			t.Errorf("%v %v: limit = %v (%v), want %v", c.env, c.argv, inv.Values["limit"], err, c.limit)
		}
	}
}

func TestSuggest(t *testing.T) {
	cands := []string{"version", "help", "object"}
	for word, want := range map[string]string{
		"verison": "version", "VERSION": "version", "hepl": "help", "objekt": "object",
		"x": "", "zz": "", "nope": "", "versionxyz": "",
	} {
		if got := Suggest(word, cands); got != want {
			t.Errorf("Suggest(%q) = %q, want %q", word, got, want)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"version -o json", "-p x", "object list --type a,b", "object show -- @x", "@args.txt -q", "--=x", "-=", "object --type"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		inv, err := Parse(testTree(), strings.Fields(line), Options{ReadFile: readFile})
		if (inv == nil) == (err == nil) {
			t.Fatalf("inv %v, err %v", inv, err)
		}
	})
}
