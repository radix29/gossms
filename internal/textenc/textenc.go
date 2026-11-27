// Package textenc decodes a text file's bytes by its byte-order mark, and
// encodes text back the same way. It is the one decoder behind File > Open
// and File > Save in the TUI, the command line's --file, data import's byte
// stream and showplan's .sqlplan reader, so every way a file reaches gossms
// agrees on what its bytes say. Standard library only.
//
// The mark is the only evidence read. A file with none is UTF-8, because a
// guessed encoding wrong rewrites (or, from the command line, runs) a script
// the user never wrote.
package textenc

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Encoding is how a text file was encoded on disk, so it can be written back
// the same way instead of silently converted.
type Encoding int

const (
	UTF8    Encoding = iota // no BOM — also the zero value, for text with no file
	UTF8BOM                 // what SSMS and Windows editors write by default
	UTF16LE                 // SSMS's own default .sql encoding
	UTF16BE
	UTF32LE
	UTF32BE
)

// The byte-order marks Decode recognizes.
const (
	BOMUTF8    = "\xef\xbb\xbf"
	BOMUTF16LE = "\xff\xfe"
	BOMUTF16BE = "\xfe\xff"
	BOMUTF32LE = "\xff\xfe\x00\x00"
	BOMUTF32BE = "\x00\x00\xfe\xff"
)

// Decode turns a file's bytes into text, reporting how it was encoded and
// whether the decode was lossy: bytes that could not be decoded exactly. Each
// caller decides what lossy means to it — the TUI opens the file and warns,
// since saving replaces the bad bytes for good; the command line refuses,
// since running a changed script is worse than not running it.
//
// Lossy UTF-8 (no mark, or a UTF-8 one) comes back as the bytes unchanged,
// so not valid UTF-8; lossy UTF-16 comes back valid, with U+FFFD for each
// unpaired surrogate and a trailing odd byte dropped.
//
// Stripping the BOM is not cosmetic: a leading U+FEFF reaches the server as
// part of the first batch, and SQL Server answers with "Incorrect syntax
// near" pointing at a character the user cannot see.
func Decode(data []byte) (text string, enc Encoding, lossy bool) {
	switch {
	case bytes.HasPrefix(data, []byte(BOMUTF8)):
		data = data[len(BOMUTF8):]
		return string(data), UTF8BOM, !utf8.Valid(data)
	// UTF-32LE's mark begins with UTF-16LE's, so it is tested first: a
	// UTF-16LE file that starts with a real U+0000 is not a script.
	case bytes.HasPrefix(data, []byte(BOMUTF32LE)):
		text, lossy = decodeUTF32(data[len(BOMUTF32LE):], false)
		return text, UTF32LE, lossy
	case bytes.HasPrefix(data, []byte(BOMUTF32BE)):
		text, lossy = decodeUTF32(data[len(BOMUTF32BE):], true)
		return text, UTF32BE, lossy
	case bytes.HasPrefix(data, []byte(BOMUTF16LE)):
		text, lossy = decodeUTF16(data[len(BOMUTF16LE):], false)
		return text, UTF16LE, lossy
	case bytes.HasPrefix(data, []byte(BOMUTF16BE)):
		text, lossy = decodeUTF16(data[len(BOMUTF16BE):], true)
		return text, UTF16BE, lossy
	}
	return string(data), UTF8, !utf8.Valid(data)
}

// Encode is Decode's inverse: text, which must be valid UTF-8, in enc with
// enc's byte-order mark.
func Encode(text string, enc Encoding) []byte {
	switch enc {
	case UTF8BOM:
		return []byte(BOMUTF8 + text)
	case UTF16LE:
		return encodeUTF16(text, false)
	case UTF16BE:
		return encodeUTF16(text, true)
	case UTF32LE:
		return encodeUTF32(text, false)
	case UTF32BE:
		return encodeUTF32(text, true)
	}
	return []byte(text)
}

// SkipUTF8BOM is r past a UTF-8 byte-order mark, if it starts with one, for
// a caller that streams a file rather than reading it whole (data import's
// CSV, TSV and JSONL readers). It goes on the bytes, before any format's
// reader: left in, the mark is text before a quoted header's opening quote
// ("a quote in an unquoted field"), part of an unquoted first column's name,
// or not JSON. Only the UTF-8 mark is skipped: such a caller
// reads UTF-8, and a UTF-16 file fails in its reader as not UTF-8 rather than
// being half-handled here.
func SkipUTF8BOM(r io.Reader) io.Reader {
	br := bufio.NewReader(r)
	if b, err := br.Peek(len(BOMUTF8)); err == nil && string(b) == BOMUTF8 {
		br.Discard(len(BOMUTF8))
	}
	return br
}

// decodeUTF16 decodes BOM-less UTF-16 code units of the given endianness,
// reporting whether the decode lost anything.
//
// Both ways of losing something have to be reported, because utf16.Decode
// itself reports neither: a trailing odd byte is dropped here, and Decode
// maps an unpaired surrogate to U+FFFD, so the result is always valid UTF-8
// whatever the file held. An unflagged U+FFFD is the failure this exists to
// catch: the panel opens looking fine and Save writes the replacement
// characters back over the user's script for good.
func decodeUTF16(b []byte, bigEndian bool) (text string, lossy bool) {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if bigEndian {
			units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
		} else {
			units = append(units, uint16(b[i+1])<<8|uint16(b[i]))
		}
	}
	return string(utf16.Decode(units)), len(b)%2 != 0 || hasUnpairedSurrogate(units)
}

// decodeUTF32 decodes BOM-less UTF-32 of the given endianness. An invalid
// scalar (a surrogate, or above U+10FFFF) becomes U+FFFD and a length that
// is not a multiple of 4 drops the tail; both report lossy.
func decodeUTF32(b []byte, bigEndian bool) (text string, lossy bool) {
	var sb strings.Builder
	for ; len(b) >= 4; b = b[4:] {
		var r uint32
		if bigEndian {
			r = uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
		} else {
			r = uint32(b[3])<<24 | uint32(b[2])<<16 | uint32(b[1])<<8 | uint32(b[0])
		}
		if r > utf8.MaxRune || (r >= surrHighMin && r <= surrMax) {
			lossy = true
			r = utf8.RuneError
		}
		sb.WriteRune(rune(r))
	}
	return sb.String(), lossy || len(b) != 0
}

// encodeUTF32 encodes text as UTF-32 of the given endianness, with its BOM.
func encodeUTF32(text string, bigEndian bool) []byte {
	out := make([]byte, 0, 4*len(text)+4)
	put := func(r rune) {
		if bigEndian {
			out = append(out, byte(r>>24), byte(r>>16), byte(r>>8), byte(r))
		} else {
			out = append(out, byte(r), byte(r>>8), byte(r>>16), byte(r>>24))
		}
	}
	put(0xFEFF)
	for _, r := range text {
		put(r)
	}
	return out
}

// UTF-16 surrogate ranges: a high (leading) unit must be followed by a low
// (trailing) one, and anything else is an encoding error.
const (
	surrHighMin = 0xD800
	surrLowMin  = 0xDC00
	surrMax     = 0xDFFF
)

// hasUnpairedSurrogate reports whether units contains a high surrogate not
// followed by a low one, or a low surrogate with no high one before it —
// the code units utf16.Decode silently turns into U+FFFD.
func hasUnpairedSurrogate(units []uint16) bool {
	for i := 0; i < len(units); i++ {
		u := units[i]
		switch {
		case u >= surrHighMin && u < surrLowMin:
			if i+1 >= len(units) || units[i+1] < surrLowMin || units[i+1] > surrMax {
				return true
			}
			i++ // consume the low half of a well-formed pair
		case u >= surrLowMin && u <= surrMax:
			return true
		}
	}
	return false
}

// encodeUTF16 encodes text as UTF-16 of the given endianness, with the BOM
// that Decode stripped — a UTF-16 file without one is indistinguishable from
// any other binary.
func encodeUTF16(text string, bigEndian bool) []byte {
	units := utf16.Encode([]rune(text))
	out := make([]byte, 0, 2*len(units)+2)
	put := func(u uint16) {
		if bigEndian {
			out = append(out, byte(u>>8), byte(u))
		} else {
			out = append(out, byte(u), byte(u>>8))
		}
	}
	put(0xFEFF)
	for _, u := range units {
		put(u)
	}
	return out
}

// EOL is the line ending a text file was written with, so it can be written
// back the same way. The zero value, LF, suits text with no file.
type EOL int

const (
	LF   EOL = iota // Unix, and what the editor holds
	CRLF            // Windows; SSMS's own
	CR              // classic Mac
)

// DetectEOL reports the line ending most of s's line breaks use, which is
// what Save writes the whole file back as.
//
// Presence is the wrong test: one stray CRLF in an otherwise LF file made Save
// convert every line ending in it, a whole-file rewrite of something the user
// never touched. Exact preservation is not available — the editor folds every
// ending to LF when the text is set, so which lines had which is gone long
// before Save — so the majority is the choice that leaves the fewest lines
// rewritten. Pure LF, pure CRLF and pure CR, the cases that actually occur,
// are unaffected. A tie keeps CRLF, on the grounds that a file with any CRLF
// at all came from Windows, then LF over CR; text with no line break is LF.
func DetectEOL(s string) EOL {
	crlf := strings.Count(s, "\r\n")
	lf := strings.Count(s, "\n") - crlf
	cr := strings.Count(s, "\r") - crlf
	switch {
	case crlf > 0 && crlf >= lf && crlf >= cr:
		return CRLF
	case cr > lf:
		return CR
	}
	return LF
}

// NormalizeEOL turns every line ending in s — CRLF, and a lone CR — into LF,
// the form tuikit's editor holds after load. It rewrites line breaks inside
// string literals too, so it is never applied to a script on its way to the
// server: the command line sends a script as read, and sqltext.SplitBatches
// ends a line at CRLF and a lone CR itself.
func NormalizeEOL(s string) string {
	if !strings.Contains(s, "\r") {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// WithEOL is NormalizeEOL's inverse: LF-separated text with every LF written
// as eol.
func WithEOL(s string, eol EOL) string {
	switch eol {
	case CRLF:
		return strings.ReplaceAll(s, "\n", "\r\n")
	case CR:
		return strings.ReplaceAll(s, "\n", "\r")
	}
	return s
}
