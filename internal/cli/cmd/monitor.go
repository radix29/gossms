package cmd

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// MonitorCommands is the monitor group: its node and its commands. Its
// summary names only what exists.
func MonitorCommands(d Deps) []*spec.Command {
	return []*spec.Command{
		{
			Path:    []string{"monitor"},
			Summary: "Show the server's sessions, its blocking chains, its waits, its costliest cached statements, Query Store's history and the recorded deadlocks, and end a session",
			Class:   spec.ClassRead,
		},
		monitorSessionsCommand(d),
		monitorBlockingCommand(d),
		monitorWaitsCommand(d),
		monitorTopQueriesCommand(d),
		monitorQueryStoreCommand(d),
		monitorDeadlocksCommand(d),
		monitorKillCommand(d),
	}
}

// The monitor sessions options (D2). --session-database, not --database:
// that is the connection's -d.
const (
	optActive          = "active"
	optSessionDatabase = "session-database"
	optLogin           = "login"
	optProgram         = "program"
)

// viewServerStateDenied is a monitor read's refusal without VIEW SERVER
// STATE. It is asked first (P7-3): without it the session DMVs return only
// the caller's own session, which ActiveSessions leaves out, so the answer
// would be an empty list rather than a refusal. The other DMVs refuse (Msg
// 300), but asking first gives every monitor read the one answer.
func viewServerStateDenied(what string) *output.Error {
	return output.NewError(output.CodePermissionDenied, what+": the login lacks VIEW SERVER STATE",
		"The server's DMVs need "+serverStateHint+"; without it the session DMVs show a login only its own "+
			"session. Use a login that has it, or have it granted.")
}

// connectServerState is connectCatalog for a read that needs VIEW SERVER
// STATE: exit 9 before the read when the login lacks it.
func (d Deps) connectServerState(ctx context.Context, inv *spec.Invocation, res *output.Result, what string) (func(), Catalog, error) {
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return nil, nil, err
	}
	ok, err := cat.HasViewServerState(ctx)
	if err == nil && !ok {
		err = viewServerStateDenied(what)
	}
	if err != nil {
		sc.Close()
		return nil, nil, readError(err)
	}
	return sc.Close, cat, nil
}

// sessionText is the text a session row shows: the running request's batch,
// or for an idle session the connection's most recent one, as sp_block shows
// it — an idle head blocker's open transaction began there.
func sessionText(s *gosmo.ActiveSession) string { return cmp.Or(s.CommandText, s.RecentText) }

// blockerCell is blocked_by: NULL for a session nobody blocks.
func blockerCell(id int) *string {
	if id == 0 {
		return nil
	}
	return ptr(strconv.Itoa(id))
}

// --- monitor sessions -------------------------------------------------------

func monitorSessionsCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"monitor", "sessions"},
		Summary: "List the server's sessions: login, host, program, database, request, resource use, blocker and wait",
		Description: "One row per session (sys.dm_exec_sessions with its request and connection), by spid, the " +
			"CLI's own left out; user sessions only unless --include-system. command, wait_type and " +
			"wait_resource are the running request's, NULL for an idle session. text is the running request's " +
			"batch, or for an idle session the batch its connection sent last — what opened an idle session's " +
			"open transaction — its first 512 characters, in the server's auto-parameterized form when the " +
			"server parameterized it. cpu_ms, reads, writes and logical_reads are the session's totals since " +
			"login, the running request's so far included; elapsed_ms is how long the running request has run, " +
			"NULL for an idle session. blocked_by is the session one of its requests waits on — a parallel request's blocked " +
			"worker counts, and then wait_type, wait_ms and wait_resource are that worker's. --active keeps the " +
			"sessions running a request; --session-database, --login and --program keep the sessions whose " +
			"value equals the one given, case-insensitively. Needs VIEW SERVER STATE (VIEW SERVER PERFORMANCE " +
			"STATE from SQL Server 2022): without it, exit 9 — the server would show only the login's own " +
			"session.",
		Options: append(ConnectOptions(),
			spec.Option{Name: optActive, Type: spec.TypeBool, Help: "Only sessions running a request"},
			spec.Option{Name: optSessionDatabase, Type: spec.TypeString,
				Help: "Only sessions whose current database is this (case-insensitive); -d is the one the CLI connects to"},
			spec.Option{Name: optLogin, Type: spec.TypeString, Help: "Only sessions of this login (case-insensitive)"},
			spec.Option{Name: optProgram, Type: spec.TypeString, Help: "Only sessions whose program name is this (case-insensitive)"},
			spec.Option{Name: optIncludeSystem, Type: spec.TypeBool, Help: "Add the system sessions"},
		),
		Class: spec.ClassRead,
		Run:   d.monitorSessions,
		Examples: []string{
			"gossms monitor sessions -p prod-ro",
			"gossms monitor sessions --active --session-database Sales -p prod-ro -o json",
			"gossms monitor sessions --login app_user --program \"Microsoft SQL Server Management Studio\" -p dev",
		},
	}
}

// sessionFilter is monitor sessions' filters; an empty string keeps every
// session.
type sessionFilter struct {
	Active                   bool
	Database, Login, Program string
}

func (f sessionFilter) keep(s *gosmo.ActiveSession) bool {
	eq := func(want, got string) bool { return want == "" || strings.EqualFold(want, got) }
	return (!f.Active || s.Command != "") && eq(f.Database, s.DatabaseName) && eq(f.Login, s.LoginName) && eq(f.Program, s.ProgramName)
}

func (d Deps) monitorSessions(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	f := sessionFilter{Active: inv.Values[optActive].(bool)}
	f.Database, _ = inv.Values[optSessionDatabase].(string)
	f.Login, _ = inv.Values[optLogin].(string)
	f.Program, _ = inv.Values[optProgram].(string)
	scope := gosmo.UserSessionsOnly
	if inv.Values[optIncludeSystem].(bool) {
		scope = gosmo.AllSessions
	}

	res := &output.Result{}
	closeConn, cat, err := d.connectServerState(ctx, inv, res, "monitor sessions")
	if err != nil {
		return res, err
	}
	defer closeConn()

	all, err := cat.ActiveSessions(ctx, scope)
	if err != nil {
		return res, readError(err)
	}
	var kept []*gosmo.ActiveSession
	for _, s := range all {
		if f.keep(s) {
			kept = append(kept, s)
		}
	}
	res.Sets = []output.Set{sessionsSet(kept)}
	return res, nil
}

// sessionsSet is monitor sessions' result set: D2's columns.
func sessionsSet(sessions []*gosmo.ActiveSession) output.Set {
	set := output.Set{Name: "sessions", Columns: []output.Column{
		{Name: "spid", Type: "smallint"}, {Name: "login", Type: "nvarchar(128)"},
		{Name: "host", Type: "nvarchar(128)"}, {Name: "program", Type: "nvarchar(128)"},
		{Name: "database", Type: "nvarchar(128)", Nullable: true}, {Name: "status", Type: "nvarchar(30)"},
		{Name: "command", Type: "nvarchar(32)", Nullable: true}, {Name: "open_tran", Type: "int"},
		{Name: "cpu_ms", Type: "int"}, {Name: "reads", Type: "bigint"}, {Name: "writes", Type: "bigint"},
		{Name: "logical_reads", Type: "bigint"}, {Name: "elapsed_ms", Type: "int", Nullable: true},
		{Name: "blocked_by", Type: "smallint", Nullable: true}, {Name: "wait_type", Type: "nvarchar(60)", Nullable: true},
		{Name: "wait_ms", Type: "bigint"}, {Name: "wait_resource", Type: "nvarchar(256)", Nullable: true},
		{Name: "text", Type: "nvarchar(max)", Nullable: true},
	}}
	for _, s := range sessions {
		// dm_exec_sessions' counters move only when a request ends, so the
		// running request's are added (as sp_who2's sysprocesses does): a
		// query blocked for minutes showed cpu_ms and elapsed_ms 0 (7.l).
		var elapsed *string
		if s.Command != "" {
			elapsed = int64Cell(s.RequestElapsedMS)
		}
		set.Rows = append(set.Rows, []*string{
			ptr(strconv.Itoa(s.SessionID)), ptr(s.LoginName), ptr(s.HostName), ptr(s.ProgramName),
			textOrNull(s.DatabaseName), ptr(s.Status), textOrNull(s.Command),
			ptr(strconv.Itoa(s.OpenTransactionCount)), int64Cell(s.CPUTime + s.RequestCPUTime),
			int64Cell(s.Reads + s.RequestReads), int64Cell(s.Writes + s.RequestWrites),
			int64Cell(s.LogicalReads + s.RequestLogicalReads), elapsed,
			blockerCell(s.BlockingSessionID), textOrNull(s.WaitType), int64Cell(s.WaitTimeMS),
			textOrNull(s.WaitResource), textOrNull(sessionText(s)),
		})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// --- monitor blocking -------------------------------------------------------

func monitorBlockingCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"monitor", "blocking"},
		Summary: "Show the blocking chains: each head blocker and the sessions waiting behind it, as a tree",
		Description: "One row per session in a blocking chain, read from every session, system ones included " +
			"(the same read as monitor sessions --include-system), in tree order: each chain's head, then the " +
			"sessions it blocks, depth first, by spid at each level; chains by head spid. head is the chain's " +
			"head blocker — a session that blocks and is not itself blocked — and depth its distance from it " +
			"(0 the head). A session blocked by one the read does not show (it ended between the two, or a " +
			"negative blocker: -2 an orphaned distributed transaction, -4 a latch owner) heads its own chain, " +
			"blocked_by still naming it. A cycle — a deadlock the monitor has not resolved yet, no head " +
			"anywhere — is reported once, headed by its lowest spid, cycle 1 on the sessions in the loop. " +
			"text is the running request's batch, or for an idle session (the usual head blocker, an open " +
			"transaction left behind) the batch its connection sent last. No blocking is an empty set, status " +
			"ok. Needs VIEW SERVER STATE (VIEW SERVER PERFORMANCE STATE from SQL Server 2022): without it, " +
			"exit 9.",
		Options: ConnectOptions(),
		Class:   spec.ClassRead,
		Run:     d.monitorBlocking,
		Examples: []string{
			"gossms monitor blocking -p prod-ro",
			"gossms monitor blocking -p prod-ro -o json",
		},
	}
}

func (d Deps) monitorBlocking(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	closeConn, cat, err := d.connectServerState(ctx, inv, res, "monitor blocking")
	if err != nil {
		return res, err
	}
	defer closeConn()

	all, err := cat.ActiveSessions(ctx, gosmo.AllSessions)
	if err != nil {
		return res, readError(err)
	}
	res.Sets = []output.Set{blockingSet(blockingChains(all))}
	return res, nil
}

// chainRow is one session in a blocking chain: its chain's head, its depth
// below it, and whether it is in a cycle.
type chainRow struct {
	Session *gosmo.ActiveSession
	Head    int
	Depth   int
	Cycle   bool
}

// blockingChains is the blocking chains among sessions, as rows in tree
// order: chains by head spid, each walked depth first with the sessions at
// each level by spid. Sessions in no chain are left out.
//
// A head blocks and is not blocked; a session whose blocker is not among
// sessions (gone, or a negative pseudo-session) heads its own chain. What is
// left after walking from those heads hangs off a cycle: each such component
// is walked once from the lowest spid on its loop, the loop's members marked.
// A visited set bounds every walk, so a cycle is never followed round.
func blockingChains(sessions []*gosmo.ActiveSession) []chainRow {
	byID := make(map[int]*gosmo.ActiveSession, len(sessions))
	for _, s := range sessions {
		byID[s.SessionID] = s
	}
	sorted := slices.SortedFunc(slices.Values(sessions), func(a, b *gosmo.ActiveSession) int {
		return cmp.Compare(a.SessionID, b.SessionID)
	})
	blocks := map[int][]*gosmo.ActiveSession{} // blocker → the sessions it blocks, by spid
	for _, s := range sorted {
		if s.BlockingSessionID != 0 {
			blocks[s.BlockingSessionID] = append(blocks[s.BlockingSessionID], s)
		}
	}

	visited := map[int]bool{}
	var chains [][]chainRow
	walk := func(head *gosmo.ActiveSession, loop map[int]bool) {
		var rows []chainRow
		var visit func(s *gosmo.ActiveSession, depth int)
		visit = func(s *gosmo.ActiveSession, depth int) {
			visited[s.SessionID] = true
			rows = append(rows, chainRow{Session: s, Head: head.SessionID, Depth: depth, Cycle: loop[s.SessionID]})
			for _, w := range blocks[s.SessionID] {
				if !visited[w.SessionID] {
					visit(w, depth+1)
				}
			}
		}
		visit(head, 0)
		chains = append(chains, rows)
	}

	for _, s := range sorted {
		_, blockerSeen := byID[s.BlockingSessionID]
		switch {
		case s.BlockingSessionID == 0 && len(blocks[s.SessionID]) > 0:
		case s.BlockingSessionID != 0 && !blockerSeen:
		default:
			continue
		}
		walk(s, nil)
	}
	// Every session left blocked has a blocker among sessions, so following
	// blocked_by from it must come back round: the first spid seen twice is
	// on the loop.
	for _, s := range sorted {
		if visited[s.SessionID] || s.BlockingSessionID == 0 {
			continue
		}
		seen := map[int]bool{}
		at := s
		for !seen[at.SessionID] {
			seen[at.SessionID] = true
			at = byID[at.BlockingSessionID]
		}
		loop := map[int]bool{}
		head := at
		for m := at; !loop[m.SessionID]; m = byID[m.BlockingSessionID] {
			loop[m.SessionID] = true
			if m.SessionID < head.SessionID {
				head = m
			}
		}
		walk(head, loop)
	}
	slices.SortStableFunc(chains, func(a, b []chainRow) int { return cmp.Compare(a[0].Head, b[0].Head) })
	return slices.Concat(chains...)
}

// blockingSet is monitor blocking's result set: D2's columns, cycle after
// depth.
func blockingSet(rows []chainRow) output.Set {
	set := output.Set{Name: "blocking", Columns: []output.Column{
		{Name: "head", Type: "smallint"}, {Name: "depth", Type: "int"}, {Name: "cycle", Type: "bit"},
		{Name: "spid", Type: "smallint"}, {Name: "blocked_by", Type: "smallint", Nullable: true},
		{Name: "wait_type", Type: "nvarchar(60)", Nullable: true}, {Name: "wait_ms", Type: "bigint"},
		{Name: "wait_resource", Type: "nvarchar(256)", Nullable: true}, {Name: "login", Type: "nvarchar(128)"},
		{Name: "host", Type: "nvarchar(128)"}, {Name: "program", Type: "nvarchar(128)"},
		{Name: "database", Type: "nvarchar(128)", Nullable: true}, {Name: "open_tran", Type: "int"},
		{Name: "text", Type: "nvarchar(max)", Nullable: true},
	}}
	for _, r := range rows {
		s := r.Session
		set.Rows = append(set.Rows, []*string{
			ptr(strconv.Itoa(r.Head)), ptr(strconv.Itoa(r.Depth)), bitCell(r.Cycle),
			ptr(strconv.Itoa(s.SessionID)), blockerCell(s.BlockingSessionID), textOrNull(s.WaitType),
			int64Cell(s.WaitTimeMS), textOrNull(s.WaitResource), ptr(s.LoginName), ptr(s.HostName),
			ptr(s.ProgramName), textOrNull(s.DatabaseName), ptr(strconv.Itoa(s.OpenTransactionCount)),
			textOrNull(sessionText(s)),
		})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}
