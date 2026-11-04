package cmd

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// The server log options (D7).
const (
	optLines   = "lines"
	optSearch  = "search"
	optSince   = "since"
	optArchive = "archive"
	optAgent   = "agent"
)

// defaultLines is --lines' default.
const defaultLines = 100

// maxSearches is how many --search strings xp_readerrorlog takes.
const maxSearches = 2

// sinceLayouts are the forms --since takes, all zoneless.
var sinceLayouts = []string{"2006-01-02", "2006-01-02T15:04", "2006-01-02T15:04:05"}

func serverLogCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"server", "log"},
		Summary: "Show the SQL Server error log's latest entries, or the Agent log's, filtered by text and time",
		Description: "One row per log entry, oldest first, read by xp_readerrorlog, which filters on the server. " +
			"--search keeps the entries whose text holds the string, case-insensitively; given twice, both " +
			"(AND, xp_readerrorlog's own meaning, not OR). --since keeps the entries from that moment on, " +
			"YYYY-MM-DD[THH:MM[:SS]], read as the server's local time: the log records no time zone, so a Z or " +
			"an offset is refused (exit 2), and date is shown the same way, with no zone. --lines keeps the last " +
			"that many of the matching entries (default 100, 0 for every one). --archive reads an older file " +
			"(0, the default, is the current log; 1 the one before it), one file per run: an entry older than " +
			"the file is not in it, so a --since before the file's start needs a higher --archive. --agent reads " +
			"the SQL Server Agent log instead, its level column (1 error, 2 warning, 3 information) in place of " +
			"process. A sysadmin reads through xp_readerrorlog itself; any other login through sp_readerrorlog, " +
			"which needs securityadmin, VIEW SERVER STATE or (SQL Server 2022 on) VIEW ANY ERROR LOG — without " +
			"one of them the command fails, exit 9 — and, the procedure taking no dates, the whole file is read " +
			"before --since applies. An archive that does not exist is exit 8.",
		Options: append(ConnectOptions(),
			spec.Option{Name: optLines, Type: spec.TypeInt, Default: strconv.Itoa(defaultLines),
				Help: "Keep the last this many matching entries; 0 for every one"},
			spec.Option{Name: optSearch, Type: spec.TypeRepeat,
				Help: "Only entries whose text holds this string (case-insensitive); up to twice, both must match"},
			spec.Option{Name: optSince, Type: spec.TypeString,
				Help: "Only entries from this server-local time on: YYYY-MM-DD[THH:MM[:SS]], no zone"},
			spec.Option{Name: optArchive, Type: spec.TypeInt, Default: "0",
				Help: "Read this log file: 0 the current one, 1 the one before it, and so on"},
			spec.Option{Name: optAgent, Type: spec.TypeBool, Help: "Read the SQL Server Agent log instead"},
		),
		Class: spec.ClassRead,
		Run:   d.serverLog,
		Examples: []string{
			"gossms server log --lines 50 -p prod-ro",
			"gossms server log --search Login --search failed --since 2026-10-27 -p prod-ro",
			"gossms server log --agent --archive 1 --lines 0 -p dev -o json",
		},
	}
}

// logRequest is server log's options, checked before connecting.
type logRequest struct {
	Type    gosmo.ErrorLogType
	Archive int
	Lines   int
	Search  gosmo.LogSearch
}

func readLogRequest(inv *spec.Invocation) (logRequest, error) {
	r := logRequest{Type: gosmo.ErrorLogSQLServer, Archive: inv.Values[optArchive].(int), Lines: inv.Values[optLines].(int)}
	if inv.Values[optAgent].(bool) {
		r.Type = gosmo.ErrorLogAgent
	}
	if r.Lines < 0 {
		return r, usageError(fmt.Sprintf("--lines: %d is negative", r.Lines),
			"Give the number of entries to keep, or --lines 0 for every one.")
	}
	if r.Archive < 0 {
		return r, usageError(fmt.Sprintf("--archive: %d is negative", r.Archive),
			"0 is the current log, 1 the one before it, and so on.")
	}
	searches, _ := inv.Values[optSearch].([]string)
	if len(searches) > maxSearches {
		return r, usageError(fmt.Sprintf("--search is given %d times; the log takes at most %d", len(searches), maxSearches),
			"Two strings must both match an entry; narrow with --since or --lines instead.")
	}
	for _, s := range searches {
		if s == "" {
			return r, usageError("--search is empty", "Give the text to look for, or leave --search out.")
		}
	}
	if len(searches) > 0 {
		r.Search.Text1 = searches[0]
	}
	if len(searches) > 1 {
		r.Search.Text2 = searches[1]
	}
	if since, _ := inv.Values[optSince].(string); since != "" {
		t, err := parseSince(since)
		if err != nil {
			return r, err
		}
		r.Search.From = t
	}
	return r, nil
}

// parseSince is --since as a server-local wall clock, carried in UTC as gosmo
// carries the log's dates. A zone is refused rather than converted: the log
// records none, and the server's own zone is not known here.
func parseSince(s string) (time.Time, error) {
	for _, layout := range sinceLayouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, nil
		}
	}
	if len(s) > len("2006-01-02") && (strings.HasSuffix(s, "Z") || strings.ContainsAny(s[len("2006-01-02"):], "+-")) {
		return time.Time{}, usageError(fmt.Sprintf("--since %q has a time zone; the error log records none", s),
			"Give the server's local time with no zone: YYYY-MM-DD[THH:MM[:SS]].")
	}
	return time.Time{}, usageError(fmt.Sprintf("--since %q is not a date", s),
		"Give YYYY-MM-DD, YYYY-MM-DDTHH:MM or YYYY-MM-DDTHH:MM:SS, the server's local time.")
}

func (d Deps) serverLog(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	r, err := readLogRequest(inv)
	if err != nil {
		return nil, err
	}
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	entries, err := cat.ReadLog(ctx, r.Type, r.Archive, r.Search)
	if err != nil {
		return res, logError(ctx, cat, r, err)
	}
	if r.Lines > 0 && len(entries) > r.Lines {
		entries = entries[len(entries)-r.Lines:]
	}
	res.Sets = []output.Set{logSet(entries, r.Type)}
	return res, nil
}

// logError is a failed log read's error. A refusal names the permissions
// sp_readerrorlog admits; a failed read of an archive that the server does
// not list is NOT_FOUND (xp_readerrorlog says only "cannot find the file").
func logError(ctx context.Context, cat Catalog, r logRequest, err error) error {
	if gosmo.IsPermissionDenied(err) {
		return output.NewError(output.CodePermissionDenied, err.Error(),
			"Reading the error log needs sysadmin, securityadmin, VIEW SERVER STATE or (SQL Server 2022 on) "+
				"VIEW ANY ERROR LOG. Use a login that has one, or have it granted.")
	}
	if r.Archive > 0 {
		if files, ferr := cat.EnumErrorLogs(ctx, r.Type); ferr == nil &&
			!slices.ContainsFunc(files, func(f *gosmo.ErrorLogFile) bool { return f.Number == r.Archive }) {
			hint := "The server keeps no archived " + r.Type.String() + " log."
			if n := len(files); n > 1 {
				hint = fmt.Sprintf("The server keeps the %s log archives 1 to %d.", r.Type, files[n-1].Number)
			}
			return output.NewError(output.CodeNotFound,
				fmt.Sprintf("%s log archive %d does not exist", r.Type, r.Archive), hint)
		}
	}
	return readError(err)
}

// logSet is entries as server log's result set: date, process (level on the
// Agent log), text.
func logSet(entries []*gosmo.ErrorLogEntry, t gosmo.ErrorLogType) output.Set {
	middle := output.Column{Name: "process", Type: "nvarchar(100)"}
	if t == gosmo.ErrorLogAgent {
		middle = output.Column{Name: "level", Type: "int"}
	}
	set := output.Set{Columns: []output.Column{{Name: "date", Type: "datetime"}, middle, {Name: "text", Type: "nvarchar(max)"}}}
	for _, e := range entries {
		src := e.Process
		if t == gosmo.ErrorLogAgent {
			src = strconv.Itoa(e.ErrorLevel)
		}
		set.Rows = append(set.Rows, []*string{ptr(gridTime(e.Date)), ptr(src), ptr(e.Text)})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}
