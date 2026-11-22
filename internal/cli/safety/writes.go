package safety

import (
	"strings"

	"github.com/radix29/gossms/internal/sqltext"
)

// Write is one table write of a statement — an INSERT, UPDATE, DELETE or
// MERGE, nested behind IF, ELSE, WHILE or BEGIN too — as --max-rows-affected
// reads it. The server's row count covers the target's own rows only: what a
// trigger on it does under SET NOCOUNT ON, and what a foreign key's ON DELETE
// or ON UPDATE action does to another table, is never counted. So the run
// checks each target on the server for both before the script is sent
// (docs/cli.md § Transactions), and refuses a target it cannot name.
type Write struct {
	// Verb is INSERT, UPDATE, DELETE or MERGE.
	Verb string
	// Target is the written name as the statement has it ("dbo.t", "#t",
	// "@t"), an alias resolved to the table it stands for; "" when no name
	// could be read.
	Target string
	// Parts is Target's parts, each unquoted, as written (case kept): one to
	// four. Nil when Unresolvable is set.
	Parts []string
	// Line is the verb's script line.
	Line int
	// Unresolvable says why the classifier cannot name the table the write
	// lands in — a CTE, a derived table, a linked server, no name at all;
	// "" when Parts names it.
	Unresolvable string
}

// Local reports whether w writes a table variable or a temporary table, which
// SQL Server lets carry neither a trigger nor an enforced foreign key, so the
// server's count is the whole change.
func (w Write) Local() bool {
	if len(w.Parts) == 0 {
		return false
	}
	last := w.Parts[len(w.Parts)-1]
	return strings.HasPrefix(last, "@") || strings.HasPrefix(last, "#")
}

// Describe is w as a refusal names it: "UPDATE dbo.t (line 3): why".
func (w Write) Describe(why string) Statement {
	return Statement{Leader: w.Verb, Object: w.Target, Line: w.Line, Reason: why}
}

// Writes is every table write in sc, in script order.
func (sc *Script) Writes() []Write {
	var out []Write
	for _, st := range sc.Statements {
		out = append(out, st.Writes...)
	}
	return out
}

// writeVerbs are the verbs a Write is read from.
var writeVerbs = map[string]bool{"INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true}

// notATarget are the words that, after a write verb, show it is not a
// statement's: a permission (GRANT UPDATE ON t, GRANT INSERT, UPDATE),
// an audit or security-policy action (ADD (INSERT ON t), AFTER UPDATE ON t)
// or a column list (GRANT UPDATE (c) ON t).
var notATarget = map[string]bool{",": true, "ON": true, ")": true, "(": true, "TO": true}

// readWrite reads the write whose verb is toks[i], ok false when the verb is
// not a statement's own: a MERGE action (THEN UPDATE SET …), a foreign key's
// ON DELETE/ON UPDATE, a trigger or policy event (AFTER/BEFORE/FOR …), or a
// verb followed by a permission's or column list's punctuation. toks is the
// whole statement, for its WITH clause and its aliases.
func readWrite(toks []token, i int) (Write, bool) {
	w := Write{Verb: toks[i].word, Line: toks[i].line}
	if i > 0 {
		if p := toks[i-1]; p.is("THEN") || p.is("ON") || p.is("AFTER") || p.is("BEFORE") || p.is("FOR") {
			return w, false
		}
	}
	if i+1 >= len(toks) {
		return w, false
	}
	if n := toks[i+1]; n.kind == sqltext.KindPunct && notATarget[n.word] || n.is("ON") || n.is("TO") {
		return w, false
	}
	sub := toks[i:]
	at := dmlTargetIndex(sub)
	parts, _, ok := nameAt(sub, at)
	w.Target = objectName(sub, at)
	switch {
	case at < len(sub) && (sub[at].is("OPENQUERY") || sub[at].is("OPENROWSET") || sub[at].is("OPENDATASOURCE")):
		w.Target, w.Unresolvable = sub[at].word, "a rowset function's target, which the check cannot read"
		return w, true
	case !ok:
		w.Unresolvable = "no table name the check can read"
		return w, true
	case len(parts) == 4:
		w.Unresolvable = "a linked server's table, which the check cannot read"
		return w, true
	case len(parts) > 4:
		w.Unresolvable = "no table name the check can read"
		return w, true
	}
	w.Parts = parts
	if len(parts) == 1 && !w.Local() {
		if cteNamed(toks, parts[0]) {
			w.Parts, w.Unresolvable = nil, "a common table expression, whose base tables the check cannot see"
			return w, true
		}
		// An UPDATE or DELETE may name an alias its FROM clause defines.
		if w.Verb != "UPDATE" && w.Verb != "DELETE" {
			return w, true
		}
		if base, derived, found := aliasOf(toks, i+at+1, toks[i].depth, parts[0]); derived {
			w.Parts, w.Unresolvable = nil, "a derived table, whose base tables the check cannot see"
			return w, true
		} else if found {
			w.Target, w.Parts = base.raw, base.parts
			if len(w.Parts) >= 4 {
				w.Parts, w.Unresolvable = nil, "a linked server's table, which the check cannot read"
			}
		}
	}
	return w, true
}

// dmlTargetIndex is the index in toks, which starts at an INSERT, UPDATE,
// DELETE or MERGE, of the name it writes: past TOP (n) [PERCENT] and INTO or
// FROM — dmlTarget's walk.
func dmlTargetIndex(toks []token) int {
	i := 1
	if i < len(toks) && toks[i].is("TOP") {
		i++
		if i < len(toks) && toks[i].punct("(") {
			for i++; i < len(toks) && toks[i].depth > toks[0].depth; i++ {
			}
		} else if i < len(toks) && toks[i].kind == sqltext.KindNumber {
			i++ // the legacy TOP n
		}
		if i < len(toks) && toks[i].is("PERCENT") {
			i++
		}
	}
	if i < len(toks) && (toks[i].is("INTO") || toks[i].is("FROM")) {
		i++
	}
	return i
}

// nameAt reads the dotted name starting at toks[i], each part unquoted with
// its case kept, and the index after it; an omitted part (db..t) is "". ok is false when toks[i]
// starts no name, or the name runs into one more part than T-SQL allows.
func nameAt(toks []token, i int) (parts []string, next int, ok bool) {
	expect := true
	for ; i < len(toks); i++ {
		t := toks[i]
		switch {
		case expect && t.kind == sqltext.KindWord:
			parts, expect = append(parts, t.raw), false
		case expect && t.kind == sqltext.KindQuotedIdent:
			parts, expect = append(parts, t.word), false
		case t.punct("."):
			if expect {
				parts = append(parts, "")
			}
			expect = true
		default:
			return parts, i, len(parts) > 0 && !expect
		}
	}
	return parts, i, len(parts) > 0 && !expect
}

// cteNamed reports whether toks, a statement, opens with WITH and defines a
// common table expression called name: WITH name [(cols)] AS (…), compared
// without case, as the server compares identifiers under a case-insensitive
// collation (a false match only refuses more).
func cteNamed(toks []token, name string) bool {
	if len(toks) == 0 || !toks[0].is("WITH") {
		return false
	}
	d := toks[0].depth
	for j := 1; j < len(toks); j++ {
		t := toks[j]
		if t.depth != d || t.kind != sqltext.KindWord && t.kind != sqltext.KindQuotedIdent {
			continue
		}
		if t.kind == sqltext.KindWord && writeVerbs[t.word] || t.is("SELECT") {
			return false // the main statement: the CTE list is over
		}
		if !(toks[j-1].is("WITH") || toks[j-1].punct(",")) {
			continue
		}
		k := j + 1
		if k < len(toks) && toks[k].punct("(") {
			for k++; k < len(toks) && toks[k].depth > d; k++ {
			}
		}
		if k < len(toks) && toks[k].is("AS") && strings.EqualFold(t.word, name) {
			return true
		}
	}
	return false
}

// aliasName is a FROM-clause table name an alias stands for.
type aliasName struct {
	raw   string
	parts []string
}

// aliasOf looks in toks, from index from on, for alias defined in the FROM
// clause of the statement at depth d — FROM or JOIN, or a comma after FROM,
// then a table name, then [AS] alias — and returns the name it stands for.
// derived is set when alias names a derived table or a rowset function's
// result (FROM (SELECT …) alias, CROSS APPLY f(x) alias), whose base tables
// are unknowable, or stands for two different names; found when one table
// name was found.
func aliasOf(toks []token, from, d int, alias string) (name aliasName, derived, found bool) {
	inFrom := false
	for j := from; j < len(toks); j++ {
		t := toks[j]
		if t.depth != d {
			continue
		}
		switch {
		case t.is("FROM"):
			inFrom = true
			continue
		case t.is("WHERE") || t.is("OPTION") || t.is("OUTPUT") || t.is("SET"):
			inFrom = false
			continue
		}
		if !inFrom || !isIdent(t) || !strings.EqualFold(aliasText(t), alias) || j+1 < len(toks) && toks[j+1].punct(".") {
			continue
		}
		k := j - 1
		if toks[k].is("AS") {
			k--
		}
		if toks[k].punct(")") {
			return aliasName{}, true, false
		}
		if !isIdent(toks[k]) {
			continue
		}
		start := k
		for start > 0 && toks[start-1].punct(".") {
			if start--; start > 0 && isIdent(toks[start-1]) {
				start--
			}
		}
		if start == 0 {
			continue
		}
		if p := toks[start-1]; !(p.is("FROM") || p.is("JOIN") || p.punct(",")) {
			continue
		}
		parts, _, ok := nameAt(toks, start)
		if !ok {
			continue
		}
		var raw strings.Builder
		for _, t := range toks[start : k+1] {
			raw.WriteString(t.raw)
		}
		n := aliasName{raw: raw.String(), parts: parts}
		if found && !strings.EqualFold(n.raw, name.raw) {
			return aliasName{}, true, false
		}
		name, found = n, true
	}
	return name, false, found
}

func isIdent(t token) bool { return t.kind == sqltext.KindWord || t.kind == sqltext.KindQuotedIdent }

// aliasText is an identifier token's name: a word as written, a quoted one
// unquoted.
func aliasText(t token) string {
	if t.kind == sqltext.KindQuotedIdent {
		return t.word
	}
	return t.raw
}

// NameParts splits s, a multipart object name such as a synonym's base object
// ("[db].[dbo].[t]"), into its parts, each unquoted; ok is false when s is not
// one name of one to four parts.
func NameParts(s string) (parts []string, ok bool) {
	toks, _ := lexBatch(s, 0)
	parts, next, ok := nameAt(toks, 0)
	if !ok || len(parts) > 4 || next != len(toks) {
		return nil, false
	}
	return parts, true
}

// UseDatabases is every database a USE statement in script names, in order,
// each once (compared without case): the databases the script's writes may
// land in besides the run's own. USE behind IF or ELSE counts too — the check
// cannot know which branch runs, so it looks in every one. USE HINT and USE
// PLAN are query hints, not statements.
func UseDatabases(script string) []string {
	var out []string
	for _, b := range sqltext.SplitBatches(script) {
		toks, _ := lexBatch(b.Text, b.StartLine)
		for i, tk := range toks {
			if !tk.is("USE") || i+1 >= len(toks) {
				continue
			}
			n := toks[i+1]
			if n.is("HINT") || n.is("PLAN") || n.kind != sqltext.KindWord && n.kind != sqltext.KindQuotedIdent {
				continue
			}
			name := aliasText(n)
			if !containsFold(out, name) {
				out = append(out, name)
			}
		}
	}
	return out
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
