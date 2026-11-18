package format

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Language is what a document's text is written in: it picks the formatter,
// and the query panel picks its highlighter by it too.
type Language uint8

const (
	LangSQL  Language = iota // the zero value: a new query panel is SQL
	LangXML                  // .xml files, XML cell values, XE event data
	LangJSON                 // .json files, JSON cell values
	LangText                 // plain text with no formatter, e.g. an XE field shown as .txt
)

// Ext is the file extension a document in l is saved and titled with,
// leading dot included.
func (l Language) Ext() string {
	switch l {
	case LangXML:
		return ".xml"
	case LangJSON:
		return ".json"
	case LangText:
		return ".txt"
	}
	return ".sql"
}

// String is l's display name, as a status message shows it ("Formatted as
// JSON").
func (l Language) String() string {
	switch l {
	case LangXML:
		return "XML"
	case LangJSON:
		return "JSON"
	case LangText:
		return "Text"
	}
	return "SQL"
}

// LanguageForExt maps a file extension (leading dot, any case) to its
// Language, reporting false for one it doesn't know. Detection is by
// extension only, never by sniffing content. ".txt" is deliberately unknown:
// a script opened or saved as notes.txt stays SQL, as it always has.
func LanguageForExt(ext string) (Language, bool) {
	switch strings.ToLower(ext) {
	case ".sql":
		return LangSQL, true
	case ".xml":
		return LangXML, true
	case ".json":
		return LangJSON, true
	}
	return LangSQL, false
}

// Options are the settings every formatter honours.
type Options struct {
	// IndentWidth is spaces per nesting level — the editor's own indent
	// width (Tools > Options), so formatted text indents like typed text.
	// Zero or less means DefaultIndentWidth. Output never contains tabs.
	IndentWidth int

	// LineIndent prefixes every line the formatter lays out, so a formatted
	// selection keeps the place it sat at in the surrounding text. It goes
	// in before the guard runs: a line inside a value (a multi-line SQL
	// literal, XML text) that a prefix would change is the guard's to catch.
	LineIndent string
}

// DefaultIndentWidth is the indent a zero Options gets; it matches the query
// editor's default.
const DefaultIndentWidth = 4

func (o Options) indent() string {
	n := o.IndentWidth
	if n <= 0 {
		n = DefaultIndentWidth
	}
	return strings.Repeat(" ", n)
}

// Style is one way of laying out a language. Every language with a formatter
// has at least one; SQL is the language meant to grow more.
type Style struct {
	ID   string // stable key, persisted in config once there is a choice ("standard")
	Name string // menu and status label ("Standard")

	// fn lays src out, returning ctx's error once ctx is done. A style that
	// lexed src with the guard's own lexer also returns that lexing, so
	// verify lexes only the output (SQL's lexSQL — the same function on the
	// same input, so the guard stays independent of the layout); nil
	// otherwise.
	fn func(ctx context.Context, src string, o Options) (out string, in *sqlLexed, err error)

	// ownIndent: fn applies Options.LineIndent itself, skipping the lines
	// inside a multi-line token; otherwise run prefixes every non-empty line.
	ownIndent bool
}

// styles is the registry, first entry per language the default. A language
// absent here (LangText) has no Format Document.
var styles = map[Language][]Style{
	LangSQL: {
		{ID: "standard", Name: "Standard", fn: sqlStandard.format, ownIndent: true},
		{ID: "lowercase", Name: "Lowercase", fn: sqlLowercase.format, ownIndent: true},
	},
	LangXML:  {{ID: "standard", Name: "Standard", fn: plain(formatXML)}},
	LangJSON: {{ID: "standard", Name: "Standard", fn: plain(formatJSON)}},
}

// plain adapts a formatter that hands the guard no lexing.
func plain(fn func(context.Context, string, Options) (string, error)) func(context.Context, string, Options) (string, *sqlLexed, error) {
	return func(ctx context.Context, src string, o Options) (string, *sqlLexed, error) {
		out, err := fn(ctx, src, o)
		return out, nil, err
	}
}

// Styles lists l's styles, the default first; empty when l has no formatter.
func Styles(l Language) []Style { return slices.Clone(styles[l]) }

// StyleByID resolves styleID among l's styles. An empty or unknown ID falls
// back to the default style, so a stale config value still formats; false
// only when l has no formatter at all.
func StyleByID(l Language, styleID string) (Style, bool) {
	all := styles[l]
	if len(all) == 0 {
		return Style{}, false
	}
	for _, st := range all {
		if st.ID == styleID {
			return st, true
		}
	}
	return all[0], true
}

// ErrNoFormatter is Format's error for a language with no registered style.
var ErrNoFormatter = errors.New("no formatter for this language")

// ErrGuard reports that a formatter's output failed the token-equivalence
// check (verify.go): a formatter bug, caught before it reached the editor.
var ErrGuard = errors.New("formatter output failed the safety check; text left unchanged")

// SyntaxError is a parse failure in the source, positioned for the status
// bar: "JSON line 12 col 4: invalid character …". Line and Col are 1-based;
// Col counts characters (runes), not bytes.
type SyntaxError struct {
	Lang      Language
	Line, Col int
	Msg       string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("%s line %d col %d: %s", e.Lang, e.Line, e.Col, e.Msg)
}

// Format lays src out in l's style styleID (see StyleByID for the fallback).
// It returns the formatted text or an error and never partial output: a
// *SyntaxError when src doesn't parse, ErrNoFormatter, ErrGuard when the
// result would differ from src in more than whitespace (and keyword case,
// for SQL), or ctx's error when ctx ends first.
//
// ctx is checked between phases and, inside the long ones (lexing, layout,
// the guard), every line or every checkEvery tokens, so a superseded format
// of a multi-MB script stops within milliseconds instead of running to the
// end. The one phase not checked inside is JSON's: encoding/json takes no
// context, and it runs at hundreds of MB/s.
func Format(ctx context.Context, l Language, styleID, src string, o Options) (string, error) {
	st, ok := StyleByID(l, styleID)
	if !ok {
		return "", fmt.Errorf("%s: %w", l, ErrNoFormatter)
	}
	return run(ctx, l, st, src, o)
}

// checkEvery is how many tokens a loop runs between two ctx checks. A check
// is one atomic load, but the loops it sits in do little more per token.
const checkEvery = 512

// run is Format after style lookup, split out so tests can hand it a broken
// style and watch the guard refuse its output.
func run(ctx context.Context, l Language, st Style, src string, o Options) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	out, lexed, err := st.fn(ctx, src, o)
	if err != nil {
		return "", err
	}
	if o.LineIndent != "" && !st.ownIndent {
		out = indentLines(out, o.LineIndent)
	}
	if err := verifyLexed(ctx, l, src, lexed, out); err != nil {
		return "", err
	}
	return out, nil
}

// position converts byte offset off in src to a 1-based line and rune column.
// An offset past the end is clamped to it (the position after the last
// character).
func position(src string, off int) (line, col int) {
	off = min(max(off, 0), len(src))
	before := src[:off]
	line = strings.Count(before, "\n") + 1
	col = len([]rune(before[strings.LastIndexByte(before, '\n')+1:])) + 1
	return line, col
}

// indentLines prefixes every non-empty line of s with indent.
func indentLines(s, indent string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = indent + ln
		}
	}
	return strings.Join(lines, "\n")
}
