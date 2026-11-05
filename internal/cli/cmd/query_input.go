package cmd

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/radix29/gossms/internal/cli/audit"
	"github.com/radix29/gossms/internal/cli/connect"
	"github.com/radix29/gossms/internal/cli/spec"
)

// maxScriptBytes bounds a script read from a file or stdin, so a wrong path
// (a disk image, /dev/zero) fails rather than exhausting memory.
const maxScriptBytes = 64 << 20

// sqlInputOptions is --sql, --file and --sql-base64.
func sqlInputOptions() []spec.Option {
	return []spec.Option{
		{Name: optSQL, Short: "Q", Type: spec.TypeString, Audit: spec.AuditHash, Help: "Inline T-SQL (fine for a short query)"},
		{Name: optFile, Short: "i", Type: spec.TypeString,
			Help: "T-SQL file; - reads stdin. GO separates batches. UTF-8, or UTF-16 with a byte-order mark"},
		{Name: optSQLBase64, Type: spec.TypeString, Audit: spec.AuditHash, Help: "T-SQL as Base64 of its UTF-8 text, safe from any shell quoting"},
	}
}

// readSQL is the script from the one SQL input option given. Its hash is
// the audit log's sqlSha256.
func readSQL(inv *spec.Invocation) (string, error) {
	script, err := readSQLSource(inv)
	if err == nil {
		inv.Audit.SQLSha256 = audit.Hash(script)
	}
	return script, err
}

func readSQLSource(inv *spec.Invocation) (string, error) {
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
