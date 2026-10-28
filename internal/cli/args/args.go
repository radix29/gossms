// Package args parses a command line against the spec tree: it finds the
// command, then reads its options and positional arguments into a typed
// spec.Invocation.
//
// The grammar (docs/cli.md § Syntax):
//
//   - The command is the deepest tree path the positional words spell; global
//     options may come before it.
//   - --name value and --name=value are the same; a short alias is -x value or
//     -x=value. A bool is --name, or --no-name for false, and takes no value.
//   - A list option may repeat and/or take comma-separated items.
//   - "-" is a value or positional token (stdin), never an option; "--" ends
//     the options, so every later token is positional.
//   - @file is replaced by the file's lines, one option per line (§ Response
//     files). No recursion.
//   - Every error is a usage error and carries a did-you-mean where one is
//     close enough.
//
// The package does no I/O: response files are read through Options.ReadFile
// and environment variables through Options.Getenv.
package args

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/radix29/gossms/internal/cli/spec"
)

// HelpOption is the global bool that asks for a command's help. A command line
// carrying it skips the argument-count checks, so `gossms <cmd> --help` works
// without the command's required arguments.
const HelpOption = "help"

// Options is what Parse reads from the outside world.
type Options struct {
	// ReadFile reads an @file response file, path relative to the working
	// directory. Nil refuses response files.
	ReadFile func(path string) ([]byte, error)
	// Getenv reads an option's environment variable (Option.Env). Nil reads
	// none.
	Getenv func(string) string
}

// Kind classifies a parse Error. Main maps every kind to USAGE_ERROR (exit 2);
// the kinds are for tests and for Main's choice of help.
type Kind int

const (
	KindNoCommand      Kind = iota // no positional word at all: gossms -p x
	KindUnknownCommand             // a word that is not a command (or not one under its group)
	KindUnknownOption              // --x / -x not defined for the command or globally
	KindMissingValue               // an option that takes a value without one
	KindBadValue                   // a value the option's type rejects, or a value on a bool
	KindRepeated                   // a non-list, non-repeat option given twice
	KindMissingArg                 // a required positional argument is absent
	KindExtraArg                   // more positional arguments than the command takes
	KindResponseFile               // an @file that cannot be read
)

// Error is a usage error. Command is the resolved command when parsing got
// that far (its help is the useful pointer), nil otherwise.
type Error struct {
	Kind    Kind
	Command *spec.Command
	Message string
	// Hint is the did-you-mean or the next thing to try; empty when there is
	// nothing better than the command's help.
	Hint string
}

func (e *Error) Error() string { return e.Message }

// Parse parses argv (os.Args[1:]) against tree.
//
// A group node on its own parses successfully — Invocation.Command is the
// group — so Main can answer `gossms <group>` and `gossms <group> --help`.
func Parse(tree *spec.Tree, argv []string, opts Options) (*spec.Invocation, error) {
	tokens, err := expandResponseFiles(tree, argv, opts.ReadFile)
	if err != nil {
		return nil, err
	}
	cmd, pathIdx, err := findCommand(tree, tokens)
	if err != nil {
		return nil, err
	}
	p := &parser{tree: tree, cmd: cmd, opts: opts, given: map[string]bool{}, values: map[string]any{}}
	positional, err := p.parseOptions(tokens, pathIdx)
	if err != nil {
		return nil, err
	}
	if err := p.applyDefaults(); err != nil {
		return nil, err
	}
	if help, _ := p.values[HelpOption].(bool); !help {
		if err := checkArgs(cmd, positional); err != nil {
			return nil, err
		}
	}
	return &spec.Invocation{Command: cmd, Args: positional, Values: p.values, Given: p.given}, nil
}

// --- Response files ---------------------------------------------------------

// expandResponseFiles replaces each @file token with the file's lines. A token
// is a response file only where an option or positional may start: not after
// "--", and not as the value of the token before it (--sql @x is a value), so
// an option taking a value is recognized by name across the whole tree — the
// command is not known yet.
//
// One option per line: a line starting with "-" is split at its first run of
// spaces into the option and its value, unless it spells --x=value; any other
// line is one token, spaces and all. That is the point of a response file: no
// shell quoting. Blank lines are skipped, a UTF-8 BOM and \r\n are tolerated,
// and an @token inside the file stays literal (no recursion).
func expandResponseFiles(tree *spec.Tree, argv []string, readFile func(string) ([]byte, error)) ([]string, error) {
	if !slices.ContainsFunc(argv, isResponseFile) {
		return argv, nil
	}
	valued := valueOptionNames(tree)
	var out []string
	for i, tok := range argv {
		if tok == "--" {
			return append(out, argv[i:]...), nil
		}
		if !isResponseFile(tok) || (i > 0 && valued[argv[i-1]]) {
			out = append(out, tok)
			continue
		}
		path := tok[1:]
		if readFile == nil {
			return nil, &Error{Kind: KindResponseFile, Message: fmt.Sprintf("response file %s: not available here", path)}
		}
		data, err := readFile(path)
		if err != nil {
			return nil, &Error{Kind: KindResponseFile, Message: fmt.Sprintf("response file %s: %v", path, err)}
		}
		out = append(out, responseFileTokens(string(data))...)
	}
	return out, nil
}

func isResponseFile(tok string) bool { return len(tok) > 1 && tok[0] == '@' }

func responseFileTokens(text string) []string {
	text = strings.TrimPrefix(text, "\ufeff")
	var out []string
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line[0] == '-' {
			if sp := strings.IndexAny(line, " \t"); sp > 0 && !strings.Contains(line[:sp], "=") {
				out = append(out, line[:sp], strings.TrimSpace(line[sp:]))
				continue
			}
		}
		out = append(out, line)
	}
	return out
}

// valueOptionNames is every spelling ("--name", "-x") of an option that takes
// a value, anywhere in the tree.
func valueOptionNames(tree *spec.Tree) map[string]bool {
	names := map[string]bool{}
	add := func(o spec.Option) {
		if o.Type == spec.TypeBool {
			return
		}
		names["--"+o.Name] = true
		if o.Short != "" {
			names["-"+o.Short] = true
		}
	}
	for _, o := range tree.Globals {
		add(o)
	}
	for _, c := range tree.Commands {
		for _, o := range c.Options {
			add(o)
		}
	}
	return names
}

// --- Finding the command ----------------------------------------------------

// findCommand walks the positional words to the deepest command they spell
// and returns it with the token indexes of those words.
//
// Before the command is known only the globals are; an option that is not one
// is assumed to take the next token as its value, unless that token names a
// command at the current level. That is what makes `gossms -p x` "no command"
// in every phase, whether or not -p exists yet. Options are not validated
// here — parseOptions does that once the command is known.
func findCommand(tree *spec.Tree, tokens []string) (*spec.Command, []int, error) {
	var (
		cmd     *spec.Command
		pathIdx []int
		path    []string
	)
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "--" {
			// Positional from here on; a command may still be spelled after it.
			for j := i + 1; j < len(tokens); j++ {
				next := tree.Lookup(append(slices.Clone(path), tokens[j]))
				if next == nil {
					break
				}
				cmd, path, pathIdx = next, next.Path, append(pathIdx, j)
				if !hasChildren(tree, path) {
					break
				}
			}
			break
		}
		if isOption(tok) {
			name, _, inline := strings.Cut(tok, "=")
			if o, ok := findGlobal(tree, name); ok {
				if o.Type != spec.TypeBool && !inline {
					i++
				}
			} else if !inline && i+1 < len(tokens) && !isOption(tokens[i+1]) &&
				tokens[i+1] != "--" && tree.Lookup(append(slices.Clone(path), tokens[i+1])) == nil {
				i++
			}
			continue
		}
		next := tree.Lookup(append(slices.Clone(path), tok))
		if next == nil {
			if cmd == nil || hasChildren(tree, path) {
				return nil, nil, unknownCommand(tree, path, tok)
			}
			break // the leaf's first positional argument
		}
		cmd, path, pathIdx = next, next.Path, append(pathIdx, i)
		if !hasChildren(tree, path) {
			break
		}
	}
	if cmd == nil {
		return nil, nil, &Error{Kind: KindNoCommand, Message: "no command given",
			Hint: "Commands: " + strings.Join(childNames(tree, nil), ", ") + ". Run `gossms help` for usage."}
	}
	return cmd, pathIdx, nil
}

func unknownCommand(tree *spec.Tree, parent []string, word string) error {
	full := strings.Join(append(slices.Clone(parent), word), " ")
	e := &Error{Kind: KindUnknownCommand, Message: fmt.Sprintf("unknown command %q", full)}
	if s := Suggest(word, childNames(tree, parent)); s != "" {
		e.Hint = fmt.Sprintf("Did you mean `gossms %s`?", strings.Join(append(slices.Clone(parent), s), " "))
	} else if len(parent) > 0 {
		e.Hint = fmt.Sprintf("Run `gossms help %s` for its commands.", strings.Join(parent, " "))
	} else {
		e.Hint = "Run `gossms help` for the command list."
	}
	if len(parent) > 0 {
		e.Command = tree.Lookup(parent)
	}
	return e
}

func hasChildren(tree *spec.Tree, path []string) bool { return len(tree.Children(path)) > 0 }

// childNames is the last word of every command one level below path, in tree
// order.
func childNames(tree *spec.Tree, path []string) []string {
	var names []string
	for _, c := range tree.Children(path) {
		names = append(names, c.Path[len(path)])
	}
	return names
}

func findGlobal(tree *spec.Tree, name string) (spec.Option, bool) {
	return findOption(tree.Globals, name)
}

// isOption reports whether tok is spelled as an option. "-" alone is a value
// (stdin), "--" the end of options.
func isOption(tok string) bool { return len(tok) > 1 && tok[0] == '-' && tok != "--" }

// --- Options and positionals ------------------------------------------------

type parser struct {
	tree   *spec.Tree
	cmd    *spec.Command
	opts   Options
	given  map[string]bool // long names set on the command line or by their environment variable
	values map[string]any
}

// parseOptions reads every token except the command's own words, which
// pathIdx lists, and returns the positional arguments.
func (p *parser) parseOptions(tokens []string, pathIdx []int) ([]string, error) {
	var positional []string
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if slices.Contains(pathIdx, i) {
			continue
		}
		if tok == "--" {
			for j := i + 1; j < len(tokens); j++ {
				if !slices.Contains(pathIdx, j) {
					positional = append(positional, tokens[j])
				}
			}
			break
		}
		if !isOption(tok) {
			positional = append(positional, tok)
			continue
		}
		name, value, inline := strings.Cut(tok, "=")
		o, negated, err := p.resolve(name)
		if err != nil {
			return nil, err
		}
		if o.Type == spec.TypeBool {
			if inline {
				return nil, p.errorf(KindBadValue, "", "%s takes no value", name)
			}
			if err := p.set(o, name, strconv.FormatBool(!negated)); err != nil {
				return nil, err
			}
			continue
		}
		if !inline {
			// A value may start with "-" ("-", "-5"), never with "--": that
			// is the next option, and --x=--y spells the odd value instead.
			// Nor is it a command word findCommand already took.
			if i+1 >= len(tokens) || strings.HasPrefix(tokens[i+1], "--") || slices.Contains(pathIdx, i+1) {
				return nil, p.errorf(KindMissingValue, fmt.Sprintf("Write %s <%s> or %s=<%s>.", name, o.Type, name, o.Type),
					"%s needs a value", name)
			}
			i++
			value = tokens[i]
		}
		if err := p.set(o, name, value); err != nil {
			return nil, err
		}
	}
	return positional, nil
}

// resolve finds the option a --name / -x spelling names among the command's
// options and the globals. negated is true for --no-<bool>.
func (p *parser) resolve(name string) (o spec.Option, negated bool, err error) {
	all := append(slices.Clone(p.cmd.Options), p.tree.Globals...)
	if o, ok := findOption(all, name); ok {
		return o, false, nil
	}
	if base, ok := strings.CutPrefix(name, "--no-"); ok {
		if o, ok := findOption(all, "--"+base); ok && o.Type == spec.TypeBool {
			return o, true, nil
		}
	}
	hint := ""
	if long, ok := strings.CutPrefix(name, "--"); ok {
		var names []string
		for _, o := range all {
			names = append(names, o.Name)
		}
		if s := Suggest(long, names); s != "" {
			hint = fmt.Sprintf("Did you mean --%s?", s)
		}
	}
	return spec.Option{}, false, p.errorf(KindUnknownOption, hint, "unknown option %s", name)
}

// findOption matches "--name" or "-x" against opts.
func findOption(opts []spec.Option, name string) (spec.Option, bool) {
	for _, o := range opts {
		if name == "--"+o.Name || (o.Short != "" && name == "-"+o.Short) {
			return o, true
		}
	}
	return spec.Option{}, false
}

// set records one occurrence of o, spelled as `as` (for errors), with the raw
// value text.
func (p *parser) set(o spec.Option, as, raw string) error {
	multi := o.Type == spec.TypeList || o.Type == spec.TypeRepeat
	if p.given[o.Name] && !multi {
		return p.errorf(KindRepeated, "", "--%s given more than once", o.Name)
	}
	p.given[o.Name] = true
	v, err := p.convert(o, as, raw)
	if err != nil {
		return err
	}
	if multi {
		prev, _ := p.values[o.Name].([]string)
		v = append(prev, v.([]string)...)
	}
	p.values[o.Name] = v
	return nil
}

// convert parses raw by o.Type. as names where the value came from in an
// error: the option's spelling, or its environment variable or default.
func (p *parser) convert(o spec.Option, as, raw string) (any, error) {
	switch o.Type {
	case spec.TypeBool:
		return raw == "true", nil
	case spec.TypeInt:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return nil, p.errorf(KindBadValue, "", "%s: %q is not an integer", as, raw)
		}
		return n, nil
	case spec.TypeEnum:
		if slices.Contains(o.Enum, raw) {
			return raw, nil
		}
		hint := "One of: " + strings.Join(o.Enum, ", ") + "."
		if s := Suggest(raw, o.Enum); s != "" {
			hint = fmt.Sprintf("Did you mean %s? %s", s, hint)
		}
		return nil, p.errorf(KindBadValue, hint, "%s: %q is not a valid value", as, raw)
	case spec.TypeList:
		var items []string
		for item := range strings.SplitSeq(raw, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				return nil, p.errorf(KindBadValue, "", "%s: empty item in %q", as, raw)
			}
			items = append(items, item)
		}
		return items, nil
	case spec.TypeRepeat:
		return []string{raw}, nil
	default:
		return raw, nil
	}
}

// applyDefaults fills every option not given on the command line: from its
// environment variable when set, else its Default, else the type's zero value.
// Both go through convert, so a bad environment value is an error naming the
// variable.
func (p *parser) applyDefaults() error {
	for _, o := range append(slices.Clone(p.cmd.Options), p.tree.Globals...) {
		if p.given[o.Name] {
			continue
		}
		raw, as := o.Default, "default of --"+o.Name
		if o.Env != "" && p.opts.Getenv != nil {
			if env := p.opts.Getenv(o.Env); env != "" {
				raw, as = env, o.Env
				p.given[o.Name] = true
			}
		}
		if raw == "" {
			p.values[o.Name] = zeroValue(o.Type)
			continue
		}
		v, err := p.convert(o, as, raw)
		if err != nil {
			return err
		}
		p.values[o.Name] = v
	}
	return nil
}

func zeroValue(t spec.OptionType) any {
	switch t {
	case spec.TypeBool:
		return false
	case spec.TypeInt:
		return 0
	case spec.TypeList, spec.TypeRepeat:
		return []string(nil)
	default:
		return ""
	}
}

func (p *parser) errorf(kind Kind, hint, format string, a ...any) error {
	if hint == "" {
		hint = fmt.Sprintf("Run `gossms help %s` for its options.", strings.Join(p.cmd.Path, " "))
	}
	return &Error{Kind: kind, Command: p.cmd, Message: fmt.Sprintf(format, a...), Hint: hint}
}

// checkArgs matches the positional arguments to cmd.Args.
func checkArgs(cmd *spec.Command, positional []string) error {
	where := strings.Join(cmd.Path, " ")
	hint := fmt.Sprintf("Run `gossms help %s` for its usage.", where)
	repeat := len(cmd.Args) > 0 && cmd.Args[len(cmd.Args)-1].Repeat
	if len(positional) > len(cmd.Args) && !repeat {
		return &Error{Kind: KindExtraArg, Command: cmd, Hint: hint,
			Message: fmt.Sprintf("%s: unexpected argument %q", where, positional[len(cmd.Args)])}
	}
	for i, a := range cmd.Args {
		if a.Required && i >= len(positional) {
			return &Error{Kind: KindMissingArg, Command: cmd, Hint: hint,
				Message: fmt.Sprintf("%s: missing argument <%s>", where, a.Name)}
		}
	}
	return nil
}

// --- Did you mean -----------------------------------------------------------

// Suggest returns the candidate closest to word by edit distance, if within 2
// and shorter than word itself (so "ab" suggests nothing for "xy"); ties go to
// the earlier candidate. The comparison ignores case.
func Suggest(word string, candidates []string) string {
	best, bestDist := "", 3
	lw := strings.ToLower(word)
	for _, c := range candidates {
		d := editDistance(lw, strings.ToLower(c))
		if d < bestDist && d < len([]rune(word)) {
			best, bestDist = c, d
		}
	}
	return best
}

// editDistance is the optimal-string-alignment distance: insertions,
// deletions, substitutions and adjacent transpositions ("verison") each cost 1.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}
