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
// as a keyword — see todo/keywords.md (reserved words, data types,
// constants/system variables, control flow) and todo/functions.md
// (built-in functions by category), which are the source lists these
// blocks mirror.
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

	// Built-in functions (todo/functions.md), by the same categories used
	// there. Entries already listed above as reserved words or data types
	// (CHAR, LEFT, RIGHT, NCHAR, CONVERT, TRY_CONVERT, COALESCE, NULLIF,
	// FOR, XML, OPENXML, GEOGRAPHY, GEOMETRY) aren't repeated here.
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
func SQLHighlighter(p *theme.Palette) Highlighter {
	kwStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorKeyword).Bold(true)
	strStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorString)
	cmtStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorComment)
	numStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorNumber)

	return func(lines [][]rune, idx int) []ColorRun {
		line := lines[idx]
		runs := make([]ColorRun, 0, 8)
		i := 0

		// A block comment carried over, unterminated, from an earlier line.
		if startsInBlockComment(lines, idx) {
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

// startsInBlockComment reports whether line idx begins already inside an
// unterminated /* ... */ block comment carried over from an earlier line —
// found by toggling on every "/*"/"*/" pair across lines[0:idx]. Like the
// rest of this highlighter, it doesn't also account for one of those
// appearing inside a string literal or after a "--" line comment; that's an
// accepted simplification, not a goal.
func startsInBlockComment(lines [][]rune, idx int) bool {
	in := false
	for i := 0; i < idx; i++ {
		line := lines[i]
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
	}
	return in
}
