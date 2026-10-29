package safety

import (
	"fmt"
	"strings"

	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/tuikit/sqltext"
)

// Policy is what a run of a classified script is allowed to do.
type Policy struct {
	// Allow is --allow, the highest class the caller asks for.
	Allow spec.Class
	// Ceiling is the profile's max_allow, which no --allow raises; empty (an
	// ad-hoc connection) is admin.
	Ceiling spec.Class

	// Transaction: the script runs inside a wrapping transaction (--transaction
	// rollback or commit), which it must not control or need to be outside of.
	Transaction bool
	// MaxRowsAffected: the run commits only if the server's row counts stay
	// under a limit, so a statement whose count can be invisible is refused.
	MaxRowsAffected bool
}

// Blocked is a refusal: what Check found and why the run may not go ahead.
type Blocked struct {
	// Detected is the script's class; Allowed the effective --allow (the lower
	// of Allow and Ceiling).
	Detected, Allowed spec.Class
	// Statements is the statements that cause the refusal, in script order.
	Statements []Statement
	Message    string
	Hint       string
}

// Allowed is the class p lets a script reach: the lower of Allow and Ceiling.
func (p Policy) Allowed() spec.Class { return effective(p.Allow, p.Ceiling) }

// effective is the lower of allow and ceiling; an empty ceiling is admin.
func effective(allow, ceiling spec.Class) spec.Class {
	if ceiling != "" && Rank(ceiling) < Rank(allow) {
		return ceiling
	}
	return allow
}

// Check decides whether sc may run under p, nil when it may. The checks run in
// a fixed order, and the first that refuses is reported: the class against
// --allow and the ceiling, then the modes the command line owns (SET
// PARSEONLY, NOEXEC, SHOWPLAN_*), then what a wrapping transaction cannot hold,
// then what --max-rows-affected cannot count.
func Check(sc *Script, p Policy) *Blocked {
	allowed := p.Allowed()
	if b := p.Refuse("the script", sc.Class); b != nil {
		b.Statements = matching(sc, func(st Statement) bool { return Rank(st.Class) > Rank(allowed) })
		return b
	}
	b := &Blocked{Detected: sc.Class, Allowed: allowed}
	if sc.Flags.Has(FlagModeOption) {
		b.Statements = flagged(sc, FlagModeOption)
		b.Message = "SET PARSEONLY, NOEXEC and SHOWPLAN_* are refused: the command line runs these modes itself"
		b.Hint = "Remove the SET statement."
		return b
	}
	if p.Transaction {
		if sc.Flags.Has(FlagTranControl) {
			b.Statements = flagged(sc, FlagTranControl)
			b.Message = "the script controls transactions itself, so the wrapping transaction cannot hold it"
			b.Hint = "Remove BEGIN TRAN, COMMIT, ROLLBACK, SAVE TRAN and SET IMPLICIT_TRANSACTIONS from the script, or run it without --transaction."
			return b
		}
		if sc.Flags.Has(FlagNotInTransaction) {
			b.Statements = flagged(sc, FlagNotInTransaction)
			b.Message = "the script has a statement SQL Server refuses inside a transaction"
			b.Hint = "Run it without --transaction."
			return b
		}
	}
	if p.MaxRowsAffected {
		const blind = FlagNoCount | FlagDynamic | FlagExec | FlagOutput
		if sc.Flags&blind != 0 {
			b.Statements = matching(sc, func(st Statement) bool { return st.Flags&blind != 0 })
			b.Message = "--max-rows-affected cannot count every row this script changes " +
				"(SET NOCOUNT ON, dynamic SQL, a procedure call or an OUTPUT clause)"
			b.Hint = "Use --transaction rollback to measure the rows affected first."
			return b
		}
	}
	return nil
}

// Refuse is the class check alone: the refusal of what — "the script", or a
// command's name — whose class is class, nil when p allows it. A write
// command has a fixed class and no statements, so its refusal is this one
// with the same hints a script's gets.
func (p Policy) Refuse(what string, class spec.Class) *Blocked {
	allowed := p.Allowed()
	if Rank(class) <= Rank(allowed) {
		return nil
	}
	b := &Blocked{Detected: class, Allowed: allowed,
		Message: fmt.Sprintf("%s is %s; the allowed class is %s", what, class, allowed)}
	switch {
	case Rank(class) <= Rank(p.Allow):
		// --allow would do; the profile's ceiling is what refuses.
		b.Hint = fmt.Sprintf("The profile's ceiling (max_allow) is %s, and no --allow raises it. "+
			"Use a profile whose ceiling allows %s; the ceiling is set in the TUI's Profile... dialog.", p.Ceiling, class)
	case p.Ceiling != "" && Rank(class) > Rank(p.Ceiling):
		b.Hint = fmt.Sprintf("It needs --allow %s, above the profile's ceiling (max_allow %s); use a profile whose ceiling allows it.",
			class, p.Ceiling)
	default:
		b.Hint = fmt.Sprintf("Re-run with --allow %s if the change is intended.", class)
	}
	return b
}

func matching(sc *Script, keep func(Statement) bool) []Statement {
	var out []Statement
	for _, st := range sc.Statements {
		if keep(st) {
			out = append(out, st)
		}
	}
	return out
}

func flagged(sc *Script, f Flag) []Statement {
	return matching(sc, func(st Statement) bool { return st.Flags.Has(f) })
}

// Describe is st as a refusal names it: "UPDATE Sales.Customer (line 1)", with
// the reason after a colon when the leader alone did not decide.
func (st Statement) Describe() string {
	var b strings.Builder
	b.WriteString(st.Leader)
	if st.Object != "" {
		b.WriteString(" " + st.Object)
	}
	fmt.Fprintf(&b, " (line %d)", st.Line)
	if st.Reason != "" {
		b.WriteString(": " + st.Reason)
	}
	return b.String()
}

// Describe is every refused statement, "; "-separated.
func (b *Blocked) Describe() string {
	parts := make([]string, len(b.Statements))
	for i, st := range b.Statements {
		parts[i] = st.Describe()
	}
	return strings.Join(parts, "; ")
}

// ModeWords is the script lines naming PARSEONLY, NOEXEC or a SHOWPLAN_*
// option anywhere outside literals, comments and quoted identifiers, in
// order. query parse refuses them all, OFF included: under its PARSEONLY, a
// SET PARSEONLY OFF in the script would run what follows. Check's mode rule
// refuses only ON, since a run's OFF is a no-op scripts often end with.
func ModeWords(script string) []int {
	var lines []int
	for _, b := range sqltext.SplitBatches(script) {
		toks, _ := lexBatch(b.Text, b.StartLine)
		for _, tk := range toks {
			if tk.kind == sqltext.KindWord && (tk.word == "PARSEONLY" || tk.word == "NOEXEC" || strings.HasPrefix(tk.word, "SHOWPLAN_")) {
				lines = append(lines, tk.line)
			}
		}
	}
	return lines
}

// StatisticsXMLWords is the script lines of a SET STATISTICS list naming XML
// (SET STATISTICS IO, XML ON), ON or OFF, outside literals, comments and
// quoted identifiers, in order. plan actual captures under STATISTICS XML
// itself, so the script's OFF would lose the plans of what follows.
func StatisticsXMLWords(script string) []int {
	var lines []int
	for _, b := range sqltext.SplitBatches(script) {
		toks, _ := lexBatch(b.Text, b.StartLine)
		for i, tk := range toks {
			if tk.kind != sqltext.KindWord || tk.word != "STATISTICS" || i == 0 || toks[i-1].word != "SET" {
				continue
			}
			for _, o := range toks[i+1:] {
				if o.word == "," {
					continue
				}
				if o.kind != sqltext.KindWord || (o.word != "IO" && o.word != "TIME" && o.word != "PROFILE" && o.word != "XML") {
					break
				}
				if o.word == "XML" {
					lines = append(lines, o.line)
				}
			}
		}
	}
	return lines
}
