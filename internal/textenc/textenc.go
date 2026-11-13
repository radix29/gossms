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
)

// The byte-order marks Decode recognizes.
const (
	BOMUTF8    = "\xef\xbb\xbf"
	BOMUTF16LE = "\xff\xfe"
	BOMUTF16BE = "\xfe\xff"
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
