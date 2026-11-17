package format

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// formatJSON re-indents src with json.Indent, which moves whitespace between
// tokens and nothing else: key order, number spelling (1.0, 1e5, ints past
// 2^53) and string escapes come through byte for byte. Unmarshal/MarshalIndent
// would reorder object keys and rewrite numbers, so they are not used.
//
// Exactly one top-level value is accepted. Empty {} and [] stay on one line.
// Trailing whitespace is dropped and one final newline kept iff src ended
// with one.
//
// encoding/json takes no context, so ctx is checked only between validating
// and indenting.
func formatJSON(ctx context.Context, src string, o Options) (string, error) {
	body := strings.TrimRight(src, " \t\r\n")
	// Validate before indenting: Indent writes each level's indentation
	// before it finds an error, so a bad document nested thousands deep
	// (10001 "[" hits json's depth limit) built ~200 MB to throw away.
	// Compact runs only on the invalid path, for the positioned error.
	if !json.Valid([]byte(body)) {
		err := json.Compact(new(bytes.Buffer), []byte(body))
		se, ok := errors.AsType[*json.SyntaxError](err)
		if !ok {
			return "", err
		}
		// Offset counts the offending byte, so the byte itself is one before
		// it — except at end of input, where the position is the end.
		off := int(se.Offset) - 1
		if se.Error() == "unexpected end of JSON input" {
			off = len(body)
		}
		line, col := position(body, off)
		return "", &SyntaxError{Lang: LangJSON, Line: line, Col: col, Msg: se.Error()}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := json.Indent(&b, []byte(body), "", o.indent()); err != nil {
		return "", err // unreachable: body is valid
	}
	if strings.HasSuffix(src, "\n") {
		b.WriteByte('\n')
	}
	return b.String(), nil
}
