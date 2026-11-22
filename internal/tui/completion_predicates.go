package tui

import (
	"strings"

	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/gossms/internal/tui/sqlparse"
)

var predicateOperators = []string{
	"=", "<>", "!=", ">", ">=", "<", "<=",
	"+", "-", "*", "/", "%", "||",
	"IS NULL", "IS NOT NULL", "IS DISTINCT FROM", "IS NOT DISTINCT FROM",
	"LIKE", "NOT LIKE", "IN", "NOT IN", "BETWEEN", "NOT BETWEEN",
}

type predicateFrame struct {
	operand, comparison, value bool
	group                      bool
	between, is, cases         int
	not, quantified            bool
}

func scanPredicate(buf []rune, start, end int) (predicateFrame, bool) {
	stack := []predicateFrame{{}}
	previousDot, afterComparison := false, false
	for i := start; i < end; {
		tok, _ := sqltext.Next(buf, i, end, sqltext.State{})
		if tok.Kind == sqltext.KindEnd {
			break
		}
		i = tok.End
		f := &stack[len(stack)-1]
		if tok.Kind == sqltext.KindComment || tok.Kind == sqltext.KindNewline {
			continue
		}
		qualified := previousDot
		previousDot = tok.Kind == sqltext.KindPunct && buf[tok.Start] == '.'
		quantifierExpected := afterComparison
		afterComparison = false
		word := ""
		if tok.Kind == sqltext.KindWord {
			word = strings.ToUpper(string(buf[tok.Start:tok.End]))
		}
		if word == "CASE" {
			f.cases++
			f.operand = false
			continue
		}
		if f.cases > 0 {
			if word == "END" {
				f.cases--
				f.operand = f.cases == 0
			}
			continue
		}
		if tok.Kind == sqltext.KindPunct {
			switch buf[tok.Start] {
			case '(':
				stack = append(stack, predicateFrame{group: !f.operand && !f.comparison && !f.not && !f.value})
			case ')':
				if len(stack) > 1 {
					child := *f
					stack = stack[:len(stack)-1]
					f = &stack[len(stack)-1]
					f.comparison = f.comparison || child.group && child.comparison
				}
				f.operand = true
			case '=', '<', '>', '!':
				op := buf[tok.Start]
				afterComparison = f.operand && op != '!'
				if i < end && (op == '<' && buf[i] == '>' ||
					(op == '<' || op == '>') && buf[i] == '=' ||
					op == '!' && strings.ContainsRune("=<>", buf[i])) {
					i++
					afterComparison = f.operand
				}
				f.comparison, f.operand = true, false
			default:
				f.operand = false
				f.value = f.value || strings.ContainsRune("+-*/%|", buf[tok.Start])
			}
			continue
		}
		switch word {
		case "SELECT":
			f.group, f.operand = false, false
		case "AND", "OR":
			if word == "AND" && f.between == 1 {
				f.between, f.operand = 2, false
			} else {
				group := f.group
				*f = predicateFrame{group: group}
			}
		case "IS":
			f.comparison, f.operand, f.is = true, false, 1
		case "NOT":
			if f.is > 0 {
				f.is = 2
			} else if f.operand && !f.comparison {
				f.not, f.operand = true, false
			}
		case "NULL":
			f.is, f.operand = 0, true
		case "DISTINCT":
			if f.is > 0 {
				f.is, f.operand = 3, false
			}
		case "FROM":
			if f.is == 3 {
				f.is, f.operand = 0, false
			}
		case "LIKE", "IN", "BETWEEN":
			f.comparison, f.operand, f.not = true, false, false
			if word == "BETWEEN" {
				f.between = 1
			}
		case "ALL", "ANY", "SOME":
			if quantifierExpected {
				f.quantified = true
			} else {
				f.operand = true
			}
		case "EXISTS", "CONTAINS", "FREETEXT", "REGEXP_LIKE":
			if qualified {
				f.operand = true
				continue
			}
			f.comparison, f.operand, f.quantified = true, false, true
		case "COLLATE", "ESCAPE":
			f.operand = false
		default:
			f.operand = true
		}
	}
	f := stack[len(stack)-1]
	return f, !f.comparison && !f.value && (len(stack) == 1 || f.group)
}

func predicateSubquery(buf []rune, parent []sqlparse.Token, start, open int) bool {
	n := len(parent)
	if n == 0 {
		return false
	}
	last := parent[n-1]
	if keywordIs(last, "EXISTS") {
		return true
	}
	if !keywordIs(last, "ALL") && !keywordIs(last, "ANY") && !keywordIs(last, "SOME") ||
		keywordNextToken(buf[:open], last.Start+len(last.Text)).Kind != sqltext.KindEnd {
		return false
	}
	// Use the raw tail; the reduced stream has neither the comparison
	// operator nor a string literal that might be its left operand.
	if n >= 2 {
		start = parent[n-2].Start
	}
	f, _ := scanPredicate(buf, start, open)
	return f.quantified && !f.operand
}

func predicateExpressionPosition(buf []rune, start, end int) (scalar, condition bool) {
	f, condition := scanPredicate(buf, start, end)
	scalar = !f.operand && f.is == 0 && !f.not && !f.quantified && f.cases == 0
	return scalar, scalar && condition
}

// The reduced sqlparse stream omits operators and literals, so inspect the
// current predicate with the shared lexer.
func predicateChoices(buf []rune, start, end int, fallback []string) (choices []string, operatorExpected, keywordsOnly bool) {
	f, _ := scanPredicate(buf, start, end)
	if f.cases > 0 {
		return nil, false, false
	}
	switch {
	case f.is == 1:
		return []string{"NULL", "NOT NULL", "DISTINCT FROM", "NOT DISTINCT FROM"}, false, true
	case f.is == 2:
		return []string{"NULL", "DISTINCT FROM"}, false, true
	case f.is == 3:
		return []string{"FROM"}, false, true
	case f.not:
		return []string{"LIKE", "IN", "BETWEEN"}, false, true
	case f.quantified && !f.operand:
		return nil, false, true
	case !f.operand:
		return fallback, false, false
	case f.between == 1:
		return []string{"AND"}, false, true
	case !f.comparison:
		return predicateOperators, true, true
	default:
		return fallback, false, false
	}
}

func predicateClause(word string) bool {
	return word == "WHERE" || word == "ON" || word == "HAVING" || word == "QUALIFY" || word == "WHEN"
}
