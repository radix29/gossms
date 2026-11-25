package cmd

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/cli/output"
)

func TestParseParam(t *testing.T) {
	for _, tt := range []struct {
		raw              string
		name, typ, value string
	}{
		{"id=42:int", "@id", "int", "N'42'"},
		{"@id=42:INT", "@id", "int", "N'42'"},
		// The type follows the last ':', the value the first '='.
		{"at=12:30:00:time(0)", "@at", "time(0)", "N'12:30:00'"},
		{"q=a=b:nvarchar(max)", "@q", "nvarchar(max)", "N'a=b'"},
		{"who=O'Brien:nvarchar(50)", "@who", "nvarchar(50)", "N'O''Brien'"},
		{"amount=1,5:decimal(9, 2)", "@amount", "decimal(9,2)", "N'1,5'"},
		{"e=:varchar(10)", "@e", "varchar(10)", "N''"},
		{"b=0xDEADbeef:varbinary(max)", "@b", "varbinary(max)", "0xDEADbeef"},
		{"név=ő:nchar(1)", "@név", "nchar(1)", "N'ő'"},
		{"t#1=x:sysname", "@t#1", "sysname", "N'x'"},
	} {
		p, err := parseParam(tt.raw)
		if err != nil {
			t.Errorf("%q: %v", tt.raw, err)
			continue
		}
		if p.name != tt.name || p.typ != tt.typ || p.value != tt.value {
			t.Errorf("%q: got %+v", tt.raw, p)
		}
	}
}

// Name and type are T-SQL in the declaration list: anything outside the
// closed forms is refused, never escaped.
func TestParseParamRefuses(t *testing.T) {
	for _, raw := range []string{
		"id42:int",                         // no =
		"id=42",                            // no type
		"=42:int",                          // no name
		"@@id=1:int",                       // @@
		"[id]=1:int",                       // quoted name
		"id x=1:int",                       // space in the name
		"id=1:int, @x int",                 // a second declaration through the type
		"id=1:int OUTPUT",                  // OUTPUT
		"id=1:nvarchar(50) OUTPUT",         // OUTPUT after arguments
		"id=1:dbo.mytype",                  // user type
		"id=1:text",                        // not declared by --param
		"id=1:int(4)",                      // arguments where none are taken
		"id=1:nvarchar()",                  // empty arguments
		"id=1:nvarchar(1,2)",               // two for a length
		"id=1:decimal(max)",                // max for a precision
		"id=1:decimal(1,2,3)",              // three
		"id=1:float(99999)",                // over four digits
		"b=DEAD:varbinary(4)",              // binary not 0x
		"b=0xZZ:binary(1)",                 // not hex
		"b=0x1');DROP TABLE t--:binary(8)", // not hex either
		"a=1,5:money",                      // the server drops the comma: 15.00
		"a=12,50:smallmoney",               // 1250.00
		"a=$5:money",                       // currency sign
		"a=1.23456:money",                  // more than four decimals
		"a=214749:smallmoney",              // out of range
	} {
		if p, err := parseParam(raw); err == nil {
			t.Errorf("%q: accepted as %+v", raw, p)
		} else if e, ok := err.(*output.Error); !ok || e.Code != output.CodeUsageError {
			t.Errorf("%q: %v", raw, err)
		}
	}
}

// A money amount is sent as written once it has the only shape the server
// cannot read as another amount.
func TestParseParamMoney(t *testing.T) {
	for raw, want := range map[string]string{
		"a=-12.50:money":           "N'-12.50'",
		"a=1250:money":             "N'1250'",
		"a=+0.0001:smallmoney":     "N'+0.0001'",
		"a=214748.3647:smallmoney": "N'214748.3647'",
	} {
		p, err := parseParam(raw)
		if err != nil || p.value != want {
			t.Errorf("%q: %+v %v, want %s", raw, p, err, want)
		}
	}
	if _, err := parseParam("a=12,50:money"); err == nil || !strings.Contains(err.Error(), "is not a money amount") {
		t.Errorf("12,50: %v", err)
	}
}

func TestReadParamsRefusesADuplicate(t *testing.T) {
	if _, err := readParams([]string{"id=1:int", "ID=2:int"}); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Errorf("got %v", err)
	}
	if ps, err := readParams(nil); ps != nil || err != nil {
		t.Errorf("none: %v %v", ps, err)
	}
}

func TestParameterize(t *testing.T) {
	ps, err := readParams([]string{"id=42:int", "who=O'Brien:nvarchar(50)"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := parameterize("SELECT * FROM t WHERE id = @id AND n = @who AND s = 'it''s'", ps)
	if err != nil {
		t.Fatal(err)
	}
	want := `EXEC sp_executesql N'SELECT * FROM t WHERE id = @id AND n = @who AND s = ''it''''s''', ` +
		`N'@id int, @who nvarchar(50)', @id = N'42', @who = N'O''Brien';`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	// A batch that starts below line 1 keeps its line numbers: the server
	// numbers the dynamic text from its own first line.
	got, _ = parameterize("\n\n-- note\nSELECT @id\nGO\n", ps)
	if !strings.HasPrefix(got, "EXEC sp_executesql N'\n\n-- note\nSELECT @id") {
		t.Errorf("lines: %q", got)
	}
	for _, script := range []string{"SELECT 1\nGO\nSELECT 2", "SELECT @id\nGO 3", "GO"} {
		if _, err := parameterize(script, ps); err == nil {
			t.Errorf("%q: accepted", script)
		}
	}
}

// A date is ISO 8601 only, sent as YYYYMMDD or with the T: the forms no
// DATEFORMAT or LANGUAGE reads another way.
func TestParseParamDate(t *testing.T) {
	for _, tt := range []struct {
		raw  string
		want string // the literal, or "!" and a substring of the error
	}{
		{"d=2026-01-02:datetime", "N'20260102'"},
		{"d=2026-01-02:smalldatetime", "N'20260102'"},
		{"d=2026-01-02:date", "N'20260102'"},
		{"d= 2026-01-02 :date", "N'20260102'"},
		{"d=2026-01-02:datetime2(3)", "N'20260102'"},
		{"d=2026-01-02:datetimeoffset", "N'20260102'"},
		{"d=2026-01-02 10:30:datetime", "N'2026-01-02T10:30:00'"},
		{"d=2026-01-02T10:30:15:smalldatetime", "N'2026-01-02T10:30:15'"},
		{"d=2026-01-02T10:30:15.123:datetime", "N'2026-01-02T10:30:15.123'"},
		{"d=2026-01-02 10:30:15.1234567:datetime2(7)", "N'2026-01-02T10:30:15.1234567'"},
		{"d=2026-01-02T10:30:15Z:datetimeoffset(0)", "N'2026-01-02T10:30:15Z'"},
		{"d=2026-01-02T10:30+02:00:datetimeoffset", "N'2026-01-02T10:30:00+02:00'"},
		{"d=2026-01-02T10:30:15.5-14:00:datetimeoffset", "N'2026-01-02T10:30:15.5-14:00'"},
		{"d=2024-02-29:date", "N'20240229'"},
		{"d=01/02/2026:datetime", "!not an ISO 8601 date"},
		{"d=2026-1-2:date", "!not an ISO 8601 date"},
		{"d=20260102:date", "!not an ISO 8601 date"},
		{"d=2026-01-02 25:00:datetime", "!not a time of day"},
		{"d=2026-01-02 10:60:datetime", "!not a time of day"},
		{"d=2026-02-30:date", "!not a date"},
		{"d=2025-02-29:date", "!not a date"},
		{"d=2026-13-01:date", "!not a date"},
		{"d=0000-01-01:date", "!not a date"},
		{"d=2026-01-02T10:00:date", "!date keeps none"},
		{"d=2026-01-02T10:00Z:datetime", "!datetime keeps none"},
		{"d=2026-01-02T10:00+02:00:datetime2", "!datetime2 keeps none"},
		{"d=2026-01-02+02:00:datetimeoffset", "!no time"},
		{"d=2026-01-02T10:00+15:00:datetimeoffset", "!outside"},
		{"d=2026-01-02T10:00:00.12345678:datetime2", "!not an ISO 8601 date"},
	} {
		p, err := parseParam(tt.raw)
		if want, ok := strings.CutPrefix(tt.want, "!"); ok {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%q: %v (%+v), want %q", tt.raw, err, p, want)
			} else if e, ok := err.(*output.Error); !ok || e.Code != output.CodeUsageError {
				t.Errorf("%q: not a usage error: %v", tt.raw, err)
			}
			continue
		}
		if err != nil || p.value != tt.want {
			t.Errorf("%q: %v %q, want %q", tt.raw, err, p.value, tt.want)
		}
	}
}

// The server turns any number but 0 into 1.
func TestParseParamBit(t *testing.T) {
	for raw, want := range map[string]string{
		"b=1:bit": "N'1'", "b=0:bit": "N'0'", "b=TRUE:bit": "N'1'", "b=false:bit": "N'0'", "b= 1 :bit": "N'1'",
	} {
		if p, err := parseParam(raw); err != nil || p.value != want {
			t.Errorf("%q: %v %q, want %q", raw, err, p.value, want)
		}
	}
	for _, raw := range []string{"b=2:bit", "b=-1:bit", "b=yes:bit", "b=1.0:bit"} {
		if _, err := parseParam(raw); err == nil || !strings.Contains(err.Error(), "is not a bit") {
			t.Errorf("%q: %v", raw, err)
		}
	}
}

// An empty value would convert to 0 or 1900-01-01; only a string type may be
// empty.
func TestParseParamEmptyValue(t *testing.T) {
	for _, typ := range []string{"int", "date", "decimal(5,1)", "bit", "uniqueidentifier", "datetime2(3)"} {
		if _, err := parseParam("p=:" + typ); err == nil {
			t.Errorf("empty %s accepted", typ)
		}
		if _, err := parseParam("p= :" + typ); err == nil {
			t.Errorf("blank %s accepted", typ)
		}
	}
	for typ, want := range map[string]string{"int": "not an int", "uniqueidentifier": "not a uniqueidentifier"} {
		if _, err := parseParam("p=:" + typ); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("empty %s: %v, want %q", typ, err, want)
		}
	}
	for _, typ := range []string{"nvarchar(10)", "varchar(max)", "xml"} {
		if _, err := parseParam("p=:" + typ); err != nil {
			t.Errorf("empty %s: %v", typ, err)
		}
	}
}

// sp_executesql truncates silently, so a value over (n) is refused.
func TestParseParamTooLong(t *testing.T) {
	for _, tt := range []struct {
		raw string
		ok  bool
	}{
		{"p=abcde:nvarchar(5)", true},
		{"p=abcdef:nvarchar(5)", false},
		{"p=abcde:varchar(5)", true},
		{"p=abcdef:varchar(5)", false},
		{"p=😀:nvarchar(1)", false}, // surrogate pair: 2 UTF-16 units
		{"p=😀:nvarchar(2)", true},
		{"p=😀:varchar(1)", true}, // one rune
		{"p=ab:nchar(1)", false},
		{"p=" + strings.Repeat("x", 128) + ":sysname", true},
		{"p=" + strings.Repeat("x", 129) + ":sysname", false},
		{"p=" + strings.Repeat("x", 9000) + ":nvarchar(max)", true},
		{"b=0xAABB:varbinary(2)", true},
		{"b=0xAABBCC:varbinary(2)", false},
		{"b=0xAABBCC:varbinary(max)", true},
	} {
		_, err := parseParam(tt.raw)
		if (err == nil) != tt.ok {
			t.Errorf("%.40q: err = %v, want ok=%v", tt.raw, err, tt.ok)
		}
	}
}

// A length-less string or binary type is length 1 in a declaration: the
// server would receive the value's first character.
func TestParseParamRefusesALengthlessType(t *testing.T) {
	for _, typ := range []string{"char", "varchar", "nchar", "nvarchar", "binary", "varbinary", "NVARCHAR"} {
		value := "Smith"
		if strings.HasSuffix(strings.ToLower(typ), "binary") {
			value = "0xAABB"
		}
		_, err := parseParam("n=" + value + ":" + typ)
		want := strings.ToLower(typ) + " needs a length"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", typ, err, want)
		}
	}
	for _, raw := range []string{"n=S:nvarchar(1)", "n=S:char(1)", "b=0xAA:binary(1)"} {
		if _, err := parseParam(raw); err != nil {
			t.Errorf("%q: %v", raw, err)
		}
	}
}

// The server rounds a decimal with more places than its scale, silently; a
// bare decimal is decimal(18,0).
func TestParseParamDecimalFits(t *testing.T) {
	for _, tt := range []struct {
		raw  string
		want string // "" accepted, else a substring of the error
	}{
		{"d=12:decimal", ""},
		{"d=12.5:decimal", "1 decimal place; decimal(18,0) keeps 0"},
		{"d=12.0:decimal", ""}, // a trailing zero changes nothing
		{"d=123.45:decimal(5,2)", ""},
		{"d=123.456:decimal(5,2)", "3 decimal places"},
		{"d=1234.5:decimal(5,2)", "4 digits before the point; decimal(5,2) keeps 3"},
		{"d=0123.45:decimal(5,2)", ""}, // leading zero
		{"d=999:numeric(3)", ""},
		{"d=1000:numeric(3)", "numeric(3,0) keeps 3"},
		{"d=-123.45:decimal(5,2)", ""},
		{"d=-1234.5:decimal(5,2)", "4 digits"},
		{"d=+123.45:decimal(5,2)", ""},
		{"d= 1.5 :decimal(2,1)", ""},
		{"d=.5:decimal(1,1)", ""},
		{"d=5.:decimal(1)", ""},
		{"d=" + strings.Repeat("9", 38) + ":decimal(38)", ""},
		{"d=1e3:decimal", ""},      // not a decimal literal: the server refuses it
		{"d=1,5:decimal(9,2)", ""}, // likewise
		{"d=1:decimal(0)", "wrong arguments"},
		{"d=1:decimal(39)", "wrong arguments"},
		{"d=1:decimal(2,3)", "wrong arguments"},
	} {
		_, err := parseParam(tt.raw)
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("%q: %v", tt.raw, err)
		case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
			t.Errorf("%q: %v, want %q", tt.raw, err, tt.want)
		case err != nil:
			if e, ok := err.(*output.Error); !ok || e.Code != output.CodeUsageError {
				t.Errorf("%q: not a usage error: %v", tt.raw, err)
			}
		}
	}
}
