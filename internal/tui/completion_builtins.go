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
	builtinBare
)

type completionBuiltin struct {
	name        string
	kind        builtinKind
	since       gosmo.ServerVersion
	noWarehouse bool
}

// Kind, call shape and availability belong to the same entry. The slices
// below are indexes, not independent inventories to keep in sync.
var builtinDefinitions = func() []completionBuiltin {
	defs := []completionBuiltin{
		{"COUNT", builtinAggregate | builtinWindow, 0, false},
		{"COUNT_BIG", builtinAggregate | builtinWindow, 0, false},
		{"AVG", builtinAggregate | builtinWindow, 0, false},
		{"SUM", builtinAggregate | builtinWindow, 0, false},
		{"MIN", builtinAggregate | builtinWindow, 0, false},
		{"MAX", builtinAggregate | builtinWindow, 0, false},
		{"STRING_AGG", builtinAggregate, 14, false},
		{"APPROX_COUNT_DISTINCT", builtinAggregate, 15, false},
		{"APPROX_PERCENTILE_CONT", builtinAggregate | builtinOrdered, 16, false},
		{"APPROX_PERCENTILE_DISC", builtinAggregate | builtinOrdered, 16, false},
		{"JSON_ARRAYAGG", builtinAggregate, 17, false},
		{"JSON_OBJECTAGG", builtinAggregate, 17, false},
		{"ANY_VALUE", builtinFabricAggregate | builtinWindow | builtinWarehouseOnly, 0, false},
		{"PRODUCT", builtinFabricAggregate | builtinWindow | builtinWarehouseOnly, 0, false},
		{"MEDIAN", builtinFabricAggregate | builtinWindow | builtinPartitionOnly | builtinWarehouseOnly, 0, false},
		{"QUANTILE", builtinFabricAggregate | builtinWindow | builtinPartitionOnly | builtinWarehouseOnly, 0, false},
		{"APPROX_MEDIAN", builtinFabricAggregate | builtinWindow | builtinPartitionOnly | builtinWarehouseOnly, 0, false},
		{"APPROX_QUANTILE", builtinFabricAggregate | builtinWindow | builtinPartitionOnly | builtinWarehouseOnly, 0, false},
		{"PERCENTILE_CONT", builtinFabricAggregate | builtinWindow | builtinOrdered | builtinPartitionOnly, 0, false},
		{"PERCENTILE_DISC", builtinFabricAggregate | builtinWindow | builtinOrdered | builtinPartitionOnly, 0, false},
		{"VAR", builtinAggregate | builtinWindow, 0, false},
		{"VARP", builtinAggregate | builtinWindow, 0, false},
		{"STDEV", builtinAggregate | builtinWindow, 0, false},
		{"STDEVP", builtinAggregate | builtinWindow, 0, false},
		{"CHECKSUM_AGG", builtinAggregate | builtinWindow, 0, false},
		{"GROUPING", builtinAggregate, 0, false},
		{"GROUPING_ID", builtinAggregate, 0, false},
		{"ROW_NUMBER", builtinWindow | builtinNoArgs, 0, false},
		{"RANK", builtinWindow | builtinNoArgs, 0, false},
		{"DENSE_RANK", builtinWindow | builtinNoArgs, 0, false},
		{"PERCENT_RANK", builtinWindow | builtinNoArgs, 0, false},
		{"CUME_DIST", builtinWindow | builtinNoArgs, 0, false},
		{"NTILE", builtinWindow, 0, false},
		{"LAG", builtinWindow, 0, false},
		{"LEAD", builtinWindow, 0, false},
		{"FIRST_VALUE", builtinWindow, 0, false},
		{"LAST_VALUE", builtinWindow, 0, false},
		{"ABS", builtinScalar, 0, false},
		{"CEILING", builtinScalar, 0, false},
		{"FLOOR", builtinScalar, 0, false},
		{"POWER", builtinScalar, 0, false},
		{"ROUND", builtinScalar, 0, false},
		{"SQRT", builtinScalar, 0, false},
		{"CHARINDEX", builtinScalar, 0, false},
		{"CONCAT", builtinScalar, 0, false},
		{"CONCAT_WS", builtinScalar, 14, false},
		{"FORMAT", builtinScalar, 0, false},
		{"LEFT", builtinScalar, 0, false},
		{"LEN", builtinScalar, 0, false},
		{"LOWER", builtinScalar, 0, false},
		{"LTRIM", builtinScalar, 0, false},
		{"REPLACE", builtinScalar, 0, false},
		{"REPLICATE", builtinScalar, 0, false},
		{"REVERSE", builtinScalar, 0, false},
		{"RIGHT", builtinScalar, 0, false},
		{"RTRIM", builtinScalar, 0, false},
		{"STUFF", builtinScalar, 0, false},
		{"SUBSTRING", builtinScalar, 0, false},
		{"TRIM", builtinScalar, 14, false},
		{"UPPER", builtinScalar, 0, false},
		{"CAST", builtinScalar, 0, false},
		{"COALESCE", builtinScalar, 0, false},
		{"CONVERT", builtinScalar, 0, false},
		{"ISNULL", builtinScalar, 0, false},
		{"NULLIF", builtinScalar, 0, false},
		{"NULL", builtinScalar | builtinBare, 0, false},
		{"TRY_CAST", builtinScalar, 0, false},
		{"TRY_CONVERT", builtinScalar, 0, false},
		{"DATEADD", builtinScalar, 0, false},
		{"DATEDIFF", builtinScalar, 0, false},
		{"DATENAME", builtinScalar, 0, false},
		{"DATEPART", builtinScalar, 0, false},
		{"DAY", builtinScalar, 0, false},
		{"GETDATE", builtinScalar | builtinNoArgs, 0, false},
		{"GETUTCDATE", builtinScalar | builtinNoArgs, 0, false},
		{"MONTH", builtinScalar, 0, false},
		{"YEAR", builtinScalar, 0, false},
		{"ISJSON", builtinScalar, 0, false},
		{"JSON_QUERY", builtinScalar, 0, false},
		{"JSON_VALUE", builtinScalar, 0, false},
		{"JSON_MODIFY", builtinScalar, 0, false},
		{"JSON_ARRAY", builtinScalar, 16, false},
		{"JSON_OBJECT", builtinScalar, 16, false},
		{"JSON_PATH_EXISTS", builtinScalar, 16, false},
		{"JSON_CONTAINS", builtinScalar, 17, true},
		{"DATE_BUCKET", builtinScalar, 16, false},
		{"DATETRUNC", builtinScalar, 16, false},
		{"DATEDIFF_BIG", builtinScalar, 0, false},
		{"EOMONTH", builtinScalar, 0, false},
		{"SYSDATETIME", builtinScalar | builtinNoArgs, 0, false},
		{"SYSUTCDATETIME", builtinScalar | builtinNoArgs, 0, false},
		{"SYSDATETIMEOFFSET", builtinScalar | builtinNoArgs, 0, false},
		{"PI", builtinScalar | builtinNoArgs, 0, false},
		{"NEWID", builtinScalar | builtinNoArgs, 0, false},
		{"APP_NAME", builtinScalar | builtinNoArgs, 0, false},
		{"HOST_ID", builtinScalar | builtinNoArgs, 0, false},
		{"HOST_NAME", builtinScalar | builtinNoArgs, 0, false},
		{"ORIGINAL_LOGIN", builtinScalar | builtinNoArgs, 0, false},
		{"CURRENT_TIMESTAMP", builtinScalar | builtinBare, 0, false},
		{"CURRENT_USER", builtinScalar | builtinBare, 0, false},
		{"SESSION_USER", builtinScalar | builtinBare, 0, false},
		{"SYSTEM_USER", builtinScalar | builtinBare, 0, false},
		{"USER", builtinScalar | builtinBare, 0, false},
		{"IS_SRVROLEMEMBER", builtinScalar, 0, true},
		{"TRANSLATE", builtinScalar, 14, false},
		{"GREATEST", builtinScalar, 16, false},
		{"LEAST", builtinScalar, 16, false},
		{"BIT_COUNT", builtinScalar, 16, false},
		{"GET_BIT", builtinScalar, 16, false},
		{"SET_BIT", builtinScalar, 16, false},
		{"LEFT_SHIFT", builtinScalar, 16, false},
		{"RIGHT_SHIFT", builtinScalar, 16, false},
		{"REGEXP_COUNT", builtinScalar, 17, false},
		{"REGEXP_INSTR", builtinScalar, 17, false},
		{"REGEXP_REPLACE", builtinScalar, 17, false},
		{"REGEXP_SUBSTR", builtinScalar, 17, false},
		{"EDIT_DISTANCE", builtinScalar, 17, true},
		{"EDIT_DISTANCE_SIMILARITY", builtinScalar, 17, true},
		{"JARO_WINKLER_DISTANCE", builtinScalar, 17, true},
		{"JARO_WINKLER_SIMILARITY", builtinScalar, 17, true},
		{"BASE64_ENCODE", builtinScalar, 17, false},
		{"BASE64_DECODE", builtinScalar, 17, false},
		{"VECTOR_DISTANCE", builtinScalar, 17, true},
		{"VECTOR_NORM", builtinScalar, 17, true},
		{"VECTOR_NORMALIZE", builtinScalar, 17, true},
		{"VECTORPROPERTY", builtinScalar, 17, true},
		{"OPENJSON", builtinTable, 0, false},
		{"OPENROWSET", builtinTable, 0, false},
		{"STRING_SPLIT", builtinTable, 0, false},
		{"GENERATE_SERIES", builtinTable, 16, false},
		{"REGEXP_MATCHES", builtinTable, 17, true},
		{"REGEXP_SPLIT_TO_TABLE", builtinTable, 17, true},
		{"VECTOR_SEARCH", builtinTable, 17, true},
		{"EXISTS", builtinPredicate, 0, false},
		{"NOT EXISTS", builtinPredicate, 0, false},
		{"REGEXP_LIKE", builtinPredicate, 17, false},
		{"CONTAINS", builtinPredicate, 0, true},
		{"FREETEXT", builtinPredicate, 0, true},
		{"||", 0, 17, false},
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
			"HASHBYTES CHECKSUM BINARY_CHECKSUM") {
		defs = append(defs, completionBuiltin{name, builtinScalar, 0, false})
	}
	for _, typ := range []string{"geometry", "geography"} {
		for _, method := range spatialConstructors {
			defs = append(defs, completionBuiltin{typ + "::" + strings.TrimSuffix(method, "("), builtinScalar, 0, false})
		}
	}
	return defs
}()

var completionBuiltins = func() map[string]completionBuiltin {
	index := make(map[string]completionBuiltin, len(builtinDefinitions))
	for _, builtin := range builtinDefinitions {
		index[builtin.name] = builtin
	}
	return index
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
