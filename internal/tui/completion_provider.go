package tui

import (
	"github.com/radix29/gossms/internal/tui/sqlparse"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// ---------------------------------------------------------------------------
// SQL-aware completion.Provider for the query editor — the only caller of
// controls.Editor.SetCompletionProvider in the app. Resolves the identifier at
// the cursor against the connected database's completionInventory
// (completion_inventory.go): schemas, tables, views, and columns, with
// schema/alias/table-dot member lookup and FROM-clause alias resolution.
//
// A lexical approximation, not a full T-SQL parser — the same spirit as
// controls.Editor's SelectStatementAtCursor (tuikit/controls/sql_statement.go).
// It recognises enough of the grammar (comments, string/quoted-identifier
// literals, clause keywords, dot-qualified names, CTE bodies, derived tables)
// to get common queries right; anything genuinely ambiguous offers nothing
// rather than guessing wrong.
//
// In scope: FROM/JOIN/APPLY refs and their aliases; WITH bindings (their own
// column list or the one their body produces, a CTE built on an earlier CTE, a
// recursive one without looping); derived tables and sub-SELECTs at any
// nesting; the clause state of the innermost query rather than the statement,
// so a cursor inside a CTE body completes against that body; temp tables (#t,
// ##t) and table variables (@t), resolved from their declaration — CREATE
// TABLE, DECLARE ... TABLE, SELECT ... INTO — found by scanning the cursor's
// GO-delimited batch, with PIVOT/UNPIVOT reshaping the reference it follows
// (see sqlparse.ScanBindings and sqlparse.Pivot).
//
// Out of scope, answered with nothing rather than a plausible wrong list:
// keyword completion, table-valued function result shapes, OPENJSON/OPENROWSET
// WITH column lists, and cross-database chains.
// ---------------------------------------------------------------------------

// newCompletionProvider builds the controls.CompletionProvider installed on
// this panel's editor (see NewQueryPanel). p's conn/database are read fresh on
// every call, so reconnecting or switching database (a mid-script USE) takes
// effect on the next keystroke without rebuilding it.
func (p *QueryPanel) newCompletionProvider() controls.CompletionProvider {
	return func(req controls.CompletionRequest) ([]controls.CompletionItem, int) {
		return p.sqlCompletionCandidates(req)
	}
}

// loadingCompletionItem is shown, alone, until the backing inventory finishes
// its first load. completion_inventory.go's refreshCompletionPopups re-queries
// this provider once the data lands, replacing it without another keystroke.
var loadingCompletionItem = controls.CompletionItem{Label: "Loading suggestions...", Placeholder: true}

// sqlCompletionCandidates answers one provider call. req.Text identifies the
// revision of req.Lines, which lets the prefix scan resume from
// p.completionPrefix rather than restarting at offset 0 on every keystroke; a
// zero Text (as the tests pass) never resumes.
func (p *QueryPanel) sqlCompletionCandidates(req controls.CompletionRequest) ([]controls.CompletionItem, int) {
	lines, row, col := req.Lines, req.Row, req.Col
	if p.app.cfg.IntelliSenseDisabled {
		return nil, col
	}
	if p.conn == nil || !p.app.isConnected(p.conn) {
		return nil, col
	}

	p.completionBuf = sqlparse.FlattenLinesInto(p.completionBuf, lines)
	buf := p.completionBuf
	upTo := sqlparse.OffsetForCursor(lines, row, col)

	// Scoped to the current statement — a table named in an earlier ';'- or
	// GO-separated statement must not leak into this one's FROM-scope/clause
	// detection (see sqlparse.ScanPrefix, which PrefixCache answers for).
	pre := p.completionPrefix.Scan(lines, buf, row, upTo, sqlparse.TextRevision{
		Doc: req.Text.Doc, Version: req.Text.Version, DirtyFrom: req.Text.DirtyFrom,
	})
	tokens, state, batchStart, quoteStart := pre.Tokens, pre.State, pre.BatchStart, pre.QuoteStart

	var qualifier, prefix string
	var replaceFrom int
	var hasQualifier bool
	switch state {
	case sqlparse.LexNormal:
		qualifier, prefix, replaceFrom, hasQualifier = sqlparse.TokenContext(tokens, upTo)
	case sqlparse.LexBracket:
		// An unterminated bracket identifier ("FROM [Cus|") is the one
		// non-normal lexer state completion still works in: everything after
		// the '[' is the prefix, and the whole "[..." span is replaced on
		// commit (bracketIfNeeded re-quotes only when needed).
		qualifier, _, _, hasQualifier = sqlparse.TokenContext(tokens, quoteStart)
		prefix = string(buf[quoteStart+1 : upTo])
		replaceFrom = quoteStart
	default:
		return nil, col // inside a string literal or comment
	}

	// Everything above works in flattened-buffer offsets, but the
	// controls.CompletionProvider contract wants a column on the cursor's row —
	// the editor replaces [replaceFrom, col) there and anchors the popup at it.
	// The replaced span always starts on the cursor's own row (identifiers
	// can't span lines; a '[' on an earlier row is malformed and bails), so
	// subtracting the row's start offset converts it.
	rowStart := sqlparse.OffsetForCursor(lines, row, 0)
	if replaceFrom < rowStart {
		return nil, col
	}
	replaceFrom -= rowStart

	inv := p.app.ensureCompletionInventory(p.conn, p.database)
	if inv.loading {
		return []controls.CompletionItem{loadingCompletionItem}, replaceFrom
	}
	if inv.err != nil || inv.catalog == nil {
		return nil, replaceFrom
	}
	sysInv := p.app.ensureSysCompletionInventory(p.conn)

	// FROM-scope/clause analysis looks at the whole statement, not just the
	// part already typed — a table named in "SELECT | FROM Customers c"
	// resolves as well as one typed above the cursor. The forward scan must
	// resume in normal lexer state, so when the cursor sits inside an
	// unterminated bracket identifier, skip past its closing ']' first.
	forwardFrom := upTo
	if state == sqlparse.LexBracket {
		for forwardFrom < len(buf) && buf[forwardFrom] != ']' {
			forwardFrom++
		}
		if forwardFrom < len(buf) {
			forwardFrom++
		}
	}
	batchEnd := sqlparse.StatementEndOffset(lines, buf, row, forwardFrom)
	forwardTokens, _, _, _ := sqlparse.TokenizeRange(buf, forwardFrom, batchEnd, false)

	// Statements stacked with no ';' between them still parse as one
	// ';'/GO-delimited batch above; narrow to the DML statement holding the
	// cursor so a bare column context doesn't pick up FROM refs from an
	// unrelated statement above or below it (see sqlparse.NarrowToDMLStatement).
	combined := append(append([]sqlparse.Token{}, tokens...), forwardTokens...)
	stmtStart, stmtEnd := sqlparse.NarrowToDMLStatement(combined, batchStart, batchEnd, upTo)
	tokens = sqlparse.TokensFrom(tokens, stmtStart)
	if stmtEnd < batchEnd {
		forwardTokens, _, _, _ = sqlparse.TokenizeRange(buf, forwardFrom, stmtEnd, false)
	}
	stmtTokens := append(append([]sqlparse.Token{}, tokens...), forwardTokens...)

	// The query tree, not the flat scan: the cursor's innermost SELECT, the
	// CTEs visible from it, and its own clause state (see sqlparse.ScopeAt). A
	// statement the parser can make nothing of leaves Query nil, and the flat
	// FROM-scope scan answers for it.
	scope := sqlparse.ScopeAt(stmtTokens, upTo)
	refs, clause := scope.Query.FromRefs(), scope.Clause
	if scope.Query == nil {
		refs, clause = sqlparse.ParseFromScope(stmtTokens), sqlparse.CurrentClause(tokens)
	}

	// Temp tables and table variables are declared in a different statement
	// from the one using them, so their shapes come from a scan of the whole
	// GO-delimited batch — the one piece of cross-statement work a keystroke
	// does. bindingsWanted keeps it off the path of scripts that name none.
	var bindings []sqlparse.Binding
	if bindingsWanted(clause, scope.Query, refs, scope.CTEs, qualifier, prefix) &&
		sqlparse.ContainsSigil(buf, pre.GoStart) {
		batchEnd := sqlparse.BatchEndOffset(lines, buf, row, forwardFrom)
		batchTokens, _, _, _ := sqlparse.TokenizeRange(buf, pre.GoStart, batchEnd, false)
		bindings = sqlparse.ScanBindings(batchTokens)
	}
	rels := resolveRefs(newResolveCtx(inv, sysInv, scope.CTEs, bindings), refs)

	switch {
	case hasQualifier:
		return p.memberCandidates(inv, sysInv, rels, qualifier, prefix), replaceFrom
	case clause == sqlparse.ClauseTable:
		return p.tableCandidates(inv, sysInv, scope.CTEs, bindings, prefix), replaceFrom
	case len(rels) == 0:
		// Column context but nothing resolvable FROM'd yet: no columns to
		// pull, so fall back to the object list.
		return p.tableCandidates(inv, sysInv, nil, bindings, prefix), replaceFrom
	default:
		return p.scopedColumnCandidates(rels, prefix), replaceFrom
	}
}

// bindingsWanted reports whether this keystroke's answer can depend on the
// batch's declarations: something it is about to resolve carries a temp-table
// or table-variable sigil — a FROM-scope name at any nesting, a CTE body's, or
// the name being typed — or it is a table clause, where every temp table in the
// batch belongs in the list even before the sigil is typed.
//
// It gates the batch scan ScanBindings needs. A column-context keystroke in a
// script that merely passes scalar variables around ("WHERE id = @id", most of
// them) names no table variable, so it never pays for one.
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
