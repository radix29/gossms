package format

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFormatXMLIndentWidth(t *testing.T) {
	got, err := Format(t.Context(), LangXML, "", `<a><b><c/></b></a>`, Options{IndentWidth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if want := "<a>\n  <b>\n    <c/>\n  </b>\n</a>"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatXMLTrailingNewline(t *testing.T) {
	for src, want := range map[string]string{
		`<a/>`:             `<a/>`,
		"<a/>\n":           "<a/>\n",
		"<a/>\n\n \t\n":    "<a/>\n",
		"\n\n  <a/>  ":     `<a/>`,
		"<a/>\r\n<b/>\r\n": "<a/>\n<b/>\n",
	} {
		got, err := Format(t.Context(), LangXML, "", src, Options{})
		if err != nil || got != want {
			t.Errorf("Format(%q) = %q, %v; want %q", src, got, err, want)
		}
	}
}

// Whitespace that is part of a value stays exactly where it was.
func TestFormatXMLKeepsValueWhitespace(t *testing.T) {
	for _, src := range []string{
		"<a> x </a>",                   // text-only, padded
		"<a>\n  x\n</a>",               // text-only over lines
		"<a> </a>",                     // whitespace is the whole value
		"<a>x <b/> y</a>",              // mixed content
		"<a><b/>\n<![CDATA[ c ]]></a>", // CDATA makes it mixed
		"<a xml:space=\"preserve\"><b/>\n  <c/></a>",
		"lead<a><b/></a>", // top-level text: whole document mixed
	} {
		got, err := Format(t.Context(), LangXML, "", src, Options{})
		if err != nil || got != src {
			t.Errorf("Format(%q) = %q, %v; want it unchanged", src, got, err)
		}
	}
}

func TestFormatXMLSyntaxErrorPosition(t *testing.T) {
	for _, tc := range []struct {
		src       string
		line, col int
		msg       string
	}{
		{"<a>\n  <b x=1/>\n</a>", 2, 8, "unquoted"},
		{"<a>\n<b></c>\n</a>", 2, 4, "</c> does not close <b> (line 2)"},
		{"<a>\n  <b>\n</a>", 3, 1, "</a> does not close <b> (line 2)"},
		{"<a><b/>", 1, 1, "<a> is never closed"},
		{"<a/>\n</b>", 2, 1, "</b> has no start tag"},
		{"<a>&nbsp;</a>", 1, 9, "entity"},
		{"<a", 1, 3, "unexpected EOF"},
		{"<é x='é😀' y=2/>", 1, 13, "unquoted"}, // columns count runes, not bytes
		{"  \n ", 1, 1, "no XML content"},
	} {
		_, err := Format(t.Context(), LangXML, "", tc.src, Options{})
		se, ok := errors.AsType[*SyntaxError](err)
		if !ok {
			t.Errorf("Format(%q): err %v, want *SyntaxError", tc.src, err)
			continue
		}
		if se.Lang != LangXML || se.Line != tc.line || se.Col != tc.col || !strings.Contains(se.Msg, tc.msg) {
			t.Errorf("Format(%q): %v; want line %d col %d containing %q", tc.src, se, tc.line, tc.col, tc.msg)
		}
	}
}

// The lexer is the guard's too, so it must cut every construct where
// encoding/xml does, whatever '>' or '<' sits inside it.
func TestXMLTokensDelimiters(t *testing.T) {
	src := `<!DOCTYPE r [<!ENTITY e "a>b"><!-- > -->]><r a='>' b="<?"><!-- <x> --><?p >?><![CDATA[<y>]]></r>`
	toks, err := xmlTokens(t.Context(), src)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []xmlKind
	for _, tk := range toks {
		kinds = append(kinds, tk.kind)
	}
	want := []xmlKind{xmlDirective, xmlStart, xmlComment, xmlPI, xmlCDATA, xmlEnd}
	if !slices.Equal(kinds, want) {
		t.Errorf("kinds %v, want %v", kinds, want)
	}
	if toks[1].name != "r" || toks[5].name != "r" {
		t.Errorf("names %q %q", toks[1].name, toks[5].name)
	}
	for _, bad := range []string{"<!><a/>", "<!-- x", "<![CDATA[ x", "<?p x", "<!DOCTYPE x", "<a b='>", "</a"} {
		if _, err := xmlTokens(t.Context(), bad); err == nil {
			t.Errorf("xmlTokens(t.Context(), %q) accepted an unterminated construct", bad)
		}
	}
}

func TestXMLGuardRefusesChangedOutput(t *testing.T) {
	const src = `<a x="1"><b> v </b><c>t <d/> u</c><![CDATA[k]]><!--n--></a>`
	for name, out := range map[string]string{
		"requotes an attribute":    `<a x='1'><b> v </b><c>t <d/> u</c><![CDATA[k]]><!--n--></a>`,
		"trims a text value":       `<a x="1"><b>v</b><c>t <d/> u</c><![CDATA[k]]><!--n--></a>`,
		"respaces mixed content":   "<a x=\"1\"><b> v </b><c>t\n<d/> u</c><![CDATA[k]]><!--n--></a>",
		"unwraps CDATA":            `<a x="1"><b> v </b><c>t <d/> u</c>k<!--n--></a>`,
		"drops a comment":          `<a x="1"><b> v </b><c>t <d/> u</c><![CDATA[k]]></a>`,
		"self-closes an element":   `<a x="1"><b> v </b><c>t <d></d> u</c><![CDATA[k]]><!--n--></a>`,
		"respaces inside a tag":    `<a  x="1"><b> v </b><c>t <d/> u</c><![CDATA[k]]><!--n--></a>`,
		"adds whitespace by CDATA": "<a x=\"1\"><b> v </b><c>t <d/> u</c>\n<![CDATA[k]]><!--n--></a>",
	} {
		if err := verify(t.Context(), LangXML, src, out); !errors.Is(err, ErrGuard) {
			t.Errorf("%s: verify = %v, want ErrGuard", name, err)
		}
	}
	if err := verify(t.Context(), LangXML, "<a><b/><c>x</c></a>", "<a>\n  <b/>\n\t<c>x</c>\n</a>\n"); err != nil {
		t.Errorf("whitespace between elements refused: %v", err)
	}
	if err := verify(t.Context(), LangXML, "<a> </a>", "<a></a>"); !errors.Is(err, ErrGuard) {
		t.Errorf("verify let a whitespace-only value go: %v", err)
	}
	if err := verify(t.Context(), LangXML, bom+"<a/>", "<a/>"); !errors.Is(err, ErrGuard) {
		t.Errorf("verify let a BOM go: %v", err)
	}
}

// FuzzFormatXML checks Format against encoding/xml's full decoder (which
// matches end tags itself) as an independent oracle: whatever it accepts
// formats, the decoded token stream survives with only whitespace between
// markup changed, and the output formats to itself.
func FuzzFormatXML(f *testing.F) {
	ins, _ := filepath.Glob(filepath.Join("testdata", "xml", "*.in"))
	for _, in := range ins {
		if b, err := os.ReadFile(in); err == nil {
			f.Add(string(b))
		}
	}
	f.Add(`<a><b x="1">t</b><c/></a>`)
	f.Add(`<a><b></a>`)
	f.Fuzz(func(t *testing.T, src string) {
		got, err := Format(t.Context(), LangXML, "", src, Options{IndentWidth: 2})
		want, oracleErr := decodedXML(src)
		if errors.Is(err, ErrGuard) {
			t.Fatalf("guard tripped on formatter output: %v", err)
		}
		if err != nil {
			if oracleErr == nil && len(want) > 0 {
				t.Fatalf("decoder accepts but Format failed: %v", err)
			}
			return
		}
		if oracleErr != nil {
			t.Fatalf("decoder rejects (%v) but Format succeeded", oracleErr)
		}
		have, err := decodedXML(got)
		if err != nil {
			t.Fatalf("output doesn't decode: %v\n%q", err, got)
		}
		if !slices.Equal(have, want) {
			t.Fatalf("tokens changed:\n%q\n%q", src, got)
		}
		if again, err := Format(t.Context(), LangXML, "", got, Options{IndentWidth: 2}); err != nil || again != got {
			t.Fatalf("not idempotent: %q -> %q (%v)", got, again, err)
		}
	})
}

// decodedXML is s's token stream as encoding/xml decodes it, whitespace-only
// character data and a leading BOM left out.
func decodedXML(s string) ([]string, error) {
	s = strings.TrimPrefix(s, bom)
	d := xml.NewDecoder(strings.NewReader(s))
	d.Strict = true
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	var toks []string
	for {
		tk, err := d.Token()
		if err == io.EOF {
			return toks, nil
		}
		if err != nil {
			return nil, err
		}
		switch v := tk.(type) {
		case xml.CharData:
			if strings.Trim(string(v), xmlSpace) != "" {
				toks = append(toks, "T"+string(v))
			}
		case xml.StartElement:
			s := "S" + v.Name.Space + ":" + v.Name.Local
			for _, a := range v.Attr {
				s += " " + a.Name.Space + ":" + a.Name.Local + "=" + a.Value
			}
			toks = append(toks, s)
		case xml.EndElement:
			toks = append(toks, "E"+v.Name.Space+":"+v.Name.Local)
		case xml.Comment:
			toks = append(toks, "C"+string(v))
		case xml.ProcInst:
			toks = append(toks, "P"+v.Target+" "+string(v.Inst))
		case xml.Directive:
			toks = append(toks, "D"+string(v))
		}
	}
}

// BenchmarkFormatXMLShowplan formats a ~5 MB showplan (the real actual plan
// in testdata, repeated). Target: under 300 ms.
func BenchmarkFormatXMLShowplan(b *testing.B) {
	one, err := os.ReadFile(filepath.Join("testdata", "xml", "showplan_actual.in"))
	if err != nil {
		b.Fatal(err)
	}
	body := string(one)
	body = body[strings.Index(body, "?>")+2:] // drop the declaration
	var sb strings.Builder
	sb.WriteString("<Plans>")
	for sb.Len() < 5<<20 {
		sb.WriteString(body)
	}
	sb.WriteString("</Plans>\n")
	src := sb.String()
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		if _, err := Format(b.Context(), LangXML, "", src, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestFormatXMLRecoversCheckPanic(t *testing.T) {
	saved := xmlCheck
	t.Cleanup(func() { xmlCheck = saved })
	xmlCheck = func(context.Context, string) error { panic("boom") }
	_, err := Format(t.Context(), LangXML, "", `<a><b/></a>`, Options{})
	if err == nil || !strings.Contains(err.Error(), "XML check panicked: boom") {
		t.Fatalf("err = %v, want the recovered panic", err)
	}
}
