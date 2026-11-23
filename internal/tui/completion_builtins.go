package tui

import (
	"strings"

	gosmo "github.com/radix29/gosmo"
)

type builtinKind uint16

const (
	builtinScalar builtinKind = 1 << iota
	builtinAggregate
	builtinFabricAggregate
	builtinWindow
	builtinTable
	builtinPredicate
	builtinOrdered
	builtinNoArgs
	builtinPartitionOnly
	builtinWarehouseOnly
	builtinNoWarehouse
	builtinBare
	// builtinDataType: a type a DDL column or parameter list offers, gated
	// only where one is (completionDataTypes), since a type name can also be
	// a keyword elsewhere (FOR JSON).
	builtinDataType
	// SQL Server, Azure SQL/MI and Fabric SQL database, not Warehouse or Synapse.
	builtinSQLOnly
)

type completionBuiltin struct {
	name             string
	kind             builtinKind
	since            gosmo.ServerVersion
	minCompatibility gosmo.CompatibilityLevel
}

// Kind, call shape and availability belong to the same entry. The slices
// below are indexes, not independent inventories to keep in sync. A row with
// no call kind only gates a keyword or type by version and edition:
// builtinCompletionSupported is the one gate for every completion item.
var builtinDefinitions = func() []completionBuiltin {
	defs := []completionBuiltin{
		{name: "COUNT", kind: builtinAggregate | builtinWindow},
		{name: "COUNT_BIG", kind: builtinAggregate | builtinWindow},
		{name: "AVG", kind: builtinAggregate | builtinWindow},
		{name: "SUM", kind: builtinAggregate | builtinWindow},
		{name: "MIN", kind: builtinAggregate | builtinWindow},
		{name: "MAX", kind: builtinAggregate | builtinWindow},
		{name: "STRING_AGG", kind: builtinAggregate, since: gosmo.SQLServer2017},
		{name: "APPROX_COUNT_DISTINCT", kind: builtinAggregate, since: gosmo.SQLServer2019},
		{name: "APPROX_PERCENTILE_CONT", kind: builtinAggregate | builtinOrdered, since: gosmo.SQLServer2022},
		{name: "APPROX_PERCENTILE_DISC", kind: builtinAggregate | builtinOrdered, since: gosmo.SQLServer2022},
		{name: "JSON_ARRAYAGG", kind: builtinAggregate, since: gosmo.SQLServer2025},
		{name: "JSON_OBJECTAGG", kind: builtinAggregate, since: gosmo.SQLServer2025},
		{name: "ANY_VALUE", kind: builtinFabricAggregate | builtinWindow | builtinWarehouseOnly},
		{name: "PRODUCT", kind: builtinFabricAggregate | builtinWindow | builtinWarehouseOnly},
		{name: "MEDIAN", kind: builtinFabricAggregate | builtinWindow | builtinPartitionOnly | builtinWarehouseOnly},
		{name: "QUANTILE", kind: builtinFabricAggregate | builtinWindow | builtinPartitionOnly | builtinWarehouseOnly},
		{name: "APPROX_MEDIAN", kind: builtinFabricAggregate | builtinWindow | builtinPartitionOnly | builtinWarehouseOnly},
		{name: "APPROX_QUANTILE", kind: builtinFabricAggregate | builtinWindow | builtinPartitionOnly | builtinWarehouseOnly},
		{name: "PERCENTILE_CONT", kind: builtinFabricAggregate | builtinWindow | builtinOrdered | builtinPartitionOnly},
		{name: "PERCENTILE_DISC", kind: builtinFabricAggregate | builtinWindow | builtinOrdered | builtinPartitionOnly},
		{name: "VAR", kind: builtinAggregate | builtinWindow},
		{name: "VARP", kind: builtinAggregate | builtinWindow},
		{name: "STDEV", kind: builtinAggregate | builtinWindow},
		{name: "STDEVP", kind: builtinAggregate | builtinWindow},
		{name: "CHECKSUM_AGG", kind: builtinAggregate | builtinWindow},
		{name: "GROUPING", kind: builtinAggregate},
		{name: "GROUPING_ID", kind: builtinAggregate},
		{name: "ROW_NUMBER", kind: builtinWindow | builtinNoArgs},
		{name: "RANK", kind: builtinWindow | builtinNoArgs},
		{name: "DENSE_RANK", kind: builtinWindow | builtinNoArgs},
		{name: "PERCENT_RANK", kind: builtinWindow | builtinNoArgs},
		{name: "CUME_DIST", kind: builtinWindow | builtinNoArgs},
		{name: "NTILE", kind: builtinWindow},
		{name: "LAG", kind: builtinWindow},
		{name: "LEAD", kind: builtinWindow},
		{name: "FIRST_VALUE", kind: builtinWindow},
		{name: "LAST_VALUE", kind: builtinWindow},
		{name: "ABS", kind: builtinScalar},
		{name: "CEILING", kind: builtinScalar},
		{name: "FLOOR", kind: builtinScalar},
		{name: "POWER", kind: builtinScalar},
		{name: "ROUND", kind: builtinScalar},
		{name: "SQRT", kind: builtinScalar},
		{name: "CHARINDEX", kind: builtinScalar},
		{name: "CONCAT", kind: builtinScalar},
		{name: "CONCAT_WS", kind: builtinScalar, since: gosmo.SQLServer2017},
		{name: "FORMAT", kind: builtinScalar},
		{name: "LEFT", kind: builtinScalar},
		{name: "LEN", kind: builtinScalar},
		{name: "LOWER", kind: builtinScalar},
		{name: "LTRIM", kind: builtinScalar},
		{name: "REPLACE", kind: builtinScalar},
		{name: "REPLICATE", kind: builtinScalar},
		{name: "REVERSE", kind: builtinScalar},
		{name: "RIGHT", kind: builtinScalar},
		{name: "RTRIM", kind: builtinScalar},
		{name: "STUFF", kind: builtinScalar},
		{name: "SUBSTRING", kind: builtinScalar},
		{name: "TRIM", kind: builtinScalar, since: gosmo.SQLServer2017},
		{name: "UPPER", kind: builtinScalar},
		{name: "CAST", kind: builtinScalar},
		{name: "COALESCE", kind: builtinScalar},
		{name: "CONVERT", kind: builtinScalar},
		{name: "ISNULL", kind: builtinScalar},
		{name: "NULLIF", kind: builtinScalar},
		{name: "NULL", kind: builtinScalar | builtinBare},
		{name: "TRY_CAST", kind: builtinScalar},
		{name: "TRY_CONVERT", kind: builtinScalar},
		{name: "PARSE", kind: builtinScalar},
		{name: "TRY_PARSE", kind: builtinScalar},
		{name: "CHOOSE", kind: builtinScalar},
		{name: "IIF", kind: builtinScalar},
		{name: "UNISTR", kind: builtinScalar, since: gosmo.SQLServer2025},
		{name: "DATEADD", kind: builtinScalar},
		{name: "DATEDIFF", kind: builtinScalar},
		{name: "DATENAME", kind: builtinScalar},
		{name: "DATEPART", kind: builtinScalar},
		{name: "DAY", kind: builtinScalar},
		{name: "GETDATE", kind: builtinScalar | builtinNoArgs},
		{name: "GETUTCDATE", kind: builtinScalar | builtinNoArgs},
		{name: "MONTH", kind: builtinScalar},
		{name: "YEAR", kind: builtinScalar},
		{name: "ISJSON", kind: builtinScalar},
		{name: "JSON_QUERY", kind: builtinScalar},
		{name: "JSON_VALUE", kind: builtinScalar},
		{name: "JSON_MODIFY", kind: builtinScalar},
		{name: "JSON_ARRAY", kind: builtinScalar, since: gosmo.SQLServer2022},
		{name: "JSON_OBJECT", kind: builtinScalar, since: gosmo.SQLServer2022},
		{name: "JSON_PATH_EXISTS", kind: builtinScalar, since: gosmo.SQLServer2022},
		{name: "JSON_CONTAINS", kind: builtinScalar | builtinNoWarehouse, since: gosmo.SQLServer2025},
		{name: "DATE_BUCKET", kind: builtinScalar, since: gosmo.SQLServer2022},
		{name: "DATETRUNC", kind: builtinScalar, since: gosmo.SQLServer2022},
		{name: "DATEDIFF_BIG", kind: builtinScalar},
		{name: "EOMONTH", kind: builtinScalar},
		{name: "SYSDATETIME", kind: builtinScalar | builtinNoArgs},
		{name: "SYSUTCDATETIME", kind: builtinScalar | builtinNoArgs},
		{name: "SYSDATETIMEOFFSET", kind: builtinScalar | builtinNoArgs},
		{name: "PI", kind: builtinScalar | builtinNoArgs},
		{name: "NEWID", kind: builtinScalar | builtinNoArgs},
		{name: "APP_NAME", kind: builtinScalar | builtinNoArgs},
		{name: "HOST_ID", kind: builtinScalar | builtinNoArgs},
		{name: "HOST_NAME", kind: builtinScalar | builtinNoArgs},
		{name: "ORIGINAL_LOGIN", kind: builtinScalar | builtinNoArgs},
		{name: "CURRENT_TIMESTAMP", kind: builtinScalar | builtinBare},
		{name: "CURRENT_DATE", kind: builtinScalar | builtinBare | builtinSQLOnly, since: gosmo.SQLServer2025},
		{name: "CURRENT_TIMEZONE", kind: builtinScalar | builtinNoArgs, since: gosmo.SQLServer2022},
		{name: "CURRENT_TIMEZONE_ID", kind: builtinScalar | builtinNoArgs | builtinSQLOnly, since: gosmo.SQLServer2022},
		{name: "CURRENT_USER", kind: builtinScalar | builtinBare},
		{name: "SESSION_USER", kind: builtinScalar | builtinBare},
		{name: "SYSTEM_USER", kind: builtinScalar | builtinBare},
		{name: "USER", kind: builtinScalar | builtinBare},
		{name: "IS_SRVROLEMEMBER", kind: builtinScalar | builtinNoWarehouse},
		{name: "TRANSLATE", kind: builtinScalar, since: gosmo.SQLServer2017},
		{name: "GREATEST", kind: builtinScalar, since: gosmo.SQLServer2022},
		{name: "LEAST", kind: builtinScalar, since: gosmo.SQLServer2022},
		{name: "BIT_COUNT", kind: builtinScalar, since: gosmo.SQLServer2022},
		{name: "GET_BIT", kind: builtinScalar, since: gosmo.SQLServer2022},
		{name: "SET_BIT", kind: builtinScalar, since: gosmo.SQLServer2022},
		{name: "LEFT_SHIFT", kind: builtinScalar, since: gosmo.SQLServer2022},
		{name: "RIGHT_SHIFT", kind: builtinScalar, since: gosmo.SQLServer2022},
		{name: "REGEXP_COUNT", kind: builtinScalar, since: gosmo.SQLServer2025},
		{name: "REGEXP_INSTR", kind: builtinScalar, since: gosmo.SQLServer2025},
		{name: "REGEXP_REPLACE", kind: builtinScalar, since: gosmo.SQLServer2025},
		{name: "REGEXP_SUBSTR", kind: builtinScalar, since: gosmo.SQLServer2025},
		{name: "EDIT_DISTANCE", kind: builtinScalar | builtinNoWarehouse, since: gosmo.SQLServer2025},
		{name: "EDIT_DISTANCE_SIMILARITY", kind: builtinScalar | builtinNoWarehouse, since: gosmo.SQLServer2025},
		{name: "JARO_WINKLER_DISTANCE", kind: builtinScalar | builtinNoWarehouse, since: gosmo.SQLServer2025},
		{name: "JARO_WINKLER_SIMILARITY", kind: builtinScalar | builtinNoWarehouse, since: gosmo.SQLServer2025},
		{name: "BASE64_ENCODE", kind: builtinScalar, since: gosmo.SQLServer2025},
		{name: "BASE64_DECODE", kind: builtinScalar, since: gosmo.SQLServer2025},
		{name: "VECTOR_DISTANCE", kind: builtinScalar | builtinNoWarehouse, since: gosmo.SQLServer2025},
		{name: "VECTOR_NORM", kind: builtinScalar | builtinNoWarehouse, since: gosmo.SQLServer2025},
		{name: "VECTOR_NORMALIZE", kind: builtinScalar | builtinNoWarehouse, since: gosmo.SQLServer2025},
		{name: "VECTORPROPERTY", kind: builtinScalar | builtinNoWarehouse, since: gosmo.SQLServer2025},
		{name: "AI_ANALYZE_SENTIMENT", kind: builtinScalar | builtinWarehouseOnly},
		{name: "AI_CLASSIFY", kind: builtinScalar | builtinWarehouseOnly},
		{name: "AI_EXTRACT", kind: builtinScalar | builtinWarehouseOnly},
		{name: "AI_FIX_GRAMMAR", kind: builtinScalar | builtinWarehouseOnly},
		{name: "AI_GENERATE_RESPONSE", kind: builtinScalar | builtinWarehouseOnly},
		{name: "AI_SUMMARIZE", kind: builtinScalar | builtinWarehouseOnly},
		{name: "AI_TRANSLATE", kind: builtinScalar | builtinWarehouseOnly},
		{name: "AI_GENERATE_EMBEDDINGS", kind: builtinScalar | builtinSQLOnly, since: gosmo.SQLServer2025},
		{name: "ERROR_LINE", kind: builtinScalar | builtinNoArgs | builtinNoWarehouse},
		{name: "ERROR_MESSAGE", kind: builtinScalar | builtinNoArgs},
		{name: "ERROR_NUMBER", kind: builtinScalar | builtinNoArgs},
		{name: "ERROR_PROCEDURE", kind: builtinScalar | builtinNoArgs},
		{name: "ERROR_SEVERITY", kind: builtinScalar | builtinNoArgs},
		{name: "ERROR_STATE", kind: builtinScalar | builtinNoArgs},
		{name: "CONTEXT_INFO", kind: builtinScalar | builtinNoArgs | builtinNoWarehouse},
		{name: "CURRENT_TRANSACTION_ID", kind: builtinScalar | builtinNoArgs | builtinNoWarehouse},
		{name: "ROWCOUNT_BIG", kind: builtinScalar | builtinNoArgs | builtinNoWarehouse},
		{name: "SCOPE_IDENTITY", kind: builtinScalar | builtinNoArgs | builtinNoWarehouse},
		{name: "XACT_STATE", kind: builtinScalar | builtinNoArgs | builtinNoWarehouse},
		{name: "AI_GENERATE_CHUNKS", kind: builtinTable | builtinSQLOnly, since: gosmo.SQLServer2025, minCompatibility: 170},
		{name: "OPENJSON", kind: builtinTable},
		{name: "OPENROWSET", kind: builtinTable},
		{name: "STRING_SPLIT", kind: builtinTable},
		{name: "GENERATE_SERIES", kind: builtinTable, since: gosmo.SQLServer2022},
		{name: "REGEXP_MATCHES", kind: builtinTable | builtinNoWarehouse, since: gosmo.SQLServer2025},
		{name: "REGEXP_SPLIT_TO_TABLE", kind: builtinTable | builtinNoWarehouse, since: gosmo.SQLServer2025},
		{name: "VECTOR_SEARCH", kind: builtinTable | builtinNoWarehouse, since: gosmo.SQLServer2025},
		{name: "EXISTS", kind: builtinPredicate},
		{name: "NOT EXISTS", kind: builtinPredicate},
		{name: "REGEXP_LIKE", kind: builtinPredicate, since: gosmo.SQLServer2025},
		{name: "CONTAINS", kind: builtinPredicate | builtinNoWarehouse},
		{name: "FREETEXT", kind: builtinPredicate | builtinNoWarehouse},
		{name: "||", since: gosmo.SQLServer2025},
		{name: "IS DISTINCT FROM", since: gosmo.SQLServer2022},
		{name: "IS NOT DISTINCT FROM", since: gosmo.SQLServer2022},
		{name: "DISTINCT FROM", since: gosmo.SQLServer2022},
		{name: "NOT DISTINCT FROM", since: gosmo.SQLServer2022},
		{name: "WINDOW", since: gosmo.SQLServer2022},
		{name: "JSON", kind: builtinDataType | builtinSQLOnly, since: gosmo.SQLServer2025},
		{name: "VECTOR", kind: builtinDataType | builtinNoWarehouse, since: gosmo.SQLServer2025},
	}
	for _, name := range strings.Fields(
		"ACOS ASIN ATAN ATN2 COS COT DEGREES EXP LOG LOG10 RADIANS RAND SIGN SIN SQUARE TAN " +
			"ASCII CHAR DATALENGTH DIFFERENCE NCHAR PATINDEX QUOTENAME SOUNDEX SPACE STR STRING_ESCAPE UNICODE " +
			"DATEFROMPARTS DATETIMEFROMPARTS DATETIME2FROMPARTS DATETIMEOFFSETFROMPARTS " +
			"SMALLDATETIMEFROMPARTS TIMEFROMPARTS ISDATE ISNUMERIC SWITCHOFFSET TODATETIMEOFFSET " +
			"DB_ID DB_NAME SCHEMA_ID SCHEMA_NAME OBJECT_ID OBJECT_NAME OBJECT_SCHEMA_NAME OBJECT_DEFINITION " +
			"OBJECTPROPERTY OBJECTPROPERTYEX COLUMNPROPERTY COL_LENGTH COL_NAME TYPE_ID TYPE_NAME TYPEPROPERTY " +
			"DATABASEPROPERTYEX SERVERPROPERTY CONNECTIONPROPERTY SESSION_CONTEXT " +
			"USER_ID USER_NAME SUSER_ID SUSER_NAME SUSER_SID SUSER_SNAME IS_MEMBER IS_ROLEMEMBER HAS_PERMS_BY_NAME " +
			"COLLATIONPROPERTY PARSENAME STATS_DATE INDEX_COL INDEXKEY_PROPERTY INDEXPROPERTY COMPRESS DECOMPRESS " +
			"HASHBYTES CHECKSUM BINARY_CHECKSUM") {
		defs = append(defs, completionBuiltin{name: name, kind: builtinScalar})
	}
	for _, name := range strings.Fields(
		"IDENT_CURRENT IDENT_INCR IDENT_SEED SQL_VARIANT_PROPERTY FORMATMESSAGE " +
			"DATABASE_PRINCIPAL_ID SESSIONPROPERTY LOGINPROPERTY CRYPT_GEN_RANDOM") {
		defs = append(defs, completionBuiltin{name: name, kind: builtinScalar | builtinNoWarehouse})
	}
	for _, typ := range []string{"geometry", "geography"} {
		for _, method := range spatialConstructors {
			defs = append(defs, completionBuiltin{name: typ + "::" + strings.TrimSuffix(method, "("), kind: builtinScalar})
		}
	}
	return defs
}()

var completionBuiltins, completionDataTypes = func() (builtins, types map[string]completionBuiltin) {
	builtins, types = make(map[string]completionBuiltin, len(builtinDefinitions)), map[string]completionBuiltin{}
	for _, builtin := range builtinDefinitions {
		if builtin.kind&builtinDataType != 0 {
			types[builtin.name] = builtin
		} else {
			builtins[builtin.name] = builtin
		}
	}
	return builtins, types
}()

func builtinCompletionNames(kind builtinKind) []string {
	var names []string
	for _, builtin := range builtinDefinitions {
		if builtin.kind&kind != 0 {
			names = append(names, builtin.name+"(")
		}
	}
	return names
}

var (
	scalarCompletions          = builtinCompletionNames(builtinScalar)
	aggregateCompletions       = builtinCompletionNames(builtinAggregate)
	fabricAggregateCompletions = builtinCompletionNames(builtinAggregate | builtinFabricAggregate)
	projectionCompletions      = builtinCompletionNames(builtinAggregate | builtinFabricAggregate | builtinWindow)
	windowCompletions          = builtinCompletionNames(builtinWindow)
	tableValuedCompletions     = builtinCompletionNames(builtinTable)
	predicateCompletions       = builtinCompletionNames(builtinPredicate)
)
