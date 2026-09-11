package query

import "testing"

// SSMS spelling per type shape: length only where declared, (max) for
// sentinels, precision+scale for decimal, scale for sub-second date/time, bare
// otherwise.
func TestColumnTypeName(t *testing.T) {
	tests := []struct {
		name      string
		dbType    string
		length    int64
		lengthOK  bool
		precision int64
		scale     int64
		decimalOK bool
		want      string
	}{
		{"int", "INT", 0, false, 0, 0, false, "int"},
		{"bigint", "BIGINT", 0, false, 0, 0, false, "bigint"},
		{"bit", "BIT", 0, false, 0, 0, false, "bit"},
		{"float", "FLOAT", 0, false, 0, 0, false, "float"},
		{"real", "REAL", 0, false, 0, 0, false, "real"},
		{"money", "MONEY", 0, false, 0, 0, false, "money"},
		{"guid", "UNIQUEIDENTIFIER", 0, false, 0, 0, false, "uniqueidentifier"},
		{"nvarchar(50)", "NVARCHAR", 50, true, 0, 0, false, "nvarchar(50)"},
		{"varchar(10)", "VARCHAR", 10, true, 0, 0, false, "varchar(10)"},
		{"char(3)", "CHAR", 3, true, 0, 0, false, "char(3)"},
		{"nchar(3)", "NCHAR", 3, true, 0, 0, false, "nchar(3)"},
		{"binary(8)", "BINARY", 8, true, 0, 0, false, "binary(8)"},
		{"varbinary(16)", "VARBINARY", 16, true, 0, 0, false, "varbinary(16)"},
		{"nvarchar(max)", "NVARCHAR", nvarcharMaxLength, true, 0, 0, false, "nvarchar(max)"},
		{"varchar(max)", "VARCHAR", varcharMaxLength, true, 0, 0, false, "varchar(max)"},
		{"varbinary(max)", "VARBINARY", varcharMaxLength, true, 0, 0, false, "varbinary(max)"},
		{"decimal(18,2)", "DECIMAL", 0, false, 18, 2, true, "decimal(18,2)"},
		{"datetime2(3)", "DATETIME2", 0, false, 23, 3, true, "datetime2(3)"},
		{"datetime2(0)", "DATETIME2", 0, false, 19, 0, true, "datetime2(0)"},
		{"time(7)", "TIME", 0, false, 16, 7, true, "time(7)"},
		{"datetimeoffset(7)", "DATETIMEOFFSET", 0, false, 34, 7, true, "datetimeoffset(7)"},
		{"datetime", "DATETIME", 0, false, 0, 0, false, "datetime"},
		{"date", "DATE", 0, false, 0, 0, false, "date"},
		// A capacity, not a declaration.
		{"text", "TEXT", 2147483647, true, 0, 0, false, "text"},
		{"ntext", "NTEXT", 1073741823, true, 0, 0, false, "ntext"},
		{"image", "IMAGE", 2147483647, true, 0, 0, false, "image"},
		{"xml", "XML", 1073741822, true, 0, 0, false, "xml"},
		{"sql_variant", "SQL_VARIANT", 0, false, 0, 0, false, "sql_variant"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := columnTypeName(tt.dbType, tt.length, tt.lengthOK, tt.precision, tt.scale, tt.decimalOK)
			if got != tt.want {
				t.Errorf("columnTypeName(%q, …) = %q, want %q", tt.dbType, got, tt.want)
			}
		})
	}
}

// Without length or precision metadata, the bare name is right, not
// "nvarchar(0)".
func TestColumnTypeNameMissingMetadata(t *testing.T) {
	if got := columnTypeName("NVARCHAR", 0, false, 0, 0, false); got != "nvarchar" {
		t.Errorf("length not reported: got %q, want %q", got, "nvarchar")
	}
	if got := columnTypeName("DECIMAL", 0, false, 0, 0, false); got != "decimal" {
		t.Errorf("precision not reported: got %q, want %q", got, "decimal")
	}
	if got := columnTypeName("TIME", 0, false, 0, 0, false); got != "time" {
		t.Errorf("scale not reported: got %q, want %q", got, "time")
	}
}
