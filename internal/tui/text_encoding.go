package tui

import (
	"github.com/radix29/gossms/internal/textenc"
)

// decodeTextFile turns a file's bytes into editor text, reporting how it was
// encoded (textenc.Decode: from a byte-order mark only, never guessed) and
// which line ending it mostly used (textenc.DetectEOL), so File > Save can
// write the same shape back instead of silently converting it. lossy is true
// when the bytes could not be decoded exactly — saving such a file replaces
// them for good, so the caller warns rather than doing it silently. The
// command line reads --file through the same decoder and refuses a lossy
// script instead.
func decodeTextFile(data []byte) (text string, enc textenc.Encoding, eol textenc.EOL, lossy bool) {
	text, enc, lossy = textenc.Decode(data)
	return text, enc, textenc.DetectEOL(text), lossy
}

// encodeTextFile is decodeTextFile's inverse: it turns the editor's text —
// always LF-separated, always valid UTF-8 — back into the file's own encoding
// and line endings. eol comes from DetectEOL, so a file of mixed endings is
// written with the one it mostly used rather than all-CRLF.
func encodeTextFile(text string, enc textenc.Encoding, eol textenc.EOL) []byte {
	return textenc.Encode(textenc.WithEOL(text, eol), enc)
}
