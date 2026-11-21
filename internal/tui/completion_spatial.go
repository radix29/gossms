package tui

import (
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/gossms/internal/tui/sqlparse"
	"github.com/radix29/tuikit/controls/editor"
)

var spatialConstructors = []string{
	"Point(", "Parse(", "GeomFromGml(", "STGeomFromText(", "STGeomFromWKB(",
	"STPointFromText(", "STPointFromWKB(", "STLineFromText(", "STLineFromWKB(",
	"STPolyFromText(", "STPolyFromWKB(", "STMPointFromText(", "STMPointFromWKB(",
	"STMLineFromText(", "STMLineFromWKB(", "STMPolyFromText(", "STMPolyFromWKB(",
	"STGeomCollFromText(", "STGeomCollFromWKB(",
}

var spatialAggregates = []string{"CollectionAggregate(", "ConvexHullAggregate(", "EnvelopeAggregate(", "UnionAggregate("}

// A trailing '(' takes arguments; '()' is a zero-argument method; bare names
// are CLR properties and must not acquire parentheses.
var spatialInstanceMethods = []string{
	"STArea()", "STLength()", "STAsBinary()", "STAsText()", "STGeometryType()", "STDimension()", "STSrid",
	"STStartPoint()", "STEndPoint()", "STPointN(", "STCurveN(", "STCurveToLine()",
	"STGeometryN(", "STNumGeometries()", "STNumPoints()", "STNumCurves()",
	"STContains(", "STDisjoint(", "STEquals(", "STIntersects(", "STOverlaps(", "STWithin(", "STDistance(",
	"STBuffer(", "STDifference(", "STIntersection(", "STSymDifference(", "STUnion(",
	"STIsClosed()", "STIsEmpty()", "STIsValid()", "STConvexHull()",
	"AsBinaryZM()", "AsTextZM()", "AsGml()", "ToString()", "Z", "M", "HasZ", "HasM", "IsNull",
	"BufferWithTolerance(", "BufferWithCurves(", "Reduce(", "CurveToLineWithTolerance(",
	"ShortestLineTo(", "IsValidDetailed()", "MakeValid()", "InstanceOf(", "Filter(", "MinDbCompatibilityLevel()",
}

var geometryInstanceMethods = []string{
	"STBoundary()", "STCentroid()", "STEnvelope()", "STPointOnSurface()", "STX", "STY",
	"STExteriorRing()", "STInteriorRingN(", "STNumInteriorRing()", "STCrosses(", "STRelate(", "STTouches(",
	"STIsRing()", "STIsSimple()",
}

var geographyInstanceMethods = []string{
	"Lat", "Long", "EnvelopeCenter()", "EnvelopeAngle()", "NumRings()", "RingN(", "ReorientObject()",
}

func spatialType(name string) string {
	switch strings.ToLower(name) {
	case "geometry":
		return "geometry"
	case "geography":
		return "geography"
	}
	return ""
}

func spatialCompletionItems(methods []string, buf []rune, cursor int, prefix string) []editor.CompletionItem {
	var items []editor.CompletionItem
	for _, method := range methods {
		name := strings.TrimRight(method, "()")
		if !strings.HasPrefix(strings.ToUpper(name), strings.ToUpper(prefix)) {
			continue
		}
		item := editor.CompletionItem{Text: name, Label: name, Detail: "property"}
		if strings.HasSuffix(method, "(") || strings.HasSuffix(method, "()") {
			item.Text, item.ReplaceAfter, item.CursorOffset = keywordFunctionTemplate(buf, cursor, name, false)
			item.Detail = "function"
			if strings.HasSuffix(method, "()") {
				if strings.HasSuffix(item.Text, ")") {
					item.CursorOffset = 0
				} else if end, closed := keywordCallEnd(buf, keywordNextToken(buf, cursor)); closed {
					item.CursorOffset = end - cursor
				}
			}
		}
		items = append(items, item)
	}
	sortCompletionItemsInContext(items, false, false)
	return items
}

func (p *QueryPanel) spatialStaticCandidates(buf []rune, tokens []sqlparse.Token, q *sqlparse.Query, from, cursor int, prefix string) ([]editor.CompletionItem, bool) {
	end := 0
	for end < len(tokens) && tokens[end].Start < from {
		end++
	}
	if end == 0 {
		return nil, false
	}
	tok := tokens[end-1]
	if spatialType(tok.Text) == "" || tok.Quoted || end > 1 && tokens[end-2].Kind == sqlparse.TokenDot {
		return nil, false
	}
	first := keywordNextToken(buf[:from], tok.Start+len([]rune(tok.Text)))
	second := keywordNextToken(buf[:from], first.End)
	if first.Kind != sqltext.KindPunct || buf[first.Start] != ':' ||
		second.Kind != sqltext.KindPunct || buf[second.Start] != ':' ||
		keywordNextToken(buf[:from], second.End).Kind != sqltext.KindEnd {
		return nil, false
	}
	context := p.analyzeCompletionContext(buf[:tok.Start], tokens, q, tok.Start)
	if !context.scalar {
		return nil, true
	}
	methods := append(slices.Clone(spatialConstructors), "Null")
	if context.aggregate {
		methods = append(methods, spatialAggregates...)
	}
	return spatialCompletionItems(methods, buf, cursor, prefix), true
}

func spatialVariableType(buf []rune, start, end int, collation, name string) string {
	buf = buf[:end]
	depth, cases := 0, 0
	declaring, variableSlot := false, false
	for tok := keywordNextToken(buf, start); tok.Kind != sqltext.KindEnd; tok = keywordNextToken(buf, tok.End) {
		if tok.Kind == sqltext.KindPunct {
			switch buf[tok.Start] {
			case '(':
				depth++
			case ')':
				depth = max(0, depth-1)
			case ';':
				if depth == 0 {
					declaring, variableSlot = false, false
					cases = 0
				}
			case ',':
				variableSlot = depth == 0 && cases == 0 && declaring
			}
			continue
		}
		if depth != 0 {
			continue
		}
		if tok.Kind != sqltext.KindWord {
			variableSlot = false
			continue
		}
		word := string(buf[tok.Start:tok.End])
		switch strings.ToUpper(word) {
		case "DECLARE":
			declaring, variableSlot = true, true
			cases = 0
			continue
		case "CASE":
			if declaring {
				cases++
			}
		case "END":
			if cases > 0 {
				cases--
				continue
			}
			declaring, variableSlot = false, false
		case "SELECT", "SET", "INSERT", "UPDATE", "EXEC", "EXECUTE", "DELETE", "MERGE", "WITH",
			"IF", "WHILE", "BEGIN", "RETURN", "PRINT", "CREATE", "ALTER", "DROP":
			declaring, variableSlot = false, false
			cases = 0
		}
		if !variableSlot {
			continue
		}
		variableSlot = false
		if !gosmo.SameName(collation, word, name) {
			continue
		}
		typ := keywordNextToken(buf, tok.End)
		if keywordNextIs(buf, tok.End, "AS") {
			typ = keywordNextToken(buf, typ.End)
		}
		switch typ.Kind {
		case sqltext.KindWord:
			return spatialType(string(buf[typ.Start:typ.End]))
		case sqltext.KindQuotedIdent:
			if typ.End-typ.Start >= 2 && (buf[typ.End-1] == ']' || buf[typ.End-1] == '"') {
				return spatialType(string(buf[typ.Start+1 : typ.End-1]))
			}
		}
		return ""
	}
	return ""
}

func (p *QueryPanel) spatialExpressionType(buf []rune, start, end int, tokens []sqlparse.Token, rels []relation, collation string, depth int) string {
	if len(tokens) == 0 || depth >= maxRelationDepth {
		return ""
	}
	last := len(tokens) - 1
	tok := tokens[last]
	if tok.Kind != sqlparse.TokenParenClose {
		if tok.Kind != sqlparse.TokenIdent {
			return ""
		}
		if last >= 2 && tokens[last-1].Kind == sqlparse.TokenDot {
			qualifier := sqlparse.QualifierChain(tokens, tok.Start+len([]rune(tok.Text)))
			return p.qualifiedSpatialType(rels, collation, qualifier, tok.Text)
		}
		if !tok.Quoted && strings.HasPrefix(tok.Text, "@") && !strings.HasPrefix(tok.Text, "@@") {
			return spatialVariableType(buf, start, end, collation, tok.Text)
		}
		if _, alias := findRelation(collation, rels, tok.Text); alias {
			return ""
		}
		matches, typ := 0, ""
		for _, rel := range rels {
			if rel.obj == nil {
				continue
			}
			if col, ok := findColumnIn(collation, rel.columns(), tok.Text); ok {
				matches++
				typ = spatialType(string(col.DataType))
			}
		}
		if matches == 1 {
			return typ
		}
		return ""
	}
	level, open := 1, last-1
	for ; open >= 0; open-- {
		switch tokens[open].Kind {
		case sqlparse.TokenParenClose:
			level++
		case sqlparse.TokenParenOpen:
			level--
		}
		if level == 0 {
			break
		}
	}
	if open < 2 {
		return ""
	}
	method, receiver := tokens[open-1], tokens[open-2]
	if receiver.Kind != sqlparse.TokenDot {
		if spatialType(receiver.Text) == "" || receiver.Quoted {
			return ""
		}
		first := keywordNextToken(buf[:method.Start], receiver.Start+len([]rune(receiver.Text)))
		second := keywordNextToken(buf[:method.Start], first.End)
		if first.Kind == sqltext.KindPunct && buf[first.Start] == ':' &&
			second.Kind == sqltext.KindPunct && buf[second.Start] == ':' &&
			keywordNextToken(buf[:method.Start], second.End).Kind == sqltext.KindEnd &&
			(slices.Contains(spatialConstructors, method.Text+"(") || slices.Contains(spatialAggregates, method.Text+"(")) {
			return spatialType(receiver.Text)
		}
		return ""
	}
	typ := p.spatialExpressionType(buf, start, end, tokens[:open-2], rels, collation, depth+1)
	if typ == "" {
		return ""
	}
	switch method.Text {
	case "STStartPoint", "STEndPoint", "STPointN", "STCurveN", "STCurveToLine", "STGeometryN",
		"STBuffer", "STDifference", "STIntersection", "STSymDifference", "STUnion", "STConvexHull",
		"BufferWithTolerance", "BufferWithCurves", "Reduce", "CurveToLineWithTolerance", "ShortestLineTo", "MakeValid":
		return typ
	case "STBoundary", "STCentroid", "STEnvelope", "STPointOnSurface", "STExteriorRing", "STInteriorRingN":
		if typ == "geometry" {
			return typ
		}
	case "EnvelopeCenter", "RingN", "ReorientObject":
		if typ == "geography" {
			return typ
		}
	}
	return ""
}

func (p *QueryPanel) qualifiedSpatialType(rels []relation, collation string, qualifier []string, name string) string {
	if len(qualifier) == 1 {
		if rel, ok := findRelation(collation, rels, qualifier[0]); ok && rel.obj != nil {
			if col, ok := findColumnIn(collation, rel.columns(), name); ok {
				return spatialType(string(col.DataType))
			}
		}
	}
	return ""
}

func (p *QueryPanel) spatialMemberCandidates(buf []rune, tokens []sqlparse.Token, start, from, cursor int, rels []relation, collation, prefix string) ([]editor.CompletionItem, bool) {
	for len(tokens) > 0 && tokens[len(tokens)-1].Start >= from {
		tokens = tokens[:len(tokens)-1]
	}
	n := len(tokens)
	if n < 2 || tokens[n-1].Kind != sqlparse.TokenDot {
		return nil, false
	}
	typ := p.spatialExpressionType(buf, start, from, tokens[:n-1], rels, collation, 0)
	if typ == "" {
		return nil, tokens[n-2].Kind == sqlparse.TokenParenClose
	}
	methods := slices.Clone(spatialInstanceMethods)
	if typ == "geometry" {
		methods = append(methods, geometryInstanceMethods...)
	} else {
		methods = append(methods, geographyInstanceMethods...)
	}
	return spatialCompletionItems(methods, buf, cursor, prefix), true
}
