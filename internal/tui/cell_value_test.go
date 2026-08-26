package tui

import (
	"strings"
	"testing"
)

func TestClassifyCellValue(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  cellValueKind
	}{
		{"element", "<root><a id=\"1\">x</a></root>", cellXML},
		{"declaration", "<?xml version=\"1.0\"?><root/>", cellXML},
		{"leading and trailing space", "  \n<root/>\n ", cellXML},
		{"multi-root fragment", "<a/><b/>", cellXML},
		{"showplan", `<ShowPlanXML xmlns="http://x"><BatchSequence/></ShowPlanXML>`, cellXML},
		{"comment and cdata", "<r><!-- c --><![CDATA[<not markup>]]></r>", cellXML},
		// Bracketed but malformed still counts — see classifyCellValue.
		{"unclosed element", "<root>", cellXML},
		{"mismatched tags", "<a></b>", cellXML},

		{"json object", `{"a": 1}`, cellJSON},
		{"json object with space", "  { \"a\": [1,2] }  ", cellJSON},
		{"empty object", "{}", cellJSON},
		{"json array of objects", `[{"a":1},{"a":2}]`, cellJSON},
		{"json array of strings", `["a", "b"]`, cellJSON},
		{"json array of numbers", "[1, 2, 3]", cellJSON},
		{"json array of negatives", "[-1]", cellJSON},
		{"json array of literals", "[true, false, null]", cellJSON},
		{"empty array", "[]", cellJSON},
		{"nested array", "[[1],[2]]", cellJSON},
		{"malformed object still json", `{"a": }`, cellJSON},

		// The reason jsonArrayLike exists: bracket-quoted SQL names are
		// everywhere in result sets and must keep the grid's own popup.
		{"quoted name", "[dbo]", cellPlain},
		{"quoted name with dot", "[Ord.ers]", cellPlain},
		{"quoted name with space", "[my db]", cellPlain},

		{"plain text", "hello world", cellPlain},
		{"empty", "", cellPlain},
		{"bare bracket", "<", cellPlain},
		{"less-than only", "<3 and >2", cellPlain},
		{"html-ish, unbracketed end", "<p>one<p>two", cellPlain},
		{"xml with trailing text", "<root/> see above", cellPlain},
		{"json with trailing text", `{"a":1} and more`, cellPlain},
		{"lone brace", "{", cellPlain},
	}
	for _, tt := range tests {
		if got := classifyCellValue(tt.value); got != tt.want {
			t.Errorf("%s: classifyCellValue(%q) = %v, want %v", tt.name, tt.value, got, tt.want)
		}
	}
}

// TestClassifyCellKind pins the declared-type path: an xml column opens as XML
// even when its serialized value doesn't end in '>', which is the sp_block
// case (`try_cast('<?query --' + text + '--?>' as xml)` comes back ending in
// "--?&gt;") that the text sniff alone dropped into the grid's popup.
func TestClassifyCellKind(t *testing.T) {
	tests := []struct {
		name    string
		sqlType string
		value   string
		want    cellValueKind
	}{
		{"xml column, entity-escaped tail", "xml", "<?query --\r\nselect 1\r\n--?&gt;", cellXML},
		{"xml column, ordinary document", "xml", "<root/>", cellXML},
		{"xml column, NULL cell", "xml", "NULL", cellPlain},
		{"xml column, empty cell", "xml", "", cellPlain},
		{"json column", "json", `{"a":1}`, cellJSON},
		{"type case-insensitive", "XML", "<?query --x--?&gt;", cellXML},
		{"sized type is not xml", "nvarchar(max)", "plain text", cellPlain},
		{"no type falls back to sniff", "", "<root/>", cellXML},
		{"no type, plain value", "", "hello", cellPlain},
		{"nvarchar holding xml still sniffs", "nvarchar(max)", "<root/>", cellXML},
	}
	for _, tt := range tests {
		if got := classifyCellKind(tt.sqlType, tt.value); got != tt.want {
			t.Errorf("%s: classifyCellKind(%q, %q) = %v, want %v", tt.name, tt.sqlType, tt.value, got, tt.want)
		}
	}
}

// TestClassifyCellValueLargeValue pins that detection stays a bracket test on
// a value far past any parse budget — an xml or json column holding megabytes
// must still open in its own panel, and a large plain-text one must not.
func TestClassifyCellValueLargeValue(t *testing.T) {
	bigXML := "<root>" + strings.Repeat("<row><c>data</c></row>", 20000) + "</root>"
	if got := classifyCellValue(bigXML); got != cellXML {
		t.Errorf("a large XML document classified as %v, want cellXML", got)
	}
	bigJSON := "[" + strings.TrimSuffix(strings.Repeat(`{"c":"data"},`, 20000), ",") + "]"
	if got := classifyCellValue(bigJSON); got != cellJSON {
		t.Errorf("a large JSON document classified as %v, want cellJSON", got)
	}
	if got := classifyCellValue(strings.Repeat("x", len(bigXML))); got != cellPlain {
		t.Errorf("a large plain-text value classified as %v, want cellPlain", got)
	}
}
