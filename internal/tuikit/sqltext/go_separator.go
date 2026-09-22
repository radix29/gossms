package sqltext

import (
	"math"
	"unicode"
)

// GoSeparatorAt reports whether the line beginning at buf[start] — running to
// the next '\n' or to limit — is a "GO" batch separator. If it is, next is the
// offset of the line after it and count the number of times the batch before
// it runs: the line's repeat count, or 1 if it has none.
//
// A separator is optional leading whitespace, "GO" (case-insensitive, and not
// the head of a longer word like "goto" or "gone"), then only whitespace, at
// most one ASCII repeat count, and/or a trailing "--" line comment. Anything
// else on the line — "GO;", "GO x", "GO/*c*/", "GO 1 2" — makes it an ordinary
// line of SQL, which the server then rejects. A digit inside the comment is
// not a count: "GO -- step 2" runs the batch once.
//
// A count too large for an int saturates at math.MaxInt; a run that long is
// ended by cancelling it, as any other. "GO 0" runs the batch no times.
//
// The scan bails on the first rune that can't be part of a separator, so an
// ordinary line costs a rune or two rather than a walk to its end: IntelliSense
// runs this once per line of the whole prefix on every keystroke while its
// popup is open.
func GoSeparatorAt(buf []rune, start, limit int) (next, count int, ok bool) {
	i := start
	for i < limit && buf[i] != '\n' && unicode.IsSpace(buf[i]) {
		i++
	}
	if i+1 >= limit ||
		(buf[i] != 'G' && buf[i] != 'g') ||
		(buf[i+1] != 'O' && buf[i+1] != 'o') {
		return 0, 0, false
	}
	i += 2
	if i < limit && (unicode.IsLetter(buf[i]) || unicode.IsDigit(buf[i]) || buf[i] == '_') {
		return 0, 0, false
	}
	count = 1
	sawCount := false
	for i < limit && buf[i] != '\n' {
		switch {
		case unicode.IsSpace(buf[i]):
			i++
		case buf[i] >= '0' && buf[i] <= '9' && !sawCount:
			sawCount = true
			count = 0
			for i < limit && buf[i] >= '0' && buf[i] <= '9' {
				d := int(buf[i] - '0')
				if count > (math.MaxInt-d)/10 {
					count = math.MaxInt
				} else if count != math.MaxInt {
					count = count*10 + d
				}
				i++
			}
			// "GO 5x": the count must end at whitespace, a comment or the line's end.
			if i < limit && buf[i] != '\n' && !unicode.IsSpace(buf[i]) && buf[i] != '-' {
				return 0, 0, false
			}
		case buf[i] == '-' && i+1 < limit && buf[i+1] == '-':
			// The rest of the line is a comment; skip to its end.
			for i < limit && buf[i] != '\n' {
				i++
			}
		default:
			return 0, 0, false
		}
	}
	return min(i+1, limit), count, true
}

// IsGoSeparatorLine applies GoSeparatorAt's rule to a standalone line.
func IsGoSeparatorLine(line []rune) bool {
	_, _, ok := GoSeparatorAt(line, 0, len(line))
	return ok
}
