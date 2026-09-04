package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/controls"
)

// TestDecodeEncodeRoundTripsEveryEncodingWeDetect pins the property the whole
// feature exists for: opening a file and saving it back unchanged leaves the
// bytes on disk exactly as they were. Before this, a UTF-16 script came back
// as UTF-8 full of U+FFFD and a CRLF one lost its line endings.
func TestDecodeEncodeRoundTripsEveryEncodingWeDetect(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string // the decoded text
		enc  fileEncoding
		crlf bool
	}{
		{"plain utf-8", []byte("SELECT 1;\n"), "SELECT 1;\n", encUTF8, false},
		{"utf-8 crlf", []byte("SELECT 1;\r\nGO\r\n"), "SELECT 1;\r\nGO\r\n", encUTF8, true},
		{"utf-8 bom", []byte(bomUTF8 + "SELECT 1;\n"), "SELECT 1;\n", encUTF8BOM, false},
		{"utf-8 bom crlf", []byte(bomUTF8 + "SELECT 1;\r\n"), "SELECT 1;\r\n", encUTF8BOM, true},
		{"utf-16le bom", encodeUTF16("SELECT N'café';\n", false), "SELECT N'café';\n", encUTF16LE, false},
		{"utf-16be bom", encodeUTF16("SELECT N'café';\r\n", true), "SELECT N'café';\r\n", encUTF16BE, true},
		{"non-bmp", encodeUTF16("PRINT N'😀';\n", false), "PRINT N'😀';\n", encUTF16LE, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, enc, crlf, lossy := decodeTextFile(tc.data)
			if text != tc.want {
				t.Errorf("text = %q, want %q", text, tc.want)
			}
			if enc != tc.enc || crlf != tc.crlf || lossy {
				t.Errorf("enc/crlf/lossy = %v/%v/%v, want %v/%v/false", enc, crlf, lossy, tc.enc, tc.crlf)
			}
			// The editor holds LF-separated text, so that is what Save hands
			// back to encodeTextFile.
			lf := text
			if crlf {
				lf = strings.ReplaceAll(text, "\r\n", "\n")
			}
			if got := string(encodeTextFile(lf, enc, crlf)); got != string(tc.data) {
				t.Errorf("round trip = %q, want %q", got, string(tc.data))
			}
		})
	}
}

// TestMixedLineEndingsSaveAsTheMajority pins that one stray ending does not
// decide the file. Detecting CRLF by presence made Save rewrite every line
// ending of a mostly-LF script — a whole-file change of something the user
// never edited. The assertions count endings in the saved bytes rather than
// checking the flag, so a majority rule that then wrote the other ending
// cannot pass.
func TestMixedLineEndingsSaveAsTheMajority(t *testing.T) {
	for _, tc := range []struct {
		name     string
		data     string
		wantCRLF int // in the bytes Save writes
		wantLF   int
	}{
		{"mostly lf, one stray crlf", "a\nb\r\nc\nd\n", 0, 4},
		{"mostly crlf, one stray lf", "a\r\nb\nc\r\nd\r\n", 4, 0},
		{"half and half keeps crlf", "a\r\nb\n", 2, 0},
		{"no line endings at all", "SELECT 1", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, enc, crlf, lossy := decodeTextFile([]byte(tc.data))
			if lossy {
				t.Fatal("decode reported a loss on plain ASCII")
			}
			// The editor normalizes to LF, which is what Save hands back.
			out := string(encodeTextFile(strings.ReplaceAll(text, "\r\n", "\n"), enc, crlf))
			gotCRLF := strings.Count(out, "\r\n")
			if gotCRLF != tc.wantCRLF || strings.Count(out, "\n")-gotCRLF != tc.wantLF {
				t.Errorf("saved %q: %d CRLF and %d bare LF, want %d and %d",
					out, gotCRLF, strings.Count(out, "\n")-gotCRLF, tc.wantCRLF, tc.wantLF)
			}
		})
	}
}

// TestDecodeStripsTheUTF8BOM pins the defect with the worst symptom: an
// unstripped U+FEFF reaches the server inside the first batch and SQL Server
// answers "Incorrect syntax near" pointing at an invisible character.
func TestDecodeStripsTheUTF8BOM(t *testing.T) {
	text, _, _, _ := decodeTextFile([]byte(bomUTF8 + "SELECT 1"))
	if text != "SELECT 1" {
		t.Fatalf("text = %q, want %q", text, "SELECT 1")
	}
}

// TestDecodeReportsUndecodableBytes covers the one case that cannot be
// round-tripped: a file in some non-UTF-8 single-byte encoding, which has no
// BOM to detect and so is read as UTF-8. It still loads, but the caller has to
// be told, because saving it replaces the bad bytes for good.
func TestDecodeReportsUndecodableBytes(t *testing.T) {
	// latin-1 "café"
	text, enc, _, lossy := decodeTextFile([]byte("caf\xe9"))
	if !lossy {
		t.Error("lossy = false, want true for invalid UTF-8")
	}
	if enc != encUTF8 {
		t.Errorf("enc = %v, want encUTF8", enc)
	}
	if text != "caf\xe9" {
		t.Errorf("text = %q — decode must not alter the bytes it could not decode", text)
	}
}

// TestDecodeUTF16WithATrailingOddByteIsLossy pins the truncated-file case:
// the dangling byte is dropped rather than decoded as half a code unit.
func TestDecodeUTF16WithATrailingOddByteIsLossy(t *testing.T) {
	data := append(encodeUTF16("AB", false), 0x41)
	text, enc, _, lossy := decodeTextFile(data)
	if text != "AB" || enc != encUTF16LE || !lossy {
		t.Errorf("got %q/%v/%v, want \"AB\"/encUTF16LE/true", text, enc, lossy)
	}
}

// TestDecodeUTF16UnpairedSurrogateYieldsValidText pins that a malformed
// UTF-16 file cannot produce invalid UTF-8 downstream.
func TestDecodeUTF16UnpairedSurrogateYieldsValidText(t *testing.T) {
	data := []byte{0xFF, 0xFE, 0x00, 0xD8} // BOM + a lone high surrogate
	text, _, _, _ := decodeTextFile(data)
	if text != "�" {
		t.Errorf("text = %q, want the replacement character", text)
	}
}

// ...and that it is reported as lossy, which is the half that matters to the
// user: an unflagged U+FFFD opens looking fine and Save writes it back over
// the original bytes for good. Byte length is even here, so nothing but the
// surrogate scan can catch it.
func TestDecodeUTF16UnpairedSurrogateIsLossy(t *testing.T) {
	cases := map[string][]byte{
		"lone high surrogate":       {0xFF, 0xFE, 0x00, 0xD8},
		"lone low surrogate":        {0xFF, 0xFE, 0x00, 0xDC},
		"high surrogate then plain": {0xFF, 0xFE, 0x00, 0xD8, 0x41, 0x00},
		"big-endian lone high":      {0xFE, 0xFF, 0xD8, 0x00},
	}
	for name, data := range cases {
		if _, _, _, lossy := decodeTextFile(data); !lossy {
			t.Errorf("%s: lossy = false, want true", name)
		}
	}
}

// A well-formed surrogate pair is not lossy — the scan must not flag every
// astral character (an emoji in a comment) as a damaged file.
func TestDecodeUTF16SurrogatePairIsNotLossy(t *testing.T) {
	data := encodeUTF16("SELECT '🚀';", false)
	text, _, _, lossy := decodeTextFile(data)
	if lossy {
		t.Error("lossy = true for a well-formed surrogate pair")
	}
	if text != "SELECT '🚀';" {
		t.Errorf("text = %q, want the original", text)
	}
}

// TestOpenedFileIsNotBornDirty pins the false-dirty bug at its source: the
// editor normalizes what it is given, so seeding savedText with the raw text
// makes a panel report unsaved changes the moment it opens.
func TestOpenedFileIsNotBornDirty(t *testing.T) {
	for _, text := range []string{"SELECT 1;\n", "\tSELECT 1;\n", "SELECT 1;\r\nGO\r\n"} {
		qp := new(QueryPanel{editor: controls.NewEditor(nil)})
		qp.editor.SetText(text)
		qp.savedText = qp.editor.Text()
		if qp.Dirty() {
			t.Errorf("panel opened with %q is dirty", text)
		}
	}
}
