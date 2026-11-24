package tui

import (
	"slices"
	"strings"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/gossms/internal/tui/sqlparse"
	"github.com/radix29/tuikit/controls/editor"
)

// ---------------------------------------------------------------------------
// SQL-aware completion.Provider for the query editor, the only caller of
// editor.Editor.SetCompletionProvider. Resolves the identifier at the cursor
// against the connected database's completionInventory (completion_inventory.go):
// schemas, tables, views and columns, with schema/alias/table-dot member lookup
// and FROM-clause alias resolution.
//
// A lexical approximation, not a full T-SQL parser (same spirit as
// editor.Editor's SelectStatementAtCursor). Anything genuinely ambiguous offers
// nothing rather than guessing; common keyword/phrase contexts are supplemented
// by completion_keywords.go.
//
// Covered: FROM/JOIN/APPLY refs and aliases; CTEs (including recursive and
// chained); derived tables and sub-SELECTs, completing against the innermost
// query's clause state; temp tables and table variables, resolved from their
// declaration in the cursor's GO batch (temp tables also from earlier batches until
// a DROP TABLE); PIVOT/UNPIVOT; and OPENJSON/OPENROWSET/OPENXML (see
// sqlparse.ScanBindings, CarryTempBindings, Pivot and Rowset).
//
// Table-valued functions resolve to their catalog result columns. Three-part names
// ("Sales.dbo.Orders", "Sales..Orders") resolve against that database's own
// inventory, loaded on first use (completion_crossdb.go); four-part names against
// the remote catalog via the linked server (completion_linked.go).
// ---------------------------------------------------------------------------

// newCompletionProvider builds the editor.CompletionProvider installed on
// this panel's editor (see NewQueryPanel). p's conn/database are read fresh on
// every call, so reconnecting or switching database (a mid-script USE) takes
// effect on the next keystroke without rebuilding it.
func (p *QueryPanel) newCompletionProvider() editor.CompletionProvider {
	return func(req editor.CompletionRequest) ([]editor.CompletionItem, int) {
		return p.sqlCompletionCandidates(req)
	}
}

// loadingCompletionItem stands in for identifiers until the backing inventory finishes
// its first load. completion_inventory.go's refreshSysCompletionPopups
// re-queries this provider once the data lands, replacing it without another
// keystroke.
var loadingCompletionItem = editor.CompletionItem{Label: "Loading suggestions...", Placeholder: true}

// sqlCompletionCandidates answers one provider call. req.Text identifies the
// revision of req.Lines, which lets the prefix scan resume from
// p.completionPrefix rather than restarting at offset 0 on every keystroke; a
// zero Text (as the tests pass) never resumes.
//
// baseCompletionCandidates picks the candidates; the completionPost steps it
// armed on the way to its return then reshape them in one fixed order.
func (p *QueryPanel) sqlCompletionCandidates(req editor.CompletionRequest) ([]editor.CompletionItem, int) {
	items, replaceCol, post := p.baseCompletionCandidates(req)
	items = post.apply(items)
	items = slices.DeleteFunc(items, func(item editor.CompletionItem) bool { return hasControlChar(item.Label) })
	softenExactMatches(items, req, replaceCol, post.collation)
	return items, replaceCol
}

// hasControlChar reports a C0 control character or DEL in s. A catalog name
// holding one (legal inside brackets) cannot be offered faithfully: its popup
// label draws without the character and a CR is lost to the editor's CRLF
// normalisation, so the insert would name a different object. Typing such a
// name by hand still works. Labels are the raw names; keyword and template
// Text may legitimately hold newlines, which is why the Label is tested.
func hasControlChar(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// softenExactMatches marks Soft every name candidate whose Text the typed span
// [replaceCol, Col+ReplaceAfter) already equals under collation: committing it
// would change nothing, so Enter after a name typed in full ("FROM
// sys.tables⏎") must break the line, not swallow the key. Tab still commits.
// Under a case-sensitive collation `ORDERS` is not `Orders`, and Enter commits
// the fix. Keywords are left alone: a fully typed one still commits on Enter,
// upper-cased with its space (TestSQLKeywordCompletionKeepsFullyTypedKeywords).
func softenExactMatches(items []editor.CompletionItem, req editor.CompletionRequest, replaceCol int, collation string) {
	if req.Row < 0 || req.Row >= len(req.Lines) {
		return
	}
	line := req.Lines[req.Row]
	if replaceCol < 0 || replaceCol > req.Col || req.Col > len(line) {
		return
	}
	for i := range items {
		item := &items[i]
		if item.Detail == "keyword" || item.Placeholder || item.Soft || item.ReplaceBefore > 0 || item.CursorOffset != 0 {
			continue
		}
		end := req.Col + max(0, item.ReplaceAfter)
		if end > len(line) {
			continue
		}
		if typed := string(line[replaceCol:end]); typed != "" &&
			(gosmo.SameName(collation, typed, item.Text) || qualifiesTyped(collation, typed, item.Text)) {
			item.Soft = true
		}
	}
}

// qualifiesTyped reports whether text is typed with qualifiers added in front
// ("dbo.Customers" for "Customers"): an unqualified object candidate inserts
// its schema-qualified name (issue #65), and a name typed in full still must
// not swallow Enter just because the commit would add the schema.
func qualifiesTyped(collation, typed, text string) bool {
	cut := len(text) - len(typed)
	return cut > 0 && text[cut-1] == '.' && gosmo.SameName(collation, typed, text[cut:])
}

// completionPost is the post-processing baseCompletionCandidates arms as it
// goes. Each step is armed at a fixed point in that function, so a return
// carries exactly the steps armed before it: moving a return across an arming
// point changes the output. apply runs them in this order:
//
//  1. tvf: drop table-valued functions outside FROM/JOIN (or everything but
//     sources inside APPLY) and render the catalog TVFs as calls. It runs
//     before the keyword merge, so the built-in templates that merge adds are
//     neither filtered nor re-rendered.
//  2. keywords: merge the keyword/template candidates — "keyword" items lead,
//     the rest sort in with the catalog items. On a lone loading placeholder
//     with no prefix, only the built-in TVF templates join it.
//  3. shift: in an unterminated bracket identifier whose ']' was found further
//     on, extend every item's replaced span past it.
//
// keywords is armed only in normal lexer state and shift only in a bracket,
// so steps 2 and 3 never both apply.
type completionPost struct {
	tvf      bool
	tvfApply bool   // the cursor is an APPLY source (completionContext.apply)
	tvfWhere string // completionContext.clause
	callBuf  []rune // the statement up to its end, for completionCallItem
	callFrom int    // the forward-scan start, for completionCallItem

	keywords          []editor.CompletionItem
	keywordsNoPrefix  bool
	keywordsTableSort bool // clause == sqlparse.ClauseTable
	keywordsHavingAgg bool // a HAVING frame expecting an aggregate

	shift int

	// collation is the database's (completionInventory.collation) once the
	// inventory was read; softenExactMatches compares under it (empty folds).
	collation string
}

func (post completionPost) apply(items []editor.CompletionItem) []editor.CompletionItem {
	if post.tvf {
		items = slices.DeleteFunc(items, func(item editor.CompletionItem) bool {
			if post.tvfApply && !item.Placeholder {
				return item.Detail != "table-valued function" && item.Detail != "schema" &&
					!strings.HasPrefix(item.Detail, "database") && item.Detail != "linked server"
			}
			return item.Detail == "table-valued function" && post.tvfWhere != "FROM" && post.tvfWhere != "JOIN"
		})
		for i, item := range items {
			if item.Detail == "table-valued function" {
				call := completionCallItem(post.callBuf, post.callFrom, item.Text, item.Detail, false)
				call.Label, call.Icon, call.Partial = item.Label, item.Icon, item.Partial
				items[i] = call
			}
		}
	}
	if len(post.keywords) > 0 {
		var leadingKeywords []editor.CompletionItem
		for _, item := range post.keywords {
			if len(items) == 1 && items[0].Placeholder && post.keywordsNoPrefix && item.Detail != "table-valued function" {
				continue
			}
			if item.Detail == "keyword" {
				leadingKeywords = append(leadingKeywords, item)
			} else {
				items = append(items, item)
			}
		}
		sortCompletionItemsInContext(items, post.keywordsTableSort, post.keywordsHavingAgg)
		items = append(leadingKeywords, items...)
	}
	if post.shift != 0 {
		for i := range items {
			items[i].ReplaceAfter += post.shift
		}
	}
	return items
}

// baseCompletionCandidates is sqlCompletionCandidates before its
// completionPost steps. Its phases run in a fixed order, each either answering
// or passing on: routine signatures (read from the un-narrowed prefix), the
// positional answers that need no inventory, the catalog answers (DDL targets,
// procedures), then the relation answers. They arm post as they go, so moving
// a phase changes which steps a return carries (see completionPost).
func (p *QueryPanel) baseCompletionCandidates(req editor.CompletionRequest) (items []editor.CompletionItem, replaceCol int, post completionPost) {
	if p.app.cfg.IntelliSenseDisabled {
		return nil, req.Col, post
	}
	if p.conn == nil || !p.app.isConnected(p.conn) {
		return nil, req.Col, post
	}
	pos, ok := p.completionPositionAt(req, &post)
	if !ok {
		return nil, req.Col, post
	}
	if items, done := p.routineSignatureCandidates(pos); done {
		return items, pos.replaceFrom, post
	}
	p.scopeCompletionPosition(pos)
	if items, done := p.positionalCandidates(pos, &post); done {
		return items, pos.replaceFrom, post
	}
	inv := p.app.ensureCompletionInventory(p.conn, p.database)
	if inv.loading {
		return []editor.CompletionItem{loadingCompletionItem}, pos.replaceFrom, post
	}
	if inv.err != nil || inv.catalog == nil {
		return nil, pos.replaceFrom, post
	}
	post.collation = inv.collation
	if items, done := p.catalogCandidates(pos, inv, &post); done {
		return items, pos.replaceFrom, post
	}
	return p.relationCandidates(pos, inv), pos.replaceFrom, post
}

// completionPosition is what baseCompletionCandidates's phases share about the
// cursor. completionPositionAt fills the lexical half; scopeCompletionPosition
// fills the statement half once routineSignatureCandidates has had its turn.
type completionPosition struct {
	req   editor.CompletionRequest
	pre   sqlparse.PrefixScan
	buf   []rune
	upTo  int // the cursor's offset in buf
	state sqlparse.LexState

	qualifier, prefix string
	hasQualifier      bool
	chain             []string

	rowStart    int // the cursor row's offset in buf
	replaceFrom int // the replaced span's start, as a column on the cursor's row
	at          int // rowStart+replaceFrom: the same start as a buf offset
	forwardFrom int // where the forward scan resumes (past an open '[''s ']')
	batchStart  int

	// tokens is the statement up to the cursor: split at a DML header by
	// completionPositionAt, then narrowed by scopeCompletionPosition.
	tokens []sqlparse.Token

	// Set by scopeCompletionPosition.
	stmt               []rune           // buf up to the statement's end
	stmtTokens         []sqlparse.Token // the whole statement, both sides of the cursor
	scope              sqlparse.Scope
	refs               []sqlparse.FromRef
	clause             sqlparse.Clause
	cc                 completionContext
	memberAccess       bool // the last token before the replaced span is a '.'
	ddlAction, ddlType string
	ddlTail            []sqlparse.Token
}

// completionPositionAt lexes up to the cursor and finds the replaced span. It
// reports false when nothing completes here (a string or comment, mid-word, a
// span the next token continues as a name); post.shift is armed on the way.
func (p *QueryPanel) completionPositionAt(req editor.CompletionRequest, post *completionPost) (*completionPosition, bool) {
	lines, row, col := req.Lines, req.Row, req.Col
	p.completionBuf = sqlparse.FlattenLinesInto(p.completionBuf, lines)
	pos := &completionPosition{req: req, buf: p.completionBuf}
	buf := pos.buf
	pos.upTo = sqlparse.OffsetForCursor(lines, row, col)
	upTo := pos.upTo

	// Scoped to the current statement — a table named in an earlier ';'- or
	// GO-separated statement must not leak into this one's FROM-scope/clause
	// detection (see sqlparse.ScanPrefix, which PrefixCache answers for).
	pos.pre = p.completionPrefix.Scan(lines, buf, row, upTo, sqlparse.TextRevision{
		Doc: req.Text.Doc, Version: req.Text.Version, DirtyFrom: req.Text.DirtyFrom,
	})
	tokens, quoteStart := pos.pre.Tokens, pos.pre.QuoteStart
	pos.state, pos.batchStart = pos.pre.State, pos.pre.BatchStart

	var replaceFrom int
	switch pos.state {
	case sqlparse.LexNormal:
		if upTo < len(buf) && sqltext.IsWordContinue(buf[upTo]) {
			return nil, false
		}
		pos.qualifier, pos.prefix, replaceFrom, pos.hasQualifier = sqlparse.TokenContext(tokens, upTo)
		pos.chain = sqlparse.QualifierChain(tokens, upTo)
	case sqlparse.LexBracket:
		// An unterminated bracket identifier ("FROM [Cus|") is the one
		// non-normal lexer state completion still works in: everything after
		// the '[' is the prefix, and the whole "[..." span is replaced on
		// commit (gosmo.QuoteNameIfNeeded re-quotes only when needed).
		pos.qualifier, _, _, pos.hasQualifier = sqlparse.TokenContext(tokens, quoteStart)
		pos.chain = sqlparse.QualifierChain(tokens, quoteStart)
		pos.prefix = string(buf[quoteStart+1 : upTo])
		replaceFrom = quoteStart
	default:
		return nil, false // inside a string literal or comment
	}

	// Everything above works in flattened-buffer offsets, but the
	// editor.CompletionProvider contract wants a column on the cursor's row (the
	// editor replaces [replaceFrom, col) there and anchors the popup at it). The
	// replaced span always starts on the cursor's own row (identifiers can't span
	// lines; a '[' on an earlier row is malformed and bails), so subtracting the
	// row's start offset converts it.
	pos.rowStart = sqlparse.OffsetForCursor(lines, row, 0)
	if replaceFrom < pos.rowStart {
		return nil, false
	}
	pos.at, pos.replaceFrom = replaceFrom, replaceFrom-pos.rowStart

	// FROM-scope/clause analysis looks at the whole statement, not just the
	// part already typed — a table named in "SELECT | FROM Customers c"
	// resolves as well as one typed above the cursor. The forward scan must
	// resume in normal lexer state, so when the cursor sits inside an
	// unterminated bracket identifier, skip past its closing ']' first.
	pos.forwardFrom = upTo
	if pos.state == sqlparse.LexBracket {
		quoted, after := sqltext.Next(buf, quoteStart, len(buf), sqltext.State{})
		pos.forwardFrom = quoted.End
		if after.Mode == sqltext.ModeNormal {
			post.shift = quoted.End - upTo
		}
	}
	if pos.at < upTo {
		tail := keywordNextToken(buf, pos.forwardFrom)
		if tail.Kind == sqltext.KindPunct &&
			(buf[tail.Start] == '.' || buf[tail.Start] == ':' && keywordNextIs(buf, tail.End, ":")) {
			return nil, false
		}
	}
	// DML splitting leaves non-DML headers together. Also recognize the next
	// statement after a table definition while its leader is only a prefix.
	// Every DML ender is a tokenizer keyword, and a keyword token's Text is
	// already upper-cased: no per-token ToUpper on every keystroke.
	if slices.ContainsFunc(tokens, func(tok sqlparse.Token) bool {
		return tok.Kind == sqlparse.TokenKeyword && sqltext.EndsDML(tok.Text) ||
			keywordIs(tok, "EXEC") || keywordIs(tok, "EXECUTE")
	}) {
		start := keywordStatementStart(buf, tokens[0].Start, pos.at)
		current := sqlparse.TokensFrom(tokens, start)
		action, objectType, tail := ddlCompletionTarget(current, pos.at)
		if action == "CREATE" && objectType == "TABLE" {
			depth, closed := 0, false
			for _, tok := range tail {
				switch tok.Kind {
				case sqlparse.TokenParenOpen:
					depth++
				case sqlparse.TokenParenClose:
					depth--
					closed = closed || depth == 0
				}
			}
			if closed && depth == 0 && (pos.prefix == "" || slices.ContainsFunc(
				[]string{"CREATE", "ALTER", "DROP", "TRUNCATE", "SELECT", "INSERT", "UPDATE", "DELETE", "EXEC", "EXECUTE", "DECLARE"},
				func(word string) bool { return strings.HasPrefix(word, strings.ToUpper(pos.prefix)) },
			)) {
				start = pos.at
			}
		}
		tokens, pos.batchStart = sqlparse.TokensFrom(tokens, start), start
	}
	pos.tokens = tokens
	return pos, true
}

// routineSignatureCandidates answers a data-type position in a routine's
// parameter list or RETURNS clause, read from the un-narrowed prefix. An open
// '[' reaches it too: there it starts a user-defined type name
// (keywordCandidates).
func (p *QueryPanel) routineSignatureCandidates(pos *completionPosition) ([]editor.CompletionItem, bool) {
	if !pos.hasQualifier && len(pos.chain) == 0 {
		if routine := routineCompletionTokens(pos.buf, pos.pre, pos.at); len(routine) > 0 {
			if keywords, only := p.keywordCandidates(pos.buf, routine, nil, pos.at, pos.upTo, pos.prefix); only {
				return keywords, true
			}
		}
	}
	if len(pos.chain) == 1 {
		if routine := routineCompletionTokens(pos.buf, pos.pre, pos.at); len(routine) > 0 {
			if types, handled := p.qualifiedTypeCandidates(pos.buf, routine, nil, pos.at, pos.upTo, pos.prefix); handled {
				return types, true
			}
		}
	}
	return nil, false
}

// scopeCompletionPosition narrows pos to the statement holding the cursor and
// fills the statement half of pos: its query scope, clause and DDL target.
func (p *QueryPanel) scopeCompletionPosition(pos *completionPosition) {
	// Statements stacked with no ';' between them still lex as one
	// ';'/GO-delimited span; narrow to the DML statement holding the cursor so
	// a bare column context doesn't pick up FROM refs from an unrelated
	// statement above or below it. The forward lex stops at the next statement
	// rather than running to the next ';' or GO (see
	// sqlparse.NarrowStatementForward).
	stmtStart, stmtEnd, forwardTokens := sqlparse.NarrowStatementForward(pos.req.Lines, pos.buf, pos.req.Row, pos.batchStart, pos.forwardFrom, pos.upTo, pos.tokens)
	pos.tokens = sqlparse.TokensFrom(pos.tokens, stmtStart)
	pos.stmt = pos.buf[:stmtEnd]
	pos.stmtTokens = append(append([]sqlparse.Token{}, pos.tokens...), forwardTokens...)

	// The query tree, not the flat scan: the cursor's innermost SELECT, the
	// CTEs visible from it, and its own clause state (see sqlparse.ScopeAt). A
	// statement the parser can make nothing of leaves Query nil, and the flat
	// FROM-scope scan answers for it.
	pos.scope = sqlparse.ScopeAt(pos.stmtTokens, pos.upTo)
	pos.refs, pos.clause = pos.scope.Query.FromRefs(), pos.scope.Clause
	if pos.scope.Query == nil {
		pos.refs, pos.clause = sqlparse.ParseFromScope(pos.stmtTokens), sqlparse.CurrentClause(pos.tokens)
	}
	pos.cc = p.analyzeCompletionContext(pos.stmt, pos.stmtTokens, pos.scope.Query, pos.at)
	if pos.cc.apply {
		pos.clause = sqlparse.ClauseTable
	}
	if predicateClause(pos.cc.clause) {
		pos.clause = sqlparse.ClauseColumn
	}

	for _, tok := range pos.tokens {
		if tok.Start >= pos.at {
			break
		}
		pos.memberAccess = tok.Kind == sqlparse.TokenDot
	}
	pos.ddlAction, pos.ddlType, pos.ddlTail = ddlCompletionTarget(pos.tokens, pos.at)
}

// positionalCandidates answers what the position alone settles, with no
// inventory: spatial statics, qualified data types (CREATE TABLE and
// qualifiedDataTypePosition), and keywords. Keywords that leave room for
// catalog names are armed on post instead of returned.
func (p *QueryPanel) positionalCandidates(pos *completionPosition, post *completionPost) ([]editor.CompletionItem, bool) {
	var options []editor.CompletionItem
	if pos.state == sqlparse.LexNormal && !pos.hasQualifier && len(pos.chain) == 0 &&
		!pos.memberAccess && !sqlparse.HasSigil(pos.prefix) {
		var only bool
		options, only = p.functionOptionCandidates(pos.stmt, pos.stmtTokens, pos.at, pos.upTo, pos.prefix)
		if only {
			return options, true
		}
	}
	if pos.cc.only && len(pos.cc.choices) == 0 {
		return nil, true
	}
	if pos.state == sqlparse.LexNormal {
		if spatial, handled := p.spatialStaticCandidates(pos.stmt, pos.stmtTokens, pos.scope.Query, pos.at, pos.upTo, pos.prefix); handled {
			return spatial, true
		}
	}
	if len(pos.chain) == 1 &&
		(pos.ddlAction == "CREATE" && pos.ddlType == "TABLE" || qualifiedDataTypePosition(pos.buf, pos.stmtTokens, pos.at)) {
		if types, handled := p.qualifiedTypeCandidates(pos.stmt, pos.stmtTokens, pos.scope.Query, pos.at, pos.upTo, pos.prefix); handled {
			return types, true
		}
	}
	if pos.hasQualifier || len(pos.chain) > 0 || pos.memberAccess {
		return nil, false
	}
	switch pos.state {
	case sqlparse.LexBracket:
		if types, only := p.keywordCandidates(pos.stmt, pos.stmtTokens, pos.scope.Query, pos.at, pos.upTo, pos.prefix); only {
			return types, true
		}
	case sqlparse.LexNormal:
		keywords, sourceComplete := p.keywordCandidates(pos.stmt, pos.stmtTokens, pos.scope.Query, pos.at, pos.upTo, pos.prefix)
		keywords = append(options, keywords...)
		for i := range keywords {
			if strings.HasSuffix(keywords[i].Label, "DISTINCT FROM") {
				keywords[i] = reuseDistinctCompletionTail(keywords[i], pos.buf, pos.upTo)
			}
		}
		if sourceComplete {
			return keywords, true
		}
		post.keywords = keywords
		post.keywordsNoPrefix = pos.prefix == ""
		post.keywordsTableSort = pos.clause == sqlparse.ClauseTable
		post.keywordsHavingAgg = pos.cc.frame.having && pos.cc.aggregate
	}
	return nil, false
}

// ddlTargetType reports an object type whose DDL names a catalog object
// ddlTargetCandidates lists.
func ddlTargetType(objectType string) bool {
	switch objectType {
	case "TABLE", "VIEW", "PROCEDURE", "FUNCTION":
		return true
	}
	return false
}

// catalogCandidates answers the DDL target and procedure-call positions from
// the inventory. post.tvf is armed between the two, so a DDL target list
// carries no TVF step and everything after it does.
func (p *QueryPanel) catalogCandidates(pos *completionPosition, inv *completionInventory, post *completionPost) ([]editor.CompletionItem, bool) {
	ddlTail := pos.ddlTail
	if pos.ddlAction == "DROP" {
		if len(ddlTail) >= 2 && keywordIs(ddlTail[0], "IF") && keywordIs(ddlTail[1], "EXISTS") {
			ddlTail = ddlTail[2:]
		}
		for i := len(ddlTail) - 1; i >= 0; i-- {
			if ddlTail[i].Kind == sqlparse.TokenComma {
				ddlTail = ddlTail[i+1:]
				break
			}
		}
	}
	if ddlTargetType(pos.ddlType) && !slices.ContainsFunc(ddlTail, func(tok sqlparse.Token) bool {
		return tok.Kind != sqlparse.TokenIdent && tok.Kind != sqlparse.TokenDot
	}) {
		if len(ddlTail) > 0 && ddlTail[len(ddlTail)-1].Kind != sqlparse.TokenDot {
			return nil, true
		}
		targets, wait := p.ddlTargetCandidates(inv, pos.ddlAction, pos.ddlType, pos.chain, pos.prefix)
		if wait {
			return []editor.CompletionItem{loadingCompletionItem}, true
		}
		if pos.ddlType == "TABLE" && pos.ddlAction != "CREATE" && len(pos.chain) == 0 && sqlparse.ContainsSigil(pos.buf, 0) {
			bindings := p.completionBatch.Bindings(pos.req.Lines, pos.buf, pos.req.Row)
			temps := slices.DeleteFunc(p.bindingItems(bindings, pos.prefix), func(item editor.CompletionItem) bool {
				return item.Detail != "temp table"
			})
			targets = append(targets, temps...)
			sortCompletionItems(targets)
		}
		return targets, true
	}
	post.tvf, post.tvfApply, post.tvfWhere = true, pos.cc.apply, pos.cc.clause
	post.callBuf, post.callFrom = pos.stmt, pos.forwardFrom
	if statementProcedureContext(pos.buf, pos.tokens, pos.at, len(pos.chain)) {
		tokens := pos.tokens
		procStart := pos.at
		if len(tokens) > 0 {
			procStart = tokens[0].Start
		}
		explicit := len(tokens) > 0 && tokens[0].Start < pos.at &&
			(keywordIs(tokens[0], "EXEC") || keywordIs(tokens[0], "EXECUTE"))
		// A bare procedure call is valid only as the batch's first statement.
		// Keep the keyword-only list until EXEC is supplied in later statements.
		if !explicit && keywordNextToken(pos.buf, pos.pre.GoStart).Start < procStart {
			return nil, true
		}
		return p.procedureCandidates(inv, pos.chain, pos.prefix), true
	}
	return nil, false
}

// relationCandidates answers from the statement's FROM scope: objects,
// columns, members of a qualifier, and multi-part names.
func (p *QueryPanel) relationCandidates(pos *completionPosition, inv *completionInventory) []editor.CompletionItem {
	sysInv := p.app.ensureSysCompletionInventory(p.conn)
	scope, refs, clause, tokens := pos.scope, pos.refs, pos.clause, pos.tokens
	chain, qualifier, prefix := pos.chain, pos.qualifier, pos.prefix

	// Temp tables and table variables are declared in a different statement from
	// the one using them, so their shapes come from a scan of the whole
	// GO-delimited batch plus the temp tables the batches above carry in: the one
	// piece of cross-statement work a keystroke does. bindingsWanted keeps it off
	// the path of scripts that name none, and p.completionBatch re-lexes only what
	// changed since the last scan. The sigil test starts at the top, not the
	// batch: a temp table outlives GO.
	var bindings []sqlparse.Binding
	wantBindings := bindingsWanted(clause, scope.Query, refs, scope.CTEs, qualifier, prefix)
	if pos.hasQualifier {
		for _, outer := range scope.Outer {
			wantBindings = wantBindings || bindingsWanted(clause, outer.Query, nil, outer.CTEs, qualifier, prefix)
		}
	}
	if wantBindings && sqlparse.ContainsSigil(pos.buf, 0) {
		bindings = p.completionBatch.Bindings(pos.req.Lines, pos.buf, pos.req.Row)
	}
	// A three-part ref whose database's inventory is still loading leaves
	// pending set: the column answer would be missing its columns, so the
	// loading row stands in until refreshSysCompletionPopups re-asks.
	var pending bool
	rc := newResolveCtx(inv, sysInv, scope.CTEs, bindings)
	rc.otherDB = func(name string) (*completionInventory, bool) { return p.databaseInventory(inv, name) }
	rc.linked = p.linkedObject
	rc.pending = &pending
	rels := resolveRefs(rc, refs)
	sourceObject := clause == sqlparse.ClauseTable && !pos.cc.scalar
	if pos.hasQualifier && !sourceObject {
		matches := func(ref sqlparse.FromRef) bool {
			name := ref.Alias
			if name == "" {
				name = ref.Name
			}
			return inv.sameName(name, qualifier)
		}
		if !slices.ContainsFunc(refs, matches) {
		outerScopes:
			for _, outer := range scope.Outer {
				for _, ref := range outer.Query.FromRefs() {
					if matches(ref) {
						outerRC := rc
						outerRC.ctes = outer.CTEs
						rels = append(rels, resolveRefs(outerRC, []sqlparse.FromRef{ref})...)
						break outerScopes
					}
				}
			}
		}
	}
	if pos.state == sqlparse.LexNormal && pos.memberAccess && pos.cc.scalar {
		if spatial, handled := p.spatialMemberCandidates(pos.stmt, tokens, pos.pre.GoStart, pos.at, pos.upTo, rels, inv.collation, prefix); handled {
			return spatial
		}
	}
	loadingOr := func(items []editor.CompletionItem, wait bool) []editor.CompletionItem {
		if len(items) == 0 && wait {
			return []editor.CompletionItem{loadingCompletionItem}
		}
		return items
	}
	if len(tokens) >= 2 && keywordIs(tokens[0], "TRUNCATE") && keywordIs(tokens[1], "TABLE") {
		if len(chain) > 0 {
			return loadingOr(p.ddlTargetCandidates(inv, "ALTER", "TABLE", chain, prefix))
		}
		items := p.tableCandidates(inv, sysInv, nil, bindings, prefix)
		return slices.DeleteFunc(items, func(item editor.CompletionItem) bool {
			return item.Detail != "schema" && item.Detail != "table" && item.Detail != "temp table" &&
				!strings.HasPrefix(item.Detail, "database")
		})
	}

	switch {
	case len(chain) >= 2:
		return loadingOr(p.chainCandidates(inv, sysInv, chain, prefix))
	case pos.hasQualifier:
		if sourceObject {
			rels = nil
		}
		items, wait := p.memberCandidates(inv, sysInv, rels, qualifier, prefix)
		return loadingOr(items, wait || pending)
	case clause == sqlparse.ClauseTable:
		return p.tableCandidates(inv, sysInv, scope.CTEs, bindings, prefix)
	case pending:
		return []editor.CompletionItem{loadingCompletionItem}
	case len(rels) == 0:
		// Column context but nothing resolvable FROM'd yet: no columns to
		// pull, so fall back to the object list.
		return p.tableCandidates(inv, sysInv, nil, bindings, prefix)
	default:
		return p.scopedColumnCandidates(rels, inv.collation, prefix)
	}
}

// bindingsWanted reports whether this keystroke's answer can depend on the batch's
// declarations: something it is about to resolve carries a temp-table or
// table-variable sigil — a FROM-scope name at any nesting, a CTE body's, or the
// name being typed — or it is a table clause, where every temp table in the batch
// belongs in the list even before the sigil is typed.
//
// It gates the batch scan ScanBindings needs. A column-context keystroke in a
// script that merely passes scalar variables around ("WHERE id = @id") names no
// table variable, so it never pays for one.
func bindingsWanted(clause sqlparse.Clause, q *sqlparse.Query, refs []sqlparse.FromRef, ctes []sqlparse.CTE, qualifier, prefix string) bool {
	if clause == sqlparse.ClauseTable || sqlparse.HasSigil(qualifier) || sqlparse.HasSigil(prefix) {
		return true
	}
	for _, cte := range ctes {
		if queryUsesSigil(cte.Body, 0) {
			return true
		}
	}
	if q != nil {
		return queryUsesSigil(q, 0)
	}
	return refsUseSigil(refs)
}

// queryUsesSigil walks one query's refs, derived tables, sub-SELECTs and CTE
// bodies, bounded by the same depth cap relation resolution uses.
func queryUsesSigil(q *sqlparse.Query, depth int) bool {
	if q == nil || depth >= maxRelationDepth {
		return false
	}
	if refsUseSigil(q.From) {
		return true
	}
	for _, r := range q.From {
		if queryUsesSigil(r.Derived, depth+1) {
			return true
		}
	}
	for _, sub := range q.Subqueries {
		if queryUsesSigil(sub, depth+1) {
			return true
		}
	}
	for _, cte := range q.CTEs {
		if queryUsesSigil(cte.Body, depth+1) {
			return true
		}
	}
	return false
}

func refsUseSigil(refs []sqlparse.FromRef) bool {
	for _, r := range refs {
		if r.Derived == nil && r.Schema == "" && sqlparse.HasSigil(r.Name) {
			return true
		}
	}
	return false
}
