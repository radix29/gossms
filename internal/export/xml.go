package export

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// XMLWriter writes one result set as an XML document, the shape of SQL
// Server's FOR XML PATH('Row'), ROOT('ResultSet'), ELEMENTS XSINIL:
//
//	<?xml version="1.0" encoding="UTF-8"?>
//	<ResultSet xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
//	  <Row>
//	    <id>1</id>
//	    <note xsi:nil="true"/>
//	  </Row>
//	</ResultSet>
//
// A NULL is an empty element with xsi:nil="true", so it never reads as an
// empty string. Each column is an element named by XMLName — a name XML does
// not allow is encoded the way FOR XML encodes it ("Unit Price" is
// Unit_x0020_Price) — then made unique by ColumnKeys. Every cell is text,
// escaped: an xml column's value is not nested as markup, so the document is
// well formed whatever a cell holds. A character XML 1.0 cannot carry at all
// (most control characters) is written as U+FFFD.
type XMLWriter struct {
	w     *bufio.Writer
	names []string
	begun bool
}

// NewXMLWriter returns a writer of rows with the given columns.
func NewXMLWriter(w io.Writer, columns []string) *XMLWriter {
	names := make([]string, len(columns))
	for i, c := range columns {
		names[i] = XMLName(c)
	}
	return &XMLWriter{w: bufio.NewWriter(w), names: ColumnKeys(names)}
}

func (x *XMLWriter) begin() {
	if !x.begun {
		x.begun = true
		x.w.WriteString(xml.Header)
		x.w.WriteString(`<ResultSet xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` + "\n")
	}
}

// WriteRecord writes one Row element. isNull may be nil or shorter; cells past
// the columns are dropped, missing ones are NULL.
func (x *XMLWriter) WriteRecord(cells []string, isNull []bool) error {
	x.begin()
	x.w.WriteString("  <Row>\n")
	for i, name := range x.names {
		x.w.WriteString("    <")
		x.w.WriteString(name)
		if i >= len(cells) || (i < len(isNull) && isNull[i]) {
			x.w.WriteString(` xsi:nil="true"/>` + "\n")
			continue
		}
		x.w.WriteByte('>')
		// EscapeText also writes \n, \r and \t as character references, so a
		// parser's line-ending and attribute normalization cannot change them.
		_ = xml.EscapeText(x.w, []byte(cells[i])) // bufio's error is sticky
		x.w.WriteString("</")
		x.w.WriteString(name)
		x.w.WriteString(">\n")
	}
	_, err := x.w.WriteString("  </Row>\n")
	return err
}

// Close ends the document and flushes.
func (x *XMLWriter) Close() error {
	x.begin()
	x.w.WriteString("</ResultSet>\n")
	return x.w.Flush()
}

// XMLName encodes name as an XML element name the way SQL Server's FOR XML
// (and .NET's XmlConvert.EncodeLocalName) does: each character a name may
// not hold at its position becomes _xHHHH_ (_xHHHHHHHH_ past U+FFFF), and an
// underscore before an x becomes _x005F_ so the result decodes back to name.
// "" stays "" (ColumnKeys names it).
func XMLName(name string) string {
	var b strings.Builder
	rs := []rune(name)
	for i, r := range rs {
		ok := isXMLNameChar(r)
		if i == 0 {
			ok = isXMLNameStart(r)
		}
		if r == '_' && i+1 < len(rs) && rs[i+1] == 'x' {
			ok = false
		}
		switch {
		case ok:
			b.WriteRune(r)
		case r > 0xFFFF:
			fmt.Fprintf(&b, "_x%08X_", r)
		default:
			fmt.Fprintf(&b, "_x%04X_", r)
		}
	}
	return b.String()
}

// isXMLNameStart is XML 1.0's NameStartChar, less the colon: a local name.
func isXMLNameStart(r rune) bool {
	switch {
	case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		return true
	case r >= 0xC0 && r <= 0xD6, r >= 0xD8 && r <= 0xF6, r >= 0xF8 && r <= 0x2FF,
		r >= 0x370 && r <= 0x37D, r >= 0x37F && r <= 0x1FFF, r >= 0x200C && r <= 0x200D,
		r >= 0x2070 && r <= 0x218F, r >= 0x2C00 && r <= 0x2FEF, r >= 0x3001 && r <= 0xD7FF,
		r >= 0xF900 && r <= 0xFDCF, r >= 0xFDF0 && r <= 0xFFFD, r >= 0x10000 && r <= 0xEFFFF:
		return true
	}
	return false
}

// isXMLNameChar is XML 1.0's NameChar, less the colon.
func isXMLNameChar(r rune) bool {
	switch {
	case isXMLNameStart(r), r == '-', r == '.', r >= '0' && r <= '9', r == 0xB7,
		r >= 0x300 && r <= 0x36F, r >= 0x203F && r <= 0x2040:
		return true
	}
	return false
}
