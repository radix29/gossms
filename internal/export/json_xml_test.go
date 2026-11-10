package export

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
)

// tableOf is a Table over rows, with nulls marking the NULL cells.
type tableOf struct {
	rows  [][]string
	nulls map[[2]int]bool
}

func (t tableOf) Len() int                 { return len(t.rows) }
func (t tableOf) Row(i int) []string       { return t.rows[i] }
func (t tableOf) IsNull(row, col int) bool { return t.nulls[[2]int{row, col}] }

// typedTable has a column of each JSON kind, a duplicate and a nameless column,
// a NULL, an empty string and the string 'NULL', and cells that are not what
// their type says.
var (
	typedCols  = []string{"id", "price", "on", "doc", "name", "name", ""}
	typedTypes = []string{"int", "decimal(18,2)", "bit", "json", "nvarchar(10)", "varchar(5)", "float"}
	typedTable = tableOf{
		rows: [][]string{
			{"1", "10.50", "1", `{"a": [1, 2]}`, "Ann <&>", "NULL", "1.5E-05"},
			{"2", "NULL", "0", "{not json", "", "x\ny", "1,5"},
		},
		nulls: map[[2]int]bool{{1, 1}: true},
	}
)

func TestWriteTableJSON(t *testing.T) {
	var b bytes.Buffer
	if err := WriteTable(&b, FormatJSON, typedCols, typedTypes, typedTable); err != nil {
		t.Fatal(err)
	}
	want := `[
{"id":1,"price":"10.50","on":true,"doc":{"a":[1,2]},"name":"Ann <&>","name_6":"NULL","column7":1.5E-05},
{"id":2,"price":null,"on":false,"doc":"{not json","name":"","name_6":"x\ny","column7":"1,5"}
]
`
	if b.String() != want {
		t.Errorf("JSON =\n%s\nwant\n%s", b.String(), want)
	}
	if !json.Valid(b.Bytes()) {
		t.Error("output is not valid JSON")
	}
}

func TestWriteTableJSONEmptySetIsAnEmptyArray(t *testing.T) {
	var b bytes.Buffer
	if err := WriteTable(&b, FormatJSON, []string{"a"}, nil, tableOf{}); err != nil {
		t.Fatal(err)
	}
	if b.String() != "[]\n" {
		t.Errorf("JSON = %q, want []", b.String())
	}
}

func TestWriteTableXML(t *testing.T) {
	var b bytes.Buffer
	cols := []string{"id", "Unit Price", "note", "id", ""}
	tbl := tableOf{rows: [][]string{{"1", "2.50", "a<b & \"c\"\r\n\tz", "x", "NULL"}, {"2", "", "NULL", "y", "z"}},
		nulls: map[[2]int]bool{{1, 2}: true}}
	if err := WriteTable(&b, FormatXML, cols, nil, tbl); err != nil {
		t.Fatal(err)
	}
	want := `<?xml version="1.0" encoding="UTF-8"?>
<ResultSet xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <Row>
    <id>1</id>
    <Unit_x0020_Price>2.50</Unit_x0020_Price>
    <note>a&lt;b &amp; &#34;c&#34;&#xD;&#xA;&#x9;z</note>
    <id_4>x</id_4>
    <column5>NULL</column5>
  </Row>
  <Row>
    <id>2</id>
    <Unit_x0020_Price></Unit_x0020_Price>
    <note xsi:nil="true"/>
    <id_4>y</id_4>
    <column5>z</column5>
  </Row>
</ResultSet>
`
	if b.String() != want {
		t.Errorf("XML =\n%s\nwant\n%s", b.String(), want)
	}

	// A parser reads the text back exactly, line breaks included, and sees the
	// NULL as nil rather than as an empty element.
	var doc struct {
		Rows []struct {
			Note struct {
				Text string `xml:",chardata"`
				Nil  string `xml:"http://www.w3.org/2001/XMLSchema-instance nil,attr"`
			} `xml:"note"`
		} `xml:"Row"`
	}
	if err := xml.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.Rows[0].Note.Text; got != "a<b & \"c\"\r\n\tz" {
		t.Errorf("note read back = %q", got)
	}
	if doc.Rows[0].Note.Nil != "" || doc.Rows[1].Note.Nil != "true" {
		t.Errorf("nil attributes = %q, %q", doc.Rows[0].Note.Nil, doc.Rows[1].Note.Nil)
	}
}

func TestWriteTableXMLEmptySetIsAnEmptyRoot(t *testing.T) {
	var b bytes.Buffer
	if err := WriteTable(&b, FormatXML, []string{"a"}, nil, tableOf{}); err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(b.Bytes(), new(struct{})); err != nil || !strings.HasSuffix(b.String(), "</ResultSet>\n") {
		t.Errorf("XML = %q (%v)", b.String(), err)
	}
}

// XMLName encodes as FOR XML does (SELECT 1 AS [Unit Price] FOR XML PATH gives
// <Unit_x0020_Price>).
func TestXMLName(t *testing.T) {
	for in, want := range map[string]string{
		"id":          "id",
		"Unit Price":  "Unit_x0020_Price",
		"1st":         "_x0031_st",
		"a1.b-c":      "a1.b-c",
		"-x":          "_x002D_x",
		"ns:col":      "ns_x003A_col",
		"_x0020_":     "_x005F_x0020_",
		"a_b":         "a_b",
		"größe":       "größe",
		"日本":          "日本",
		"a😀":          "a😀",
		"a\U000F0000": "a_x000F0000_",
		"":            "",
	} {
		if got := XMLName(in); got != want {
			t.Errorf("XMLName(%q) = %q, want %q", in, got, want)
		}
	}
}

// CSV and TSV are RecordWriter's bytes for one set: the format Results To File
// and the command line write.
func TestWriteTableCSVIsRecordWritersSet(t *testing.T) {
	for _, f := range []struct {
		format Format
		sep    rune
	}{{FormatCSV, CSV}, {FormatTSV, TSV}} {
		var got, want bytes.Buffer
		if err := WriteTable(&got, f.format, typedCols, typedTypes, typedTable); err != nil {
			t.Fatal(err)
		}
		rw := NewRecordWriter(&want, f.sep)
		rw.BeginSet(typedCols)
		rw.WriteRecord(typedTable.rows[0], []bool{false, false, false, false, false, false, false})
		rw.WriteRecord(typedTable.rows[1], []bool{false, true, false, false, false, false, false})
		rw.Flush()
		if got.String() != want.String() {
			t.Errorf("format %d =\n%q\nwant\n%q", f.format, got.String(), want.String())
		}
	}
}
