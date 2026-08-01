package controls

import (
	"unicode"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// XML syntax highlighter (can be used as a Highlighter for Editor)
// ---------------------------------------------------------------------------

// xmlBlockState is what an unterminated multi-line construct a line can
// begin already inside — either an XML comment or a CDATA section, found by
// xmlOpenBlock. The two never nest inside each other in valid XML, so a
// single state (rather than a stack) is enough.
type xmlBlockState int

const (
	xmlNone xmlBlockState = iota
	xmlComment
	xmlCDATA
)

// Fixed delimiter literals, pre-converted to []rune once at package init
// rather than via []rune(literal) on every xmlHasPrefixAt/xmlFindClose call
// — those run once per '<' encountered across the whole document on every
// keystroke/redraw, so re-allocating a throwaway slice each time is wasted
// GC pressure for no benefit.
var (
	xmlCommentOpen  = []rune("<!--")
	xmlCommentClose = []rune("-->")
	xmlCDATAOpen    = []rune("<![CDATA[")
	xmlCDATAClose   = []rune("]]>")
)

// XMLHighlighter is the built-in XML syntax highlighter for Editor — used
// by PlanView's raw-XML tab (see planview.New) and by the query editor when
// a .xml file is opened via File > Open (see App.openQueryFile).
//
// Editor.Draw calls the returned Highlighter once per visible row, and Draw
// runs on every event the app processes — every keystroke, menu click, and
// mouse-move tick — for as long as the XML tab stays the visible/selected
// one, whether or not it holds keyboard focus. Determining whether a line
// starts inside an unterminated <!-- --> comment or <![CDATA[ ]]> section
// means replaying every prior line (xmlOpenBlock): O(N) per line, O(H*N) per
// Draw for a viewport of H rows, which is noticeably slow on large
// execution-plan XML.
//
// starts below holds the answer for every line, replayed once per change to
// the document and reused for every redraw in between — see prefixStates,
// and SQLHighlighter for why the previous one-line memo could not help the
// first row of a pass.
func XMLHighlighter(p *theme.Palette) Highlighter {
	tagStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorKeyword).Bold(true)
	attrStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorNumber)
	valStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorString)
	cmtStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorComment)

	var starts prefixStates[xmlBlockState]

	return func(doc *Document, idx int) []ColorRun {
		line := doc.Line(idx)
		runs := make([]ColorRun, 0, 8)
		i := 0

		startState := starts.at(doc, idx, xmlNone, xmlLineEndState)

		switch startState {
		case xmlComment:
			if end := xmlFindClose(line, 0, xmlCommentClose); end < 0 {
				return append(runs, ColorRun{0, len(line), cmtStyle})
			} else {
				runs = append(runs, ColorRun{0, end, cmtStyle})
				i = end
			}
		case xmlCDATA:
			if end := xmlFindClose(line, 0, xmlCDATAClose); end < 0 {
				return append(runs, ColorRun{0, len(line), valStyle})
			} else {
				runs = append(runs, ColorRun{0, end, valStyle})
				i = end
			}
		}

		for i < len(line) {
			switch {
			case line[i] == '<' && xmlHasPrefixAt(line, i, xmlCommentOpen):
				end := xmlFindClose(line, i+4, xmlCommentClose)
				if end < 0 {
					runs = append(runs, ColorRun{i, len(line) - i, cmtStyle})
					i = len(line)
				} else {
					runs = append(runs, ColorRun{i, end - i, cmtStyle})
					i = end
				}
			case line[i] == '<' && xmlHasPrefixAt(line, i, xmlCDATAOpen):
				end := xmlFindClose(line, i+9, xmlCDATAClose)
				if end < 0 {
					runs = append(runs, ColorRun{i, len(line) - i, valStyle})
					i = len(line)
				} else {
					runs = append(runs, ColorRun{i, end - i, valStyle})
					i = end
				}
			case line[i] == '<' && i+1 < len(line) && (line[i+1] == '!' || line[i+1] == '?'):
				// <?xml ...?> declaration or <!DOCTYPE ...> — styled as one
				// comment-colored run up to the first '>'. Doesn't track
				// cross-line state or a DOCTYPE internal subset's nested
				// '>'s; both are rare enough in practice (and never appear
				// in showplan XML) that treating this as always one line is
				// an accepted simplification, not a goal.
				j := i + 2
				for j < len(line) && line[j] != '>' {
					j++
				}
				if j < len(line) {
					j++
				}
				runs = append(runs, ColorRun{i, j - i, cmtStyle})
				i = j
			case line[i] == '<':
				// Opening or closing tag name — '<Foo', '</Foo'. The
				// delimiters themselves ('<', '</', '>', '/>') are left
				// unstyled, matching plain punctuation elsewhere.
				j := i + 1
				if j < len(line) && line[j] == '/' {
					j++
				}
				start := j
				for j < len(line) && xmlNameChar(line[j]) {
					j++
				}
				if j > start {
					runs = append(runs, ColorRun{i, j - i, tagStyle})
				}
				i = j
			case xmlNameStart(line[i]):
				j := i
				for j < len(line) && xmlNameChar(line[j]) {
					j++
				}
				// Only an attribute name if a (possibly space-padded) '='
				// immediately follows — otherwise it's plain element text
				// content and gets no run at all.
				k := j
				for k < len(line) && line[k] == ' ' {
					k++
				}
				if k < len(line) && line[k] == '=' {
					runs = append(runs, ColorRun{i, j - i, attrStyle})
					i = k + 1
					for i < len(line) && line[i] == ' ' {
						i++
					}
					if i < len(line) && (line[i] == '"' || line[i] == '\'') {
						q := line[i]
						vstart := i
						i++
						for i < len(line) && line[i] != q {
							i++
						}
						if i < len(line) {
							i++
						}
						runs = append(runs, ColorRun{vstart, i - vstart, valStyle})
					}
				} else {
					i = j
				}
			default:
				i++
			}
		}
		return runs
	}
}

// xmlNameStart reports whether r can begin an XML element or attribute
// name.
func xmlNameStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_' || r == ':'
}

// xmlNameChar reports whether r can continue an XML element or attribute
// name after its first character.
func xmlNameChar(r rune) bool {
	return xmlNameStart(r) || unicode.IsDigit(r) || r == '-' || r == '.'
}

// xmlHasPrefixAt reports whether line has the runes of prefix starting at
// i. prefix is one of the package-level xml*Open/xml*Close vars, already
// []rune — callers must not pass a freshly-converted string literal here,
// since avoiding that allocation on this hot per-character path is the
// point.
func xmlHasPrefixAt(line []rune, i int, prefix []rune) bool {
	if i+len(prefix) > len(line) {
		return false
	}
	for k, c := range prefix {
		if line[i+k] != c {
			return false
		}
	}
	return true
}

// xmlFindClose returns the rune index right after the first occurrence of
// closer found in line at or after from, or -1 if it doesn't close on this
// line (it continues onto the next one).
func xmlFindClose(line []rune, from int, closer []rune) int {
	for j := from; j+len(closer) <= len(line); j++ {
		if xmlHasPrefixAt(line, j, closer) {
			return j + len(closer)
		}
	}
	return -1
}

// xmlOpenBlock reports whether line idx begins already inside an
// unterminated <!-- --> comment or <![CDATA[ ]]> section carried over from
// an earlier line — found by replaying every line before it and toggling
// state on each construct's start/end delimiter, mirroring
// startsInBlockComment in sql_highlighter.go. This full replay is O(idx);
// XMLHighlighter's closure only falls back to it for the first row of a
// Draw pass (or a non-contiguous jump), not per visible row — see its doc
// comment.
func xmlOpenBlock(lines [][]rune, idx int) xmlBlockState {
	state := xmlNone
	for i := 0; i < idx; i++ {
		state = xmlLineEndState(lines[i], state)
	}
	return state
}

// xmlLineEndState scans one full line, honoring a comment/CDATA section
// carried in from a previous line via state, and returns the state to carry
// into the next one.
func xmlLineEndState(line []rune, state xmlBlockState) xmlBlockState {
	i := 0
	for i < len(line) {
		switch state {
		case xmlComment:
			end := xmlFindClose(line, i, xmlCommentClose)
			if end < 0 {
				return xmlComment
			}
			state, i = xmlNone, end
		case xmlCDATA:
			end := xmlFindClose(line, i, xmlCDATAClose)
			if end < 0 {
				return xmlCDATA
			}
			state, i = xmlNone, end
		default:
			switch {
			case line[i] == '<' && xmlHasPrefixAt(line, i, xmlCommentOpen):
				state, i = xmlComment, i+4
			case line[i] == '<' && xmlHasPrefixAt(line, i, xmlCDATAOpen):
				state, i = xmlCDATA, i+9
			default:
				i++
			}
		}
	}
	return state
}
