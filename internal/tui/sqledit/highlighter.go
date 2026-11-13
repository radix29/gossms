package sqledit

import (
	"unicode/utf8"

	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/tuikit/controls/editor"
	"github.com/radix29/tuikit/theme"
)

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

// Highlighter is the T-SQL syntax highlighter for a editor.Editor.
//
// It colours what sqltext.Next lexes — the lexer the executor splits batches by
// and Ctrl+Enter selects statements by — so a keyword inside a [bracketed] or
// "quoted" identifier is not coloured, and a "/*" inside one opens no comment. A
// literal, identifier or comment left open at the end of a line carries onto the
// next, as it does for the server.
//
// The state each line begins in comes from LineState, the Document's cache (see
// editor.LineStateAt): replaying every prior line per row would be O(H*N) per
// Draw, and Draw runs on every event. The cache lives on the Document, not in
// this closure, so LineState reads the same states instead of lexing the script
// a second time, and the returned Highlighter holds no state.
func Highlighter(p *theme.Palette) editor.Highlighter {
	return func(doc *editor.Document, idx int) []editor.ColorRun {
		// Built per call, not once: p is the live palette, so a theme switch
		// recolours an open editor.
		kwStyle := theme.On(p.EditorBg, p.EditorKeyword).Bold(true)
		strStyle := theme.On(p.EditorBg, p.EditorString)
		cmtStyle := theme.On(p.EditorBg, p.EditorComment)
		numStyle := theme.On(p.EditorBg, p.EditorNumber)
		line := doc.Line(idx)
		runs := make([]editor.ColorRun, 0, 8)
		st := LineState(doc, idx)
		for i := 0; ; {
			var t sqltext.Token
			t, st = sqltext.Next(line, i, len(line), st)
			if t.Kind == sqltext.KindEnd {
				return runs
			}
			i = t.End
			switch t.Kind {
			case sqltext.KindComment:
				runs = append(runs, editor.ColorRun{Start: t.Start, Len: t.End - t.Start, Style: cmtStyle})
			case sqltext.KindString:
				runs = append(runs, editor.ColorRun{Start: t.Start, Len: t.End - t.Start, Style: strStyle})
			case sqltext.KindNumber:
				runs = append(runs, editor.ColorRun{Start: t.Start, Len: t.End - t.Start, Style: numStyle})
			case sqltext.KindWord:
				if isSQLKeyword(line[t.Start:t.End]) {
					runs = append(runs, editor.ColorRun{Start: t.Start, Len: t.End - t.Start, Style: kwStyle})
				}
			}
		}
	}
}

// lineStatesKey owns the Document's T-SQL line-state cache (editor.LineStateAt).
type lineStatesKey struct{}

// LineState returns the T-SQL lexer state line row of doc begins in — inside a
// string, comment or quoted identifier carried over from the lines above, or
// sqltext.ModeNormal — as Highlighter colours it. A line-oriented rewrite of
// SQL checks it before treating the line as code. It reads the cache
// Highlighter fills, so after a Draw it costs nothing, and an edit costs only
// the lines whose state changed.
func LineState(doc *editor.Document, row int) sqltext.State {
	return editor.LineStateAt(doc, lineStatesKey{}, row, sqltext.State{}, sqltext.LineEnd)
}

// maxSQLKeywordLen bounds isSQLKeyword's stack buffer, so it must be a
// constant. TestSQLKeywordsFitTheFoldBuffer fails if a keyword outgrows it.
const maxSQLKeywordLen = 32

// isSQLKeyword reports whether word is in sqlKeywords, ignoring case, without
// allocating: it runs per word per visible line on every Draw. The word is
// ASCII-uppercased into a stack array, and Go compiles a map index on
// string(byteSlice) without copying. A word longer than any keyword, or with a
// non-ASCII rune, cannot be one.
func isSQLKeyword(word []rune) bool {
	if len(word) > maxSQLKeywordLen {
		return false
	}
	var scratch [maxSQLKeywordLen]byte
	for i, c := range word {
		if c >= utf8.RuneSelf {
			return false
		}
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		scratch[i] = byte(c)
	}
	return sqlKeywords[string(scratch[:len(word)])]
}
