package sqltext

import (
	"strings"
	"unicode"
)

// ---------------------------------------------------------------------------
// Statement boundaries: which statement a position in a script belongs to
// ---------------------------------------------------------------------------

// dmlLeaders are the statements IntelliSense scopes completion to, and the
// leaders that can be a CTE's main statement or follow AS/THEN as a trigger
// body or MERGE action.
var dmlLeaders = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true,
}

// dmlEnders are leaders that never occur inside a DML statement's text, so one
// ends the DML statement before it. EXEC is not one: INSERT ... EXEC continues
// the INSERT.
var dmlEnders = map[string]bool{
	"DECLARE": true, "CREATE": true, "ALTER": true, "DROP": true, "TRUNCATE": true,
}

// IsDMLLeader reports whether word, upper-cased, is a DML statement verb:
// SELECT, INSERT, UPDATE, DELETE or MERGE.
func IsDMLLeader(word string) bool { return dmlLeaders[word] }

// EndsDML reports whether word, upper-cased, is a statement verb that cannot
// occur inside a DML statement (DECLARE, CREATE, ALTER, DROP, TRUNCATE), so a
// top-level one ends the DML statement before it.
//
// IntelliSense (internal/tui/sqlparse) splits a batch into statements with
// these two sets and WITH alone; StatementAt splits on every statementLeaders
// verb, a superset of them.
func EndsDML(word string) bool { return dmlEnders[word] }

// statementLeaders are the top-level keywords that begin a statement unless a
// stmtSplitter rule makes them a continuation: WITH, dmlLeaders, dmlEnders,
// and every other statement verb whose running by accident does harm.
var statementLeaders = func() map[string]bool {
	m := map[string]bool{
		"WITH": true, "SET": true,
		"EXEC": true, "EXECUTE": true, "PRINT": true, "RAISERROR": true, "IF": true, "WHILE": true,
		"BEGIN": true, "COMMIT": true, "ROLLBACK": true, "SAVE": true, "USE": true,
		"GRANT": true, "DENY": true, "REVOKE": true, "BACKUP": true, "RESTORE": true, "DBCC": true,
		"KILL": true, "SHUTDOWN": true, "RECONFIGURE": true, "CHECKPOINT": true, "WAITFOR": true,
		"OPEN": true, "CLOSE": true, "FETCH": true, "DEALLOCATE": true, "BULK": true,
	}
	for w := range dmlLeaders {
		m[w] = true
	}
	for w := range dmlEnders {
		m[w] = true
	}
	return m
}()

// continuesAfter are tokens no statement ends with, so a leader right after
// one continues the statement: CREATE OR ALTER, CREATE VIEW ... AS SELECT,
// MERGE ... THEN UPDATE, DECLARE ... CURSOR FOR SELECT, FOR UPDATE OF,
// AFTER INSERT, UPDATE / INSTEAD OF DELETE, BULK INSERT, INNER MERGE JOIN, and
// a reserved word used as a dotted name part. ON is not one: SET NOCOUNT ON
// ends a statement.
var continuesAfter = map[string]bool{
	"OR": true, "AS": true, "THEN": true, "FOR": true, "AFTER": true, "OF": true, "BULK": true,
	"INNER": true, "OUTER": true, "LEFT": true, "RIGHT": true, "FULL": true,
	",": true, ".": true,
}

// notBefore names, per leader, the next tokens that make it no statement:
// UPDATE(col) in a trigger, ON DELETE CASCADE / ON UPDATE SET NULL in a
// foreign key, MERGE ... THEN UPDATE SET, ALTER PARTITION FUNCTION ... MERGE
// RANGE.
var notBefore = map[string]map[string]bool{
	"UPDATE": {"(": true, "CASCADE": true, "NO": true, "SET": true},
	"DELETE": {"CASCADE": true, "NO": true, "SET": true},
	"MERGE":  {"RANGE": true, "JOIN": true},
}

// objectTypes are the words after ALTER or DROP that make it a statement of
// its own inside an ALTER statement; anything else (ALTER COLUMN, DROP
// CONSTRAINT, DROP MEMBER, DROP EVENT, ...) is the ALTER's own clause. Also
// the words that DROP ... IF EXISTS puts IF after.
var objectTypes = map[string]bool{
	"TABLE": true, "VIEW": true, "PROCEDURE": true, "PROC": true, "FUNCTION": true,
	"TRIGGER": true, "INDEX": true, "DATABASE": true, "LOGIN": true, "USER": true,
	"ROLE": true, "SCHEMA": true, "TYPE": true, "SYNONYM": true, "SEQUENCE": true,
	"STATISTICS": true, "ASSEMBLY": true, "CERTIFICATE": true, "CREDENTIAL": true,
	"ENDPOINT": true, "AVAILABILITY": true, "PARTITION": true, "FULLTEXT": true,
	"XML": true, "DEFAULT": true, "RULE": true, "QUEUE": true, "SERVICE": true,
	"CONTRACT": true, "MESSAGE": true, "ROUTE": true, "REMOTE": true, "BROKER": true,
	"AGGREGATE": true, "APPLICATION": true, "AUDIT": true, "SERVER": true,
	"RESOURCE": true, "WORKLOAD": true, "MASTER": true, "SYMMETRIC": true,
	"ASYMMETRIC": true, "EXTERNAL": true, "SECURITY": true, "SEARCH": true,
	"SIGNATURE": true, "AUTHORIZATION": true,
}

// moduleOptions are WITH options a CREATE PROCEDURE/FUNCTION/VIEW header can
// follow with AS, which would otherwise read as a CTE named after them.
var moduleOptions = map[string]bool{
	"RECOMPILE": true, "SCHEMABINDING": true, "ENCRYPTION": true,
	"VIEW_METADATA": true, "NATIVE_COMPILATION": true,
}

type stmtTokenKind int

const (
	tokWord   stmtTokenKind = iota // a keyword or plain identifier, upper-cased; @var and #tmp included
	tokIdent                       // a [bracketed] or "quoted" identifier
	tokString                      // a '...' literal
	tokOpen                        // (
	tokClose                       // )
	tokPunct                       // any other non-space rune
)

type stmtToken struct {
	kind     stmtTokenKind
	text     string
	row, col int
}

type pendKind int

const (
	pendNone      pendKind = iota
	pendWith               // WITH: the next token decides hint, option or CTE
	pendWithName           // WITH name: AS or ( next makes it a CTE
	pendSubClause          // ALTER/DROP inside an ALTER: objectTypes next makes it a statement
	pendNotBefore          // UPDATE/DELETE/MERGE: notBefore next makes it no statement
)

// stmtSplitter decides, one token at a time, where statements begin within a
// ';'/GO-delimited batch. Leaders whose role the following token decides are
// held in pend and cut at their own position once it is known.
type stmtSplitter struct {
	cut func(row, col int) // begins a new statement at (row, col)

	depth          int
	prev, prevPrev string // the last two tokens' text
	leader         string // the keyword that began the current statement

	pendingMainSelect bool // INSERT waiting for its SELECT or EXEC
	pendingCTEMain    bool // a CTE waiting for its main statement
	permission        bool // GRANT/DENY/REVOKE before TO/FROM: verbs are permission names

	pend     pendKind
	pendTok  stmtToken
	pendCont bool // the pending WITH continues the statement (CREATE VIEW v AS WITH ...)
}

func (s *stmtSplitter) feed(t stmtToken) {
	if s.pend != pendNone && s.resolve(t) {
		s.shift(t)
		return
	}
	s.advance(t)
	s.shift(t)
}

func (s *stmtSplitter) shift(t stmtToken) {
	s.prevPrev, s.prev = s.prev, t.text
}

// start makes t, a leader, begin a new statement.
func (s *stmtSplitter) start(t stmtToken) {
	s.cut(t.row, t.col)
	s.leader = t.text
	s.pendingMainSelect = t.text == "INSERT"
	s.pendingCTEMain = false
	s.permission = t.text == "GRANT" || t.text == "DENY" || t.text == "REVOKE"
}

// resolve settles the pending leader with t, the token after it, and reports
// whether t is consumed (it is then no leader itself).
func (s *stmtSplitter) resolve(t stmtToken) bool {
	p, tok, cont := s.pend, s.pendTok, s.pendCont
	s.pend = pendNone
	switch p {
	case pendWith:
		switch {
		case t.kind == tokWord && (statementLeaders[t.text] || moduleOptions[t.text]):
			return true // WITH ROLLBACK IMMEDIATE, WITH GRANT OPTION, WITH EXECUTE AS
		case t.kind == tokWord || t.kind == tokIdent:
			s.pend, s.pendTok, s.pendCont = pendWithName, tok, cont
			return true
		}
		return false // WITH (NOLOCK), OPENJSON(...) WITH (...): a hint or column list
	case pendWithName:
		if (t.kind == tokWord && t.text == "AS") || t.kind == tokOpen {
			if !cont {
				s.start(tok)
			}
			s.pendingCTEMain = true
		}
		return false // otherwise an option: WITH NOWAIT, WITH MOVE '...' TO '...'
	case pendSubClause:
		if t.kind == tokWord && objectTypes[t.text] {
			s.start(tok)
		}
	case pendNotBefore:
		if !notBefore[tok.text][t.text] {
			s.start(tok)
		}
	}
	return false
}

func (s *stmtSplitter) advance(t stmtToken) {
	switch t.kind {
	case tokOpen:
		s.depth++
		return
	case tokClose:
		if s.depth > 0 {
			s.depth--
		}
		return
	}
	if s.depth != 0 || t.kind != tokWord {
		return
	}
	w := t.text
	if s.permission {
		s.permission = w != "TO" && w != "FROM"
		return
	}
	if w == "VALUES" {
		s.pendingMainSelect = false
		return
	}
	if !statementLeaders[w] {
		return
	}
	if continuesAfter[s.prev] || (w == "IF" && (s.leader == "DROP" || s.leader == "ALTER") &&
		(objectTypes[s.prev] || s.prev == "COLUMN" || s.prev == "CONSTRAINT")) ||
		(w == "FETCH" && (s.prev == "ROWS" || s.prev == "ROW")) {
		switch {
		case w == "WITH":
			s.pend, s.pendTok, s.pendCont = pendWith, t, true
		case (s.prev == "AS" || s.prev == "THEN") && dmlLeaders[w]:
			s.leader, s.pendingMainSelect = w, w == "INSERT"
		}
		return
	}
	continuesUnion := s.prev == "UNION" || s.prev == "EXCEPT" || s.prev == "INTERSECT" ||
		(s.prev == "ALL" && s.prevPrev == "UNION")
	switch {
	case w == "SELECT" && (s.pendingMainSelect || s.pendingCTEMain):
		s.pendingMainSelect, s.pendingCTEMain = false, false
	case w == "SELECT" && continuesUnion:
	case (w == "EXEC" || w == "EXECUTE") && s.pendingMainSelect:
		s.pendingMainSelect = false // INSERT ... EXEC
	case s.pendingCTEMain && dmlLeaders[w]:
		s.pendingCTEMain = false
		s.leader, s.pendingMainSelect = w, w == "INSERT"
	case w == "SET" && (s.leader == "UPDATE" || s.leader == "ALTER" || s.leader == "MERGE" ||
		s.prev == "DELETE" || s.prev == "UPDATE"):
	case w == "WITH":
		s.pend, s.pendTok, s.pendCont = pendWith, t, false
	case s.leader == "ALTER" && (w == "ALTER" || w == "DROP"):
		s.pend, s.pendTok = pendSubClause, t
	case notBefore[w] != nil:
		s.pend, s.pendTok = pendNotBefore, t
	default:
		s.start(t)
	}
}

// StatementAt returns the trimmed [startRow,startCol]-[endRow,endCol] span of
// the T-SQL statement containing (row, col): what Ctrl+Enter selects, and so
// what the next F5 runs. ok is false when that statement is only white space,
// e.g. a blank line between two GO separators.
//
// Boundaries are ';', a "GO" batch separator line (IsGoSeparatorLine, the rule
// SplitBatches executes by), and a top-level (paren-depth zero)
// statementLeaders keyword, so scripts stacking statements with no ';' still
// split. Continuations (a UNION-chained SELECT, a CTE's main statement,
// INSERT ... SELECT/EXEC, a table hint's WITH (...), GRANT's permission list,
// an ALTER TABLE's own ALTER COLUMN/DROP CONSTRAINT and the rest listed on
// stmtSplitter) stay part of the statement. Next lexes the lines, so a
// boundary inside a literal, quoted identifier or comment is none.
//
// A missed boundary runs the statement after the cursor's too (review plan T2);
// a false one can cut a statement short into one that still parses (T3: a
// DELETE losing its WHERE). Where the two conflict, over-split into a fragment
// that fails to parse.
//
// This is a lexical approximation, not a parser: a statement inside a module
// body (CREATE PROCEDURE ... AS ...) is split from the header, an IF from its
// body, and INSERT ... VALUES followed by a separate SELECT with no ';' is
// (rarely) missed.
func StatementAt(lines [][]rune, row, col int) (startRow, startCol, endRow, endCol int, ok bool) {
	type span struct{ sr, sc, er, ec int }
	var segments []span
	curRow, curCol := 0, 0

	// The splitter is reset at every ';'/GO boundary, since a boundary of
	// either kind always falls at paren depth 0 in valid SQL.
	var sp stmtSplitter
	cut := func(r, c int) {
		if r > curRow || (r == curRow && c > curCol) {
			segments = append(segments, span{curRow, curCol, r, c})
			curRow, curCol = r, c
		}
	}
	reset := func() { sp = stmtSplitter{cut: cut} }
	reset()

	var st State
	for r, line := range lines {
		if st.Mode == ModeNormal && IsGoSeparatorLine(line) {
			segments = append(segments, span{curRow, curCol, r, 0})
			curRow, curCol = r+1, 0
			reset()
			continue
		}
		for c := 0; ; {
			// A token resumed from the line above was fed where it began.
			fresh := st.Mode == ModeNormal
			var t Token
			t, st = Next(line, c, len(line), st)
			if t.Kind == KindEnd {
				break
			}
			c = t.End
			if !fresh {
				continue
			}
			switch t.Kind {
			case KindWord, KindNumber:
				sp.feed(stmtToken{tokWord, strings.ToUpper(string(line[t.Start:t.End])), r, t.Start})
			case KindString:
				sp.feed(stmtToken{tokString, "'", r, t.Start})
			case KindQuotedIdent:
				sp.feed(stmtToken{tokIdent, string(line[t.Start]), r, t.Start})
			case KindPunct:
				switch line[t.Start] {
				case ';':
					segments = append(segments, span{curRow, curCol, r, t.End})
					curRow, curCol = r, t.End
					reset()
				case '(':
					sp.feed(stmtToken{tokOpen, "(", r, t.Start})
				case ')':
					sp.feed(stmtToken{tokClose, ")", r, t.Start})
				default:
					sp.feed(stmtToken{tokPunct, string(line[t.Start]), r, t.Start})
				}
			}
		}
		st = st.NextLine()
	}
	lastRow := len(lines) - 1
	segments = append(segments, span{curRow, curCol, lastRow, len(lines[lastRow])})

	cmp := func(r1, c1, r2, c2 int) int {
		switch {
		case r1 != r2:
			if r1 < r2 {
				return -1
			}
			return 1
		case c1 != c2:
			if c1 < c2 {
				return -1
			}
			return 1
		default:
			return 0
		}
	}

	// Adjacent segments share a boundary point, so a cursor exactly there matches
	// both; take the last so it resolves to the statement the cursor is at the
	// start of (Home, or a click on its first line), not the one it trails.
	found := -1
	for i, sg := range segments {
		if cmp(row, col, sg.sr, sg.sc) >= 0 && cmp(row, col, sg.er, sg.ec) <= 0 {
			found = i
		}
	}
	if found < 0 {
		return 0, 0, 0, 0, false
	}
	sg := segments[found]
	return trimStatementRange(lines, sg.sr, sg.sc, sg.er, sg.ec)
}

// trimStatementRange trims leading and trailing whitespace (blank lines
// included) from [sr,sc]-[er,ec], so the selection wraps the statement's text
// rather than the separator whitespace next to it. ok is false if nothing but
// whitespace remains.
func trimStatementRange(lines [][]rune, sr, sc, er, ec int) (int, int, int, int, bool) {
	for {
		if sr > er || (sr == er && sc >= ec) {
			return 0, 0, 0, 0, false
		}
		if sc >= len(lines[sr]) {
			sr++
			sc = 0
			continue
		}
		if unicode.IsSpace(lines[sr][sc]) {
			sc++
			continue
		}
		break
	}
	for {
		if sr > er || (sr == er && sc >= ec) {
			return 0, 0, 0, 0, false
		}
		if ec == 0 {
			er--
			ec = len(lines[er])
			continue
		}
		if unicode.IsSpace(lines[er][ec-1]) {
			ec--
			continue
		}
		break
	}
	return sr, sc, er, ec, true
}
