package tui

import (
	"slices"
	"strings"

	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/tuikit/controls/editor"
)

func completionCallItem(buf []rune, cursor int, label, detail string, window bool) editor.CompletionItem {
	if completionBuiltins[label].kind&builtinBare != 0 {
		return editor.CompletionItem{Text: label, Label: label, Detail: detail}
	}
	text, after, offset := keywordFunctionTemplate(buf, cursor, label, window)
	return editor.CompletionItem{Text: text, Label: label, Detail: detail, ReplaceAfter: after, CursorOffset: offset}
}

// consumed is an absolute buffer offset; caret is relative to inserted text.
// Keep trivia and existing call contents verbatim when reusing the suffix.
func completionCallEdit(buf []rune, cursor int, label string, rebuild bool) (text string, consumed, caret int, closed bool) {
	text, consumed = completionPhrasePrefix(buf, cursor, label)
	open := keywordNextToken(buf, consumed)
	if open.Kind == sqltext.KindPunct && buf[open.Start] == '(' {
		caret = len([]rune(text)) + open.End - cursor
		if !rebuild && completionBuiltins[label].kind&builtinNoArgs == 0 {
			return text, cursor, caret, true
		}
		end, ok := keywordCallEnd(buf, open)
		if !ok || !rebuild {
			if ok && completionBuiltins[label].kind&builtinNoArgs != 0 {
				caret = len([]rune(text)) + end - cursor
			}
			return text, cursor, caret, ok
		}
		text += string(buf[cursor:end])
		if completionBuiltins[label].kind&builtinNoArgs != 0 {
			caret = len([]rune(text))
		}
		return text, end, caret, true
	}
	text += string(buf[cursor:consumed])
	if label == "OPENROWSET" {
		text += "(BULK '')"
		return text, consumed, len([]rune(text)) - len("')"), true
	}
	if label == "VALUES" || strings.HasSuffix(label, "EXISTS") {
		text += " "
	}
	text += "()"
	caret = len([]rune(text))
	if completionBuiltins[label].kind&builtinNoArgs == 0 {
		caret--
	}
	return text, consumed, caret, true
}

func keywordFunctionTemplate(buf []rune, cursor int, label string, window bool) (text string, replaceAfter, offset int) {
	text, consumed, caret, closed := completionCallEdit(buf, cursor, label, window || completionBuiltins[label].kind&builtinOrdered != 0)
	if !closed {
		return text, consumed - cursor, caret - len([]rune(text))
	}
	appendClause := func(words, template string, focus bool) bool {
		first, _, _ := strings.Cut(words, " ")
		next := keywordNextToken(buf, consumed)
		if next.Kind == sqltext.KindWord {
			word := strings.ToUpper(string(buf[next.Start:next.End]))
			if word != first && strings.HasPrefix(first, word) {
				if focus {
					caret = len([]rune(text)) + next.End - consumed
				}
				return false
			}
		}
		if keywordNextIs(buf, consumed, first) && !keywordNextIs(buf, consumed, words) {
			return false
		}
		if keywordNextIs(buf, consumed, words) {
			pos := consumed
			for range strings.Fields(words) {
				pos = keywordNextToken(buf, pos).End
			}
			open := keywordNextToken(buf, pos)
			end, ok := keywordCallEnd(buf, open)
			if !ok {
				if focus {
					target := open.Start
					if open.Kind == sqltext.KindEnd {
						target = pos
						for target < len(buf) && keywordWhitespace(buf[target]) {
							target++
						}
					} else if open.Kind == sqltext.KindPunct && buf[open.Start] == '(' {
						target = open.End
					}
					caret = len([]rune(text)) + target - consumed
				}
				return false
			}
			if focus {
				caret = len([]rune(text)) + open.End - consumed
			}
			text += string(buf[consumed:end])
			consumed = end
			return true
		}
		text += template
		if focus {
			caret = len([]rune(text)) - 1
		}
		return true
	}
	if completionBuiltins[label].kind&builtinOrdered != 0 {
		if !appendClause("WITHIN GROUP", " WITHIN GROUP (ORDER BY )", false) {
			return text, consumed - cursor, caret - len([]rune(text))
		}
	}
	if window {
		if slices.Contains([]string{"LAG", "LEAD", "FIRST_VALUE", "LAST_VALUE"}, label) {
			modifier := keywordNextToken(buf, consumed)
			if modifier.Kind == sqltext.KindWord {
				word := strings.ToUpper(string(buf[modifier.Start:modifier.End]))
				if strings.HasPrefix("IGNORE", word) || strings.HasPrefix("RESPECT", word) {
					if !keywordNextIs(buf, consumed, "IGNORE NULLS") && !keywordNextIs(buf, consumed, "RESPECT NULLS") {
						return text, consumed - cursor, caret - len([]rune(text))
					}
					end := keywordNextToken(buf, modifier.End).End
					text += string(buf[consumed:end])
					consumed = end
				}
			}
		}
		kind := completionBuiltins[label].kind
		template := " OVER ()"
		if kind&builtinPartitionOnly == 0 && kind&(builtinAggregate|builtinFabricAggregate) == 0 {
			template = " OVER (ORDER BY )"
		}
		appendClause("OVER", template, kind&builtinNoArgs != 0)
	}
	return text, consumed - cursor, caret - len([]rune(text))
}

func keywordCallEnd(buf []rune, open sqltext.Token) (int, bool) {
	if open.Kind != sqltext.KindPunct || buf[open.Start] != '(' {
		return 0, false
	}
	depth := 0
	for pos := open.Start; pos < len(buf); {
		tok, _ := sqltext.Next(buf, pos, len(buf), sqltext.State{})
		if tok.Kind == sqltext.KindEnd {
			break
		}
		pos = tok.End
		if tok.Kind != sqltext.KindPunct {
			continue
		}
		switch buf[tok.Start] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return tok.End, true
			}
		}
	}
	return 0, false
}
