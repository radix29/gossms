package controls

import (
	"strings"
	"unicode"

	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/sqltext"
)

// ---------------------------------------------------------------------------
// T-SQL statement-boundary detection for Editor (Ctrl+Enter: select the
// statement at the cursor)
// ---------------------------------------------------------------------------

// SelectStatementAtCursor selects the T-SQL statement containing the
// cursor. Statement boundaries are ';', a "GO" batch separator alone on its
// own line — sqltext.IsGoSeparatorLine, the same rule internal/query splits a
// script into batches to execute by — and, additionally, a top-level (paren-depth
// zero) keyword that begins a statement (statementLeaders), so scripts
// stacking several statements with no ';' between them still split
// correctly. Continuations — a UNION-chained SELECT, a CTE's main statement,
// INSERT ... SELECT/EXEC, a table hint's WITH (...), GRANT's permission list,
// an ALTER TABLE's own ALTER COLUMN/DROP CONSTRAINT and the rest listed on
// stmtSplitter — are recognised as part of the same statement, not new ones.
// All boundary kinds are ignored inside string literals ('...'),
// bracketed/quoted identifiers ([...], "..."), and comments (--... and
// /* ... */), so one of those characters appearing inside never splits a
// statement in two.
//
// The selection is what the next F5 runs, so a missed boundary runs the
// statement after the cursor's too (T2), and a false one can cut a statement
// short into one that still parses (T3: a DELETE losing its WHERE). Where the
// two conflict the rules prefer an over-split into a fragment that fails to
// parse.
//
// This is a lexical approximation, not a full T-SQL parser: a statement
// inside a module body (CREATE PROCEDURE ... AS ...) is split from the
// header, and INSERT ... VALUES followed by a later, genuinely separate SELECT
// with no ';' between them is (rarely) missed.
//
// No-ops (returns false, selection untouched) if the statement at the
// cursor is empty or all-whitespace — e.g. the cursor sits on a blank line
// between two GO separators.
func (e *Editor) SelectStatementAtCursor() bool {
	sr, sc, er, ec, ok := sqlStatementAt(e.doc.all(), e.cursorRow, e.cursorCol)
	if !ok {
		return false
	}
	e.selecting = true
	e.selBlock = false
	e.selAnchorRow, e.selAnchorCol = sr, sc
	e.cursorRow, e.cursorCol = er, ec
	e.clampCursor()
	e.desiredCol = e.cursorDisplayCol()
	e.ensureCursorVisible()
	return true
}

// statementLeaders are the top-level keywords that begin a statement unless a
// stmtSplitter rule makes them a continuation. The DML six are
// internal/tui/sqlparse/scope.go's dmlStatementLeaders; the rest are its
// forwardStatementEnders plus every other statement verb whose running by
// accident does harm. This list is interim: T54 (docs/review-plan-2026-10-02.md)
// replaces both copies with one splitter in sqltext.
var statementLeaders = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true, "WITH": true,
	"DECLARE": true, "SET": true, "CREATE": true, "ALTER": true, "DROP": true, "TRUNCATE": true,
	"EXEC": true, "EXECUTE": true, "PRINT": true, "RAISERROR": true, "IF": true, "WHILE": true,
	"BEGIN": true, "COMMIT": true, "ROLLBACK": true, "SAVE": true, "USE": true,
	"GRANT": true, "DENY": true, "REVOKE": true, "BACKUP": true, "RESTORE": true, "DBCC": true,
	"KILL": true, "SHUTDOWN": true, "RECONFIGURE": true, "CHECKPOINT": true, "WAITFOR": true,
	"OPEN": true, "CLOSE": true, "FETCH": true, "DEALLOCATE": true, "BULK": true,
}

// dmlLeaders are the leaders that can be a CTE's main statement, or follow
// AS/THEN as a trigger body or MERGE action.
var dmlLeaders = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true,
}

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

// sqlStatementAt scans lines for statement boundaries and returns the
// trimmed [startRow,startCol]-[endRow,endCol] span of the statement
// containing (row, col). ok is false when that statement is empty.
func sqlStatementAt(lines [][]rune, row, col int) (startRow, startCol, endRow, endCol int, ok bool) {
	type span struct{ sr, sc, er, ec int }
	var segments []span

	const (
		stNormal = iota
		stBlockComment
		stSingleQuote
		stBracket
		stDoubleQuote
	)
	state := stNormal
	// T-SQL block comments nest, so "/* /* */ GO */" is one comment and its
	// GO no separator — the same rule sqltext.SplitBatches executes by.
	commentDepth := 0
	curRow, curCol := 0, 0

	// Leader-keyword boundaries (see the doc comment above) — the splitter is
	// reset at every ';'/GO batch boundary, since a boundary of either kind
	// always falls at paren depth 0 in valid SQL.
	var sp stmtSplitter
	cut := func(r, c int) {
		if r > curRow || (r == curRow && c > curCol) {
			segments = append(segments, span{curRow, curCol, r, c})
			curRow, curCol = r, c
		}
	}
	reset := func() { sp = stmtSplitter{cut: cut} }
	reset()

	for r, line := range lines {
		if state == stNormal && sqltext.IsGoSeparatorLine(line) {
			segments = append(segments, span{curRow, curCol, r, 0})
			curRow, curCol = r+1, 0
			reset()
			continue
		}
		c := 0
		for c < len(line) {
			switch state {
			case stBlockComment:
				switch {
				case c+1 < len(line) && line[c] == '/' && line[c+1] == '*':
					commentDepth++
					c += 2
				case c+1 < len(line) && line[c] == '*' && line[c+1] == '/':
					if commentDepth--; commentDepth == 0 {
						state = stNormal
					}
					c += 2
				default:
					c++
				}
			case stSingleQuote:
				if line[c] == '\'' {
					if c+1 < len(line) && line[c+1] == '\'' {
						c += 2
					} else {
						state = stNormal
						c++
					}
				} else {
					c++
				}
			case stBracket:
				if line[c] == ']' {
					if c+1 < len(line) && line[c+1] == ']' {
						c += 2
					} else {
						state = stNormal
						c++
					}
				} else {
					c++
				}
			case stDoubleQuote:
				if line[c] == '"' {
					if c+1 < len(line) && line[c+1] == '"' {
						c += 2
					} else {
						state = stNormal
						c++
					}
				} else {
					c++
				}
			default: // stNormal
				switch {
				case c+1 < len(line) && line[c] == '-' && line[c+1] == '-':
					c = len(line) // line comment: rest of the line is skipped
				case c+1 < len(line) && line[c] == '/' && line[c+1] == '*':
					state, commentDepth = stBlockComment, 1
					c += 2
				case line[c] == '\'':
					sp.feed(stmtToken{tokString, "'", r, c})
					state = stSingleQuote
					c++
				case line[c] == '[':
					sp.feed(stmtToken{tokIdent, "[", r, c})
					state = stBracket
					c++
				case line[c] == '"':
					sp.feed(stmtToken{tokIdent, `"`, r, c})
					state = stDoubleQuote
					c++
				case line[c] == ';':
					c++
					segments = append(segments, span{curRow, curCol, r, c})
					curRow, curCol = r, c
					reset()
				case line[c] == '(':
					sp.feed(stmtToken{tokOpen, "(", r, c})
					c++
				case line[c] == ')':
					sp.feed(stmtToken{tokClose, ")", r, c})
					c++
				case core.IsWordRune(line[c]) || line[c] == '@' || line[c] == '#':
					// @var, @@ROWCOUNT and #tmp are one word, so @Delete is no DELETE.
					start := c
					for c < len(line) && (line[c] == '@' || line[c] == '#') {
						c++
					}
					for c < len(line) && core.IsWordRune(line[c]) {
						c++
					}
					sp.feed(stmtToken{tokWord, strings.ToUpper(string(line[start:c])), r, start})
				case unicode.IsSpace(line[c]):
					c++
				default:
					sp.feed(stmtToken{tokPunct, string(line[c]), r, c})
					c++
				}
			}
		}
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

	// Adjacent segments share their boundary point (segment i's end equals
	// segment i+1's start), so a cursor sitting exactly there matches both;
	// take the last match, not the first, so it resolves to the statement
	// the cursor is positioned at the *start* of — the common case, since
	// users land there via Home/a mouse click on the new statement's first
	// line — rather than the one it trails.
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

// trimStatementRange trims leading and trailing whitespace (including
// blank lines) from [sr,sc]-[er,ec], so the selection wraps tightly around
// the statement's actual text instead of the separator whitespace next to
// it. ok is false if nothing but whitespace remains.
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
