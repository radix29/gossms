package cmd

import (
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"github.com/radix29/gossms/internal/cli/audit"
	"github.com/radix29/gossms/internal/cli/connect"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/textenc"
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
	// The text is refused whole when it is lossy: running a script with
	// U+FFFD where the file had bytes it could not decode is a silent change
	// to the script, where the TUI's File > Open only warns.
	text, enc, lossy := textenc.Decode(data)
	if lossy {
		as := "UTF-8"
		if enc == textenc.UTF16LE || enc == textenc.UTF16BE {
			as = "UTF-16 (its byte-order mark says so)"
		}
		return "", usageError(what+": the T-SQL is not valid "+as, "Save the file as UTF-8, or UTF-16 with a byte-order mark.")
	}
	return text, nil
}
