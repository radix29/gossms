// Package safety is the command line's guardrail over T-SQL (docs/cli.md
// § Safety): Classify puts every statement of a script into an operation class
// — read, write, ddl or admin — and flags what the safety checks refuse or
// cannot see through. It is a guardrail, not the security boundary; that is
// the login's permissions.
//
// It lexes with gossms's one lexer, tuikit/sqltext, and splits statements by
// its StatementCutter, so a keyword inside a comment, literal or quoted
// identifier means nothing here either, and a statement boundary is where
// Ctrl+Enter's is.
package safety

import (
	"strconv"
	"strings"

	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/tuikit/sqltext"
)

// Flag marks something about a statement that a safety check reads beyond its
// class.
type Flag uint16

const (
	// FlagTranControl: BEGIN TRAN, COMMIT, ROLLBACK, SAVE TRAN or SET
	// IMPLICIT_TRANSACTIONS ON — refused under --transaction rollback.
	FlagTranControl Flag = 1 << iota
	// FlagNotInTransaction: a statement SQL Server refuses inside a user
	// transaction (ALTER DATABASE, BACKUP, RESTORE, RECONFIGURE, …).
	FlagNotInTransaction
	// FlagNoCount: SET NOCOUNT ON, after which the server reports no row counts.
	FlagNoCount
	// FlagDynamic: SQL the classifier cannot see — EXEC(@sql), EXEC('…'),
	// sp_executesql, EXEC @proc, EXEC … AT, OPENQUERY/OPENROWSET/OPENDATASOURCE.
	FlagDynamic
	// FlagExec: a procedure call not on the read allow-list; its body is unseen.
	FlagExec
	// FlagModeOption: SET PARSEONLY, NOEXEC or SHOWPLAN_* ON, modes the CLI
	// owns through query parse and plan.
	FlagModeOption
	// FlagOutput: a DML statement with an OUTPUT clause, whose row count arrives
	// after its OUTPUT rows and so reads like a SELECT's (query.RowCount).
	FlagOutput
)

var flagNames = []struct {
	f    Flag
	name string
}{
	{FlagTranControl, "transaction-control"},
	{FlagNotInTransaction, "not-in-transaction"},
	{FlagNoCount, "nocount"},
	{FlagDynamic, "dynamic-sql"},
	{FlagExec, "exec"},
	{FlagModeOption, "mode-option"},
	{FlagOutput, "output-clause"},
}

// Has reports whether every flag in g is set in f.
func (f Flag) Has(g Flag) bool { return f&g == g }

// Names lists the set flags by name, in declaration order.
func (f Flag) Names() []string {
	var out []string
	for _, n := range flagNames {
		if f.Has(n.f) {
			out = append(out, n.name)
		}
	}
	return out
}

// Statement is one classified statement.
type Statement struct {
	// Batch is the 1-based GO batch the statement is in; Repeat its GO count.
	Batch, Repeat int
	// Line is the 1-based script line of the statement's first token.
	Line int

	Class spec.Class

	// Leader is the statement's leading keywords, upper-cased ("UPDATE",
	// "SELECT INTO", "CREATE PROCEDURE", "EXEC"); Object the name it acts on as
	// written ("Sales.Customer", "dbo.usp_x"), empty when there is none.
	Leader, Object string

	// Reason says what raised the class above the leader's, or why the leader
	// alone gave it: "keyword DISABLE (line 3)", "procedure dbo.p is not on the
	// read allow-list". Empty when the leader rule alone decided.
	Reason string

	Flags Flag
}

// Script is a classified script.
type Script struct {
	Statements []Statement
	// Class is the highest statement class, read for an empty script.
	Class spec.Class
	// Flags is every statement's flags.
	Flags Flag
}

// rank orders the classes; Classify never yields anything outside it.
var rank = map[spec.Class]int{spec.ClassRead: 0, spec.ClassWrite: 1, spec.ClassDDL: 2, spec.ClassAdmin: 3}

// Rank orders the classes read < write < ddl < admin; -1 for anything else.
func Rank(c spec.Class) int {
	if r, ok := rank[c]; ok {
		return r
	}
	return -1
}

func maxClass(a, b spec.Class) spec.Class {
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// Classify classifies every statement of script. It fails closed: a statement
// it does not recognise is write, and a keyword anywhere in a statement raises
// it to that keyword's class (the floor), so one the statement splitter merged
// into the statement before it (it cuts only at its leader verbs) is not
// hidden.
func Classify(script string) *Script {
	sc := &Script{Class: spec.ClassRead}
	for bi, b := range sqltext.SplitBatches(script) {
		for _, st := range classifyBatch(b.Text, b.StartLine) {
			st.Batch, st.Repeat = bi+1, b.Count
			sc.Statements = append(sc.Statements, st)
			sc.Class = maxClass(sc.Class, st.Class)
			sc.Flags |= st.Flags
		}
	}
	return sc
}

// token is one lexed token of a batch, as classification reads it.
type token struct {
	kind sqltext.Kind
	// word is a word's text upper-cased; a quoted identifier's text unquoted;
	// a punctuation rune itself. Empty for a literal.
	word string
	// raw is the token's text as written.
	raw   string
	line  int // 1-based script line
	depth int // parenthesis depth before the token
}

func (t token) is(w string) bool { return t.kind == sqltext.KindWord && t.word == w }

func (t token) punct(r string) bool { return t.kind == sqltext.KindPunct && t.word == r }

// lexBatch lexes text into tokens (comments and newlines dropped) and returns
// the indices that begin a statement: the first token, every token after a
// ';', and every token sqltext.StatementCutter cuts at. ';' itself is not kept.
func lexBatch(text string, startLine int) ([]token, []int) {
	buf := []rune(text)
	var toks []token
	starts := map[int]bool{0: true}
	cutter := sqltext.NewStatementCutter(func(pos int) { starts[pos] = true })
	line, depth := startLine+1, 0
	var st sqltext.State
	for i := 0; ; {
		t, next := sqltext.Next(buf, i, len(buf), st)
		if t.Kind == sqltext.KindEnd {
			break
		}
		st, i = next, t.End
		raw := string(buf[t.Start:t.End])
		tokLine := line
		line += strings.Count(raw, "\n")
		switch t.Kind {
		case sqltext.KindNewline, sqltext.KindComment:
			continue
		case sqltext.KindPunct:
			if raw == ";" {
				cutter.Feed(buf, t, len(toks))
				starts[len(toks)] = true
				continue
			}
		}
		tk := token{kind: t.Kind, raw: raw, line: tokLine, depth: depth}
		switch t.Kind {
		case sqltext.KindWord, sqltext.KindNumber:
			tk.word = strings.ToUpper(raw)
		case sqltext.KindQuotedIdent:
			tk.word = unquote(raw)
		case sqltext.KindPunct:
			tk.word = raw
			switch raw {
			case "(":
				depth++
			case ")":
				depth = max(depth-1, 0)
			}
		}
		cutter.Feed(buf, t, len(toks))
		toks = append(toks, tk)
	}
	var cuts []int
	for i := range len(toks) {
		if starts[i] {
			cuts = append(cuts, i)
		}
	}
	return toks, cuts
}

// unquote strips a [bracketed] or "quoted" identifier's delimiters and undoubles
// its closing one; an unterminated one keeps what is there.
func unquote(raw string) string {
	closer := "]"
	if raw[0] == '"' {
		closer = `"`
	}
	s := raw[1:]
	s = strings.TrimSuffix(s, closer)
	return strings.ReplaceAll(s, closer+closer, closer)
}

// classifyBatch classifies one GO batch's statements.
func classifyBatch(text string, startLine int) []Statement {
	toks, cuts := lexBatch(text, startLine)
	var out []Statement
	for ci, from := range cuts {
		to := len(toks)
		if ci+1 < len(cuts) {
			to = cuts[ci+1]
		}
		if isModule(toks[from:]) {
			// CREATE/ALTER PROCEDURE, FUNCTION, TRIGGER or VIEW is alone in its batch:
			// everything after its header is its body, which runs when the module is
			// called, not now. One ddl statement, no flags from the body.
			out = append(out, moduleStatement(toks[from:]))
			break
		}
		if st, ok := classifyStatement(toks[from:to], ci == 0); ok {
			out = append(out, st)
		}
	}
	return out
}

// isModule reports whether toks begins CREATE [OR ALTER] or ALTER of a module.
func isModule(toks []token) bool {
	verb, obj := createAlterObject(toks)
	return verb != "DROP" && moduleTypes[obj]
}

// createAlterObject returns the verb (CREATE, ALTER, DROP) and the object type
// word after it, OR ALTER skipped; "" when toks does not start with one.
func createAlterObject(toks []token) (verb, obj string) {
	if len(toks) == 0 || toks[0].kind != sqltext.KindWord {
		return "", ""
	}
	verb = toks[0].word
	if verb != "CREATE" && verb != "ALTER" && verb != "DROP" {
		return "", ""
	}
	i := 1
	if verb == "CREATE" && i+1 < len(toks) && toks[i].is("OR") && toks[i+1].is("ALTER") {
		i += 2
	}
	// CREATE UNIQUE CLUSTERED INDEX, CREATE NONCLUSTERED COLUMNSTORE INDEX, …
	for i < len(toks) && indexModifiers[toks[i].word] && toks[i].kind == sqltext.KindWord {
		i++
	}
	if i < len(toks) && toks[i].kind == sqltext.KindWord {
		obj = toks[i].word
	}
	return verb, obj
}

func moduleStatement(toks []token) Statement {
	verb, obj := createAlterObject(toks)
	if obj == "PROC" {
		obj = "PROCEDURE"
	}
	st := Statement{Line: toks[0].line, Class: spec.ClassDDL, Leader: verb + " " + obj}
	i := 1
	for i < len(toks) && !toks[i].is(obj) && !(obj == "PROCEDURE" && toks[i].is("PROC")) {
		i++
	}
	st.Object = objectName(toks, i+1)
	// A server-scoped DDL trigger: CREATE TRIGGER t ON ALL SERVER.
	if obj == "TRIGGER" {
		for j := i + 1; j+2 < len(toks) && !toks[j].is("AS"); j++ {
			if toks[j].is("ON") && toks[j+1].is("ALL") && toks[j+2].is("SERVER") {
				st.Class, st.Reason = spec.ClassAdmin, "a server-scoped trigger"
			}
		}
	}
	return st
}

// objectName reads the possibly dotted name starting at toks[i], as written;
// "" when there is none.
func objectName(toks []token, i int) string {
	var b strings.Builder
	for ; i < len(toks); i++ {
		t := toks[i]
		switch {
		case t.kind == sqltext.KindWord || t.kind == sqltext.KindQuotedIdent:
			if b.Len() > 0 && !strings.HasSuffix(b.String(), ".") {
				return b.String()
			}
			b.WriteString(t.raw)
		case t.punct("."):
			b.WriteString(".")
		default:
			return b.String()
		}
	}
	return b.String()
}

// nameParts reads the possibly dotted name starting at toks[i], unquoted and
// lower-cased, and the index after it. An omitted part (master..sp_who) is "".
func nameParts(toks []token, i int) ([]string, int) {
	var parts []string
	expectName := true
	for ; i < len(toks); i++ {
		t := toks[i]
		switch {
		case expectName && (t.kind == sqltext.KindWord && !strings.HasPrefix(t.raw, "@") || t.kind == sqltext.KindQuotedIdent):
			parts = append(parts, strings.ToLower(t.word))
			expectName = false
		case t.punct("."):
			if expectName {
				parts = append(parts, "")
			}
			expectName = true
		default:
			return parts, i
		}
	}
	return parts, i
}

// classifyStatement classifies one statement. first says it opens its batch,
// the one place a procedure may be called without EXEC. ok is false for a
// label alone ("retry:"), which is no statement.
func classifyStatement(toks []token, first bool) (st Statement, ok bool) {
	if len(toks) >= 2 && toks[0].kind == sqltext.KindWord && toks[1].punct(":") {
		if toks = toks[2:]; len(toks) == 0 {
			return Statement{}, false
		}
		first = false
	}
	st = Statement{Line: toks[0].line, Class: spec.ClassWrite}
	leaderRule(&st, toks, first)
	floor(&st, toks)
	return st, true
}

// raise lifts st to class c for reason, keeping the first reason that set the
// highest class.
func (st *Statement) raise(c spec.Class, reason string) {
	if rank[c] > rank[st.Class] {
		st.Class, st.Reason = c, reason
	}
}

// leaderRule sets st's class from its leading keywords (docs/cli.md § Safety).
func leaderRule(st *Statement, toks []token, first bool) {
	t := toks[0]
	next := func(i int) token {
		if i < len(toks) {
			return toks[i]
		}
		return token{}
	}
	if t.kind != sqltext.KindWord {
		if t.kind == sqltext.KindQuotedIdent && first {
			execProc(st, toks, 0) // [dbo].[usp_x] 1, 2 — an implicit EXEC
			return
		}
		if t.punct("(") {
			st.Class, st.Leader = spec.ClassRead, "(" // (SELECT …) UNION (SELECT …)
			return
		}
		st.Leader, st.Reason = t.raw, "not a statement the classifier knows"
		return
	}
	st.Leader = t.word
	switch w := t.word; w {
	case "SELECT":
		st.Class = spec.ClassRead
		selectInto(st, toks)
	case "WITH":
		// A CTE (or WITH XMLNAMESPACES) is classified by its main statement: the
		// floor raises a DML one; a SELECT … INTO creates a table.
		st.Class = spec.ClassRead
		for i, tk := range toks {
			if tk.depth == 0 && tk.kind == sqltext.KindWord && sqltext.IsDMLLeader(tk.word) {
				if tk.word == "SELECT" {
					selectInto(st, toks[i:])
				}
				break
			}
		}
	case "INSERT", "UPDATE", "DELETE", "MERGE":
		st.Class = spec.ClassWrite
		st.Object = dmlTarget(toks)
		if w == "UPDATE" && next(1).is("STATISTICS") {
			st.Class, st.Leader, st.Object = spec.ClassDDL, "UPDATE STATISTICS", objectName(toks, 2)
		}
		for _, tk := range toks {
			if tk.depth == 0 && tk.is("OUTPUT") {
				st.Flags |= FlagOutput
			}
		}
	case "BULK":
		st.Class, st.Leader, st.Object = spec.ClassWrite, "BULK INSERT", objectName(toks, 2)
	case "CREATE", "ALTER", "DROP":
		createAlterDrop(st, toks)
	case "TRUNCATE":
		st.Class, st.Leader, st.Object = spec.ClassDDL, "TRUNCATE TABLE", objectName(toks, 2)
	case "DISABLE", "ENABLE":
		st.Class, st.Leader = spec.ClassDDL, w+" TRIGGER"
	case "ADD":
		st.Class, st.Leader = spec.ClassDDL, "ADD SIGNATURE" // ADD [COUNTER]SIGNATURE
	case "GRANT", "DENY", "REVOKE":
		st.Class = spec.ClassAdmin
	case "BACKUP", "RESTORE":
		st.Class, st.Flags = spec.ClassAdmin, FlagNotInTransaction
		if n := next(1); n.kind == sqltext.KindWord {
			st.Leader += " " + n.word
			st.Object = objectName(toks, 2)
		}
	case "RECONFIGURE":
		st.Class, st.Flags = spec.ClassAdmin, FlagNotInTransaction
	case "KILL", "SHUTDOWN", "CHECKPOINT", "SETUSER":
		st.Class = spec.ClassAdmin
	case "DBCC":
		dbcc(st, toks)
	case "EXEC", "EXECUTE":
		st.Leader = "EXEC"
		if next(1).is("AS") {
			st.Class, st.Leader = spec.ClassRead, "EXECUTE AS" // a security context switch
			return
		}
		execProc(st, toks, 1)
	case "BEGIN":
		switch n := next(1); {
		case n.is("TRAN") || n.is("TRANSACTION") || n.is("DISTRIBUTED"):
			st.Class, st.Leader, st.Flags = spec.ClassWrite, "BEGIN TRAN", FlagTranControl
		case n.is("DIALOG") || n.is("CONVERSATION"):
			st.Class, st.Leader = spec.ClassWrite, "BEGIN "+n.word
		default:
			st.Class = spec.ClassRead // a block, TRY, CATCH
		}
	case "COMMIT", "ROLLBACK", "SAVE":
		st.Class, st.Flags = spec.ClassWrite, FlagTranControl
	case "END":
		st.Class = spec.ClassRead
		if next(1).is("CONVERSATION") {
			st.Class, st.Leader = spec.ClassWrite, "END CONVERSATION"
		}
	case "SET":
		setStatement(st, toks)
	case "WRITETEXT", "UPDATETEXT", "RECEIVE", "SEND", "MOVE", "GET":
		st.Class = spec.ClassWrite
	default:
		if readLeaders[w] {
			st.Class = spec.ClassRead
			return
		}
		if first && !strings.HasPrefix(t.raw, "@") && !strings.HasPrefix(t.raw, "#") {
			execProc(st, toks, 0) // sp_who2 at a batch's start — an implicit EXEC
			return
		}
		st.Reason = "not a statement the classifier knows"
	}
}

// selectInto makes a SELECT with a top-level INTO ddl: it creates the table.
// toks starts at the SELECT.
func selectInto(st *Statement, toks []token) {
	for i, tk := range toks {
		if tk.depth == toks[0].depth && tk.is("INTO") {
			st.Class, st.Leader, st.Object = spec.ClassDDL, "SELECT INTO", objectName(toks, i+1)
			st.Reason = "SELECT … INTO creates a table"
			return
		}
	}
}

// dmlTarget is the object an INSERT/UPDATE/DELETE/MERGE names first, past
// TOP (n), INTO and FROM.
func dmlTarget(toks []token) string {
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
	return objectName(toks, i)
}

func createAlterDrop(st *Statement, toks []token) {
	verb, obj := createAlterObject(toks)
	st.Class, st.Leader = spec.ClassDDL, verb
	if obj == "" {
		return
	}
	st.Leader = verb + " " + obj
	i := 1
	for i < len(toks) && !toks[i].is(obj) {
		i++
	}
	// DATABASE SCOPED CONFIGURATION, AVAILABILITY GROUP, MASTER KEY
	typ := obj
	for ; i+1 < len(toks) && toks[i+1].kind == sqltext.KindWord && objectWords[typ+" "+toks[i+1].word]; i++ {
		typ += " " + toks[i+1].word
		st.Leader += " " + toks[i+1].word
	}
	if verb == "DROP" && i+2 < len(toks) && toks[i+1].is("IF") && toks[i+2].is("EXISTS") {
		i += 2
	}
	if !unnamedObjects[typ] {
		st.Object = objectName(toks, i+1)
	}
	if adminObjects[obj] {
		st.Class = spec.ClassAdmin
		st.Reason = verb + " " + obj + " is administration"
	}
	if notInTransactionObjects[obj] {
		st.Flags |= FlagNotInTransaction
	}
}

// dbcc classifies a DBCC command: the read allow-list, else admin.
func dbcc(st *Statement, toks []token) {
	st.Class = spec.ClassAdmin
	if len(toks) < 2 || toks[1].kind != sqltext.KindWord {
		return
	}
	cmd := toks[1].word
	st.Leader = "DBCC " + cmd
	if !dbccRead[cmd] {
		st.Reason = "DBCC " + cmd + " is not on the read allow-list"
		return
	}
	// DBCC SQLPERF('sys.dm_os_wait_stats', CLEAR) resets the server's counters.
	for _, tk := range toks[2:] {
		if tk.is("CLEAR") {
			st.Reason = "DBCC " + cmd + " … CLEAR resets server statistics"
			return
		}
	}
	st.Class = spec.ClassRead
}

// execProc classifies a procedure call whose target starts at toks[i] (after
// EXEC, or at an implicit EXEC's first token).
func execProc(st *Statement, toks []token, i int) {
	st.Leader = "EXEC"
	c, obj, reason, flags := execTarget(toks, i)
	st.Class, st.Object, st.Reason, st.Flags = c, obj, reason, st.Flags|flags
}

// execTarget classifies the call whose target starts at toks[i]: the name's
// allow-lists, or write and FlagDynamic when the target is not a name.
func execTarget(toks []token, i int) (c spec.Class, obj, reason string, flags Flag) {
	// EXEC @rc = dbo.p — the return status variable.
	if i+1 < len(toks) && strings.HasPrefix(toks[i].raw, "@") && toks[i+1].punct("=") {
		i += 2
	}
	if i >= len(toks) {
		return spec.ClassWrite, "", "a call the classifier cannot read", FlagDynamic
	}
	if t := toks[i]; t.punct("(") || t.kind == sqltext.KindString || strings.HasPrefix(t.raw, "@") {
		return spec.ClassWrite, "", "dynamic SQL is not classified", FlagDynamic
	}
	parts, _ := nameParts(toks, i)
	if len(parts) == 0 {
		return spec.ClassWrite, "", "a call the classifier cannot read", FlagDynamic
	}
	obj = objectName(toks, i)
	proc := parts[len(parts)-1]
	dbName := ""
	if len(parts) >= 3 {
		dbName = parts[len(parts)-3]
	}
	switch {
	case proc == "sp_executesql":
		return spec.ClassWrite, obj, "dynamic SQL is not classified", FlagDynamic
	case readProcs[proc]:
		return spec.ClassRead, obj, "", 0
	case adminProcs[proc] || strings.HasPrefix(proc, "xp_") || strings.HasPrefix(proc, "sp_trace_") ||
		strings.HasPrefix(proc, "sp_oa") || dbName == "msdb" && strings.HasPrefix(proc, "sp_"):
		return spec.ClassAdmin, obj, "procedure " + obj + " is administration", FlagExec
	}
	return spec.ClassWrite, obj, "procedure " + obj + " is not on the read allow-list", FlagExec
}

// setStatement classifies SET: session options are read, with the ones the
// safety checks care about flagged.
func setStatement(st *Statement, toks []token) {
	st.Class = spec.ClassRead
	if len(toks) < 2 || toks[1].kind != sqltext.KindWord || strings.HasPrefix(toks[1].raw, "@") {
		return // SET @x = …: the floor reads the expression
	}
	// Each option list ends at its ON or OFF: SET NOCOUNT, XACT_ABORT ON. A
	// statement merged after it (SET NOCOUNT ON DISABLE TRIGGER …) does not hide
	// the option.
	var opts []string
	for _, tk := range toks[1:] {
		if tk.kind != sqltext.KindWord {
			continue
		}
		if tk.word != "ON" && tk.word != "OFF" {
			opts = append(opts, tk.word)
			continue
		}
		if tk.word == "ON" {
			for _, w := range opts {
				switch {
				case w == "NOCOUNT":
					st.Flags |= FlagNoCount
				case w == "PARSEONLY" || w == "NOEXEC" || strings.HasPrefix(w, "SHOWPLAN_"):
					st.Flags |= FlagModeOption
				case w == "IMPLICIT_TRANSACTIONS":
					st.Class, st.Flags = spec.ClassWrite, st.Flags|FlagTranControl
					st.Reason = "SET IMPLICIT_TRANSACTIONS ON opens transactions"
				}
			}
		}
		opts = opts[:0]
	}
}

// floor raises st to the class of any keyword in it (docs/cli.md § Safety), so a
// statement the splitter merged into this one still counts, and classifies every
// EXEC inside it (INSERT … EXEC). Read contexts are exempt: FOR UPDATE, a
// trigger's UPDATE(col), a MERGE join or union hint, FETCH … INTO. (A foreign
// key's ON DELETE/UPDATE needs none: it only stands in DDL, which outranks.)
func floor(st *Statement, toks []token) {
	fetch := toks[0].is("FETCH")
	for i, tk := range toks {
		if tk.kind != sqltext.KindWord {
			continue
		}
		prev, next := token{}, token{}
		if i > 0 {
			prev = toks[i-1]
		}
		if i+1 < len(toks) {
			next = toks[i+1]
		}
		w := tk.word
		at := " (line " + strconv.Itoa(tk.line) + ")"
		switch {
		case w == "EXEC" || w == "EXECUTE":
			if next.is("AS") {
				continue
			}
			c, obj, reason, flags := execTarget(toks, i+1)
			st.Flags |= flags
			if reason == "" {
				reason = "EXEC " + obj
			}
			st.raise(c, reason+at)
			continue
		case w == "OPENQUERY" || w == "OPENROWSET" || w == "OPENDATASOURCE":
			st.Flags |= FlagDynamic
			st.raise(spec.ClassWrite, w+" passes a query through unclassified"+at)
			continue
		case w == "SP_EXECUTESQL":
			st.Flags |= FlagDynamic
			st.raise(spec.ClassWrite, "dynamic SQL is not classified"+at)
			continue
		}
		c, ok := floorWords[w]
		if !ok {
			continue
		}
		switch {
		case prev.is("FOR") && w == "UPDATE": // a cursor's FOR UPDATE [OF]
			continue
		case w == "UPDATE" && next.punct("("): // a trigger's IF UPDATE(col)
			continue
		case w == "MERGE" && (next.is("JOIN") || next.is("UNION") || prev.punct("(")): // join hints
			continue
		case fetch && w == "INTO": // FETCH NEXT FROM c INTO @v
			continue
		case w == "CREATE" || w == "ALTER" || w == "DROP":
			// A CREATE/ALTER/DROP the splitter did not cut at: an administration
			// object makes it admin, as the leader rule would.
			if obj := next.word; next.kind == sqltext.KindWord && adminObjects[obj] {
				c = spec.ClassAdmin
				st.raise(c, "keyword "+w+" "+obj+at)
				continue
			}
		}
		st.raise(c, "keyword "+w+at)
	}
}
