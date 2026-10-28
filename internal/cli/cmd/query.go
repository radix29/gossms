package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/radix29/gossms/internal/cli/connect"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/safety"
	"github.com/radix29/gossms/internal/cli/spec"
)

// The SQL input options every query command takes, exactly one of them.
const (
	optSQL       = "sql"
	optFile      = "file"
	optSQLBase64 = "sql-base64"
	optAllow     = "allow"
)

// maxScriptBytes bounds a script read from a file or stdin, so a wrong path
// (a disk image, /dev/zero) fails rather than exhausting memory.
const maxScriptBytes = 64 << 20

// QueryCommands is the query group: its node and its commands.
func QueryCommands(d Deps) []*spec.Command {
	return []*spec.Command{
		{
			Path:    []string{"query"},
			Summary: "Classify T-SQL against the safety model",
			Description: "The query editor's commands. Every one takes its T-SQL from exactly one of --sql (-Q), " +
				"--file (-i; - reads stdin) or --sql-base64. For anything but a one-line query prefer --file - or " +
				"--sql-base64: no shell can damage them.",
			Class: spec.ClassRead,
		},
		{
			Path:    []string{"query", "classify"},
			Summary: "Report the operation class (read, write, ddl, admin) of each statement and of the script",
			Description: "Classifies without connecting: one row per statement with its batch, line, class, leading " +
				"keywords, the object it acts on, why it is above its leader's class, and flags " +
				"(transaction-control, not-in-transaction, nocount, dynamic-sql, exec, mode-option, output-clause). " +
				"Anything unrecognised counts as write. With --allow it also applies the check a run would " +
				"(SET PARSEONLY, NOEXEC and SHOWPLAN_* are refused too) and answers BLOCKED, exit 7, when the script " +
				"would be refused.",
			Options: append(sqlInputOptions(),
				spec.Option{Name: optAllow, Type: spec.TypeEnum, Enum: classNames(),
					Help: "Check the script against this class, as a run with --allow would"}),
			Class: spec.ClassRead,
			Run:   d.queryClassify,
			Examples: []string{
				"gossms query classify --file fix.sql",
				"gossms query classify --file - --allow write -o json",
				"gossms query classify --sql-base64 VVBEQVRFIGRiby50IFNFVCB4ID0gMQ==",
			},
		},
	}
}

func classNames() []string {
	return []string{string(spec.ClassRead), string(spec.ClassWrite), string(spec.ClassDDL), string(spec.ClassAdmin)}
}

// sqlInputOptions is --sql, --file and --sql-base64.
func sqlInputOptions() []spec.Option {
	return []spec.Option{
		{Name: optSQL, Short: "Q", Type: spec.TypeString, Help: "Inline T-SQL (fine for a short query)"},
		{Name: optFile, Short: "i", Type: spec.TypeString,
			Help: "T-SQL file; - reads stdin. GO separates batches. UTF-8, or UTF-16 with a byte-order mark"},
		{Name: optSQLBase64, Type: spec.TypeString, Help: "T-SQL as Base64 of its UTF-8 text, safe from any shell quoting"},
	}
}

// readSQL is the script from the one SQL input option given.
func readSQL(inv *spec.Invocation) (string, error) {
	var given []string
	for _, n := range []string{optSQL, optFile, optSQLBase64} {
		if inv.Given[n] {
			given = append(given, "--"+n)
		}
	}
	switch len(given) {
	case 0:
		return "", usageError("no T-SQL given", "Give one of --sql (-Q), --file (-i; - for stdin) or --sql-base64.")
	case 1:
	default:
		return "", usageError(strings.Join(given, " and ")+" both given", "Give exactly one T-SQL source.")
	}
	var data []byte
	what := given[0]
	switch given[0] {
	case "--" + optSQL:
		return inv.Values[optSQL].(string), nil
	case "--" + optSQLBase64:
		raw := strings.TrimSpace(inv.Values[optSQLBase64].(string))
		var err error
		if data, err = base64.StdEncoding.DecodeString(raw); err != nil {
			if data, err = base64.RawStdEncoding.DecodeString(raw); err != nil {
				return "", usageError("--sql-base64: not Base64: "+err.Error(), "Encode the UTF-8 text with standard Base64.")
			}
		}
	default:
		path := inv.Values[optFile].(string)
		if path == "-" {
			if stdin, _ := inv.Values[connect.OptPasswordStdin].(bool); stdin {
				return "", usageError("--file - and --password-stdin both read stdin",
					"Pass the password with --password-env, or the T-SQL with --file <path> or --sql-base64.")
			}
			if inv.Stdin == nil {
				return "", usageError("--file -: no stdin", "")
			}
			var err error
			if data, err = io.ReadAll(io.LimitReader(inv.Stdin, maxScriptBytes+1)); err != nil {
				return "", usageError("--file -: "+err.Error(), "")
			}
			what = "stdin"
		} else {
			if inv.ReadFile == nil {
				return "", usageError("--file: files cannot be read here", "")
			}
			var err error
			if data, err = inv.ReadFile(path); err != nil {
				return "", usageError("--file "+path+": "+err.Error(), "")
			}
			what = path
		}
		if len(data) > maxScriptBytes {
			return "", usageError(fmt.Sprintf("%s: the script is over %d MB", what, maxScriptBytes>>20), "")
		}
	}
	text, ok := decodeScript(data)
	if !ok {
		return "", usageError(what+": the T-SQL is not valid UTF-8", "Save the file as UTF-8, or UTF-16 with a byte-order mark.")
	}
	return text, nil
}

// decodeScript is a script's bytes as text: a UTF-8 byte-order mark is dropped
// and UTF-16 (which SSMS saved by default) decoded by its mark; anything else
// must be valid UTF-8. A mark is the only evidence read, as the TUI's File >
// Open does (internal/tui/text_encoding.go): a guessed encoding would run a
// script the caller never wrote. A leading U+FEFF would otherwise reach the
// server and fail as "Incorrect syntax near" an invisible character.
func decodeScript(data []byte) (string, bool) {
	var order func([]byte) uint16
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		data = data[3:]
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		order = func(b []byte) uint16 { return uint16(b[0]) | uint16(b[1])<<8 }
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		order = func(b []byte) uint16 { return uint16(b[1]) | uint16(b[0])<<8 }
	}
	if order == nil {
		return string(data), utf8.Valid(data)
	}
	data = data[2:]
	if len(data)%2 != 0 {
		return "", false
	}
	var b strings.Builder
	b.Grow(len(data))
	for i := 0; i < len(data); i += 2 {
		r := rune(order(data[i:]))
		if utf16.IsSurrogate(r) {
			// A pair, or the file is not UTF-16: an unpaired surrogate would
			// become U+FFFD, a silent change to the script.
			if i+3 >= len(data) {
				return "", false
			}
			if r = utf16.DecodeRune(r, rune(order(data[i+2:]))); r == utf8.RuneError {
				return "", false
			}
			i += 2
		}
		b.WriteRune(r)
	}
	return b.String(), true
}

func usageError(msg, hint string) *output.Error {
	return output.NewError(output.CodeUsageError, msg, hint)
}

func (d Deps) queryClassify(_ context.Context, inv *spec.Invocation) (*output.Result, error) {
	script, err := readSQL(inv)
	if err != nil {
		return nil, err
	}
	sc := safety.Classify(script)
	res := &output.Result{Fields: []output.Field{{Key: "detectedClass", Value: string(sc.Class)}}}
	var blocked *safety.Blocked
	if inv.Given[optAllow] {
		allow := spec.Class(inv.Values[optAllow].(string))
		res.Fields = append(res.Fields, output.Field{Key: "allowedClass", Value: string(allow)})
		blocked = safety.Check(sc, safety.Policy{Allow: allow})
	}
	res.Fields = append(res.Fields,
		output.Field{Key: "flags", Value: strings.Join(sc.Flags.Names(), ", ")},
		output.Field{Key: "statementCount", Value: strconv.Itoa(len(sc.Statements))})
	res.Sets = []output.Set{statementSet(sc.Statements)}
	if blocked != nil {
		res.Fields = append(res.Fields, output.Field{Key: "statements", Value: blocked.Describe()})
		return res, blockedError(blocked)
	}
	return res, nil
}

// statementSet is the classification as a result set, one row per statement.
func statementSet(stmts []safety.Statement) output.Set {
	set := output.Set{Columns: []output.Column{
		{Name: "batch", Type: "int"}, {Name: "repeat", Type: "int"}, {Name: "line", Type: "int"},
		{Name: "class"}, {Name: "leader"}, {Name: "object", Nullable: true}, {Name: "reason", Nullable: true},
		{Name: "flags"},
	}}
	orNull := func(s string) *string {
		if s == "" {
			return nil
		}
		return ptr(s)
	}
	for _, st := range stmts {
		set.Rows = append(set.Rows, []*string{
			ptr(strconv.Itoa(st.Batch)), ptr(strconv.Itoa(st.Repeat)), ptr(strconv.Itoa(st.Line)),
			ptr(string(st.Class)), ptr(st.Leader), orNull(st.Object), orNull(st.Reason),
			ptr(strings.Join(st.Flags.Names(), ", ")),
		})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// blockedError is a refusal as the error Main reports: BLOCKED, exit 7.
func blockedError(b *safety.Blocked) *output.Error {
	return output.NewError(output.CodeBlocked, b.Message, b.Hint)
}
