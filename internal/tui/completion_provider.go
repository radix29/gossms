package tui

import (
	"github.com/radix29/gossms/internal/tui/sqlparse"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// ---------------------------------------------------------------------------
// SQL-aware completion.Provider for the query editor — the only caller of
// controls.Editor.SetCompletionProvider in the app. Resolves the identifier
// at the cursor against the connected database's completionInventory
// (completion_inventory.go): schemas, tables, views, and columns, with
// schema/alias/table-dot member lookup and FROM-clause alias resolution.
//
// This is a lexical approximation, not a full T-SQL parser — the same
// spirit as controls.Editor's own SelectStatementAtCursor (see
// tuikit/controls/sql_statement.go). It recognises enough of the grammar
// (comments, string/quoted-identifier literals, FROM/JOIN/WHERE/... clause
// keywords, dot-qualified names) to get common queries right; anything
// genuinely ambiguous offers nothing rather than guessing wrong. Keyword
// completion, CTEs/derived tables, temp tables and table variables, and
// cross-database chains are out of scope.
// ---------------------------------------------------------------------------

// newCompletionProvider builds the controls.CompletionProvider installed on
// this panel's editor (see NewQueryPanel). p's conn/database are read fresh
// on every call, so reconnecting or switching database (a mid-script USE)
// takes effect on the very next keystroke without needing to rebuild it.
func (p *QueryPanel) newCompletionProvider() controls.CompletionProvider {
	return func(lines [][]rune, row, col int) ([]controls.CompletionItem, int) {
		return p.sqlCompletionCandidates(lines, row, col)
	}
}

// loadingCompletionItem is shown, alone, while the backing inventory hasn't
// finished its first load yet — see completion_inventory.go's
// refreshCompletionPopups, which re-queries this provider once the real
// data lands so this placeholder gets replaced without another keystroke.
var loadingCompletionItem = controls.CompletionItem{Label: "Loading suggestions...", Placeholder: true}

func (p *QueryPanel) sqlCompletionCandidates(lines [][]rune, row, col int) ([]controls.CompletionItem, int) {
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
	// detection (see sqlparse.ScanPrefix).
	pre := sqlparse.ScanPrefix(lines, buf, row, upTo)
	tokens, state, batchStart, quoteStart := pre.Tokens, pre.State, pre.BatchStart, pre.QuoteStart

	var qualifier, prefix string
	var replaceFrom int
	var hasQualifier bool
	switch state {
	case sqlparse.LexNormal:
		qualifier, prefix, replaceFrom, hasQualifier = sqlparse.TokenContext(tokens, upTo)
	case sqlparse.LexBracket:
		// An unterminated bracket identifier ("FROM [Cus|") is the one
		// non-normal lexer state completion still works in: everything
		// after the '[' is the prefix, and the whole "[..." span gets
		// replaced on commit (bracketIfNeeded re-quotes the inserted name
		// only when it actually needs quoting).
		qualifier, _, _, hasQualifier = sqlparse.TokenContext(tokens, quoteStart)
		prefix = string(buf[quoteStart+1 : upTo])
		replaceFrom = quoteStart
	default:
		return nil, col // inside a string literal or comment
	}

	// Everything above works in flattened-buffer offsets, but the
	// controls.CompletionProvider contract wants a column on the cursor's
	// row — the editor replaces [replaceFrom, col) there and anchors the
	// popup at it. The replaced span always starts on the cursor's own row
	// (identifiers can't span lines; a '[' on an earlier row is malformed
	// anyway and bails), so subtracting the row's start offset converts it.
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

	// FROM-scope/clause analysis looks at the whole current statement, not
	// just the part already typed — a table named in "SELECT | FROM
	// Customers c" (cursor still in the column list) resolves just as well
	// as one already fully typed above the cursor. The forward scan must
	// resume in normal lexer state; when the cursor sits inside an
	// unterminated bracket identifier, skip past its closing ']' (if any)
	// first.
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

	// Multiple statements stacked in the editor with no ';' between them
	// still parse as one ';'/GO-delimited batch above; narrow further to
	// the DML statement containing the cursor so a bare column context in
	// one statement doesn't pick up FROM refs from an unrelated statement
	// stacked above or below it (see sqlparse.DMLStatementStarts /
	// sqlparse.NarrowToDMLStatement).
	combined := append(append([]sqlparse.Token{}, tokens...), forwardTokens...)
	stmtStart, stmtEnd := sqlparse.NarrowToDMLStatement(combined, batchStart, batchEnd, upTo)
	tokens = sqlparse.TokensFrom(tokens, stmtStart)
	if stmtEnd < batchEnd {
		forwardTokens, _, _, _ = sqlparse.TokenizeRange(buf, forwardFrom, stmtEnd, false)
	}
	refs := sqlparse.ParseFromScope(append(append([]sqlparse.Token{}, tokens...), forwardTokens...))

	switch {
	case hasQualifier:
		return p.memberCandidates(inv, sysInv, refs, qualifier, prefix), replaceFrom
	case sqlparse.CurrentClause(tokens) == sqlparse.ClauseTable:
		return p.tableCandidates(inv, sysInv, prefix), replaceFrom
	case len(refs) == 0:
		// Column context but nothing's been FROM'd yet — nothing to pull
		// columns from, so fall back to the object list.
		return p.tableCandidates(inv, sysInv, prefix), replaceFrom
	default:
		return p.scopedColumnCandidates(inv, sysInv, refs, prefix), replaceFrom
	}
}
