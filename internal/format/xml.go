package format

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// formatXML re-indents src one element per line, children one indent level
// deeper. It is a raw-text re-indenter, not an encoding/xml Encoder round
// trip: the Encoder rewrites namespace prefixes, turns CDATA into escaped text
// and normalises attribute quoting, all surprising in a showplan or an XE
// event. Here every tag, comment, CDATA section, PI and directive is emitted
// byte for byte; only whitespace between them moves.
//
// What is never re-indented, because the whitespace inside is the value:
//   - an element with any non-whitespace text or CDATA child — text-only
//     (<Name>abc</Name>, one line) and mixed content alike are emitted as
//     they stand;
//   - an element whose only content is whitespace (<a> </a>);
//   - an xml:space="preserve" element.
//
// Several top-level elements are fine: FOR XML without ROOT returns a
// fragment. Top-level text other than whitespace (FOR XML PATH with an empty
// row name can return it) makes the whole document mixed content, so it comes
// back unchanged. A leading BOM is kept. Trailing whitespace is dropped and
// one final newline kept iff src ended with one.
func formatXML(src string, o Options) (string, error) {
	// The strict well-formedness pass is half the work on a big showplan and
	// shares nothing with the layout, so the two run side by side. Its error,
	// when there is one, wins: it is the more precise.
	// This goroutine is outside every safego in internal/tui: a panic here
	// would kill the process past main's recover, skipping screen.Fini and
	// EmergencySave. Recover it into an error instead; checked is buffered,
	// so neither send can block.
	checked := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				checked <- fmt.Errorf("format: XML check panicked: %v", r)
			}
		}()
		checked <- xmlCheck(src)
	}()
	out, err := layoutXML(src, o)
	if cerr := <-checked; cerr != nil {
		return "", cerr
	}
	return out, err
}

// layoutXML is formatXML without the well-formedness check.
func layoutXML(src string, o Options) (string, error) {
	toks, err := xmlTokens(src)
	if err != nil {
		return "", err
	}
	root, err := xmlTree(src, toks)
	if err != nil {
		return "", err
	}
	if len(significant(src, root.children)) == 0 {
		return "", &SyntaxError{Lang: LangXML, Line: 1, Col: 1, Msg: "no XML content"}
	}
	if xmlTextual(src, root) {
		return src, nil
	}
	f := xmlFmt{src: src, ind: o.indent()}
	if strings.HasPrefix(src, bom) {
		f.b.WriteString(bom)
	}
	f.b.Grow(len(src) + len(src)/4)
	for _, c := range significant(src, root.children) {
		f.node(c, 0)
	}
	if strings.HasSuffix(src, "\n") {
		f.b.WriteByte('\n')
	}
	return f.b.String(), nil
}

const bom = "\uFEFF"

// xmlCheck is the check formatXML runs; a variable so a test can make it panic.
var xmlCheck = checkXML

// checkXML is the well-formedness pass: encoding/xml's strict tokenizer
// checks names, attribute quoting, entities and characters. RawToken doesn't
// match end tags to start tags — xmlTree does that. The encoding a
// declaration names is irrelevant: the text is already decoded, so a
// showplan's encoding="utf-16" must not be an error.
func checkXML(src string) error {
	d := xml.NewDecoder(strings.NewReader(src))
	d.Strict = true
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	for {
		_, err := d.RawToken()
		if err == io.EOF {
			return nil
		}
		if err == nil {
			continue
		}
		msg := err.Error()
		// InputOffset is just past the offending byte, or the end of input.
		off := int(d.InputOffset()) - 1
		if se, ok := errors.AsType[*xml.SyntaxError](err); ok {
			msg = se.Msg
			if msg == "unexpected EOF" {
				off = len(src)
			}
		}
		line, col := position(src, off)
		return &SyntaxError{Lang: LangXML, Line: line, Col: col, Msg: msg}
	}
}

type xmlKind uint8

const (
	xmlText      xmlKind = iota // character data, entity references included
	xmlStart                    // <a …>
	xmlEnd                      // </a>
	xmlEmpty                    // <a …/>
	xmlComment                  // <!-- … -->
	xmlCDATA                    // <![CDATA[ … ]]>
	xmlPI                       // <? … ?>, the XML declaration included
	xmlDirective                // <!DOCTYPE …> and other <! … >
)

// xmlTok is one lexical unit of an XML text, src[start:end].
type xmlTok struct {
	kind       xmlKind
	start, end int
	name       string // start, end and empty tags
}

// xmlTokens splits src into tokens whose spans tile it exactly (a leading BOM
// aside). It is the formatter's lexer and the guard's (verify.go), so it
// must not panic on any input; an unterminated construct is a SyntaxError.
func xmlTokens(src string) ([]xmlTok, error) {
	// Every token but text starts at a '<', and text lies between them.
	toks := make([]xmlTok, 0, 2*strings.Count(src, "<")+1)
	i := 0
	if strings.HasPrefix(src, bom) {
		i = len(bom)
	}
	fail := func(at int, what string) ([]xmlTok, error) {
		line, col := position(src, at)
		return nil, &SyntaxError{Lang: LangXML, Line: line, Col: col, Msg: "unterminated " + what}
	}
	for i < len(src) {
		rest := src[i:]
		if rest[0] != '<' {
			n := strings.IndexByte(rest, '<')
			if n < 0 {
				n = len(rest)
			}
			toks = append(toks, xmlTok{kind: xmlText, start: i, end: i + n})
			i += n
			continue
		}
		var t xmlTok
		switch {
		case strings.HasPrefix(rest, "<!--"):
			n := strings.Index(rest[4:], "-->")
			if n < 0 {
				return fail(i, "comment")
			}
			t = xmlTok{kind: xmlComment, end: i + 4 + n + 3}
		case strings.HasPrefix(rest, "<![CDATA["):
			n := strings.Index(rest, "]]>")
			if n < 0 {
				return fail(i, "CDATA section")
			}
			t = xmlTok{kind: xmlCDATA, end: i + n + 3}
		case strings.HasPrefix(rest, "<?"):
			n := strings.Index(rest[2:], "?>")
			if n < 0 {
				return fail(i, "processing instruction")
			}
			t = xmlTok{kind: xmlPI, end: i + 2 + n + 2}
		case strings.HasPrefix(rest, "<!"):
			n := directiveLen(rest)
			if n < 0 {
				return fail(i, "directive")
			}
			t = xmlTok{kind: xmlDirective, end: i + n}
		case strings.HasPrefix(rest, "</"):
			n := strings.IndexByte(rest, '>')
			if n < 0 {
				return fail(i, "end tag")
			}
			t = xmlTok{kind: xmlEnd, end: i + n + 1, name: tagName(rest[2:n])}
		default:
			n := tagLen(rest)
			if n < 0 {
				return fail(i, "tag")
			}
			t = xmlTok{kind: xmlStart, end: i + n, name: tagName(rest[1 : n-1])}
			if rest[n-2] == '/' {
				t.kind = xmlEmpty
			}
		}
		t.start = i
		toks = append(toks, t)
		i = t.end
	}
	return toks, nil
}

// tagLen is the length of the start or empty tag s begins with, through its
// '>', skipping quoted attribute values; -1 if it never closes.
func tagLen(s string) int {
	var q byte
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case q != 0:
			if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		case c == '>':
			return i + 1
		}
	}
	return -1
}

// directiveLen is the length of the <!…> directive s begins with, as
// encoding/xml delimits it: quotes, nested <…> (a DOCTYPE's internal subset)
// and comments inside don't end it; -1 if it never closes. Like the decoder,
// it takes the byte after "<!" unexamined — "<!>" doesn't close there.
func directiveLen(s string) int {
	depth := 1
	var q byte
	for i := 3; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		case strings.HasPrefix(s[i:], "<!--"):
			n := strings.Index(s[i+4:], "-->")
			if n < 0 {
				return -1
			}
			i += 4 + n + 2
		case c == '<':
			depth++
		case c == '>':
			if depth--; depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// tagName is the element name at the start of a tag's inside.
func tagName(s string) string {
	if n := strings.IndexAny(s, " \t\r\n/>"); n >= 0 {
		return s[:n]
	}
	return s
}

// xmlNode is a token, or for an element its start tag through its end tag.
type xmlNode struct {
	xmlTok         // the token; for an element, its start tag
	spanEnd    int // end of the whole node (end tag included)
	closeStart int // element: where its end tag starts
	children   []*xmlNode
}

// xmlTree nests toks under a synthetic root, matching each end tag to its
// start tag — the check RawToken leaves out.
func xmlTree(src string, toks []xmlTok) (*xmlNode, error) {
	root := &xmlNode{kind: xmlStart}
	stack := []*xmlNode{root}
	errAt := func(off int, msg string) error {
		line, col := position(src, off)
		return &SyntaxError{Lang: LangXML, Line: line, Col: col, Msg: msg}
	}
	for _, t := range toks {
		top := stack[len(stack)-1]
		switch t.kind {
		case xmlStart:
			n := &xmlNode{xmlTok: t}
			top.children = append(top.children, n)
			stack = append(stack, n)
		case xmlEnd:
			if top == root {
				return nil, errAt(t.start, fmt.Sprintf("</%s> has no start tag", t.name))
			}
			if t.name != top.name {
				line, _ := position(src, top.start)
				return nil, errAt(t.start, fmt.Sprintf("</%s> does not close <%s> (line %d)", t.name, top.name, line))
			}
			top.closeStart, top.spanEnd = t.start, t.end
			stack = stack[:len(stack)-1]
		default:
			top.children = append(top.children, &xmlNode{xmlTok: t, spanEnd: t.end})
		}
	}
	if top := stack[len(stack)-1]; top != root {
		return nil, errAt(top.start, fmt.Sprintf("<%s> is never closed", top.name))
	}
	root.spanEnd = len(src)
	return root, nil
}

// xmlFmt accumulates formatted output.
type xmlFmt struct {
	src string
	ind string
	b   strings.Builder
}

// line writes s on a new line at depth.
func (f *xmlFmt) line(depth int, s string) {
	if f.b.Len() > 0 && f.b.String() != bom {
		f.b.WriteByte('\n')
	}
	for range depth {
		f.b.WriteString(f.ind)
	}
	f.b.WriteString(s)
}

func (f *xmlFmt) node(n *xmlNode, depth int) {
	kids := significant(f.src, n.children)
	if n.kind != xmlStart || len(kids) == 0 || xmlTextual(f.src, n) || preservesSpace(f.src[n.start:n.end]) {
		f.line(depth, f.src[n.start:n.spanEnd])
		return
	}
	f.line(depth, f.src[n.start:n.end])
	for _, c := range kids {
		f.node(c, depth+1)
	}
	f.line(depth, f.src[n.closeStart:n.spanEnd])
}

// significant is nodes without whitespace-only text.
func significant(src string, nodes []*xmlNode) []*xmlNode {
	out := make([]*xmlNode, 0, len(nodes))
	for _, c := range nodes {
		if c.kind != xmlText || strings.Trim(src[c.start:c.end], xmlSpace) != "" {
			out = append(out, c)
		}
	}
	return out
}

// xmlTextual reports whether n has a text child other than whitespace, or a
// CDATA section — the content whose whitespace is part of the value.
func xmlTextual(src string, n *xmlNode) bool {
	for _, c := range n.children {
		if c.kind == xmlCDATA || c.kind == xmlText && strings.Trim(src[c.start:c.end], xmlSpace) != "" {
			return true
		}
	}
	return false
}

const xmlSpace = " \t\r\n"

// preservesSpace reports whether a start tag carries xml:space="preserve".
func preservesSpace(tag string) bool {
	_, after, ok := strings.Cut(tag, "xml:space")
	if !ok {
		return false
	}
	rest := strings.TrimLeft(after, xmlSpace)
	if !strings.HasPrefix(rest, "=") {
		return false
	}
	rest = strings.TrimLeft(rest[1:], xmlSpace)
	return strings.HasPrefix(rest, `"preserve"`) || strings.HasPrefix(rest, `'preserve'`)
}
