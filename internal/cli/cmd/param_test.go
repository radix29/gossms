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
	} {
		if p, err := parseParam(raw); err == nil {
			t.Errorf("%q: accepted as %+v", raw, p)
		} else if e, ok := err.(*output.Error); !ok || e.Code != output.CodeUsageError {
			t.Errorf("%q: %v", raw, err)
		}
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
