package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/textenc"
	"github.com/radix29/tuikit/controls/editor"
)

// The decoding itself (lossy input, surrogates, odd lengths) is tested in
// internal/textenc; these pin what the TUI adds: line endings, and the round
// trip File > Open → File > Save.

// TestDecodeEncodeRoundTripsEveryEncodingWeDetect pins the property the whole
// feature exists for: opening a file and saving it back unchanged leaves the
// bytes on disk exactly as they were. Before this, a UTF-16 script came back
// as UTF-8 full of U+FFFD and a CRLF one lost its line endings.
func TestDecodeEncodeRoundTripsEveryEncodingWeDetect(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string // the decoded text
		enc  textenc.Encoding
		crlf bool
	}{
		{"plain utf-8", []byte("SELECT 1;\n"), "SELECT 1;\n", textenc.UTF8, false},
		{"utf-8 crlf", []byte("SELECT 1;\r\nGO\r\n"), "SELECT 1;\r\nGO\r\n", textenc.UTF8, true},
		{"utf-8 bom", []byte(textenc.BOMUTF8 + "SELECT 1;\n"), "SELECT 1;\n", textenc.UTF8BOM, false},
		{"utf-8 bom crlf", []byte(textenc.BOMUTF8 + "SELECT 1;\r\n"), "SELECT 1;\r\n", textenc.UTF8BOM, true},
		{"utf-16le bom", textenc.Encode("SELECT N'café';\n", textenc.UTF16LE), "SELECT N'café';\n", textenc.UTF16LE, false},
		{"utf-16be bom", textenc.Encode("SELECT N'café';\r\n", textenc.UTF16BE), "SELECT N'café';\r\n", textenc.UTF16BE, true},
		{"non-bmp", textenc.Encode("PRINT N'😀';\n", textenc.UTF16LE), "PRINT N'😀';\n", textenc.UTF16LE, false},
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
	text, _, _, _ := decodeTextFile([]byte(textenc.BOMUTF8 + "SELECT 1"))
	if text != "SELECT 1" {
		t.Fatalf("text = %q, want %q", text, "SELECT 1")
	}
}

// TestOpenedFileIsNotBornDirty pins the false-dirty bug at its source: the
// editor normalizes what it is given, so seeding savedText with the raw text
// makes a panel report unsaved changes the moment it opens.
func TestOpenedFileIsNotBornDirty(t *testing.T) {
	for _, text := range []string{"SELECT 1;\n", "\tSELECT 1;\n", "SELECT 1;\r\nGO\r\n"} {
		qp := new(QueryPanel{editor: editor.NewEditor(nil)})
		qp.editor.SetText(text)
		qp.markSaved()
		if qp.Dirty() {
			t.Errorf("panel opened with %q is dirty", text)
		}
	}
}
