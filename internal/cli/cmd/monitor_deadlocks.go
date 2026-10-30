package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/fileutil"
)

// The monitor deadlocks options (D6). --since and --until are server log's
// and monitor querystore's names; here they are UTC.
const (
	optDeadlockFormat = "deadlock-format"
	optDeadlockFile   = "deadlock-file"
)

// --deadlock-format's values; the first is the default.
const (
	deadlockSummary = "summary"
	deadlockXML     = "xml"
)

// systemHealth is the event session every SQL Server runs from startup; it
// records each deadlock as an xml_deadlock_report event.
const (
	systemHealth        = "system_health"
	xmlDeadlockReport   = "xml_deadlock_report"
	deadlockDefaultSpan = 24 * time.Hour
)

func monitorDeadlocksCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"monitor", "deadlocks"},
		Summary: "List the deadlocks the system_health event session recorded: when, who was killed, what each process ran",
		Description: "Reads the xml_deadlock_report events of the system_health Extended Events session — its " +
			"event_file target, the server keeping only those events, or its ring_buffer when it has no file " +
			"target — newest first, --top deadlocks (default 20). --since and --until bound the window in " +
			"UTC, as the events record it: a time with Z or an offset (+01:00) is converted, a time with no " +
			"zone is taken as UTC; by default the 24 hours to the server's now. system_health keeps what its " +
			"files hold (on 2019 and later up to 10 files of 100 MB, shared with every other event it " +
			"records), so an old deadlock may be gone; and the server parses every event in them to find the " +
			"deadlocks, so on a busy server the read can take a minute or more. Set deadlocks: one row per deadlock, time_utc, " +
			"victims the victims' spids (none for an intra-query parallelism deadlock, which kills nobody), " +
			"processes the tasks in the cycle, databases and objects the ones involved. Set processes: one " +
			"row per task, n its deadlock's; a parallel plan's workers share a spid and differ by ecid; " +
			"statement is the innermost frame's text, or the batch the client sent. --deadlock-format xml " +
			"gives each deadlock graph whole, as fields deadlock1, deadlock2 … beside the deadlocks set, " +
			"instead of the processes set. --deadlock-file <dir> also writes each graph to <dir> as " +
			"deadlock_<yyyymmdd_hhmmss_mmm>.xdl (UTC), which SSMS opens as a graph; --force replaces an " +
			"existing file, and without it none is written. system_health not running, or holding no " +
			"target to read, is exit 8. Needs VIEW SERVER STATE (VIEW SERVER PERFORMANCE STATE from SQL " +
			"Server 2022): without it, exit 9.",
		Options: append(ConnectOptions(),
			spec.Option{Name: optSince, Type: spec.TypeString,
				Help: "Deadlocks from this time (YYYY-MM-DD[THH:MM[:SS]]), UTC unless it has Z or an offset; default 24 hours before --until"},
			spec.Option{Name: optUntil, Type: spec.TypeString,
				Help: "Deadlocks before this time, UTC unless it has Z or an offset; default the server's now"},
			spec.Option{Name: optTop, Type: spec.TypeInt, Default: strconv.Itoa(defaultTop),
				Help: "Keep this many deadlocks, the newest first"},
			spec.Option{Name: optDeadlockFormat, Type: spec.TypeEnum, Enum: []string{deadlockSummary, deadlockXML},
				Default: deadlockSummary, Help: "summary: the deadlocks and their processes; xml: the deadlocks and each graph"},
			spec.Option{Name: optDeadlockFile, Type: spec.TypeString,
				Help: "Also write each deadlock graph to this directory as an .xdl file; --force replaces one"},
		),
		Class: spec.ClassRead,
		Run:   d.monitorDeadlocks,
		Examples: []string{
			"gossms monitor deadlocks -p prod-ro",
			"gossms monitor deadlocks --since 2026-10-27T00:00:00Z -p prod-ro",
			"gossms monitor deadlocks --top 1 --deadlock-format xml --deadlock-file ./deadlocks -p prod-ro",
		},
	}
}

// deadlockRequest is monitor deadlocks' options, checked.
type deadlockRequest struct {
	Since, Until time.Time // zero: the default
	Top          int
	Format       string
	Dir          string // --deadlock-file; empty when not given
	Force        bool
}

// deadlockTimeLayouts are --since and --until's zoneless forms (UTC) and,
// after them, the zoned ones.
var deadlockTimeLayouts = slices.Concat(sinceLayouts, qsZonedLayouts)

func parseDeadlockTime(opt, s string) (time.Time, error) {
	for _, layout := range deadlockTimeLayouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, usageError(fmt.Sprintf("--%s %q is not a date", opt, s),
		"Give YYYY-MM-DD, YYYY-MM-DDTHH:MM or YYYY-MM-DDTHH:MM:SS in UTC, or add Z or an offset (+01:00).")
}

func readDeadlockRequest(inv *spec.Invocation) (deadlockRequest, error) {
	r := deadlockRequest{Top: inv.Values[optTop].(int), Format: inv.Values[optDeadlockFormat].(string)}
	r.Force, _ = inv.Values[optForce].(bool)
	if r.Top < 1 {
		return r, usageError(fmt.Sprintf("--top: %d is not a number of deadlocks", r.Top),
			"Give the number of deadlocks to keep, 1 or more.")
	}
	var err error
	if s, _ := inv.Values[optSince].(string); s != "" {
		if r.Since, err = parseDeadlockTime(optSince, s); err != nil {
			return r, err
		}
	}
	if s, _ := inv.Values[optUntil].(string); s != "" {
		if r.Until, err = parseDeadlockTime(optUntil, s); err != nil {
			return r, err
		}
	}
	if !r.Since.IsZero() && !r.Until.IsZero() && !r.Since.Before(r.Until) {
		return r, emptyDeadlockWindow(r.Since, r.Until)
	}
	if inv.Given[optDeadlockFile] {
		// Checked before connecting, as --plan-file is; the files' own
		// names are known only after the read.
		r.Dir = inv.Values[optDeadlockFile].(string)
		if r.Dir == "" {
			return r, usageError("--"+optDeadlockFile+": empty path", "")
		}
		if fi, err := os.Stat(r.Dir); err == nil && !fi.IsDir() {
			return r, usageError(fmt.Sprintf("--%s %s: not a directory", optDeadlockFile, r.Dir),
				"Give a directory to write the .xdl files to; it is created if it does not exist.")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return r, usageError(fmt.Sprintf("--%s %s: %v", optDeadlockFile, r.Dir, err), "")
		}
	}
	return r, nil
}

func emptyDeadlockWindow(since, until time.Time) *output.Error {
	return usageError(fmt.Sprintf("the window %s to %s (UTC) is empty", gridTime(since), gridTime(until)),
		"Give a --since before --until.")
}

// deadlock is one xml_deadlock_report event, decoded.
type deadlock struct {
	Time   time.Time // UTC
	XML    string    // the <deadlock> graph, as the server wrote it
	Report *gosmo.DeadlockReport
}

func (d Deps) monitorDeadlocks(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	r, err := readDeadlockRequest(inv)
	if err != nil {
		return nil, err
	}

	res := &output.Result{}
	closeConn, cat, err := d.connectServerState(ctx, inv, res, "monitor deadlocks")
	if err != nil {
		return res, err
	}
	defer closeConn()

	if r.Until.IsZero() {
		now, err := cat.CurrentTime(ctx)
		if err != nil {
			return res, readError(err)
		}
		r.Until = now.UTC()
	}
	if r.Since.IsZero() {
		r.Since = r.Until.Add(-deadlockDefaultSpan)
	}
	if !r.Since.Before(r.Until) {
		return res, emptyDeadlockWindow(r.Since, r.Until)
	}

	events, source, err := readDeadlockEvents(ctx, cat)
	if err != nil {
		return res, err
	}
	found := decodeDeadlocks(events, r.Since, r.Until, &res.Warnings)
	inWindow := len(found)
	if len(found) > r.Top {
		found = found[:r.Top]
	}

	res.Fields = []output.Field{
		{Key: "source", Value: source},
		{Key: "from", Value: gridTime(r.Since)}, {Key: "to", Value: gridTime(r.Until)},
		{Key: "deadlocksInWindow", Value: strconv.Itoa(inWindow)},
	}
	names := deadlockDatabaseNames(ctx, cat, found)
	res.Sets = []output.Set{deadlocksSet(found, names)}
	if r.Format == deadlockXML {
		for i, dl := range found {
			res.Fields = append(res.Fields, output.Field{Key: "deadlock" + strconv.Itoa(i+1), Value: dl.XML, Verbatim: true})
		}
	} else {
		res.Sets = append(res.Sets, deadlockProcessesSet(found, names))
	}
	if r.Dir != "" {
		if err := writeDeadlockFiles(r, found); err != nil {
			res.Errors = append(res.Errors, err)
		} else if len(found) > 0 {
			res.Fields = append(res.Fields, output.Field{Key: "deadlockFile", Value: r.Dir})
		}
	}
	return res, nil
}

// readDeadlockEvents reads system_health's xml_deadlock_report events from
// its event_file target, the server filtering out every other event, or from
// its ring_buffer when it has no file. source names the target read.
func readDeadlockEvents(ctx context.Context, cat Catalog) (events []gosmo.XEvent, source string, err error) {
	es, err := cat.EventSessionByName(ctx, systemHealth)
	if errors.Is(err, gosmo.ErrNotFound) {
		return nil, "", output.NewError(output.CodeNotFound, "the system_health event session does not exist",
			"It is created with the server; it may have been dropped. It can be recreated from the server's "+
				"Install\\u_tables.sql script.")
	}
	if err != nil {
		return nil, "", readError(err)
	}
	if !es.IsRunning {
		return nil, "", output.NewError(output.CodeNotFound, "the system_health event session is not running",
			"Start it: ALTER EVENT SESSION system_health ON SERVER STATE = START.")
	}
	if _, ok := es.Target(gosmo.XETargetEventFile); ok {
		pattern, err := es.EventFilePattern()
		if err != nil {
			return nil, "", readError(err)
		}
		events, _, err = cat.ReadFileEvents(ctx, pattern, gosmo.EventFileCursor{}, 0, []string{xmlDeadlockReport})
		if err != nil {
			return nil, "", readError(err)
		}
		return events, gosmo.XETargetEventFile, nil
	}
	if _, ok := es.Target(gosmo.XETargetRingBuffer); ok {
		data, err := cat.ReadRingBuffer(ctx, es)
		if err != nil {
			return nil, "", readError(err)
		}
		for _, e := range data.Events {
			if e.Name == xmlDeadlockReport {
				events = append(events, e)
			}
		}
		return events, gosmo.XETargetRingBuffer, nil
	}
	return nil, "", output.NewError(output.CodeNotFound, "the system_health event session has no event_file or ring_buffer target",
		"Its targets have been altered; add one: ALTER EVENT SESSION system_health ON SERVER ADD TARGET package0.ring_buffer.")
}

// decodeDeadlocks is events' deadlocks in [since, until), newest first. A
// report that does not decode is a warning, not the
// command's failure: the others still answer.
func decodeDeadlocks(events []gosmo.XEvent, since, until time.Time, warnings *[]string) []deadlock {
	var out []deadlock
	for _, e := range events {
		if e.Name != xmlDeadlockReport || e.Timestamp.Before(since) || !e.Timestamp.Before(until) {
			continue
		}
		at := e.Timestamp.UTC()
		f, ok := e.Field("xml_report")
		if !ok || !f.IsXML {
			*warnings = append(*warnings, fmt.Sprintf("the deadlock at %s (UTC) has no xml_report; left out.", gridTime(at)))
			continue
		}
		rep, err := gosmo.DecodeDeadlockReport([]byte(f.Value))
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("the deadlock at %s (UTC) could not be read (%v); left out.", gridTime(at), err))
			continue
		}
		out = append(out, deadlock{Time: at, XML: f.Value, Report: rep})
	}
	// Stable, so two deadlocks in the same millisecond keep the file's
	// order, reversed with the rest.
	slices.Reverse(out)
	slices.SortStableFunc(out, func(a, b deadlock) int { return b.Time.Compare(a.Time) })
	return out
}

// deadlockDatabaseNames maps the database ids the processes name without a
// name — 2017 RTM writes none (gosmo.DeadlockProcess.Database) — to the
// catalog's names. It reads the catalog only when one is missing, and a
// failed read leaves the names out rather than failing the command.
func deadlockDatabaseNames(ctx context.Context, cat Catalog, found []deadlock) map[int]string {
	missing := false
	for _, dl := range found {
		for _, p := range dl.Report.Processes {
			missing = missing || (p.Database == "" && p.DatabaseID > 0)
		}
	}
	if !missing {
		return nil
	}
	dbs, err := cat.Databases(ctx)
	if err != nil {
		return nil
	}
	names := make(map[int]string, len(dbs))
	for _, db := range dbs {
		names[db.ID] = db.Name
	}
	return names
}

// processDatabase is p's database name: the report's, else the catalog's
// for its id.
func processDatabase(p *gosmo.DeadlockProcess, names map[int]string) string {
	if p.Database != "" {
		return p.Database
	}
	return names[p.DatabaseID]
}

// listCell is values joined with ", ", NULL when there are none.
func listCell(values []string) *string { return textOrNull(strings.Join(values, ", ")) }

// appendNew appends s to list unless it is empty or already there.
func appendNew(list []string, s string) []string {
	if s == "" || slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

// deadlocksSet is monitor deadlocks' deadlocks set: one row per deadlock.
func deadlocksSet(found []deadlock, names map[int]string) output.Set {
	set := output.Set{Name: "deadlocks", Columns: []output.Column{
		{Name: "n", Type: "int"}, {Name: "time_utc", Type: "datetime"},
		{Name: "victims", Type: "nvarchar(max)", Nullable: true}, {Name: "processes", Type: "int"},
		{Name: "databases", Type: "nvarchar(max)", Nullable: true}, {Name: "objects", Type: "nvarchar(max)", Nullable: true},
	}}
	for i, dl := range found {
		var victims, databases, objects []string
		for _, id := range dl.Report.Victims {
			if p, ok := dl.Report.Process(id); ok {
				victims = appendNew(victims, strconv.Itoa(p.SessionID))
			}
		}
		for j := range dl.Report.Processes {
			databases = appendNew(databases, processDatabase(&dl.Report.Processes[j], names))
		}
		for _, res := range dl.Report.Resources {
			objects = appendNew(objects, res.ObjectName)
		}
		set.Rows = append(set.Rows, []*string{
			ptr(strconv.Itoa(i + 1)), ptr(gridTime(dl.Time)), listCell(victims),
			ptr(strconv.Itoa(len(dl.Report.Processes))), listCell(databases), listCell(objects),
		})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// deadlockProcessesSet is monitor deadlocks' processes set: one row per task
// of each deadlock, in the report's order. The server ends a page's
// wait_resource with a space, trimmed here. ecid is beside spid (not in D6):
// a parallel plan's workers share the spid.
func deadlockProcessesSet(found []deadlock, names map[int]string) output.Set {
	set := output.Set{Name: "processes", Columns: []output.Column{
		{Name: "n", Type: "int"}, {Name: "spid", Type: "smallint"}, {Name: "ecid", Type: "int"},
		{Name: "victim", Type: "bit"}, {Name: "login", Type: "nvarchar(128)", Nullable: true},
		{Name: "host", Type: "nvarchar(128)", Nullable: true}, {Name: "program", Type: "nvarchar(128)", Nullable: true},
		{Name: "database", Type: "nvarchar(128)", Nullable: true}, {Name: "isolation", Type: "nvarchar(60)", Nullable: true},
		{Name: "lock_mode", Type: "nvarchar(60)", Nullable: true}, {Name: "wait_resource", Type: "nvarchar(256)", Nullable: true},
		{Name: "transaction", Type: "nvarchar(128)", Nullable: true}, {Name: "statement", Type: "nvarchar(max)", Nullable: true},
	}}
	for i, dl := range found {
		for j := range dl.Report.Processes {
			p := &dl.Report.Processes[j]
			victim := "0"
			if p.Victim {
				victim = "1"
			}
			set.Rows = append(set.Rows, []*string{
				ptr(strconv.Itoa(i + 1)), ptr(strconv.Itoa(p.SessionID)), ptr(strconv.Itoa(p.ExecutionContextID)),
				ptr(victim), textOrNull(p.Login), textOrNull(p.Host), textOrNull(p.ClientApp),
				textOrNull(processDatabase(p, names)), textOrNull(p.IsolationLevel), textOrNull(p.LockMode),
				textOrNull(strings.TrimSpace(p.WaitResource)), textOrNull(p.TransactionName), textOrNull(p.Statement()),
			})
		}
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// deadlockFileName is dl's .xdl name: its UTC time to the millisecond, with
// no colon (Windows refuses one), so a second run names the same deadlock
// the same and --force is what replaces it.
func deadlockFileName(t time.Time) string {
	t = t.UTC()
	return fmt.Sprintf("deadlock_%s_%03d.xdl", t.Format("20060102_150405"), t.Nanosecond()/int(time.Millisecond))
}

// writeDeadlockFiles writes each deadlock's graph to r.Dir, creating it.
// Every name is checked before any file is written, so a refusal writes
// none. Two deadlocks in one millisecond get _2, _3 … after the first.
func writeDeadlockFiles(r deadlockRequest, found []deadlock) *output.Error {
	if len(found) == 0 {
		return nil
	}
	paths := make([]string, len(found))
	seen := map[string]int{}
	for i, dl := range found {
		name := deadlockFileName(dl.Time)
		seen[name]++
		if n := seen[name]; n > 1 {
			name = strings.TrimSuffix(name, ".xdl") + "_" + strconv.Itoa(n) + ".xdl"
		}
		paths[i] = filepath.Join(r.Dir, name)
		if _, err := os.Stat(paths[i]); err == nil && !r.Force {
			return usageError(fmt.Sprintf("--%s: %s exists; no file was written", optDeadlockFile, paths[i]),
				"Add --force to replace it, or choose another directory.")
		}
	}
	if err := os.MkdirAll(r.Dir, 0o755); err != nil {
		return output.NewError(output.CodeGeneralError, fmt.Sprintf("--%s %s: %v", optDeadlockFile, r.Dir, err), "")
	}
	for i, dl := range found {
		if err := fileutil.WriteAtomic(paths[i], []byte(dl.XML), 0o644); err != nil {
			return output.NewError(output.CodeGeneralError, fmt.Sprintf("writing %s: %v", paths[i], err), "")
		}
	}
	return nil
}
