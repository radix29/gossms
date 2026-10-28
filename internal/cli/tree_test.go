package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/spec"
)

// TestCommandTreeWellFormed checks the shape rules spec documents but its
// types cannot enforce. TestCommandMentionsParse is the drift guard on the
// text around them.
func TestCommandTreeWellFormed(t *testing.T) {
	tree := commandTree()
	checkOption := func(where string, o spec.Option) {
		if o.Name == "" || strings.HasPrefix(o.Name, "-") {
			t.Errorf("%s: option name %q must be non-empty, without dashes", where, o.Name)
		}
		if len([]rune(o.Short)) > 1 {
			t.Errorf("%s --%s: short name %q is more than one letter", where, o.Name, o.Short)
		}
		if o.Help == "" {
			t.Errorf("%s --%s: no help", where, o.Name)
		}
		if (o.Type == spec.TypeEnum) != (len(o.Enum) > 0) {
			t.Errorf("%s --%s: Enum must be set exactly when Type is enum", where, o.Name)
		}
		if o.Type == spec.TypeEnum && o.Default != "" && !slices.Contains(o.Enum, o.Default) {
			t.Errorf("%s --%s: default %q is not in %v", where, o.Name, o.Default, o.Enum)
		}
	}
	// Long and short names are unique across a command's options and the
	// globals together, since the parser looks them up in that union.
	checkUnique := func(where string, opts []spec.Option) {
		long, short := map[string]bool{}, map[string]bool{}
		for _, o := range opts {
			if long[o.Name] {
				t.Errorf("%s: --%s defined twice", where, o.Name)
			}
			long[o.Name] = true
			if o.Short != "" {
				if short[o.Short] {
					t.Errorf("%s: -%s defined twice", where, o.Short)
				}
				short[o.Short] = true
			}
		}
	}

	for _, o := range tree.Globals {
		checkOption("global", o)
		if !o.Global {
			t.Errorf("global --%s: Global is not set", o.Name)
		}
	}
	checkUnique("globals", tree.Globals)

	paths := map[string]bool{}
	for _, c := range tree.Commands {
		where := strings.Join(c.Path, " ")
		if len(c.Path) < 1 || len(c.Path) > 2 {
			t.Errorf("%q: path depth %d, want 1 or 2", where, len(c.Path))
		}
		if paths[where] {
			t.Errorf("%q: defined twice", where)
		}
		paths[where] = true
		if c.Summary == "" || strings.HasSuffix(c.Summary, ".") {
			t.Errorf("%q: summary %q must be non-empty, with no trailing period", where, c.Summary)
		}
		switch c.Class {
		case spec.ClassRead, spec.ClassWrite, spec.ClassDDL, spec.ClassAdmin:
		default:
			t.Errorf("%q: class %q", where, c.Class)
		}
		for i, a := range c.Args {
			if a.Name == "" || a.Help == "" {
				t.Errorf("%q: argument %d needs a name and help", where, i)
			}
			if a.Repeat && i != len(c.Args)-1 {
				t.Errorf("%q <%s>: only the last argument may repeat", where, a.Name)
			}
			if a.Required && i > 0 && !c.Args[i-1].Required {
				t.Errorf("%q <%s>: a required argument follows an optional one", where, a.Name)
			}
		}
		for _, o := range c.Options {
			checkOption(where, o)
			if o.Global {
				t.Errorf("%q --%s: a command's own option has Global set", where, o.Name)
			}
		}
		checkUnique(where, append(slices.Clone(tree.Globals), c.Options...))
		for _, ex := range c.Examples {
			if !strings.HasPrefix(ex, "gossms "+where) {
				t.Errorf("%q: example %q does not run this command", where, ex)
			}
		}
	}
	// Every depth-2 command sits under a group node.
	for _, c := range tree.Commands {
		if len(c.Path) == 2 && !paths[c.Path[0]] {
			t.Errorf("%q: no group node %q", strings.Join(c.Path, " "), c.Path[0])
		}
	}
}

// mentionRE finds a command line quoted in text: `gossms …` in backticks.
var mentionRE = regexp.MustCompile("`(gossms(?: [^`]*)?)`")

// checkMentions fails for every `gossms …` in text that names a command the
// tree lacks (docs/cli.md § Help). A mention with a placeholder
// (<command>, [x], %s, ...) is checked up to it: `gossms help <command>` needs
// `help`; one without must parse whole, and `gossms help <topic>` must name a
// command.
func checkMentions(t *testing.T, tree *spec.Tree, where, text string) {
	t.Helper()
	for _, m := range mentionRE.FindAllStringSubmatch(text, -1) {
		if err := checkCommandLine(tree, m[1]); err != "" {
			t.Errorf("%s: %q %s", where, m[1], err)
		}
	}
}

// checkCommandLine is checkMentions for one line starting "gossms"; it
// returns why the line is wrong, or "".
func checkCommandLine(tree *spec.Tree, line string) string {
	words := strings.Fields(line)[1:]
	if len(words) > 0 && slices.Contains([]string{"--version", "-version", "-v"}, words[0]) {
		return "" // answered by cmd/gossms before the command line
	}
	placeholder := slices.IndexFunc(words, func(w string) bool {
		return strings.ContainsAny(w, "<>[]%") || strings.Contains(w, "...") || strings.Contains(w, "…")
	})
	if placeholder >= 0 {
		var path []string
		for _, w := range words[:placeholder] {
			if strings.HasPrefix(w, "-") {
				break
			}
			path = append(path, w)
		}
		// `gossms help <group> <command>` names the group as a help topic.
		if len(path) > 1 && slices.Equal(path[:1], helpCommand(tree).Path) {
			path = path[1:]
		}
		if len(path) > 0 && tree.Lookup(path) == nil {
			return "names a command the tree lacks"
		}
		return ""
	}
	inv, err := args.Parse(tree, words, args.Options{})
	if err != nil {
		return "does not parse: " + err.Error()
	}
	if inv.Command == helpCommand(tree) && len(inv.Args) > 0 && tree.Lookup(inv.Args) == nil {
		return "names a help topic the tree lacks"
	}
	return ""
}

// TestCommandMentionsParse is phase 0 D1's drift guard: every example parses,
// and every `gossms …` named in the tree's text, in the rendered help, or in a
// string literal of the command line's source (hints, usage, the TTY refusal
// in cmd/gossms) names a command that exists. A pointer to a command that
// answers "unknown command" is the worst first contact; when `agent guide`
// lands, the wording has to change with it.
func TestCommandMentionsParse(t *testing.T) {
	tree := commandTree()
	for _, c := range tree.Commands {
		where := c.Name()
		for _, ex := range c.Examples {
			if err := checkCommandLine(tree, ex); err != "" {
				t.Errorf("%s: example %q %s", where, ex, err)
			}
			if strings.ContainsAny(ex, "<>[]%") {
				t.Errorf("%s: example %q has a placeholder; examples are runnable lines", where, ex)
			}
		}
		texts := []string{c.Summary, c.Description}
		for _, a := range c.Args {
			texts = append(texts, a.Help)
		}
		for _, o := range c.Options {
			texts = append(texts, o.Help)
		}
		checkMentions(t, tree, where, strings.Join(texts, "\n"))
	}
	for _, o := range tree.Globals {
		checkMentions(t, tree, "global --"+o.Name, o.Help)
	}

	// The rendered help, for every topic.
	helpArgv := [][]string{{"--help"}}
	for _, c := range tree.Commands {
		helpArgv = append(helpArgv, append([]string{"help"}, c.Path...))
	}
	for _, argv := range helpArgv {
		r := runMain(t, argv...)
		if r.exit != 0 {
			t.Errorf("%v: exit %d", argv, r.exit)
		}
		checkMentions(t, tree, strings.Join(argv, " "), r.stdout)
	}

	// String literals in the source: the fixed hints and messages. Hints
	// built at run time are checked where tests produce them (TestUsageErrors).
	files, _ := filepath.Glob("*.go")
	for _, dir := range []string{"args", "output", "spec"} {
		more, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		files = append(files, more...)
	}
	files = append(files, filepath.Join("..", "..", "cmd", "gossms", "main.go"))
	fset := token.NewFileSet()
	literals := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("%s: %v", fset.Position(lit.Pos()), err)
			}
			if mentionRE.MatchString(s) {
				literals++
			}
			checkMentions(t, tree, fset.Position(lit.Pos()).String(), s)
			return true
		})
	}
	if literals == 0 {
		t.Error("no `gossms …` literal found in the source: the scan is not reading it")
	}
}

// TestCheckMentions pins the checker itself, so a broken checker cannot pass
// every mention.
func TestCheckMentions(t *testing.T) {
	tree := commandTree()
	for line, ok := range map[string]bool{
		"gossms help":                   true,
		"gossms help version":           true,
		"gossms version -o json":        true,
		"gossms --version":              true,
		"gossms help <command>":         true,
		"gossms help %s":                true,
		"gossms <command> --help":       true,
		"gossms agent guide":            false,
		"gossms help agent":             false,
		"gossms agent <topic>":          false,
		"gossms version --bogus":        false,
		"gossms -p dev":                 false,
		"gossms version -o parquet":     false,
		"gossms help profile <command>": true,
		"gossms help agent <command>":   false,
		"gossms profile test dev":       true,
		"gossms profile show":           false, // <name> is required
	} {
		if got := checkCommandLine(tree, line) == ""; got != ok {
			t.Errorf("checkCommandLine(%q) ok = %v, want %v", line, got, ok)
		}
	}
}
