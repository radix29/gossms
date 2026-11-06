package cmd

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/clitest"
)

// monitorServer is real gosmo over canned rows for the monitor group and
// plan cached: a catalogServer (the connection's database) with whether the
// login holds VIEW SERVER STATE, the sessions, the wait statistics and the
// server's start, the plan cache, the server's databases and clock, Query
// Store's reports (qs) and system_health's deadlocks (dl). A KILL changes
// the sessions as the server would (applyWrite), and the reads after it see
// that.
//
// Every rule answers what the state holds when the query arrives. A read
// that runs more than once (waits, start) answers the next of its list.
type monitorServer struct {
	*catalogServer
	noVSS  bool  // HAS_PERMS_BY_NAME answers 0
	vssErr error // fails the permission check

	// sessions is sys.dm_exec_sessions; the reads answer them in
	// session_id order, as the server's ORDER BY does, keeping the user
	// processes when the query asks only for them.
	sessions []*gosmo.ActiveSession
	// afterKill is the session a KILL of its id leaves behind: nil (or
	// missing) ends it; one still there or rolling back stays.
	afterKill map[int]*gosmo.ActiveSession

	// waits is each WaitStats read's rows in turn, uptimes each
	// StartTime's; start is the instance's start.
	waits     [][]gosmo.WaitStat
	uptimes   []time.Duration
	start     time.Time
	waitReads int
	upReads   int

	// queries is sys.dm_exec_query_stats, its database the plan's dbid's
	// name; plans is each cached statement's plan by "0x<handle> start end".
	queries []*gosmo.CachedQueryStat
	plans   map[string]string

	// dbs is sys.databases, for DB_ID() and the database list; clock is
	// SYSDATETIMEOFFSET().
	dbs   []*gosmo.Database
	clock time.Time

	// writes is each write the server was sent, as sent; writeErr fails
	// every one.
	writes   []string
	writeErr error

	qs *qsServer
	dl *dlServer
}

// monitorDatabases is the server's databases: master, Sales and hr.
func monitorDatabases() []*gosmo.Database {
	return []*gosmo.Database{
		{Name: "master", ID: 1, State: gosmo.DatabaseOnline, RecoveryModel: gosmo.RecoveryModelSimple, CompatibilityLevel: 160},
		{Name: "Sales", ID: 5, State: gosmo.DatabaseOnline, RecoveryModel: gosmo.RecoveryModelFull, CompatibilityLevel: 160},
		{Name: "hr", ID: 8, State: gosmo.DatabaseOnline, RecoveryModel: gosmo.RecoveryModelSimple, CompatibilityLevel: 160},
	}
}

func newMonitorServer(t *testing.T) *monitorServer {
	s := &monitorServer{dbs: monitorDatabases(), plans: map[string]string{}, afterKill: map[int]*gosmo.ActiveSession{},
		clock: dlNow, qs: newQSServer(), dl: newDLServer()}
	newCatalogServerWith(t, "Sales", func(c *catalogServer) { s.catalogServer = c; s.rules() })
	return s
}

// reads is how many queries s was sent containing every one of subs.
func (s *monitorServer) reads(subs ...string) int {
	n := 0
	for _, q := range s.Queries() {
		if has(q, subs...) {
			n++
		}
	}
	return n
}

// The reads tests count.
const (
	sessionsRead = "FROM   sys.dm_exec_sessions s"
	topRead      = "FROM   sys.dm_exec_query_stats AS qs"
	planRead     = "FROM sys.dm_exec_text_query_plan(@p1, @p2, @p3)"
)

var killStatement = regexp.MustCompile(`^KILL (\d+)$`)

// applyWrite is the server taking q: recorded, failed with writeErr, else
// a KILL leaves its session's afterKill in its place.
func (s *monitorServer) applyWrite(q gosmotest.Query) gosmotest.Result {
	s.writes = append(s.writes, q.Text)
	if s.writeErr != nil {
		return gosmotest.Result{Err: s.writeErr}
	}
	m := killStatement.FindStringSubmatch(q.Text)
	if m == nil {
		s.t.Errorf("monitorServer: a write it does not know: %s", q)
		return gosmotest.Result{}
	}
	var spid int
	fmt.Sscan(m[1], &spid)
	i := slices.IndexFunc(s.sessions, func(a *gosmo.ActiveSession) bool { return a.SessionID == spid })
	if i >= 0 {
		if after := s.afterKill[spid]; after != nil {
			s.sessions[i] = after
		} else {
			s.sessions = slices.Delete(s.sessions, i, i+1)
		}
	}
	return gosmotest.Result{}
}

// topFigures is each dm_exec_query_stats ranking column's figure.
var topFigures = map[string]func(*gosmo.CachedQueryStat) int64{
	"total_worker_time":    func(q *gosmo.CachedQueryStat) int64 { return q.TotalCPUMicros },
	"total_logical_reads":  func(q *gosmo.CachedQueryStat) int64 { return q.TotalLogicalReads },
	"total_logical_writes": func(q *gosmo.CachedQueryStat) int64 { return q.TotalLogicalWrites },
	"total_elapsed_time":   func(q *gosmo.CachedQueryStat) int64 { return q.TotalDurationMicros },
	"execution_count":      func(q *gosmo.CachedQueryStat) int64 { return q.ExecutionCount },
}

var topRanking = regexp.MustCompile(`ORDER  BY (CAST\()?qs\.(\w+)`)

// topQueries is the TOP (@p1) of s's queries in q's ranking, kept to
// database id @p2 when it filters: the server's ORDER BY and WHERE.
func (s *monitorServer) topQueries(q gosmotest.Query) [][]any {
	m := topRanking.FindStringSubmatch(q.Text)
	figure, avg := topFigures[m[2]], m[1] != ""
	rank := func(c *gosmo.CachedQueryStat) float64 {
		if !avg {
			return float64(figure(c))
		}
		if c.ExecutionCount == 0 {
			return -1 // NULL sorts last DESC
		}
		return float64(figure(c)) / float64(c.ExecutionCount)
	}
	var kept []*gosmo.CachedQueryStat
	for _, c := range s.queries {
		if !has(q, "@p2") || s.dbID(c.Database) == q.Arg(1) {
			kept = append(kept, c)
		}
	}
	slices.SortStableFunc(kept, func(a, b *gosmo.CachedQueryStat) int {
		return cmp.Or(cmp.Compare(rank(b), rank(a)), slices.Compare(a.PlanHandle, b.PlanHandle),
			cmp.Compare(a.StatementStart, b.StatementStart))
	})
	top, _ := q.Arg(0).(int64)
	var rows [][]any
	for _, c := range kept[:min(int(top), len(kept))] {
		rows = append(rows, clitest.CachedQueryStatRow(c))
	}
	return rows
}

// dbID is database name's id as DB_ID() returns it, nil for none.
func (s *monitorServer) dbID(name string) any {
	for _, d := range s.dbs {
		if strings.EqualFold(d.Name, name) {
			return int64(d.ID)
		}
	}
	return nil
}

func (s *monitorServer) rules() {
	s.OnFunc(func(q gosmotest.Query) bool { return q.Exec }, s.applyWrite)
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "HAS_PERMS_BY_NAME(NULL, NULL, 'VIEW SERVER STATE')") },
		func(gosmotest.Query) gosmotest.Result {
			if s.vssErr != nil {
				return gosmotest.Result{Err: s.vssErr}
			}
			return gosmotest.Result{Sets: []gosmotest.Set{{Rows: [][]any{{map[bool]int{true: 0, false: 1}[s.noVSS]}}}}}
		})
	s.on(func(q gosmotest.Query) [][]any {
		sessions := slices.Clone(s.sessions)
		slices.SortFunc(sessions, func(a, b *gosmo.ActiveSession) int { return cmp.Compare(a.SessionID, b.SessionID) })
		var rows [][]any
		for _, a := range sessions {
			if a.IsUserProcess || !has(q, "AND s.is_user_process = 1") {
				rows = append(rows, clitest.ActiveSessionRow(a))
			}
		}
		return rows
	}, sessionsRead, "WHERE  s.session_id != @@SPID")
	s.on(func(q gosmotest.Query) [][]any {
		for _, a := range s.sessions {
			if int64(a.SessionID) == q.Arg(0) {
				return [][]any{clitest.ActiveSessionRow(a)}
			}
		}
		return nil
	}, sessionsRead, "WHERE  s.session_id = @p1")
	s.on(func(gosmotest.Query) [][]any {
		s.waitReads++
		var rows [][]any
		for _, w := range s.waits[s.waitReads-1] {
			rows = append(rows, clitest.WaitStatRow(w))
		}
		return rows
	}, "FROM   sys.dm_os_wait_stats")
	s.on(func(gosmotest.Query) [][]any {
		s.upReads++
		return [][]any{{s.start, s.uptimes[s.upReads-1].Milliseconds()}}
	}, "sqlserver_start_time", "FROM   sys.dm_os_sys_info")
	s.on(func(q gosmotest.Query) [][]any { return [][]any{{s.dbID(argString(q, 0))}} }, "SELECT DB_ID(@p1)")
	s.on(s.topQueries, topRead)
	s.on(func(q gosmotest.Query) [][]any {
		h, _ := q.Arg(0).([]byte)
		doc, ok := s.plans[fmt.Sprintf("0x%X %d %d", h, q.Arg(1), q.Arg(2))]
		if !ok {
			return [][]any{{nil}} // the function's row for a plan no longer cached
		}
		return [][]any{{doc}}
	}, planRead)
	s.on(func(gosmotest.Query) [][]any { return [][]any{{s.clock}} }, "SELECT SYSDATETIMEOFFSET()")
	s.on(func(gosmotest.Query) [][]any {
		dbs := slices.Clone(s.dbs)
		slices.SortFunc(dbs, func(a, b *gosmo.Database) int { return strings.Compare(a.Name, b.Name) })
		var rows [][]any
		for _, d := range dbs {
			rows = append(rows, clitest.DatabaseOf(d))
		}
		return rows
	}, "FROM sys.databases", "ORDER BY name")
	s.qsRules()
	s.dlRules()
}

// qsServer is the connection's database's Query Store: its state (""
// for a database with none: no options row), the rows every per-query
// report and the wait categories read answer, or readErr failing each.
// The reads answer in their ORDER BY, keep their TOP and apply their
// HAVING and regression floor; the window is not modelled — a test asserts
// it from the query.
type qsServer struct {
	state   gosmo.QueryStoreState
	rows    []*gosmo.QSQueryStat
	waits   []*gosmo.QSWaitStat
	readErr error
	reads   []qsRead
}

// qsRead is one report read: which (top, regressed, variation, forced,
// categories, waiting) and the query.
type qsRead struct {
	report string
	q      gosmotest.Query
}

func newQSServer() *qsServer { return &qsServer{state: gosmo.QueryStoreReadWrite} }

// read is the reports read, in order.
func (s *qsServer) read() []string {
	var out []string
	for _, r := range s.reads {
		out = append(out, r.report)
	}
	return out
}

// last is the last report read's query.
func (s *qsServer) last() gosmotest.Query { return s.reads[len(s.reads)-1].q }

var (
	qsWindow     = regexp.MustCompile(`rsi\.end_time > @p(\d+) AND rsi\.start_time < @p(\d+)`)
	qsMinExec    = regexp.MustCompile(`HAVING SUM\(rs\.count_executions\) >= @p(\d+)`)
	qsRegression = regexp.MustCompile(`b\.value \* @p(\d+) / 100`)
	qsCategory   = regexp.MustCompile(`ws\.wait_category_desc = @p(\d+)`)
)

// qsParam is the value of the parameter re's first group numbers in q, nil
// when q has no such clause.
func qsParam(q gosmotest.Query, re *regexp.Regexp) any {
	m := re.FindStringSubmatch(q.Text)
	if m == nil {
		return nil
	}
	var n int
	fmt.Sscan(m[1], &n)
	return q.Arg(n - 1)
}

// qsWindows is each [from, to) q's interval predicates bound, in the
// query's order: the reported window first, then for regressed the
// baseline and the reported window it leaves out.
func qsWindows(q gosmotest.Query) [][2]time.Time {
	var out [][2]time.Time
	for _, m := range qsWindow.FindAllStringSubmatch(q.Text, -1) {
		var a, b int
		fmt.Sscan(m[1], &a)
		fmt.Sscan(m[2], &b)
		from, _ := q.Arg(a - 1).(time.Time)
		to, _ := q.Arg(b - 1).(time.Time)
		out = append(out, [2]time.Time{from, to})
	}
	return out
}

// reportRule answers the report whose queries contain every one of subs
// from s.rows, ranked by key descending.
func (m *monitorServer) reportRule(report string, key func(*gosmo.QSQueryStat) float64, subs ...string) {
	s := m.qs
	m.OnFunc(func(q gosmotest.Query) bool { return has(q, subs...) }, func(q gosmotest.Query) gosmotest.Result {
		s.reads = append(s.reads, qsRead{report, q})
		if s.readErr != nil {
			return gosmotest.Result{Err: s.readErr}
		}
		minExec, _ := qsParam(q, qsMinExec).(int64)
		pct, hasFloor := qsParam(q, qsRegression).(float64)
		var kept []*gosmo.QSQueryStat
		for _, st := range s.rows {
			switch {
			case st.ExecCount < minExec, report == "regressed" && st.BaselineExecCount < minExec:
			case hasFloor && (st.BaselineValue <= 0 || st.Regression < st.BaselineValue*pct/100):
			default:
				kept = append(kept, st)
			}
		}
		slices.SortStableFunc(kept, func(a, b *gosmo.QSQueryStat) int { return cmp.Compare(key(b), key(a)) })
		top, _ := q.Arg(0).(int64)
		var rows [][]any
		for _, st := range kept[:min(int(top), len(kept))] {
			rows = append(rows, clitest.QSQueryStatRow(st))
		}
		return gosmotest.Result{Sets: []gosmotest.Set{{Rows: rows}}}
	})
}

func (m *monitorServer) qsRules() {
	s := m.qs
	m.on(func(gosmotest.Query) [][]any {
		if s.state == "" {
			return nil
		}
		return [][]any{clitest.QueryStoreInfoRow(s.state)}
	}, "FROM   sys.database_query_store_options")
	m.OnFunc(func(q gosmotest.Query) bool { return has(q, "GROUP BY ws.wait_category_desc") }, func(q gosmotest.Query) gosmotest.Result {
		s.reads = append(s.reads, qsRead{"categories", q})
		if s.readErr != nil {
			return gosmotest.Result{Err: s.readErr}
		}
		waits := slices.Clone(s.waits)
		slices.SortStableFunc(waits, func(a, b *gosmo.QSWaitStat) int { return cmp.Compare(b.Value, a.Value) })
		var rows [][]any
		for _, w := range waits {
			rows = append(rows, clitest.QSWaitStatRow(w))
		}
		return gosmotest.Result{Sets: []gosmotest.Set{{Rows: rows}}}
	})
	value := func(st *gosmo.QSQueryStat) float64 { return st.Value }
	m.reportRule("waiting", value, "COALESCE(MAX(rs.last_execution_time), MAX(rsi.end_time))")
	m.reportRule("regressed", func(st *gosmo.QSQueryStat) float64 { return st.Regression }, "WITH recent AS (", "ORDER BY regression DESC")
	m.reportRule("variation", func(st *gosmo.QSQueryStat) float64 { return st.Variation }, "ORDER BY variation DESC")
	m.reportRule("forced", value, "MAX(CAST(p.is_forced_plan AS int)) = 1", "ORDER BY value DESC")
	m.reportRule("top", value, "FROM   sys.query_store_query ", "ORDER BY value DESC")
}

// dlServer is the event sessions monitor deadlocks reads: system_health
// (nil: it does not exist), and the events its event_file and ring_buffer
// hold. The file read keeps the events its WHERE names, as the server does.
type dlServer struct {
	session *gosmo.EventSession
	events  []gosmo.XEvent
}

func newDLServer() *dlServer {
	return &dlServer{session: systemHealthSession(gosmo.XETargetRingBuffer, gosmo.XETargetEventFile)}
}

// The reads tests count.
const (
	fileRead = "FROM   sys.fn_xe_file_target_read_file(@p1, NULL, @p2, @p3)"
	ringRead = "t.target_name = N'ring_buffer'"
	dbsRead  = "ORDER BY name"
)

func (m *monitorServer) dlRules() {
	s := m.dl
	// session is the event session a by-name read names, nil for none.
	session := func(q gosmotest.Query) *gosmo.EventSession {
		if s.session == nil || argString(q, 0) != s.session.Name {
			return nil
		}
		return s.session
	}
	child := func(pick func(events, actions, targets, fields [][]any) [][]any) func(gosmotest.Query) [][]any {
		return func(q gosmotest.Query) [][]any {
			es := session(q)
			if es == nil {
				return nil
			}
			_, events, actions, targets, fields := clitest.EventSessionRows(es, 1)
			return pick(events, actions, targets, fields)
		}
	}
	m.on(func(q gosmotest.Query) [][]any {
		es := session(q)
		if es == nil {
			return nil
		}
		row, _, _, _, _ := clitest.EventSessionRows(es, 1)
		return [][]any{row}
	}, "FROM   sys.server_event_sessions s\nLEFT   JOIN sys.dm_xe_sessions r", "WHERE  s.name = @p1")
	m.on(child(func(e, _, _, _ [][]any) [][]any { return e }), "FROM   sys.server_event_session_events e", "WHERE  s.name = @p1")
	m.on(child(func(_, a, _, _ [][]any) [][]any { return a }), "FROM   sys.server_event_session_actions a", "WHERE  s.name = @p1")
	m.on(child(func(_, _, t, _ [][]any) [][]any { return t }), "FROM   sys.server_event_session_targets t", "WHERE  s.name = @p1")
	m.on(child(func(_, _, _, f [][]any) [][]any { return f }), "FROM   sys.server_event_session_fields f", "WHERE  s.name = @p1")
	m.on(func(q gosmotest.Query) [][]any {
		var names []any
		if len(q.Args) > 3 {
			names = q.Args[3:]
		}
		var rows [][]any
		for i, e := range s.events {
			if names == nil || slices.Contains(names, any(e.Name)) {
				rows = append(rows, []any{`C:\Log\system_health_0_1.xel`, int64(4096 * (i + 1)), clitest.EventXML(e)})
			}
		}
		return rows
	}, fileRead)
	m.on(func(q gosmotest.Query) [][]any {
		if es := session(q); es == nil || !es.IsRunning {
			return nil
		}
		return [][]any{{clitest.RingBufferXML(s.events)}}
	}, ringRead)
}
