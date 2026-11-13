package textenc

import (
	"io"
	"strings"
	"testing"
)

// TestDecode covers every encoding Decode detects and both ways a decode can
// be lossy. lossy is the contract both callers act on — the TUI warns, the
// command line refuses — so every case states it.
func TestDecode(t *testing.T) {
	for _, tt := range []struct {
		name  string
		in    []byte
		want  string
		enc   Encoding
		lossy bool
	}{
		{"plain utf-8", []byte("SELECT 1"), "SELECT 1", UTF8, false},
		{"utf-8 bom stripped", []byte(BOMUTF8 + "SELECT 1"), "SELECT 1", UTF8BOM, false},
		{"utf-16le surrogate pair", []byte{0xFF, 0xFE, 'A', 0, 0x3D, 0xD8, 0x00, 0xDE}, "A😀", UTF16LE, false},
		{"utf-16be", []byte{0xFE, 0xFF, 0, 'A', 0, 'B'}, "AB", UTF16BE, false},
		{"empty", nil, "", UTF8, false},
		// A file in a single-byte encoding has no mark, so it is read as
		// UTF-8; the bytes come back untouched and the caller is told.
		{"latin-1 read as utf-8", []byte("caf\xe9"), "caf\xe9", UTF8, true},
		{"invalid utf-8 after bom", []byte(BOMUTF8 + "\xc3\x28"), "\xc3\x28", UTF8BOM, true},
		// The dangling byte is dropped rather than decoded as half a unit.
		{"utf-16 odd length", append(Encode("AB", UTF16LE), 0x41), "AB", UTF16LE, true},
		// Even byte length, so nothing but the surrogate scan catches these;
		// utf16.Decode alone would hand back U+FFFD unflagged.
		{"lone high surrogate", []byte{0xFF, 0xFE, 0x00, 0xD8}, "�", UTF16LE, true},
		{"lone low surrogate", []byte{0xFF, 0xFE, 0x00, 0xDC}, "�", UTF16LE, true},
		{"high surrogate then plain", []byte{0xFF, 0xFE, 0x00, 0xD8, 0x41, 0x00}, "�A", UTF16LE, true},
		{"big-endian lone high", []byte{0xFE, 0xFF, 0xD8, 0x00}, "�", UTF16BE, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			text, enc, lossy := Decode(tt.in)
			if text != tt.want || enc != tt.enc || lossy != tt.lossy {
				t.Errorf("Decode(% x) = %q/%v/%v, want %q/%v/%v", tt.in, text, enc, lossy, tt.want, tt.enc, tt.lossy)
			}
		})
	}
}

// TestEncodeRoundTrips pins that what Decode reads, Encode writes back byte
// for byte — File > Save of an untouched file must not change it.
func TestEncodeRoundTrips(t *testing.T) {
	for _, enc := range []Encoding{UTF8, UTF8BOM, UTF16LE, UTF16BE} {
		for _, text := range []string{"", "SELECT N'café';\r\n", "PRINT N'🚀';"} {
			data := Encode(text, enc)
			got, gotEnc, lossy := Decode(data)
			if got != text || gotEnc != enc || lossy {
				t.Errorf("enc %v, %q: decoded %q/%v/%v", enc, text, got, gotEnc, lossy)
			}
		}
	}
}

// TestSkipUTF8BOM: the mark is dropped from the stream, and a stream without
// one — including one shorter than a mark — is read whole.
func TestSkipUTF8BOM(t *testing.T) {
	for in, want := range map[string]string{
		BOMUTF8 + `"Order Date",x`: `"Order Date",x`,
		"a,b":                      "a,b",
		"a":                        "a",
		"":                         "",
		BOMUTF8:                    "",
		BOMUTF16LE + "a":           BOMUTF16LE + "a",
	} {
		b, err := io.ReadAll(SkipUTF8BOM(strings.NewReader(in)))
		if err != nil || string(b) != want {
			t.Errorf("SkipUTF8BOM(%q) = %q, %v; want %q", in, b, err, want)
		}
	}
}
