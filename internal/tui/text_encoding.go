package tui

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// fileEncoding is how a text file that File > Open read was encoded on disk,
// so File > Save can write it back the same way instead of silently
// converting it. Detected from a byte-order mark and nothing else: a file
// with no BOM is read as UTF-8, because guessing an encoding wrong rewrites
// the user's script in one they never chose.
type fileEncoding int

const (
	encUTF8    fileEncoding = iota // no BOM — also the zero value, for a panel with no file
	encUTF8BOM                     // what SSMS and Windows editors write by default
	encUTF16LE                     // SSMS's own default .sql encoding
	encUTF16BE
)

const (
	bomUTF8    = "\xef\xbb\xbf"
	bomUTF16LE = "\xff\xfe"
	bomUTF16BE = "\xfe\xff"
)

// decodeTextFile turns a file's bytes into editor text, reporting how it was
// encoded and whether its line endings were CRLF so the same shape can be
// written back. lossy is true when the bytes could not be decoded exactly and
// undecodable ones became U+FFFD — saving such a file replaces them for good,
// so the caller warns rather than doing it silently.
//
// Stripping the BOM is not cosmetic: a leading U+FEFF reaches the server as
// part of the first batch, and SQL Server answers with "Incorrect syntax
// near" pointing at a character the user cannot see.
func decodeTextFile(data []byte) (text string, enc fileEncoding, crlf, lossy bool) {
	s := string(data)
	switch {
	case strings.HasPrefix(s, bomUTF8):
		s, enc = s[len(bomUTF8):], encUTF8BOM
		lossy = !utf8.ValidString(s)
	case strings.HasPrefix(s, bomUTF16LE):
		s, lossy = decodeUTF16(data[len(bomUTF16LE):], false)
		enc = encUTF16LE
	case strings.HasPrefix(s, bomUTF16BE):
		s, lossy = decodeUTF16(data[len(bomUTF16BE):], true)
		enc = encUTF16BE
	default:
		enc = encUTF8
		lossy = !utf8.ValidString(s)
	}
	return s, enc, majorityCRLF(s), lossy
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
func encodeTextFile(text string, enc fileEncoding, crlf bool) []byte {
	if crlf {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	switch enc {
	case encUTF8BOM:
		return []byte(bomUTF8 + text)
	case encUTF16LE:
		return encodeUTF16(text, false)
	case encUTF16BE:
		return encodeUTF16(text, true)
	}
	return []byte(text)
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
// that decodeUTF16's caller stripped — a UTF-16 file without one is
// indistinguishable from any other binary.
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
