package controls

import (
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// SQL syntax highlighter (can be used as a Highlighter for Editor)
// ---------------------------------------------------------------------------

// sqlKeywords is the full T-SQL keyword/built-in-function set highlighted
// as a keyword: reserved words, data types, constants/system variables,
// control flow, and built-in functions, grouped by category below.
var sqlKeywords = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true, "INSERT": true, "UPDATE": true,
	"DELETE": true, "CREATE": true, "DROP": true, "ALTER": true, "TABLE": true,
	"INDEX": true, "VIEW": true, "PROCEDURE": true, "FUNCTION": true, "TRIGGER": true,
	"DATABASE": true, "SCHEMA": true, "AND": true, "OR": true, "NOT": true,
	"IN": true, "IS": true, "NULL": true, "LIKE": true, "BETWEEN": true,
	"JOIN": true, "INNER": true, "LEFT": true, "RIGHT": true, "FULL": true,
	"OUTER": true, "ON": true, "AS": true, "ORDER": true, "BY": true,
	"GROUP": true, "HAVING": true, "DISTINCT": true, "TOP": true, "LIMIT": true,
	"OFFSET": true, "UNION": true, "ALL": true, "EXISTS": true, "CASE": true,
	"WHEN": true, "THEN": true, "ELSE": true, "END": true, "IF": true,
	"BEGIN": true, "COMMIT": true, "ROLLBACK": true, "TRANSACTION": true,
	"EXEC": true, "EXECUTE": true, "SET": true, "USE": true, "GO": true,
	"WITH": true, "DECLARE": true, "PRINT": true, "RETURN": true,
	"INT": true, "BIGINT": true, "VARCHAR": true, "NVARCHAR": true, "CHAR": true,
	"NCHAR": true, "TEXT": true, "NTEXT": true, "DATETIME": true, "DATE": true,
	"TIME": true, "BIT": true, "FLOAT": true, "DECIMAL": true, "NUMERIC": true,
	"MONEY": true, "UNIQUEIDENTIFIER": true, "VARBINARY": true,
	"PRIMARY": true, "KEY": true, "FOREIGN": true, "REFERENCES": true,
	"CONSTRAINT": true, "DEFAULT": true, "IDENTITY": true, "UNIQUE": true,
	"CHECK": true, "CASCADE": true,

	// Reserved keywords (todo/keywords.md) not already covered above.
	"ADD": true, "ANY": true, "ASC": true, "AUTHORIZATION": true, "BACKUP": true,
	"BREAK": true, "BROWSE": true, "BULK": true, "CHECKPOINT": true, "CLOSE": true,
	"CLUSTERED": true, "COALESCE": true, "COLLATE": true, "COLUMN": true,
	"COMPUTE": true, "CONTAINS": true, "CONTAINSTABLE": true, "CONTINUE": true,
	"CONVERT": true, "CROSS": true, "CURRENT": true, "CURRENT_DATE": true,
	"CURRENT_TIME": true, "CURRENT_TIMESTAMP": true, "CURRENT_USER": true,
	"CURSOR": true, "DBCC": true, "DEALLOCATE": true, "DENY": true, "DESC": true,
	"DISK": true, "DISTRIBUTED": true, "DOUBLE": true, "DUMP": true, "ERRLVL": true,
	"ESCAPE": true, "EXCEPT": true, "EXIT": true, "EXTERNAL": true, "FETCH": true,
	"FILE": true, "FILLFACTOR": true, "FOR": true, "FREETEXT": true,
	"FREETEXTTABLE": true, "GOTO": true, "GRANT": true, "HOLDLOCK": true,
	"IDENTITY_INSERT": true, "IDENTITYCOL": true, "INTERSECT": true, "INTO": true,
	"KILL": true, "LINENO": true, "LOAD": true, "MERGE": true, "NATIONAL": true,
	"NOCHECK": true, "NONCLUSTERED": true, "NULLIF": true, "OF": true, "OFF": true,
	"OFFSETS": true, "OPEN": true, "OPENDATASOURCE": true, "OPENQUERY": true,
	"OPENROWSET": true, "OPENXML": true, "OPTION": true, "OVER": true,
	"PERCENT": true, "PIVOT": true, "PLAN": true, "PRECISION": true, "PROC": true,
	"PUBLIC": true, "RAISERROR": true, "READ": true, "READTEXT": true,
	"RECONFIGURE": true, "REPLICATION": true, "RESTORE": true, "RESTRICT": true,
	"REVERT": true, "REVOKE": true, "ROWCOUNT": true, "ROWGUIDCOL": true,
	"RULE": true, "SAVE": true, "SECURITYAUDIT": true,
	"SEMANTICKEYPHRASETABLE": true, "SEMANTICSIMILARITYDETAILSTABLE": true,
	"SEMANTICSIMILARITYTABLE": true, "SESSION_USER": true, "SETUSER": true,
	"SHUTDOWN": true, "SOME": true, "STATISTICS": true, "SYSTEM_USER": true,
	"TABLESAMPLE": true, "TEXTSIZE": true, "TO": true, "TRAN": true,
	"TRUNCATE": true, "TRY_CONVERT": true, "TSEQUAL": true, "UNPIVOT": true,
	"UPDATETEXT": true, "USER": true, "VALUES": true, "VARYING": true,
	"WAITFOR": true, "DELAY": true, "WHILE": true, "WITHIN": true, "WRITETEXT": true,

	// Data types (todo/keywords.md) not already covered above.
	"BINARY": true, "DATETIME2": true, "DATETIMEOFFSET": true, "DEC": true,
	"GEOGRAPHY": true, "GEOMETRY": true, "HIERARCHYID": true, "IMAGE": true,
	"JSON": true, "REAL": true, "ROWVERSION": true, "SMALLDATETIME": true,
	"SMALLINT": true, "SMALLMONEY": true, "SQL_VARIANT": true, "TIMESTAMP": true,
	"TINYINT": true, "VECTOR": true, "XML": true,

	// Constants, system variables, and control-flow words (todo/keywords.md)
	// not already covered above.
	"TRUE": true, "FALSE": true, "@@IDENTITY": true, "@@ROWCOUNT": true,
	"@@ERROR": true, "@@TRANCOUNT": true, "@@VERSION": true,
	"TRY": true, "CATCH": true, "THROW": true,

	// Built-in functions, by category. Entries already listed above as
	// reserved words or data types (CHAR, LEFT, RIGHT, NCHAR, CONVERT,
	// TRY_CONVERT, COALESCE, NULLIF, FOR, XML, OPENXML, GEOGRAPHY,
	// GEOMETRY) aren't repeated here.
	"AVG": true, "CHECKSUM_AGG": true, "COUNT": true, "COUNT_BIG": true,
	"GROUPING": true, "GROUPING_ID": true, "MAX": true, "MIN": true,
	"STDEV": true, "STDEVP": true, "STRING_AGG": true, "SUM": true, "VAR": true,
	"VARP": true,

	"ASCII": true, "CHARINDEX": true, "CONCAT": true, "CONCAT_WS": true,
	"DIFFERENCE": true, "FORMAT": true, "LEN": true, "LOWER": true, "LTRIM": true,
	"PATINDEX": true, "QUOTENAME": true, "REPLACE": true, "REPLICATE": true,
	"REVERSE": true, "RTRIM": true, "SOUNDEX": true, "SPACE": true,
	"STRING_ESCAPE": true, "STRING_SPLIT": true, "STUFF": true, "SUBSTRING": true,
	"TRANSLATE": true, "TRIM": true, "UNICODE": true, "UPPER": true,

	"DATEADD": true, "DATEDIFF": true, "DATEDIFF_BIG": true,
	"DATEFROMPARTS": true, "DATENAME": true, "DATEPART": true,
	"DATETIME2FROMPARTS": true, "DATETIMEFROMPARTS": true, "DAY": true,
	"EOMONTH": true, "GETDATE": true, "GETUTCDATE": true, "MONTH": true,
	"SMALLDATETIMEFROMPARTS": true, "SYSDATETIME": true,
	"SYSDATETIMEOFFSET": true, "SYSUTCDATETIME": true, "TIMEFROMPARTS": true,
	"YEAR": true,

	"ABS": true, "ACOS": true, "ASIN": true, "ATAN": true, "ATN2": true,
	"CEILING": true, "COS": true, "COT": true, "DEGREES": true, "EXP": true,
	"FLOOR": true, "LOG": true, "LOG10": true, "PI": true, "POWER": true,
	"RADIANS": true, "RAND": true, "ROUND": true, "SIGN": true, "SIN": true,
	"SQRT": true, "SQUARE": true, "TAN": true,

	"CAST": true, "PARSE": true, "TRY_CAST": true, "TRY_PARSE": true,

	"CHOOSE": true, "IIF": true, "ISNULL": true,

	"ISJSON": true, "JSON_ARRAY": true, "JSON_MODIFY": true, "JSON_OBJECT": true,
	"JSON_PATH_EXISTS": true, "JSON_QUERY": true, "JSON_VALUE": true,
	"OPENJSON": true,

	"APP_NAME": true, "DB_ID": true, "DB_NAME": true, "HOST_NAME": true,
	"NEWID": true, "NEWSEQUENTIALID": true, "OBJECT_ID": true,
	"OBJECT_NAME": true, "SCOPE_IDENTITY": true, "SESSION_CONTEXT": true,
	"SUSER_ID": true, "SUSER_NAME": true, "SUSER_SNAME": true, "USER_ID": true,
	"USER_NAME": true,

	"CHECKSUM": true, "BINARY_CHECKSUM": true, "HASHBYTES": true,

	"CUME_DIST": true, "DENSE_RANK": true, "FIRST_VALUE": true, "LAG": true,
	"LAST_VALUE": true, "LEAD": true, "NTILE": true, "PERCENT_RANK": true,
	"PERCENTILE_CONT": true, "PERCENTILE_DISC": true, "RANK": true,
	"ROW_NUMBER": true,

	"STGEOMFROMTEXT": true,
}

// SQLHighlighter is the built-in SQL syntax highlighter for Editor.
//
// The returned Highlighter is stateful and belongs to exactly one Editor —
// see the memo below. Build a fresh one per Editor (as every call site
// does); sharing one across two editors would let one document's carried-over
// block-comment state colour the other's.
//
// Editor.Draw calls it once per visible row, in strictly increasing line-index
// order within a Draw pass, and Draw runs on every event the app processes —
// every keystroke included. Deciding whether a line starts inside an
// unterminated /* */ means replaying every prior line (startsInBlockComment):
// O(N) per line, O(H*N) per Draw for a viewport of H rows. Measured on a
// 40-row viewport scrolled to the bottom of the document, that is ~4.6ms per
// pass at 1,000 lines and ~48ms at 10,000 — i.e. typing in a large script is
// bounded by the highlighter. The closure below caches the end-of-line state
// from the immediately preceding call and reuses it in O(1) when the new call
// continues that sequence (idx == lastIdx+1), which is every row but the first
// in a pass; that drops the 10,000-line pass to ~1.4ms. Only the first row of
// a pass, or a non-contiguous jump (the view just scrolled), pays the replay.
// Same treatment, for the same reason, as XMLHighlighter (xml_highlighter.go).
//
// The memo is safe across edits because a pass always starts at Editor's
// scrollRow: every mutation scrolls the cursor into view and redraws before
// the next pass, so a stale state can never be inherited by the row after an
// edited one, and SetText resets scrollRow to 0 (where the state is
// unconditionally "not in a comment"), so replacing the document can't be
// mistaken for a continuation of the old one.
func SQLHighlighter(p *theme.Palette) Highlighter {
	kwStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorKeyword).Bold(true)
	strStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorString)
	cmtStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorComment)
	numStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorNumber)

	lastIdx := -1
	lastEndState := false

	return func(lines [][]rune, idx int) []ColorRun {
		line := lines[idx]
		runs := make([]ColorRun, 0, 8)
		i := 0

		// A block comment carried over, unterminated, from an earlier line.
		startsInComment := lastEndState
		if idx != lastIdx+1 {
			startsInComment = startsInBlockComment(lines, idx)
		}
		// Recorded up front, and via the same per-line step the replay uses,
		// so the two can't disagree — see blockCommentToggleEnd. Nothing below
		// influences it, so there's no exit path that can forget to set it.
		lastIdx, lastEndState = idx, blockCommentToggleEnd(line, startsInComment)

		if startsInComment {
			end := blockCommentEnd(line, 0)
			if end < 0 {
				return append(runs, ColorRun{0, len(line), cmtStyle})
			}
			runs = append(runs, ColorRun{0, end, cmtStyle})
			i = end
		}

		for i < len(line) {
			// Block comment
			if i+1 < len(line) && line[i] == '/' && line[i+1] == '*' {
				end := blockCommentEnd(line, i+2)
				if end < 0 {
					runs = append(runs, ColorRun{i, len(line) - i, cmtStyle})
					break
				}
				runs = append(runs, ColorRun{i, end - i, cmtStyle})
				i = end
				continue
			}
			// Line comment
			if i+1 < len(line) && line[i] == '-' && line[i+1] == '-' {
				runs = append(runs, ColorRun{i, len(line) - i, cmtStyle})
				break
			}
			// String literal
			if line[i] == '\'' {
				j := i + 1
				for j < len(line) && line[j] != '\'' {
					j++
				}
				if j < len(line) {
					j++
				}
				runs = append(runs, ColorRun{i, j - i, strStyle})
				i = j
				continue
			}
			// Number
			if unicode.IsDigit(line[i]) {
				j := i
				for j < len(line) && (unicode.IsDigit(line[j]) || line[j] == '.') {
					j++
				}
				runs = append(runs, ColorRun{i, j - i, numStyle})
				i = j
				continue
			}
			// Word — a leading '@'/'@@' (local variable / system variable
			// like @@ROWCOUNT) or '#'/'##' (temp table) isn't itself a
			// letter/digit/'_', so it must be consumed by its own loop
			// before the identifier-body loop below runs; otherwise that
			// loop's condition is already false at j==i and never
			// advances, spinning forever on the same '@'/'#'.
			if unicode.IsLetter(line[i]) || line[i] == '_' || line[i] == '@' || line[i] == '#' {
				j := i
				for j < len(line) && (line[j] == '@' || line[j] == '#') {
					j++
				}
				for j < len(line) && (unicode.IsLetter(line[j]) || unicode.IsDigit(line[j]) || line[j] == '_') {
					j++
				}
				if sqlKeywords[strings.ToUpper(string(line[i:j]))] {
					runs = append(runs, ColorRun{i, j - i, kwStyle})
				}
				i = j
				continue
			}
			i++
		}
		return runs
	}
}

// blockCommentEnd returns the rune index right after the first "*/" found
// in line at or after from, or -1 if the comment doesn't close on this
// line (it continues onto the next one).
func blockCommentEnd(line []rune, from int) int {
	for j := from; j+1 < len(line); j++ {
		if line[j] == '*' && line[j+1] == '/' {
			return j + 2
		}
	}
	return -1
}

// blockCommentToggleEnd reports whether line leaves an unterminated /* open,
// given whether it started inside one — toggling on every "/*"/"*/" pair.
// Like the rest of this highlighter, it doesn't account for one of those
// appearing inside a string literal or after a "--" line comment; that's an
// accepted simplification, not a goal.
//
// This is the single definition of that per-line step, shared by
// startsInBlockComment's replay and SQLHighlighter's memo. They must agree
// exactly: the replay is still what the first row of a Draw pass uses, so a
// memo built on the main highlight loop's own (more accurate — it does stop at
// a "--" and skip string literals) notion of "ends inside a comment" would
// colour a line differently depending only on whether it happened to be the
// first visible row, which reads as a flicker while scrolling. Making the
// simplification consistent is the fix for that; making it *correct* is a
// separate change — see docs/open-threads.md.
func blockCommentToggleEnd(line []rune, in bool) bool {
	for j := 0; j < len(line); {
		if in {
			end := blockCommentEnd(line, j)
			if end < 0 {
				break // the rest of this line stays inside the comment
			}
			in = false
			j = end
			continue
		}
		if j+1 < len(line) && line[j] == '/' && line[j+1] == '*' {
			in = true
			j += 2
			continue
		}
		j++
	}
	return in
}

// startsInBlockComment reports whether line idx begins already inside an
// unterminated /* ... */ block comment carried over from an earlier line —
// found by replaying blockCommentToggleEnd across lines[0:idx].
func startsInBlockComment(lines [][]rune, idx int) bool {
	in := false
	for i := 0; i < idx; i++ {
		in = blockCommentToggleEnd(lines[i], in)
	}
	return in
}
