// Package safety is the command line's guardrail over T-SQL (docs/cli.md
// § Safety): Classify puts every statement of a script into an operation class
// — read, write, ddl or admin — and flags what the safety checks refuse or
// cannot see through. It is a guardrail, not the security boundary; that is
// the login's permissions.
//
// It lexes with gossms's one lexer, internal/sqltext, and splits statements by
// its StatementCutter, so a keyword inside a comment, literal or quoted
// identifier means nothing here either, and a statement boundary is where
// Ctrl+Enter's is.
package safety

import (
	"strconv"
	"strings"

	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/sqltext"
)

// Flag marks something about a statement that a safety check reads beyond its
// class.
type Flag uint16

const (
	// FlagTranControl: BEGIN TRAN, COMMIT, ROLLBACK, SAVE TRAN, SET
	// IMPLICIT_TRANSACTIONS ON or SET ANSI_DEFAULTS ON (which sets it) —
	// refused under --transaction rollback.
	FlagTranControl Flag = 1 << iota
	// FlagNotInTransaction: a statement SQL Server refuses inside a user
	// transaction (ALTER DATABASE, BACKUP, KILL, a resumable index operation,
	// …; notInTransaction).
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
	// FlagSequence: NEXT VALUE FOR, which advances a sequence outside any
	// transaction — a rollback does not undo it (docs/cli.md § Transactions).
	FlagSequence
	// FlagUncounted: RECEIVE, WRITETEXT, UPDATETEXT, or TRUNCATE TABLE, DROP
	// TABLE, ALTER TABLE … SWITCH, ALTER TABLE … DROP COLUMN or ALTER TABLE …
	// ALTER COLUMN of a permanent table (uncountedDDL), whose
	// changes the server's row counts miss (live, 17): RECEIVE's count arrives
	// after its result set and so reads like a SELECT's, the others report none.
	FlagUncounted
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
	{FlagSequence, "sequence-advance"},
	{FlagUncounted, "uncounted"},
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

	// Writes is the statement's table writes, nested ones included, for
	// --max-rows-affected's check of their targets.
	Writes []Write
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

// classifyBatch classifies one GO batch's statements.
func classifyBatch(text string, startLine int) []Statement {
	toks, cuts := lexBatch(text, startLine)
	var out []Statement
	// Inside CREATE SCHEMA, CREATE VIEW is a schema element: its body ends at the
	// next element (CREATE TABLE/VIEW, GRANT, REVOKE, DENY), and every element
	// runs now, so each cut is classified on its own (a GRANT after the view was
	// hidden as the view's body). The server refuses any other module there.
	inSchema := false
	if len(cuts) > 0 {
		verb, obj := createAlterObject(toks[cuts[0]:])
		inSchema = verb == "CREATE" && obj == "SCHEMA"
	}
	for ci, from := range cuts {
		to := len(toks)
		if ci+1 < len(cuts) {
			to = cuts[ci+1]
		}
		if !inSchema && isModule(toks[from:]) {
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
	if obj == "TRIGGER" && onAllServer(toks, i+1) {
		st.Class, st.Reason = spec.ClassAdmin, serverTriggerReason
	}
	return st
}

// serverTriggerReason is the reason a statement on a server-scoped trigger
// gives for its admin class.
const serverTriggerReason = "a server-scoped trigger"

// onAllServer reports whether the trigger name(s) starting at toks[from] are
// followed by ON ALL SERVER: CREATE TRIGGER t ON ALL SERVER, DROP TRIGGER
// [IF EXISTS] a, b ON ALL SERVER, DISABLE TRIGGER ALL ON ALL SERVER. Such a
// trigger is often an audit or a LOGON access control, so creating, dropping
// or switching one off is administration, not ddl.
func onAllServer(toks []token, from int) bool {
	for j := from; j < len(toks); j++ {
		switch t := toks[j]; {
		case t.is("ON"):
			return j+2 < len(toks) && toks[j+1].is("ALL") && toks[j+2].is("SERVER")
		case t.kind == sqltext.KindWord || t.kind == sqltext.KindQuotedIdent || t.punct(".") || t.punct(","):
		default:
			return false
		}
	}
	return false
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
		if next(1).is("TRIGGER") && onAllServer(toks, 2) {
			st.Class, st.Reason = spec.ClassAdmin, serverTriggerReason
		}
	case "BACKUP", "RESTORE":
		st.Class = spec.ClassAdmin
		if n := next(1); n.kind == sqltext.KindWord {
			st.Leader += " " + n.word
			st.Object = objectName(toks, 2)
		}
	case "RECONFIGURE":
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
		default:
			st.Class = spec.ClassRead // a block, TRY, CATCH
			verbLeader(st, toks)      // BEGIN DIALOG, BEGIN CONVERSATION TIMER
		}
	case "COMMIT", "ROLLBACK", "SAVE":
		st.Class, st.Flags = spec.ClassWrite, FlagTranControl
	case "END":
		st.Class = spec.ClassRead
		verbLeader(st, toks) // END CONVERSATION
	case "SET":
		setStatement(st, toks)
	default:
		if verbLeader(st, toks) { // GRANT, KILL, SEND ON CONVERSATION, …
			// ADD SIGNATURE TO p, ADD SENSITIVITY CLASSIFICATION TO s.t.c, COPY INTO t
			switch n := len(strings.Fields(st.Leader)); {
			case w == "ADD" && next(n).is("TO"):
				st.Object = objectName(toks, n+1)
			case w == "COPY":
				st.Object = objectName(toks, n)
			}
			return
		}
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

// verbLeader gives st the class and leader of the verbs entry toks opens with,
// reporting whether there is one.
func verbLeader(st *Statement, toks []token) bool {
	v, ok := verbAt(toks, 0)
	if ok {
		st.Class, st.Leader = v.class, v.name
	}
	return ok
}

// verbAt returns the longest verbs entry whose words start at toks[i].
func verbAt(toks []token, i int) (verbForm, bool) {
	var best verbForm
	found := false
	if toks[i].kind != sqltext.KindWord {
		return best, false
	}
forms:
	for _, v := range verbsByFirst[toks[i].word] {
		if i+len(v.words) > len(toks) || found && len(v.words) <= len(best.words) {
			continue
		}
		for k, w := range v.words {
			if !toks[i+k].is(w) {
				continue forms
			}
		}
		best, found = v, true
	}
	return best, found
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
func dmlTarget(toks []token) string { return objectName(toks, dmlTargetIndex(toks)) }

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
	if obj == "TRIGGER" && onAllServer(toks, i+1) {
		st.Class, st.Reason = spec.ClassAdmin, serverTriggerReason
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
				case w == "ANSI_DEFAULTS":
					// ANSI_DEFAULTS ON sets IMPLICIT_TRANSACTIONS ON with the
					// ANSI options: left read, a procedure's COMMIT would let
					// the next statement open a transaction the wrapper then
					// rolls back as its own, reporting rolled-back over
					// committed rows. OFF turns it off, so it stays read.
					st.Class, st.Flags = spec.ClassWrite, st.Flags|FlagTranControl
					st.Reason = "SET ANSI_DEFAULTS ON turns on IMPLICIT_TRANSACTIONS, which opens transactions"
				}
			}
		}
		opts = opts[:0]
	}
}

// raiserrorLogs reports whether the RAISERROR at toks[i] has the LOG option:
// past its parenthesised arguments, WITH and a comma-separated option list
// naming LOG. It writes the SQL Server error log and the Windows application
// log, as xp_logevent (admin) does, and needs ALTER TRACE or sysadmin.
func raiserrorLogs(toks []token, i int) bool {
	d := toks[i].depth
	j := i + 1
	if j >= len(toks) || !toks[j].punct("(") {
		return false
	}
	// Past the arguments: a token's depth is the one before it, so the
	// closing parenthesis is still deeper than d.
	for j++; j < len(toks) && toks[j].depth > d; j++ {
	}
	if j >= len(toks) || !toks[j].is("WITH") {
		return false
	}
	for j++; j < len(toks) && toks[j].depth == d; j += 2 {
		if toks[j].is("LOG") {
			return true
		}
		if j+1 >= len(toks) || !toks[j+1].punct(",") {
			return false
		}
	}
	return false
}

// floor raises st to the class of any verb in it (verbs; docs/cli.md § Safety),
// so a statement the splitter merged into this one, or nested behind IF, ELSE,
// WHILE or BEGIN, still counts, and classifies every
// EXEC inside it (INSERT … EXEC). NEXT VALUE FOR is write: a SELECT of it
// advances the sequence, for good. The floor is the three-word phrase, so a
// column named next stays read. Read contexts are exempt: FOR UPDATE, a
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
		if notInTransaction(toks, i) {
			st.Flags |= FlagNotInTransaction
		}
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
		case w == "RAISERROR" && raiserrorLogs(toks, i):
			st.raise(spec.ClassAdmin, "RAISERROR … WITH LOG writes the server error log"+at)
			continue
		case w == "NEXT" && next.is("VALUE") && i+2 < len(toks) && toks[i+2].is("FOR"):
			st.Flags |= FlagSequence
			st.raise(spec.ClassWrite, "NEXT VALUE FOR advances a sequence, which no rollback undoes"+at)
			continue
		}
		v, ok := verbAt(toks, i)
		if !ok {
			continue
		}
		c := v.class
		switch {
		case prev.is("FOR") && w == "UPDATE": // a cursor's FOR UPDATE [OF]
			continue
		case w == "UPDATE" && next.punct("("): // a trigger's IF UPDATE(col)
			continue
		case w == "MERGE" && (next.is("JOIN") || next.is("UNION") || prev.punct("(")): // join hints
			continue
		case fetch && w == "INTO": // FETCH NEXT FROM c INTO @v
			continue
		case w == "UPDATE" && next.is("STATISTICS"):
			// UPDATE STATISTICS the splitter did not cut at is ddl, as the leader
			// rule makes it: it changes optimizer state, not rows.
			st.raise(spec.ClassDDL, "keyword UPDATE STATISTICS"+at)
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
		// DROP/DISABLE/ENABLE TRIGGER … ON ALL SERVER merged or nested behind IF
		// is admin, as the leader rules make it.
		if (w == "DROP" || w == "DISABLE" || w == "ENABLE") && next.is("TRIGGER") && onAllServer(toks, i+2) {
			st.raise(spec.ClassAdmin, serverTriggerReason+at)
			continue
		}
		if writeVerbs[v.name] {
			if wr, ok := readWrite(toks, i); ok {
				st.Writes = append(st.Writes, wr)
			}
		}
		if uncountedVerb(toks, i, v.name) || uncountedDDL(toks, i) {
			st.Flags |= FlagUncounted
		}
		st.raise(c, "keyword "+v.name+at)
	}
}

// uncountedVerb reports whether toks[i], the verb name, is RECEIVE, WRITETEXT
// or UPDATETEXT used as one (FlagUncounted), not a column of that name. The
// floor raises a bare column named receive to write regardless (fail-safe);
// the flag would also make --max-rows-affected refuse a plain counted
// UPDATE t SET receive = 1, so it takes the verb's shape:
//
//   - WRITETEXT/UPDATETEXT [BULK] table.column …: a dotted name of two or
//     more parts follows. A column of that name is followed by FROM, a
//     comma, an operator — never a dotted name.
//   - RECEIVE [TOP (n)] columns FROM queue: TOP, *, or a word or quoted
//     name follows, and the word is not a select list's (after SELECT, a
//     comma, SET, BY, DISTINCT or a dot). A word that only continues an
//     expression (FROM, AS, IS, AND …) is a column's. What remains
//     ambiguous (receive * 2) counts as the verb, the safe side.
func uncountedVerb(toks []token, i int, name string) bool {
	at := func(k int) token {
		if k >= 0 && k < len(toks) {
			return toks[k]
		}
		return token{}
	}
	switch name {
	case "WRITETEXT", "UPDATETEXT":
		j := i + 1
		if at(j).is("BULK") {
			j++
		}
		n, _, ok := readName(toks, j)
		return ok && len(n.parts) >= 2
	case "RECEIVE":
		prev, next := at(i-1), at(i+1)
		if prev.punct(".") || prev.punct(",") || prev.is("SELECT") || prev.is("SET") ||
			prev.is("BY") || prev.is("DISTINCT") {
			return false
		}
		switch {
		case next.is("TOP") || next.punct("*") || next.kind == sqltext.KindQuotedIdent:
			return true
		case next.kind == sqltext.KindWord:
			return !receiveColumnFollowers[next.word]
		}
	}
	return false
}

// uncountedDDL reports whether toks[i] begins TRUNCATE TABLE, DROP TABLE,
// ALTER TABLE … SWITCH, ALTER TABLE … DROP COLUMN or ALTER TABLE … ALTER
// COLUMN of a permanent table (FlagUncounted): each removes or rewrites rows
// and reports no count, so a --max-rows-affected limit of any size passed
// them and committed (a 1000-row TRUNCATE under a limit of 10). A temporary
// table is exempt, as Write.Local exempts it from the trigger check: what it
// held, the script put there.
func uncountedDDL(toks []token, i int) bool {
	verb, obj := createAlterObject(toks[i:])
	if toks[i].is("TRUNCATE") {
		verb, obj = "TRUNCATE", ""
		if i+1 < len(toks) && toks[i+1].is("TABLE") {
			obj = "TABLE"
		}
	}
	if obj != "TABLE" {
		return false
	}
	permanent := func(j int) (bool, int) {
		parts, next := nameParts(toks, j)
		return len(parts) > 0 && !strings.HasPrefix(parts[len(parts)-1], "#"), next
	}
	j := i + 2
	switch verb {
	case "TRUNCATE":
		p, _ := permanent(j)
		return p
	case "DROP":
		// DROP TABLE [IF EXISTS] a, #b, c
		if j+1 < len(toks) && toks[j].is("IF") && toks[j+1].is("EXISTS") {
			j += 2
		}
		for {
			p, next := permanent(j)
			if p {
				return true
			}
			if next >= len(toks) || !toks[next].punct(",") || next == j {
				return false
			}
			j = next + 1
		}
	case "ALTER":
		p, next := permanent(j)
		if !p || next >= len(toks) {
			return false
		}
		switch {
		case toks[next].is("SWITCH"): // ALTER TABLE t SWITCH [PARTITION n] TO t2 …
			return true
		case toks[next].is("ALTER"):
			// ALTER TABLE t ALTER COLUMN c <type> converts every row, and the
			// conversion rounds or substitutes silently (decimal(10,4) → (10,2),
			// datetime2(7) → (0), nvarchar → varchar's ?). ALTER COLUMN … ADD/DROP
			// ROWGUIDCOL, PERSISTED, SPARSE, MASKED … change no data but are
			// flagged too: one fail-safe rule rather than a branch per option.
			return next+1 < len(toks) && toks[next+1].is("COLUMN")
		case toks[next].is("DROP"):
			return dropsColumn(toks, next+1)
		}
	}
	return false
}

// dropsColumn reports whether the ALTER TABLE … DROP list starting at toks[j]
// drops a column: its items are [CONSTRAINT] [IF EXISTS] name [WITH (…)],
// COLUMN [IF EXISTS] name [, name …] or PERIOD FOR SYSTEM_TIME, so a column
// can follow a constraint (DROP CONSTRAINT c, COLUMN d). A bare DROP c is a
// constraint: DROP defaults to CONSTRAINT.
func dropsColumn(toks []token, j int) bool {
	for j < len(toks) {
		switch {
		case toks[j].is("COLUMN"):
			return true
		case toks[j].is("PERIOD"):
			return false
		case toks[j].is("CONSTRAINT"):
			j++
		}
		if j+1 < len(toks) && toks[j].is("IF") && toks[j+1].is("EXISTS") {
			j += 2
		}
		_, next := nameParts(toks, j)
		if next == j {
			return false
		}
		j = next
		if j+1 < len(toks) && toks[j].is("WITH") && toks[j+1].punct("(") {
			d := toks[j+1].depth
			for j += 2; j < len(toks) && !(toks[j].punct(")") && toks[j].depth == d+1); j++ {
			}
			j++
		}
		if j >= len(toks) || !toks[j].punct(",") {
			return false
		}
		j++
	}
	return false
}

// receiveColumnFollowers are words that follow a column named receive but
// never the RECEIVE verb, whose next token is TOP, * or a column.
var receiveColumnFollowers = map[string]bool{
	"FROM": true, "AS": true, "IS": true, "IN": true, "LIKE": true, "BETWEEN": true,
	"NOT": true, "AND": true, "OR": true, "COLLATE": true, "ASC": true, "DESC": true,
	"WHEN": true, "THEN": true, "ELSE": true, "END": true, "WHERE": true, "GROUP": true,
	"ORDER": true, "HAVING": true, "UNION": true, "EXCEPT": true, "INTERSECT": true,
	"FOR": true, "OPTION": true, "INTO": true, "ON": true, "JOIN": true,
}

// notInTransaction reports whether toks[i] begins a statement SQL Server
// refuses inside a user transaction (docs/cli.md § Transactions): BACKUP,
// RESTORE, RECONFIGURE, KILL (Msg 6115), DBCC SHRINKFILE/SHRINKDATABASE (Msg
// 8920), CREATE/ALTER/DROP of a notInTransactionObjects object or an EVENT
// SESSION (Msg 574), a resumable index operation — any RESUMABLE = ON (CREATE
// INDEX, ALTER INDEX … REBUILD, ALTER TABLE … ADD CONSTRAINT) and ALTER INDEX
// … RESUME, PAUSE or ABORT (Msg 574). floor asks at every word, so a form the
// splitter merged into another statement, or nested behind IF, flags as one
// that leads does. CHECKPOINT, DBCC CHECKDB and CREATE EVENT NOTIFICATION run
// in a transaction (all three live, 17).
func notInTransaction(toks []token, i int) bool {
	at := func(j int) token {
		if j < len(toks) {
			return toks[j]
		}
		return token{}
	}
	switch toks[i].word {
	case "BACKUP", "RESTORE", "RECONFIGURE", "KILL":
		return true
	case "DBCC":
		return at(i+1).is("SHRINKFILE") || at(i+1).is("SHRINKDATABASE")
	case "RESUMABLE":
		return at(i+1).punct("=") && at(i+2).is("ON")
	case "CREATE", "ALTER", "DROP":
		_, obj := createAlterObject(toks[i:])
		switch {
		case notInTransactionObjects[obj]:
			return true
		case obj == "EVENT":
			return at(i + 2).is("SESSION")
		case obj == "INDEX" && toks[i].word == "ALTER":
			// ALTER INDEX {ix | ALL} ON t {RESUME | PAUSE | ABORT}
			_, j := nameParts(toks, i+2)
			if !at(j).is("ON") {
				return false
			}
			_, j = nameParts(toks, j+1)
			a := at(j)
			return a.is("RESUME") || a.is("PAUSE") || a.is("ABORT")
		}
	}
	return false
}
