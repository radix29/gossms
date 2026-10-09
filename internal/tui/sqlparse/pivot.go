package sqlparse

// ---------------------------------------------------------------------------
// PIVOT / UNPIVOT
//
// A pivoted reference is where a FROM source's own column list is not what the
// rest of the query sees: PIVOT drops the aggregated and pivoted columns and
// adds one per IN-list value, UNPIVOT drops the IN-list columns and adds the
// value and name columns. Unparsed, "FROM t PIVOT (...) AS p" read as "t AS
// PIVOT" (keyword taken for an alias) and offered t's columns under PIVOT.
// ---------------------------------------------------------------------------

// Pivot is the output shape a PIVOT/UNPIVOT clause imposes on the reference it
// follows. Only names are recorded; resolving them is the caller's job.
type Pivot struct {
	Unpivot bool

	// Agg is the column inside a PIVOT's aggregate call ("Amount" in
	// "SUM(o.Amount)"), empty for an aggregate over no column ("COUNT(*)").
	Agg string

	// Func is a PIVOT's aggregate as written ("SUM", or "MyAgg" in
	// "dbo.MyAgg(x)"), so the caller can type the output columns. Empty for
	// UNPIVOT.
	Func string

	// FuncQualifier is the parts before Func in a qualified call: ["dbo"] for
	// "dbo.MyAgg(x)", ["Sales", "dbo"] for "Sales.dbo.MyAgg(x)", an omitted
	// part as "". nil for an unqualified call, always a built-in (T-SQL refuses
	// an unqualified user-defined aggregate, Msg 195); a qualified one is
	// user-defined whatever its name ("dbo.SUM").
	FuncQualifier []string

	// Value is the value column an UNPIVOT names before FOR.
	Value string

	// For is the column after FOR: the one PIVOT spreads into columns, or the
	// one UNPIVOT collects the old column names into.
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
// PIVOT, UNPIVOT and FOR are reserved (bare, none is a legal column or alias
// name: Msg 156, probed on 17), but are matched as identifiers followed by the
// shape they must have, not added to sqlKeywordList where they would change how
// every other statement tokenizes. A bare one that doesn't open its clause is
// no alias either (notAliases). skipSelectModifiers matches PERCENT and TIES
// the same way.
//
// Anything not matching rewinds p.i and returns nil, so the reference parses as
// before rather than by a guess.
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

	head, call, ok := p.pivotNameUntil(func() bool { return p.atIdentFold("FOR") })
	if !ok {
		p.i = start
		return nil
	}
	if unpivot {
		// An UNPIVOT's value column is the name itself; none means not one.
		if head == "" {
			p.i = start
			return nil
		}
		pv.Value = head
	} else {
		// A PIVOT's aggregate may wrap no column (COUNT(*)): empty Agg, nothing
		// to drop.
		pv.Agg = head
		if n := len(call); n > 0 {
			pv.Func = call[n-1]
			if n > 1 {
				pv.FuncQualifier = call[:n-1]
			}
		}
	}
	p.i++ // FOR

	forCol, _, ok := p.pivotNameUntil(func() bool { return p.atKeyword("IN") })
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
// name it passed and the dotted name the argument list belongs to, part by part
// (nil when none opened); ok reports only whether the terminator was reached. A
// clause can legitimately name no column ("COUNT(*)"); telling that from a
// non-pivot shape is the caller's job.
//
// Which identifier is the column depends on whether an argument list opened:
//
//   - one did ("SUM(Total)", "SUM(o.Total)", "COUNT(*)"): the last name inside
//     it, "" when none
//   - none did (a bare "Amount", a qualified "o.Year"): the last name at the
//     clause's own level
//
// stop is consulted only at that level, so a nested expression's FOR can't end
// the aggregate early, and the clause's own closing ')' arriving first means
// this is not a pivot clause.
func (p *queryParser) pivotNameUntil(stop func() bool) (name string, call []string, ok bool) {
	// chain is the dotted name read at the clause's own level, an omitted part
	// ("db..agg") as "": the column once the clause ends, the call's name when
	// an argument list opens after it.
	var chain []string
	inner := ""
	depth, grouped, afterDot := 0, false, false
	for p.i < len(p.toks) {
		if depth == 0 && stop() {
			if grouped {
				return inner, call, true
			}
			if len(chain) == 0 {
				return "", nil, true
			}
			return chain[len(chain)-1], nil, true
		}
		t := p.toks[p.i]
		if depth == 0 {
			switch t.Kind {
			case TokenIdent:
				if !afterDot {
					chain = nil
				}
				chain = append(chain, t.Text)
			case TokenDot:
				if afterDot {
					chain = append(chain, "")
				}
			}
			afterDot = t.Kind == TokenDot
		}
		switch t.Kind {
		case TokenParenOpen:
			if depth == 0 && !grouped {
				call = chain
			}
			depth++
			grouped = true
		case TokenParenClose:
			if depth == 0 {
				return "", nil, false
			}
			depth--
		case TokenIdent:
			// Only the argument list's own level: a deeper name belongs to an
			// inner call, not the aggregated column.
			if depth == 1 {
				inner = t.Text
			}
		}
		p.i++
	}
	return "", nil, false
}
