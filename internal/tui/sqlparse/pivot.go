package sqlparse

// ---------------------------------------------------------------------------
// PIVOT / UNPIVOT
//
// A pivoted reference is the one place in a FROM clause where the source's own
// column list is not what the rest of the query sees: PIVOT drops the
// aggregated and pivoted columns and adds one per IN-list value, UNPIVOT drops
// the IN-list columns and adds the value and name columns. Before this parsed,
// "FROM t PIVOT (...) AS p" read as "t AS PIVOT" — the clause keyword taken for
// an alias — and offered t's own columns under the name PIVOT.
// ---------------------------------------------------------------------------

// Pivot is the output shape a PIVOT/UNPIVOT clause imposes on the reference it
// follows. Only the names are recorded; what they resolve to is the caller's
// job, the same division the rest of this package keeps.
type Pivot struct {
	Unpivot bool

	// Agg is the column inside a PIVOT's aggregate call — "Amount" in
	// "SUM(o.Amount)" — and is empty for an aggregate over no column at all
	// ("COUNT(*)").
	Agg string

	// Value is the value column an UNPIVOT names before FOR.
	Value string

	// For is the column named after FOR: the one PIVOT spreads into columns,
	// or the one UNPIVOT collects the old column names into.
	For string

	// In is the IN ( ... ) list: PIVOT's new column names, or the source
	// columns UNPIVOT folds away.
	In []string
}

// pivotClauseSkip is how far past the keyword a PIVOT clause's '(' sits.
const pivotClauseSkip = 2

// parsePivot parses a PIVOT/UNPIVOT clause at p.i:
//
//	PIVOT   ( <agg> ( col ) FOR <col> IN ( a, b, c ) )
//	UNPIVOT ( <col>         FOR <col> IN ( a, b, c ) )
//
// PIVOT, UNPIVOT and FOR are context-sensitive in T-SQL — all three are legal
// column and alias names — so they are matched as identifiers followed by the
// shape they must have, not added to sqlKeywordList where they would change
// how every other statement tokenizes. skipSelectModifiers matches PERCENT and
// TIES the same way.
//
// Anything that does not match rewinds p.i and returns nil, so the reference
// parses exactly as it did before rather than by a guess.
func (p *queryParser) parsePivot() *Pivot {
	unpivot := p.atIdentFold("UNPIVOT")
	if !unpivot && !p.atIdentFold("PIVOT") {
		return nil
	}
	if p.i+1 >= len(p.toks) || p.toks[p.i+1].Kind != TokenParenOpen {
		return nil // an alias that happens to be spelled "pivot"
	}
	start := p.i
	p.i += pivotClauseSkip
	pv := &Pivot{Unpivot: unpivot}

	head, ok := p.pivotNameUntil(func() bool { return p.atIdentFold("FOR") })
	if !ok {
		p.i = start
		return nil
	}
	if unpivot {
		// An UNPIVOT's value column is the name itself, so a clause that names
		// none is not one.
		if head == "" {
			p.i = start
			return nil
		}
		pv.Value = head
	} else {
		// A PIVOT's aggregate may well wrap no column at all — COUNT(*) — and
		// then there is nothing for it to drop. An empty Agg says exactly that.
		pv.Agg = head
	}
	p.i++ // FOR

	forCol, ok := p.pivotNameUntil(func() bool { return p.atKeyword("IN") })
	if !ok || forCol == "" {
		p.i = start
		return nil
	}
	p.i++ // IN
	if !p.at(TokenParenOpen) {
		p.i = start
		return nil
	}
	names, ok := p.parseColumnNameList()
	if !ok || !p.at(TokenParenClose) {
		p.i = start
		return nil
	}
	p.i++ // the clause's own ')'
	pv.For, pv.In = forCol, names
	return pv
}

// pivotNameUntil walks to the terminator stop reports and returns the column
// name it passed, with ok reporting only whether the terminator was reached —
// a clause can legitimately name no column ("COUNT(*)"), and telling that
// apart from a shape that isn't a pivot clause at all is the caller's job.
//
// Which identifier is the column depends on whether an argument list opened:
//
//   - one did — "SUM(Total)", "SUM(o.Total)", "COUNT(*)" — and the column is
//     the last name inside it, an empty string when it holds none
//   - none did — a bare "Amount", a qualified "o.Year" — and it is the last
//     name at the clause's own level
//
// stop is only consulted at that level, so the FOR of a nested expression
// cannot end the aggregate early, and the clause's own closing ')' arriving
// first means this is not a pivot clause.
func (p *queryParser) pivotNameUntil(stop func() bool) (string, bool) {
	outer, inner := "", ""
	depth, grouped := 0, false
	for p.i < len(p.toks) {
		if depth == 0 && stop() {
			if grouped {
				return inner, true
			}
			return outer, true
		}
		switch t := p.toks[p.i]; t.Kind {
		case TokenParenOpen:
			depth++
			grouped = true
		case TokenParenClose:
			if depth == 0 {
				return "", false
			}
			depth--
		case TokenIdent:
			// Only the argument list's own level: a name nested deeper belongs
			// to an inner call, not to the column being aggregated.
			switch depth {
			case 0:
				outer = t.Text
			case 1:
				inner = t.Text
			}
		}
		p.i++
	}
	return "", false
}
