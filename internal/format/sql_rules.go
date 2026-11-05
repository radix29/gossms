package format

// Word tables for the SQL styles, and the styles themselves: each is a set of
// switches (sqlStyle) over the same tables and layout, not a new engine.
//
// Upper-casing is the one change the style makes to a token rather than to
// the whitespace around it, so it is confined to words that cannot be an
// identifier where they stand: in a case-sensitive database `date` the column
// and `DATE` are different names, while `int` the type and `INT` are not.

// sqlStyle is one SQL style's rule table, registered in format.go's styles.
// The zero value is Standard.
type sqlStyle struct {
	// lowerKeywords writes a keyword lower-case wherever Standard would
	// upper-case it; every other word keeps its source case as before.
	lowerKeywords bool

	// aliasEquals writes a select-list "expr AS name" as "name = expr"
	// (sql_alias.go): the one change a style makes to token order, which the
	// guard re-derives and accepts on its own terms.
	aliasEquals bool

	// clauseAlone puts FROM, WHERE, GROUP BY, HAVING and ORDER BY on a line
	// of their own, what follows each one level under it.
	clauseAlone bool

	// indentJoins puts a join one level under FROM, with its tables, rather
	// than at FROM's indent; an AND/OR of its ON one level under the join.
	indentJoins bool
}

var (
	sqlStandard  = sqlStyle{}
	sqlLowercase = sqlStyle{lowerKeywords: true, aliasEquals: true, clauseAlone: true, indentJoins: true}
)

// sqlReserved are T-SQL's reserved keywords: never a bare identifier, so
// upper-casing one is always safe (a name spelled like one must be quoted,
// and a quoted identifier is never touched).
var sqlReserved = wordSet(`
ADD ALL ALTER AND ANY AS ASC AUTHORIZATION BACKUP BEGIN BETWEEN BREAK BROWSE
BULK BY CASCADE CASE CHECK CHECKPOINT CLOSE CLUSTERED COALESCE COLLATE COLUMN
COMMIT COMPUTE CONSTRAINT CONTAINS CONTAINSTABLE CONTINUE CONVERT CREATE CROSS
CURRENT CURRENT_DATE CURRENT_TIME CURRENT_TIMESTAMP CURRENT_USER CURSOR
DATABASE DBCC DEALLOCATE DECLARE DEFAULT DELETE DENY DESC DISK DISTINCT
DISTRIBUTED DOUBLE DROP DUMP ELSE END ERRLVL ESCAPE EXCEPT EXEC EXECUTE EXISTS
EXIT EXTERNAL FETCH FILE FILLFACTOR FOR FOREIGN FREETEXT FREETEXTTABLE FROM
FULL FUNCTION GOTO GRANT GROUP HAVING HOLDLOCK IDENTITY IDENTITY_INSERT
IDENTITYCOL IF IN INDEX INNER INSERT INTERSECT INTO IS JOIN KEY KILL LEFT LIKE
LINENO LOAD MERGE NATIONAL NOCHECK NONCLUSTERED NOT NULL NULLIF OF OFF OFFSETS
ON OPEN OPENDATASOURCE OPENQUERY OPENROWSET OPENXML OPTION OR ORDER OUTER OVER
PERCENT PIVOT PLAN PRECISION PRIMARY PRINT PROC PROCEDURE PUBLIC RAISERROR
READ READTEXT RECONFIGURE REFERENCES REPLICATION RESTORE RESTRICT RETURN
REVERT REVOKE RIGHT ROLLBACK ROWCOUNT ROWGUIDCOL RULE SAVE SCHEMA SECURITYAUDIT
SELECT SEMANTICKEYPHRASETABLE SEMANTICSIMILARITYDETAILSTABLE
SEMANTICSIMILARITYTABLE SESSION_USER SET SETUSER SHUTDOWN SOME STATISTICS
SYSTEM_USER TABLE TABLESAMPLE TEXTSIZE THEN TO TOP TRAN TRANSACTION TRIGGER
TRUNCATE TRY_CONVERT TSEQUAL UNION UNIQUE UNPIVOT UPDATE UPDATETEXT USE USER
VALUES VARYING VIEW WAITFOR WHEN WHERE WHILE WITH WITHIN WRITETEXT
`)

// sqlFunctions are built-in functions, upper-cased only where called — the
// next token is "(", no "." qualifies the name, and it is not where an object
// name stands (sqlLayout.namePos: INSERT INTO log (msg)) — since COUNT, LEFT or
// FORMAT standing alone may be a column. A scalar UDF needs its schema, so an
// unqualified call is a built-in.
var sqlFunctions = wordSet(`
ABS ACOS APPROX_COUNT_DISTINCT APP_NAME ASCII ASIN ATAN ATN2 AVG CAST CEILING
CHAR CHARINDEX CHECKSUM CHECKSUM_AGG CHOOSE COLUMNPROPERTY COL_LENGTH COL_NAME
COMPRESS CONCAT CONCAT_WS COS COT COUNT COUNT_BIG CUME_DIST CURSOR_STATUS
DATABASEPROPERTYEX DATALENGTH DATEADD DATEDIFF DATEDIFF_BIG DATEFROMPARTS
DATENAME DATEPART DATETIME2FROMPARTS DATETIMEFROMPARTS DATETIMEOFFSETFROMPARTS
DATETRUNC DATE_BUCKET DAY DB_ID DB_NAME DECOMPRESS DEGREES DENSE_RANK
DIFFERENCE EOMONTH ERROR_LINE ERROR_MESSAGE ERROR_NUMBER ERROR_PROCEDURE
ERROR_SEVERITY ERROR_STATE EXP FILEPROPERTY FIRST_VALUE FLOOR FORMAT
FORMATMESSAGE GENERATE_SERIES GETDATE GETUTCDATE GREATEST GROUPING GROUPING_ID
HASHBYTES HAS_PERMS_BY_NAME HOST_NAME IIF ISDATE ISJSON ISNULL ISNUMERIC
IS_MEMBER IS_SRVROLEMEMBER JSON_ARRAY JSON_MODIFY JSON_OBJECT JSON_QUERY
JSON_VALUE LAG LAST_VALUE LEAD LEAST LEN LOG LOG10 LOWER LTRIM MAX MIN MONTH
NCHAR NEWID NEWSEQUENTIALID NTILE OBJECTPROPERTY OBJECT_ID OBJECT_NAME
OBJECT_SCHEMA_NAME OPENJSON ORIGINAL_LOGIN PARSE PARSENAME PATINDEX
PERCENTILE_CONT PERCENTILE_DISC PERCENT_RANK PI POWER QUOTENAME RADIANS RAND
RANK REPLACE REPLICATE REVERSE ROUND ROW_NUMBER RTRIM SCHEMA_ID SCHEMA_NAME
SCOPE_IDENTITY SERVERPROPERTY SESSION_CONTEXT SIGN SIN SOUNDEX SPACE SQRT
SQUARE STDEV STDEVP STR STRING_AGG STRING_ESCAPE STRING_SPLIT STUFF SUBSTRING
SUM SUSER_NAME SUSER_SNAME SWITCHOFFSET SYSDATETIME SYSDATETIMEOFFSET
SYSUTCDATETIME TAN TIMEFROMPARTS TODATETIMEOFFSET TRANSLATE TRIM TRY_CAST
TRY_PARSE TYPE_ID TYPE_NAME UNICODE UPPER USER_ID USER_NAME VAR VARP
XACT_STATE YEAR
`)

// sqlTableFunctions are the built-ins called where a table name stands
// (FROM OPENJSON(@j), CROSS APPLY STRING_SPLIT(...)), so sqlLayout.namePos
// leaves them to be upper-cased there.
var sqlTableFunctions = wordSet(`GENERATE_SERIES OPENJSON STRING_SPLIT`)

// sqlTypes are system data types, upper-cased only in a type position (after
// a @variable or parameter, a column name in a table definition, CAST's AS,
// RETURNS, CONVERT's first argument). sysname and the CLR types (geography,
// geometry, hierarchyid) are absent: they are names looked up in sys.types
// rather than keywords, so upper-casing one is a bet on collation. (A
// case-sensitive database on a case-insensitive server takes SYSNAME;
// a case-sensitive server was not tried.)
var sqlTypes = wordSet(`
BIGINT BINARY BIT CHAR CURSOR DATE DATETIME DATETIME2 DATETIMEOFFSET DEC
DECIMAL FLOAT IMAGE INT INTEGER MONEY NCHAR NTEXT NUMERIC NVARCHAR REAL
ROWVERSION SMALLDATETIME SMALLINT SMALLMONEY SQL_VARIANT TABLE TEXT TIME
TIMESTAMP TINYINT UNIQUEIDENTIFIER VARBINARY VARCHAR XML
`)

// sqlSetOptions are the words a SET statement (SET NOCOUNT ON, SET
// TRANSACTION ISOLATION LEVEL READ COMMITTED) names an option or its value
// with; only there are they upper-cased.
var sqlSetOptions = wordSet(`
ANSI_DEFAULTS ANSI_NULLS ANSI_NULL_DFLT_OFF ANSI_NULL_DFLT_ON ANSI_PADDING
ANSI_WARNINGS ARITHABORT ARITHIGNORE COMMITTED CONCAT_NULL_YIELDS_NULL
CONTEXT_INFO CURSOR_CLOSE_ON_COMMIT DATEFIRST DATEFORMAT DEADLOCK_PRIORITY
FMTONLY FORCEPLAN HIGH IMPLICIT_TRANSACTIONS IO ISOLATION LANGUAGE LEVEL
LOCK_TIMEOUT LOW NOCOUNT NOEXEC NORMAL NUMERIC_ROUNDABORT PARSEONLY PROFILE
QUERY_GOVERNOR_COST_LIMIT QUOTED_IDENTIFIER REMOTE_PROC_TRANSACTIONS
REPEATABLE SERIALIZABLE SHOWPLAN_ALL SHOWPLAN_TEXT SHOWPLAN_XML SNAPSHOT TIME
UNCOMMITTED XACT_ABORT XML
`)

// sqlPhraseWords are non-reserved words upper-cased inside the phrase that
// makes them a keyword — BEGIN TRY, WHEN NOT MATCHED BY SOURCE, CROSS APPLY,
// FETCH NEXT 5 ROWS ONLY, PARTITION BY — as sqlLayout.phraseWord decides.
var sqlPhraseWords = wordSet(`
APPLY ATOMIC CATCH FIRST HASH LOOP MATCHED NEXT OFFSET ONLY OUT OUTPUT
PARTITION READONLY REMOTE RETURNS ROW ROWS SOURCE TARGET THROW TIES TRY USING
`)

// sqlModuleWords are a module header's own words: its WITH options
// (RECOMPILE, EXECUTE AS OWNER) and a trigger's AFTER / INSTEAD OF.
var sqlModuleWords = wordSet(`
AFTER CALLER ENCRYPTION INLINE INSTEAD NATIVE_COMPILATION OWNER RECOMPILE
RETURNS_NULL_ON_NULL_INPUT SCHEMABINDING SELF VIEW_METADATA
`)

// sqlForWords are the options of a FOR XML / FOR JSON / FOR BROWSE clause.
var sqlForWords = wordSet(`
ABSENT AUTO BASE64 BINARY BROWSE ELEMENTS EXPLICIT INCLUDE_NULL_VALUES JSON
PATH RAW ROOT TYPE WITHOUT_ARRAY_WRAPPER XML XMLDATA XMLSCHEMA XSINIL
`)

// sqlTableHints are the table hints of a WITH (...) after a table in a
// SELECT or DML statement (WITH (NOLOCK, INDEX(ix))), upper-cased only as an
// item of that list. HOLDLOCK and INDEX are reserved already.
var sqlTableHints = wordSet(`
FORCESCAN FORCESEEK IGNORE_CONSTRAINTS IGNORE_TRIGGERS KEEPDEFAULTS
KEEPIDENTITY NOEXPAND NOLOCK NOWAIT PAGLOCK READCOMMITTED READCOMMITTEDLOCK
READPAST READUNCOMMITTED REPEATABLEREAD ROWLOCK SERIALIZABLE SNAPSHOT TABLOCK
TABLOCKX UPDLOCK XLOCK
`)

// sqlKeywords is every word the style may upper-case: the guard compares
// these case-insensitively and every other word exactly.
var sqlKeywords = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range []map[string]bool{sqlReserved, sqlFunctions, sqlTypes, sqlSetOptions, sqlPhraseWords, sqlModuleWords, sqlForWords, sqlTableHints} {
		for w := range s {
			m[w] = true
		}
	}
	return m
}()

// sqlAliasExprWords are the reserved words a select-list item may hold
// outside its parentheses and CASE and still have its "AS name" moved to the
// front: what a scalar expression is spelled with. Any other reserved word
// there (DISTINCT, NEXT VALUE FOR, a statement the item ran into) leaves the
// item as written. GROUP counts only after WITHIN.
var sqlAliasExprWords = wordSet(`
COALESCE COLLATE CONVERT CURRENT_DATE CURRENT_TIME CURRENT_TIMESTAMP
CURRENT_USER GROUP IDENTITY LEFT NULL NULLIF OVER RIGHT SESSION_USER
SYSTEM_USER TRY_CONVERT USER WITHIN
`)

// sqlExtraLeaders begin a statement on top of sqltext's statement leaders,
// which leave them out because running one by accident does no harm — but an
// IF's body can be one, and the layout must know where that body begins.
var sqlExtraLeaders = wordSet(`BREAK CONTINUE GOTO RETURN THROW`)

// sqlModuleKinds follow CREATE/ALTER [OR ALTER] in a module header, whose
// body after AS is statements.
var sqlModuleKinds = wordSet(`FUNCTION PROC PROCEDURE TRIGGER VIEW`)

// sqlBlockBeginNot follow a BEGIN that starts a statement, not a block.
var sqlBlockBeginNot = wordSet(`CONVERSATION DIALOG DISTRIBUTED TRAN TRANSACTION`)

// sqlJoinWords open a join phrase ([INNER|LEFT|RIGHT|FULL] [OUTER] [hint]
// JOIN, CROSS JOIN|APPLY, OUTER APPLY) when sqlLayout.joinAt agrees.
var sqlJoinWords = wordSet(`CROSS FULL INNER JOIN LEFT OUTER RIGHT`)

// sqlJoinHints may sit between a join's type and JOIN.
var sqlJoinHints = wordSet(`HASH LOOP MERGE REMOTE`)

// sqlRulesStatements are the statements the Standard style lays out by its
// clause rules. Any other statement (EXEC, ALTER TABLE, CREATE INDEX, GRANT,
// BACKUP, ...) keeps its own line breaks and only has its indentation and
// spacing fixed: guessing at a layout for syntax the style doesn't model
// would be worse than leaving the author's.
var sqlRulesStatements = wordSet(`
BEGIN BREAK CONTINUE DECLARE DELETE ELSE END GOTO IF INSERT MERGE PRINT RETURN
SELECT SET THROW UPDATE VALUES WHILE WITH
`)

func wordSet(s string) map[string]bool {
	m := map[string]bool{}
	start := -1
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' || s[i] == '\n' {
			if start >= 0 {
				m[s[start:i]] = true
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	return m
}
