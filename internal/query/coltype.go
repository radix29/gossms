package query

import (
	"database/sql"
	"strconv"
	"strings"
)

// varcharMaxLength / nvarcharMaxLength are go-mssqldb's reported lengths for
// (max) columns (2^31-3 bytes, halved for nvarchar; types.go
// makeGoLangTypeLength). Sentinels, rendered as "(max)" as SSMS does.
const (
	varcharMaxLength  = 2147483645
	nvarcharMaxLength = 2147483645 / 2
)

// columnTypeName renders a column's declared type as SSMS writes it
// ("nvarchar(50)", "decimal(18,2)", "datetime2(3)", "int"). dbType is the
// uppercase DatabaseTypeName; length/lengthOK from Length;
// precision/scale/decimalOK from DecimalSize.
//
// Only character and binary types take a length. The driver also reports
// capacities for text/ntext/image/xml, which SSMS writes bare.
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

// isMaxLength reports whether length is the driver's (max) sentinel for this
// type.
func isMaxLength(dbType string, length int64) bool {
	if dbType == "NVARCHAR" {
		return length == nvarcharMaxLength
	}
	return length == varcharMaxLength
}

// columnTypeNames renders each column's declared type in order (Result.Sets'
// ColumnTypes).
func columnTypeNames(types []*sql.ColumnType) []string {
	names := make([]string, len(types))
	for i, ct := range types {
		length, lengthOK := ct.Length()
		precision, scale, decimalOK := ct.DecimalSize()
		names[i] = columnTypeName(ct.DatabaseTypeName(), length, lengthOK, precision, scale, decimalOK)
	}
	return names
}
