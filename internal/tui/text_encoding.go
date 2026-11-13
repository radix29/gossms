package tui

import (
	"strings"

	"github.com/radix29/gossms/internal/textenc"
)

// decodeTextFile turns a file's bytes into editor text, reporting how it was
// encoded (textenc.Decode: from a byte-order mark only, never guessed) and
// whether its line endings were CRLF, so File > Save can write the same
// shape back instead of silently converting it. lossy is true when the bytes
// could not be decoded exactly — saving such a file replaces them for good,
// so the caller warns rather than doing it silently. The command line reads
// --file through the same decoder and refuses a lossy script instead.
func decodeTextFile(data []byte) (text string, enc textenc.Encoding, crlf, lossy bool) {
	text, enc, lossy = textenc.Decode(data)
	return text, enc, majorityCRLF(text), lossy
}

// majorityCRLF reports whether more of the file's line endings are CRLF than
// bare LF, which is what Save writes the whole file back as.
//
// Presence is the wrong test: one stray CRLF in an otherwise LF file made Save
// convert every line ending in it, a whole-file rewrite of something the user
// never touched. Exact preservation is not available — the editor folds CRLF
// to LF when the text is set, so which lines had a CR is gone long before Save
// — so the majority is the choice that leaves the fewest lines rewritten. Pure
// LF and pure CRLF, the cases that actually occur, are unaffected; a tie keeps
// CRLF, on the grounds that a file with any CRLF at all came from Windows.
func majorityCRLF(s string) bool {
	crlf := strings.Count(s, "\r\n")
	return crlf > 0 && crlf >= strings.Count(s, "\n")-crlf
}

// encodeTextFile is decodeTextFile's inverse: it turns the editor's text —
// always LF-separated, always valid UTF-8 — back into the file's own encoding
// and line endings. crlf comes from majorityCRLF, so a file of mixed endings
// is written with the one it mostly used rather than all-CRLF.
func encodeTextFile(text string, enc textenc.Encoding, crlf bool) []byte {
	if crlf {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	return textenc.Encode(text, enc)
}
