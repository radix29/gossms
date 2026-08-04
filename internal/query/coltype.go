package query

import (
	"database/sql"
	"strconv"
	"strings"
)

// varcharMaxLength / nvarcharMaxLength are the lengths go-mssqldb reports for
// a varchar(max)/varbinary(max) and an nvarchar(max) column — 2^31-3 bytes,
// halved for the two-byte characters of nvarchar (types.go,
// makeGoLangTypeLength). They are sentinels, not real declared lengths:
// printing them literally would render "nvarchar(1073741822)" where SSMS
// shows "nvarchar(max)".
const (
	varcharMaxLength  = 2147483645
	nvarcharMaxLength = 2147483645 / 2
)

// columnTypeName renders one column's declared SQL Server type the way SSMS
// writes it — "nvarchar(50)", "decimal(18,2)", "datetime2(3)", "int" — from
// what the driver reports about it. dbType is the uppercase
// DatabaseTypeName; length/lengthOK come from Length; precision/scale/
// decimalOK from DecimalSize.
//
// Only the character and binary types take a length suffix. The driver also
// reports a length for text/ntext/image/xml (their maximum, e.g. 2147483647),
// which is a capacity rather than a declaration — SSMS writes those bare.
func columnTypeName(dbType string, length int64, lengthOK bool, precision, scale int64, decimalOK bool) string {
	name := strings.ToLower(dbType)
	switch dbType {
	case "CHAR", "NCHAR", "VARCHAR", "NVARCHAR", "BINARY", "VARBINARY":
		if !lengthOK {
			return name
		}
		if isMaxLength(dbType, length) {
			return name + "(max)"
		}
		return name + "(" + strconv.FormatInt(length, 10) + ")"
	case "DECIMAL":
		if !decimalOK {
			return name
		}
		return name + "(" + strconv.FormatInt(precision, 10) + "," + strconv.FormatInt(scale, 10) + ")"
	case "DATETIME2", "TIME", "DATETIMEOFFSET":
		if !decimalOK {
			return name
		}
		return name + "(" + strconv.FormatInt(scale, 10) + ")"
	}
	return name
}

// isMaxLength reports whether length is the driver's sentinel for a (max)
// column of this type rather than a declared length.
func isMaxLength(dbType string, length int64) bool {
	if dbType == "NVARCHAR" {
		return length == nvarcharMaxLength
	}
	return length == varcharMaxLength
}

// columnTypeNames renders every column's declared type for the current
// result set, in column order — Result.Sets' ColumnTypes.
func columnTypeNames(types []*sql.ColumnType) []string {
	names := make([]string, len(types))
	for i, ct := range types {
		length, lengthOK := ct.Length()
		precision, scale, decimalOK := ct.DecimalSize()
		names[i] = columnTypeName(ct.DatabaseTypeName(), length, lengthOK, precision, scale, decimalOK)
	}
	return names
}
