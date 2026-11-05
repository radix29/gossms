package query

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/radix29/gosmo"
)

func mustParser(t *testing.T, col gosmo.BulkColumn) func(string) (any, error) {
	t.Helper()
	p, err := NewCellParser(col)
	if err != nil {
		t.Fatalf("NewCellParser(%+v): %v", col, err)
	}
	return p
}

// TestCellParserValues pins the value each text becomes, by hand: a parser
// checked only against cells.go would agree with a renderer wrong the same way.
func TestCellParserValues(t *testing.T) {
	plus0530 := time.FixedZone("", 5*3600+30*60)
	cases := []struct {
		col  gosmo.BulkColumn
		in   string
		want any
	}{
		{gosmo.BulkColumn{Type: "bit"}, "1", true},
		{gosmo.BulkColumn{Type: "bit"}, "0", false},
		{gosmo.BulkColumn{Type: "bit"}, "TRUE", true},
		{gosmo.BulkColumn{Type: "tinyint"}, "255", int64(255)},
		{gosmo.BulkColumn{Type: "smallint"}, "-32768", int64(-32768)},
		{gosmo.BulkColumn{Type: "int"}, "+7", int64(7)},
		{gosmo.BulkColumn{Type: "bigint"}, "-9223372036854775808", int64(math.MinInt64)},
		{gosmo.BulkColumn{Type: "decimal", Precision: 5, Scale: 2}, "123.45", "123.45"},
		{gosmo.BulkColumn{Type: "decimal", Precision: 5, Scale: 2}, "-0.5", "-0.5"},
		{gosmo.BulkColumn{Type: "numeric", Precision: 5, Scale: 2}, ".5", "0.5"},
		{gosmo.BulkColumn{Type: "decimal", Precision: 5, Scale: 2}, "007", "7"},
		{gosmo.BulkColumn{Type: "decimal", Precision: 38, Scale: 0}, strings.Repeat("9", 38), strings.Repeat("9", 38)},
		{gosmo.BulkColumn{Type: "money"}, "12.50", "12.50"},
		{gosmo.BulkColumn{Type: "money"}, "-922337203685477.5808", "-922337203685477.5808"},
		{gosmo.BulkColumn{Type: "money"}, "922337203685477.5807", "922337203685477.5807"},
		{gosmo.BulkColumn{Type: "smallmoney"}, "-214748.3648", "-214748.3648"},
		{gosmo.BulkColumn{Type: "float"}, "1.5E+300", 1.5e300},
		{gosmo.BulkColumn{Type: "float"}, "0.1", 0.1},
		{gosmo.BulkColumn{Type: "real"}, "3.4028235E+38", float32(math.MaxFloat32)},
		{gosmo.BulkColumn{Type: "real"}, "0.1", float32(0.1)},
		{gosmo.BulkColumn{Type: "date"}, "0001-01-01", time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)},
		{gosmo.BulkColumn{Type: "time", Scale: 7}, "23:59:59.9999999", time.Date(1900, 1, 1, 23, 59, 59, 999999900, time.UTC)},
		{gosmo.BulkColumn{Type: "time", Scale: 0}, "08:00:00", time.Date(1900, 1, 1, 8, 0, 0, 0, time.UTC)},
		{gosmo.BulkColumn{Type: "datetime"}, "2024-02-29 13:45:30.123", time.Date(2024, 2, 29, 13, 45, 30, 123000000, time.UTC)},
		{gosmo.BulkColumn{Type: "datetime"}, "2024-02-29T13:45:30", time.Date(2024, 2, 29, 13, 45, 30, 0, time.UTC)},
		{gosmo.BulkColumn{Type: "smalldatetime"}, "2024-02-29 13:45:29.998", time.Date(2024, 2, 29, 13, 45, 0, 0, time.UTC)},
		{gosmo.BulkColumn{Type: "smalldatetime"}, "2024-02-29 13:45:29.999", time.Date(2024, 2, 29, 13, 46, 0, 0, time.UTC)},
		{gosmo.BulkColumn{Type: "smalldatetime"}, "2024-02-29 23:59:30", time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)},
		{gosmo.BulkColumn{Type: "datetime2", Scale: 3}, "9999-12-31 23:59:59.9", time.Date(9999, 12, 31, 23, 59, 59, 900000000, time.UTC)},
		{gosmo.BulkColumn{Type: "datetimeoffset", Scale: 3}, "2024-02-29 13:45:30.123 +05:30", time.Date(2024, 2, 29, 13, 45, 30, 123000000, plus0530)},
		{gosmo.BulkColumn{Type: "datetimeoffset", Scale: 0}, "2024-02-29T13:45:30+05:30", time.Date(2024, 2, 29, 13, 45, 30, 0, plus0530)},
		{gosmo.BulkColumn{Type: "datetimeoffset", Scale: 0}, "2024-02-29 13:45:30", time.Date(2024, 2, 29, 13, 45, 30, 0, time.FixedZone("", 0))},
		{gosmo.BulkColumn{Type: "datetimeoffset", Scale: 0}, "2024-02-29 13:45:30Z", time.Date(2024, 2, 29, 13, 45, 30, 0, time.FixedZone("", 0))},
		// The wire order: the first three groups byte-reversed.
		{gosmo.BulkColumn{Type: "uniqueidentifier"}, "6F9619FF-8B86-D011-B42D-00C04FD430C8",
			[]byte{0xFF, 0x19, 0x96, 0x6F, 0x86, 0x8B, 0x11, 0xD0, 0xB4, 0x2D, 0x00, 0xC0, 0x4F, 0xD4, 0x30, 0xC8}},
		{gosmo.BulkColumn{Type: "uniqueidentifier"}, "{6f9619ff-8b86-d011-b42d-00c04fd430c8}",
			[]byte{0xFF, 0x19, 0x96, 0x6F, 0x86, 0x8B, 0x11, 0xD0, 0xB4, 0x2D, 0x00, 0xC0, 0x4F, 0xD4, 0x30, 0xC8}},
		{gosmo.BulkColumn{Type: "varbinary", Length: 4}, "0x01ff", []byte{0x01, 0xFF}},
		{gosmo.BulkColumn{Type: "varbinary", Length: -1}, "0x", []byte{}},
		{gosmo.BulkColumn{Type: "binary", Length: 4}, "0X01FF", []byte{0x01, 0xFF, 0, 0}},
		// sys.columns' 16 for image/ntext/text is not a length limit.
		{gosmo.BulkColumn{Type: "image", Length: 16}, "0x" + strings.Repeat("AB", 17), bytes.Repeat([]byte{0xAB}, 17)},
		{gosmo.BulkColumn{Type: "ntext", Length: 8}, strings.Repeat("n", 9), strings.Repeat("n", 9)},
		{gosmo.BulkColumn{Type: "text", Length: 16, CodePage: 1252, DatabaseCodePage: 1252}, strings.Repeat("t", 17), bytes.Repeat([]byte{'t'}, 17)},
		{gosmo.BulkColumn{Type: "nvarchar", Length: 2}, "😀", "😀"},
		{gosmo.BulkColumn{Type: "nvarchar", Length: -1}, "", ""},
		{gosmo.BulkColumn{Type: "nchar", Length: 4}, "ab", "ab  "},
		{gosmo.BulkColumn{Type: "ntext"}, "NULL", "NULL"},
		{gosmo.BulkColumn{Type: "xml"}, "<a>é</a>", "<a>é</a>"},
		{gosmo.BulkColumn{Type: "varchar", Length: 6, CodePage: 1252, DatabaseCodePage: 1252}, "café €", []byte{'c', 'a', 'f', 0xE9, ' ', 0x80}},
		// A UTF-8 column takes a string (the driver sees nvarchar), its length
		// in UTF-8 bytes, whatever the database's code page.
		{gosmo.BulkColumn{Type: "varchar", Length: 5, CodePage: 65001, DatabaseCodePage: 65001}, "café", "café"},
		{gosmo.BulkColumn{Type: "char", Length: 7, CodePage: 65001, DatabaseCodePage: 1252}, "日", "日    "},
		{gosmo.BulkColumn{Type: "char", Length: 4, CodePage: 1252, DatabaseCodePage: 1252}, "é", []byte{0xE9, ' ', ' ', ' '}},
		{gosmo.BulkColumn{Type: "varchar", Length: 4, CodePage: 932, DatabaseCodePage: 932}, "日本", []byte{0x93, 0xFA, 0x96, 0x7B}},
		{gosmo.BulkColumn{Type: "text", CodePage: 1251, DatabaseCodePage: 1251}, "Жж", []byte{0xC6, 0xE6}},
		// The bytes travel in the database's code page and the server
		// converts them; the length is the column's.
		{gosmo.BulkColumn{Type: "varchar", Length: 4, CodePage: 932, DatabaseCodePage: 65001}, "日本", []byte("日本")},
		{gosmo.BulkColumn{Type: "char", Length: 4, CodePage: 1252, DatabaseCodePage: 65001}, "é", []byte("é   ")},
		{gosmo.BulkColumn{Type: "text", CodePage: 1252, DatabaseCodePage: 65001}, "é", []byte("é")},
		{gosmo.BulkColumn{Type: "varchar", Length: 2, CodePage: 1251, DatabaseCodePage: 932}, "Ж", []byte{0x84, 0x47}},
	}
	for _, c := range cases {
		got, err := mustParser(t, c.col)(c.in)
		if err != nil {
			t.Errorf("%s %q: %v", c.col.Type, c.in, err)
			continue
		}
		if !sameValue(got, c.want) {
			t.Errorf("%s %q = %#v (%T), want %#v (%T)", c.col.Type, c.in, got, got, c.want, c.want)
		}
	}
}

// sameValue compares parsed values: times by instant and offset, bytes by
// content, everything else by ==.
func sameValue(got, want any) bool {
	switch w := want.(type) {
	case time.Time:
		g, ok := got.(time.Time)
		_, gOff := g.Zone()
		_, wOff := w.Zone()
		return ok && g.Equal(w) && gOff == wOff && g.Hour() == w.Hour()
	case []byte:
		g, ok := got.([]byte)
		return ok && bytes.Equal(g, w)
	}
	return got == want
}

// TestCellParserRefuses: each value go-mssqldb would load wrong, or SQL Server
// would round, is an error that says why.
func TestCellParserRefuses(t *testing.T) {
	cases := []struct {
		col  gosmo.BulkColumn
		in   string
		want string
	}{
		{gosmo.BulkColumn{Type: "bit"}, "2", "not a bit"},
		{gosmo.BulkColumn{Type: "bit"}, "", "not a bit"},
		{gosmo.BulkColumn{Type: "tinyint"}, "300", "out of range (0 to 255)"},
		{gosmo.BulkColumn{Type: "tinyint"}, "-1", "out of range"},
		{gosmo.BulkColumn{Type: "int"}, "2147483648", "out of range"},
		{gosmo.BulkColumn{Type: "bigint"}, "9223372036854775808", "out of range"},
		{gosmo.BulkColumn{Type: "int"}, "1.0", "not an integer"},
		{gosmo.BulkColumn{Type: "int"}, " 1", "not an integer"},
		{gosmo.BulkColumn{Type: "int"}, "", "not an integer"},
		{gosmo.BulkColumn{Type: "decimal", Precision: 5, Scale: 2}, "1234.5", "does not fit decimal(5,2)"},
		{gosmo.BulkColumn{Type: "decimal", Precision: 5, Scale: 2}, "1.234", "more than 2 decimal places"},
		{gosmo.BulkColumn{Type: "decimal", Precision: 5, Scale: 2}, "1e3", "not a decimal"},
		{gosmo.BulkColumn{Type: "decimal", Precision: 5, Scale: 2}, ".", "not a decimal"},
		{gosmo.BulkColumn{Type: "decimal", Precision: 5, Scale: 2}, "-", "not a decimal"},
		{gosmo.BulkColumn{Type: "money"}, "922337203685477.5808", "out of range (-922337203685477.5808 to 922337203685477.5807)"},
		{gosmo.BulkColumn{Type: "money"}, "-922337203685477.5809", "out of range"},
		{gosmo.BulkColumn{Type: "money"}, "99999999999999999999", "out of range"},
		{gosmo.BulkColumn{Type: "money"}, "1.23456", "more than 4 decimal places"},
		{gosmo.BulkColumn{Type: "money"}, "$1", "not a money amount"},
		{gosmo.BulkColumn{Type: "smallmoney"}, "214748.3648", "out of range (-214748.3648 to 214748.3647)"},
		{gosmo.BulkColumn{Type: "float"}, "NaN", "out of range"},
		{gosmo.BulkColumn{Type: "float"}, "Inf", "out of range"},
		{gosmo.BulkColumn{Type: "float"}, "1E+309", "out of range"},
		{gosmo.BulkColumn{Type: "real"}, "3.5E+38", "out of range for real"},
		{gosmo.BulkColumn{Type: "float"}, "x", "not a number"},
		{gosmo.BulkColumn{Type: "date"}, "2023-02-29", "not a date"},
		{gosmo.BulkColumn{Type: "date"}, "2024-02-29 00:00:00", "not a date"},
		{gosmo.BulkColumn{Type: "date"}, "24-02-29", "not a date"},
		{gosmo.BulkColumn{Type: "datetime"}, "1752-12-31 00:00:00", "out of range for datetime"},
		{gosmo.BulkColumn{Type: "datetime"}, "9999-12-31 23:59:59.999", "out of range for datetime"},
		{gosmo.BulkColumn{Type: "datetime"}, "2024-01-01 00:00:00.1234", "more than 3 fractional-second digits"},
		{gosmo.BulkColumn{Type: "datetime"}, "2024-01-01 24:00:00", "not a datetime"},
		{gosmo.BulkColumn{Type: "datetime"}, "2024-01-01", "not a datetime"},
		{gosmo.BulkColumn{Type: "smalldatetime"}, "1899-12-31 23:59:00", "out of range for smalldatetime"},
		{gosmo.BulkColumn{Type: "smalldatetime"}, "2079-06-06 23:59:30", "out of range for smalldatetime"},
		{gosmo.BulkColumn{Type: "datetime2", Scale: 2}, "2024-01-01 00:00:00.123", "more than 2 fractional-second digits"},
		{gosmo.BulkColumn{Type: "datetime2", Scale: 7}, "2024-01-01 00:00:00.", "not a datetime2"},
		{gosmo.BulkColumn{Type: "time", Scale: 0}, "08:00:00.5", "more than 0 fractional-second digits"},
		{gosmo.BulkColumn{Type: "time", Scale: 7}, "8:00:00", "not a time"},
		{gosmo.BulkColumn{Type: "datetimeoffset", Scale: 0}, "2024-01-01 00:00:00 +14:01", "not a datetimeoffset"},
		{gosmo.BulkColumn{Type: "datetimeoffset", Scale: 0}, "2024-01-01 00:00:00 05:30", "not a datetimeoffset"},
		{gosmo.BulkColumn{Type: "uniqueidentifier"}, "6F9619FF8B86D011B42D00C04FD430C8", "not a uniqueidentifier"},
		{gosmo.BulkColumn{Type: "uniqueidentifier"}, "6F9619FF-8B86-D011-B42D-00C04FD430CG", "not a uniqueidentifier"},
		{gosmo.BulkColumn{Type: "varbinary", Length: 2}, "0x010203", "3 bytes is longer than the column's 2"},
		{gosmo.BulkColumn{Type: "varbinary", Length: -1}, "01", "not binary"},
		{gosmo.BulkColumn{Type: "varbinary", Length: -1}, "0x123", "not binary"},
		{gosmo.BulkColumn{Type: "nvarchar", Length: 1}, "😀", "2 characters is longer than the column's 1"},
		{gosmo.BulkColumn{Type: "nvarchar", Length: -1}, "a\xffb", "not valid UTF-8"},
		{gosmo.BulkColumn{Type: "varchar", Length: 3, CodePage: 1252, DatabaseCodePage: 1252}, "café", "4 bytes is longer than the column's 3"},
		{gosmo.BulkColumn{Type: "varchar", Length: 10, CodePage: 1252, DatabaseCodePage: 1252}, "a日b", `'日' (U+65E5) is not in the column's code page 1252`},
		{gosmo.BulkColumn{Type: "varchar", Length: -1, CodePage: 1252, DatabaseCodePage: 1252}, "\xff", "not valid UTF-8"},
		{gosmo.BulkColumn{Type: "varchar", Length: 10, CodePage: 932, DatabaseCodePage: 1252}, "日", "not in the database's code page 1252"},
		{gosmo.BulkColumn{Type: "varchar", Length: 10, CodePage: 1252, DatabaseCodePage: 65001}, "日", "not in the column's code page 1252"},
		{gosmo.BulkColumn{Type: "varchar", Length: 4, CodePage: 932, DatabaseCodePage: 65001}, "日本語", "6 bytes is longer than the column's 4"},
		{gosmo.BulkColumn{Type: "varchar", Length: 4, CodePage: 65001, DatabaseCodePage: 1252}, "café", "5 bytes is longer than the column's 4"},
		{gosmo.BulkColumn{Type: "varchar", Length: -1, CodePage: 65001, DatabaseCodePage: 65001}, "\xff", "not valid UTF-8"},
	}
	for _, c := range cases {
		got, err := mustParser(t, c.col)(c.in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %q = %#v, %v; want an error with %q", c.col.Type, c.in, got, err, c.want)
		}
	}
}

func TestCellParserUnsupportedTypes(t *testing.T) {
	for _, col := range []gosmo.BulkColumn{
		{Type: "sql_variant"}, {Type: "geography"}, {Type: "geometry"}, {Type: "hierarchyid"},
		{Type: "timestamp"}, {Type: "rowversion"}, {Type: "vector"}, {Type: "json"}, {Type: "sysname"},
		{Type: "varchar", Length: 10, CodePage: 1252}, {Type: "char", Length: 10, CodePage: 1361, DatabaseCodePage: 1252},
		{Type: "text", CodePage: 1252, DatabaseCodePage: 1361},
	} {
		if _, err := NewCellParser(col); !errors.Is(err, ErrUnsupportedType) {
			t.Errorf("%+v: err %v, want ErrUnsupportedType", col, err)
		}
	}
}

// TestCellParserInvertsCells renders values the way the grid does (cells.go,
// with the layout and scale of the column) and parses the text back: each
// gives the value it was rendered from, at the column's precision.
func TestCellParserInvertsCells(t *testing.T) {
	render := func(v any, decimalLike bool, layout string) string {
		return string(appendValue(nil, v, decimalLike, layout))
	}
	type roundTrip struct {
		col  gosmo.BulkColumn
		text string
		want any
	}
	var cases []roundTrip

	for _, n := range []int64{0, 1, -1, 255, math.MaxInt16, math.MinInt32, math.MaxInt64, math.MinInt64} {
		col := gosmo.BulkColumn{Type: "bigint"}
		cases = append(cases, roundTrip{col, render(n, false, ""), n})
	}
	for _, b := range []bool{true, false} {
		cases = append(cases, roundTrip{gosmo.BulkColumn{Type: "bit"}, render(b, false, ""), b})
	}
	for _, d := range []string{"0.00", "-12345.67", "99999999999999999999999999999999.999999", "0.000001"} {
		col := gosmo.BulkColumn{Type: "decimal", Precision: 38, Scale: 6}
		got := render([]byte(d), true, "")
		cases = append(cases, roundTrip{col, got, d})
	}
	for _, m := range []string{"12.5000", "-922337203685477.5808", "0.0001", "0.0000", "100.1230"} {
		// The grid's text, its leading zeros trimmed ("0.0001" stays).
		text := string(appendMoney(nil, []byte(m)))
		want, neg := strings.CutPrefix(text, "-")
		if want = strings.TrimLeft(want, "0"); strings.HasPrefix(want, ".") {
			want = "0" + want
		}
		if neg {
			want = "-" + want
		}
		cases = append(cases, roundTrip{gosmo.BulkColumn{Type: "money"}, text, want})
	}
	for _, f := range []float64{0, 0.1, -1e-5, 123456789012345, 1e15, math.MaxFloat64, math.SmallestNonzeroFloat64, 1.0 / 3} {
		cases = append(cases, roundTrip{gosmo.BulkColumn{Type: "float"}, string(appendFloat(nil, f, 64)), f})
	}
	for _, f := range []float32{0, 0.1, -1e-5, math.MaxFloat32, math.SmallestNonzeroFloat32, 1.0 / 3} {
		cases = append(cases, roundTrip{gosmo.BulkColumn{Type: "real"}, string(appendFloat(nil, float64(f), 32)), f})
	}
	for _, b := range [][]byte{{}, {0}, {0xDE, 0xAD, 0xBE, 0xEF}} {
		cases = append(cases, roundTrip{gosmo.BulkColumn{Type: "varbinary", Length: -1}, render(b, false, ""), b})
	}
	var g mssql.UniqueIdentifier
	g.Scan("01234567-89AB-CDEF-0123-456789ABCDEF")
	wire, _ := g.Value()
	cases = append(cases, roundTrip{gosmo.BulkColumn{Type: "uniqueidentifier"},
		string(appendGUID(nil, mssql.NullUniqueIdentifier{UUID: g, Valid: true})), wire})

	// Every date/time type at every scale, from a value with all nine
	// fractional digits: the grid's text cuts it to the scale, and parsing
	// gives that cut value back.
	full := time.Date(2024, 2, 29, 13, 45, 30, 123456789, time.UTC)
	offset := time.FixedZone("", -(9*3600 + 30*60))
	for _, typ := range []string{"date", "datetime", "smalldatetime", "time", "datetime2", "datetimeoffset"} {
		scales := []int{7}
		if typ == "time" || typ == "datetime2" || typ == "datetimeoffset" {
			scales = []int{0, 1, 2, 3, 4, 5, 6, 7}
		}
		for _, scale := range scales {
			layout := timeLayout(strings.ToUpper(typ), scale, true)
			v := full
			switch typ {
			case "datetimeoffset":
				v = full.In(offset)
			case "smalldatetime":
				v = full.Truncate(time.Minute) // all a smalldatetime holds
			}
			text := render(v, false, layout)
			want, err := time.ParseInLocation(layout, text, v.Location())
			if err != nil {
				t.Fatalf("%s(%d): %q does not parse under its own layout: %v", typ, scale, text, err)
			}
			if typ == "time" {
				want = time.Date(1900, 1, 1, want.Hour(), want.Minute(), want.Second(), want.Nanosecond(), time.UTC)
			}
			cases = append(cases, roundTrip{gosmo.BulkColumn{Type: typ, Scale: scale}, text, want})
		}
	}

	for _, c := range cases {
		got, err := mustParser(t, c.col)(c.text)
		if err != nil {
			t.Errorf("%s(%d) %q: %v", c.col.Type, c.col.Scale, c.text, err)
			continue
		}
		if !sameValue(got, c.want) {
			t.Errorf("%s(%d) %q = %#v, want %#v", c.col.Type, c.col.Scale, c.text, got, c.want)
		}
	}
}
