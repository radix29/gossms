package showplan

import (
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Parse decodes a ShowPlanXML document. data may be UTF-8 or UTF-16
// (with BOM, as SSMS saves .sqlplan files).
func Parse(data []byte) (*Plan, error) {
	text := decodeText(data)
	dec := xml.NewDecoder(strings.NewReader(text))
	// The text is already UTF-8; accept whatever the XML declaration says.
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		return input, nil
	}

	plan := new(Plan{XML: text})
	sawRoot := false
	var stack []*Statement // currently open Stmt* elements
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("showplan: parse XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case t.Name.Local == "ShowPlanXML":
				sawRoot = true
				plan.Version = attrOf(t, "Version")
				plan.Build = attrOf(t, "Build")
			case strings.HasPrefix(t.Name.Local, "Stmt"):
				st := newStatement(t)
				plan.Statements = append(plan.Statements, st)
				stack = append(stack, st)
			case len(stack) > 0:
				cur := stack[len(stack)-1]
				if err := parseStatementChild(dec, t, cur); err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			if strings.HasPrefix(t.Name.Local, "Stmt") && len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if !sawRoot {
		return nil, errors.New("showplan: not a ShowPlanXML document")
	}
	if len(plan.Statements) == 0 {
		return nil, errors.New("showplan: document contains no statements")
	}
	return plan, nil
}

// ParseAll parses multiple ShowPlanXML documents — one per statement, the
// shape SET STATISTICS XML ON produces for a multi-statement actual-plan
// capture (an extra result set after each statement's own, unlike SET
// SHOWPLAN_XML ON's single document covering the whole batch) — into one
// combined Plan whose Statements holds every document's statements, in
// order. Version/Build are taken from the first document; XML is every
// document's own decoded text joined for display purposes only — it is not
// reparsed as a single document.
func ParseAll(docs []string) (*Plan, error) {
	if len(docs) == 0 {
		return nil, errors.New("showplan: no documents to parse")
	}
	combined := new(Plan{})
	texts := make([]string, 0, len(docs))
	for i, doc := range docs {
		p, err := Parse([]byte(doc))
		if err != nil {
			return nil, fmt.Errorf("showplan: parse statement %d: %w", i+1, err)
		}
		if i == 0 {
			combined.Version = p.Version
			combined.Build = p.Build
		}
		combined.Statements = append(combined.Statements, p.Statements...)
		texts = append(texts, p.XML)
	}
	combined.XML = strings.Join(texts, "\n")
	return combined, nil
}

// newStatement builds a Statement from a Stmt* element's attributes.
func newStatement(el xml.StartElement) *Statement {
	st := new(Statement{
		Text:        attrOf(el, "StatementText"),
		Type:        attrOf(el, "StatementType"),
		SubTreeCost: attrF(el, "StatementSubTreeCost"),
		EstRows:     attrF(el, "StatementEstRows"),
		QueryHash:   attrOf(el, "QueryHash"),
	})
	if st.Text == "" {
		if db := attrOf(el, "Database"); db != "" { // StmtUseDb
			st.Text = "USE " + strings.Trim(db, "[]")
		}
	}
	st.Props = appendAttrs(st.Props, el)
	return st
}

// parseStatementChild handles one element inside an open statement.
// Elements it doesn't consume (QueryPlan and unknown containers) are left
// open for the main loop to walk into.
func parseStatementChild(dec *xml.Decoder, el xml.StartElement, st *Statement) error {
	switch el.Name.Local {
	case "QueryPlan":
		st.DOP = int(attrI(el, "DegreeOfParallelism"))
		st.Props = appendAttrs(st.Props, el)
	case "QueryTimeStats":
		st.TimeStats = new(TimeStats{
			CPUMS:     attrI(el, "CpuTime"),
			ElapsedMS: attrI(el, "ElapsedTime"),
		})
		return skip(dec)
	case "MemoryGrantInfo":
		st.MemoryGrant = new(MemoryGrant{
			GrantedKB: attrI(el, "GrantedMemory"),
			MaxUsedKB: attrI(el, "MaxUsedMemory"),
		})
		return skip(dec)
	case "MissingIndexes":
		mi, err := decodeMissingIndexes(dec)
		if err != nil {
			return err
		}
		st.MissingIndexes = mi
	case "Warnings":
		// Node-level Warnings are consumed inside decodeRelOp, so any
		// Warnings seen here is statement-level.
		ws, err := decodeWarnings(dec, el)
		if err != nil {
			return err
		}
		st.Warnings = append(st.Warnings, ws...)
	case "RelOp":
		root, err := decodeRelOp(dec, el)
		if err != nil {
			return err
		}
		st.Root = root
	}
	return nil
}

// decodeRelOp consumes one <RelOp> element (start already read) and its
// whole subtree, returning the operator node.
func decodeRelOp(dec *xml.Decoder, start xml.StartElement) (*Node, error) {
	n := new(Node{
		ID:             int(attrI(start, "NodeId")),
		PhysicalOp:     attrOf(start, "PhysicalOp"),
		LogicalOp:      attrOf(start, "LogicalOp"),
		EstRows:        attrF(start, "EstimateRows"),
		EstRowsRead:    attrF(start, "EstimatedRowsRead"),
		EstIO:          attrF(start, "EstimateIO"),
		EstCPU:         attrF(start, "EstimateCPU"),
		EstSubtreeCost: attrF(start, "EstimatedTotalSubtreeCost"),
		AvgRowSize:     attrF(start, "AvgRowSize"),
		Parallel:       attrBool(start, "Parallel"),
		ExecMode:       attrOf(start, "EstimatedExecutionMode"),
	})
	n.Props = appendAttrs(n.Props, start)
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("showplan: parse RelOp %d: %w", n.ID, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "OutputList":
				cols, err := decodeColumnList(dec)
				if err != nil {
					return nil, err
				}
				n.OutputColumns = cols
			case "Warnings":
				ws, err := decodeWarnings(dec, t)
				if err != nil {
					return nil, err
				}
				n.Warnings = append(n.Warnings, ws...)
			case "RunTimeInformation":
				rt, err := decodeRuntime(dec)
				if err != nil {
					return nil, err
				}
				n.Runtime = rt
			default:
				// The operator-specific element (NestedLoops, IndexScan,
				// Top, ...) — or any other container; walked generically.
				if err := walkOpElement(dec, t, n); err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			// All child elements are consumed above, so this is </RelOp>.
			return n, nil
		}
	}
}

// walkOpElement walks an operator-specific element generically: its own
// attributes go to Props, nested <RelOp>s become children, the first
// <Object> fills n.Object, and the first ScalarString of each direct
// sub-section (Predicate, SeekPredicates, ...) is captured.
func walkOpElement(dec *xml.Decoder, start xml.StartElement, n *Node) error {
	n.Props = appendAttrs(n.Props, start)
	section := ""       // name of the open direct child of the wrapper
	sectionDone := true // first ScalarString of the section already taken
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("showplan: parse %s: %w", start.Name.Local, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "RelOp":
				child, err := decodeRelOp(dec, t)
				if err != nil {
					return err
				}
				n.Children = append(n.Children, child)
				continue // fully consumed; depth unchanged
			case "Object":
				if n.Object.IsZero() {
					n.Object = objectFrom(t)
				} else {
					n.Props = appendAttrs(n.Props, t)
				}
				if err := skip(dec); err != nil {
					return err
				}
				continue
			}
			if depth == 0 {
				section = t.Name.Local
				sectionDone = false
			}
			if !sectionDone {
				if ss := attrOf(t, "ScalarString"); ss != "" {
					n.setScalar(section, ss)
					sectionDone = true
				}
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				return nil // the wrapper's own end tag
			}
			depth--
		}
	}
}

// setScalar files a captured ScalarString under the section it was found
// in: the two everywhere-shown ones get fields, the rest go to Props.
func (n *Node) setScalar(section, s string) {
	switch section {
	case "Predicate":
		n.Predicate = s
	case "SeekPredicates":
		n.SeekPredicate = s
	default:
		n.Props = append(n.Props, KV{section, s})
	}
}

// objectFrom builds an Object from an <Object> element, stripping the
// [brackets] SQL Server puts around every name.
func objectFrom(el xml.StartElement) Object {
	unb := func(name string) string { return strings.Trim(attrOf(el, name), "[]") }
	return Object{
		Database:  unb("Database"),
		Schema:    unb("Schema"),
		Table:     unb("Table"),
		Index:     unb("Index"),
		Alias:     unb("Alias"),
		IndexKind: attrOf(el, "IndexKind"),
	}
}

// decodeColumnList consumes the open element and formats each
// ColumnReference inside it as "alias.Column" / "Table.Column" / "Column".
func decodeColumnList(dec *xml.Decoder) ([]string, error) {
	var cols []string
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("showplan: parse column list: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "ColumnReference" {
				col := attrOf(t, "Column")
				if col != "" {
					if q := strings.Trim(attrOf(t, "Alias"), "[]"); q != "" {
						col = q + "." + col
					} else if q := strings.Trim(attrOf(t, "Table"), "[]"); q != "" {
						col = q + "." + col
					}
					cols = append(cols, col)
				}
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				return cols, nil
			}
			depth--
		}
	}
}

// decodeWarnings consumes the open <Warnings> element: boolean attributes
// on the element itself and each child element become one warning string.
func decodeWarnings(dec *xml.Decoder, start xml.StartElement) ([]string, error) {
	var ws []string
	for _, a := range start.Attr {
		if a.Value == "true" || a.Value == "1" {
			ws = append(ws, a.Name.Local)
		}
	}
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("showplan: parse warnings: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				ws = append(ws, warnString(t))
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				return ws, nil
			}
			depth--
		}
	}
}

// warnString formats one warning element as "Name (attr=value, ...)".
func warnString(el xml.StartElement) string {
	var attrs []string
	for _, a := range el.Attr {
		if a.Name.Space != "" {
			continue
		}
		attrs = append(attrs, a.Name.Local+"="+a.Value)
	}
	if len(attrs) == 0 {
		return el.Name.Local
	}
	return el.Name.Local + " (" + strings.Join(attrs, ", ") + ")"
}

// decodeRuntime consumes the open <RunTimeInformation> element,
// aggregating its per-thread counters.
func decodeRuntime(dec *xml.Decoder) (*Runtime, error) {
	rt := &Runtime{}
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("showplan: parse runtime counters: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "RunTimeCountersPerThread" {
				rt.Threads++
				rt.Rows += attrI(t, "ActualRows")
				rt.RowsRead += attrI(t, "ActualRowsRead")
				rt.Executions += attrI(t, "ActualExecutions")
				rt.LogicalReads += attrI(t, "ActualLogicalReads")
				rt.PhysicalReads += attrI(t, "ActualPhysicalReads")
				rt.ElapsedMS = max(rt.ElapsedMS, attrI(t, "ActualElapsedms"))
				rt.CPUMS = max(rt.CPUMS, attrI(t, "ActualCPUms"))
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				return rt, nil
			}
			depth--
		}
	}
}

// decodeMissingIndexes consumes the open <MissingIndexes> element.
func decodeMissingIndexes(dec *xml.Decoder) ([]MissingIndex, error) {
	var out []MissingIndex
	var cur *MissingIndex
	usage := ""
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("showplan: parse missing indexes: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "MissingIndexGroup":
				out = append(out, MissingIndex{Impact: attrF(t, "Impact")})
				cur = &out[len(out)-1]
			case "MissingIndex":
				if cur != nil {
					cur.Database = strings.Trim(attrOf(t, "Database"), "[]")
					cur.Schema = strings.Trim(attrOf(t, "Schema"), "[]")
					cur.Table = strings.Trim(attrOf(t, "Table"), "[]")
				}
			case "ColumnGroup":
				usage = attrOf(t, "Usage")
			case "Column":
				if cur != nil {
					name := strings.Trim(attrOf(t, "Name"), "[]")
					if usage == "INCLUDE" {
						cur.Include = append(cur.Include, name)
					} else {
						cur.Equality = append(cur.Equality, name)
					}
				}
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				return out, nil
			}
			depth--
		}
	}
}

// skip consumes tokens up to and including the end of the element whose
// start tag was just read.
func skip(dec *xml.Decoder) error {
	if err := dec.Skip(); err != nil {
		return fmt.Errorf("showplan: parse XML: %w", err)
	}
	return nil
}

// appendAttrs appends every non-namespace attribute of el to props.
func appendAttrs(props []KV, el xml.StartElement) []KV {
	for _, a := range el.Attr {
		if a.Name.Space != "" || a.Name.Local == "xmlns" {
			continue
		}
		props = append(props, KV{a.Name.Local, a.Value})
	}
	return props
}

// attrOf returns the named attribute's value, or "".
func attrOf(el xml.StartElement, name string) string {
	for _, a := range el.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// attrF returns the named attribute as a float64, 0 when absent/invalid.
func attrF(el xml.StartElement, name string) float64 {
	v, _ := strconv.ParseFloat(attrOf(el, name), 64)
	return v
}

// attrI returns the named attribute as an int64, 0 when absent/invalid.
func attrI(el xml.StartElement, name string) int64 {
	v, _ := strconv.ParseInt(attrOf(el, name), 10, 64)
	return v
}

// attrBool returns the named attribute as a bool. XSD's boolean lexical
// space allows both {true, false} and {1, 0} — different ShowPlan XML
// producers use either for the same attribute (e.g. NoJoinPredicate="1"
// vs. Parallel="true" have both been observed from real SQL Server
// builds), so both forms must be accepted or a real warning/flag reads
// back as silently absent.
func attrBool(el xml.StartElement, name string) bool {
	switch attrOf(el, name) {
	case "true", "1":
		return true
	default:
		return false
	}
}

// decodeText converts raw plan bytes to a UTF-8 string, honouring a
// UTF-16 or UTF-8 byte-order mark.
func decodeText(data []byte) string {
	switch {
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE:
		return decodeUTF16(data[2:], binary.LittleEndian)
	case len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF:
		return decodeUTF16(data[2:], binary.BigEndian)
	case len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF:
		return string(data[3:])
	}
	return string(data)
}

// decodeUTF16 converts UTF-16 bytes (BOM already stripped) to a string.
func decodeUTF16(b []byte, order binary.ByteOrder) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, order.Uint16(b[i:]))
	}
	return string(utf16.Decode(u))
}
