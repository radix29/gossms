package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// logCatalog serves entries for any read, recording the request; readErr
// fails the read, files is what EnumErrorLogs lists (enumErr fails it).
type logCatalog struct {
	Catalog
	entries []*gosmo.ErrorLogEntry
	readErr error
	files   []*gosmo.ErrorLogFile
	enumErr error

	gotType    gosmo.ErrorLogType
	gotArchive int
	gotSearch  gosmo.LogSearch
}

func (c *logCatalog) ReadLog(_ context.Context, t gosmo.ErrorLogType, archive int, search gosmo.LogSearch) ([]*gosmo.ErrorLogEntry, error) {
	c.gotType, c.gotArchive, c.gotSearch = t, archive, search
	return c.entries, c.readErr
}

func (c *logCatalog) EnumErrorLogs(context.Context, gosmo.ErrorLogType) ([]*gosmo.ErrorLogFile, error) {
	return c.files, c.enumErr
}

// runLog runs server log <argv> over cat, an ad-hoc connection; dials counts
// the connects.
func runLog(t *testing.T, cat *logCatalog, argv ...string) (res *output.Result, dials int, err error) {
	t.Helper()
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
			dials++
			return &db.ServerConn{}, nil
		},
		Catalog: func(*db.ServerConn) Catalog { return cat },
	}
	tree := &spec.Tree{Commands: ServerCommands(d)}
	inv, perr := args.Parse(tree, append([]string{"server", "log", "--server", "srv", "--auth", "windows"}, argv...), args.Options{})
	if perr != nil {
		t.Fatalf("%q: %v", argv, perr)
	}
	res, err = inv.Command.Run(context.Background(), inv)
	return res, dials, err
}

// logEntries is n SQL Server log entries a second apart from 10:00:00.250,
// text "e0", "e1", ….
func logEntries(n int) []*gosmo.ErrorLogEntry {
	var es []*gosmo.ErrorLogEntry
	for i := range n {
		es = append(es, &gosmo.ErrorLogEntry{
			Date:    time.Date(2026, 10, 29, 10, 0, i, 250e6, time.UTC),
			Process: "spid" + fmt.Sprint(i), Text: fmt.Sprintf("e%d", i), ErrorLevel: i%3 + 1,
		})
	}
	return es
}

func TestServerLogRows(t *testing.T) {
	cat := &logCatalog{entries: logEntries(3)}
	res, _, err := runLog(t, cat)
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	if got := []string{set.Columns[0].Name, set.Columns[1].Name, set.Columns[2].Name}; !slices.Equal(got, []string{"date", "process", "text"}) {
		t.Errorf("columns %q", got)
	}
	want := [][]string{
		{"2026-10-29 10:00:00.250", "spid0", "e0"},
		{"2026-10-29 10:00:01.250", "spid1", "e1"},
		{"2026-10-29 10:00:02.250", "spid2", "e2"},
	}
	if got := setRows(set); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("rows %q, want %q", got, want)
	}
	// The defaults: the current SQL Server log, no search.
	if cat.gotType != gosmo.ErrorLogSQLServer || cat.gotArchive != 0 || cat.gotSearch != (gosmo.LogSearch{}) {
		t.Errorf("read %v %d %+v", cat.gotType, cat.gotArchive, cat.gotSearch)
	}
}

func TestServerLogLinesIsATail(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want []string
	}{
		{nil, []string{"e50", "e149"}}, // the default 100: e50…e149
		{[]string{"--lines", "2"}, []string{"e148", "e149"}},
		{[]string{"--lines", "0"}, []string{"e0", "e149"}},
		{[]string{"--lines", "500"}, []string{"e0", "e149"}},
	} {
		res, _, err := runLog(t, &logCatalog{entries: logEntries(150)}, tc.argv...)
		if err != nil {
			t.Fatal(err)
		}
		rows := setRows(res.Sets[0])
		if got := []string{rows[0][2], rows[len(rows)-1][2]}; !slices.Equal(got, tc.want) {
			t.Errorf("%q: first and last %q, want %q", tc.argv, got, tc.want)
		}
		if res.Sets[0].TotalRowCount != len(rows) {
			t.Errorf("%q: TotalRowCount %d, rows %d", tc.argv, res.Sets[0].TotalRowCount, len(rows))
		}
	}
}

func TestServerLogSearchAndSince(t *testing.T) {
	cat := &logCatalog{}
	if _, _, err := runLog(t, cat, "--search", "Login failed", "--search", "sa", "--since", "2026-10-27T08:30"); err != nil {
		t.Fatal(err)
	}
	want := gosmo.LogSearch{Text1: "Login failed", Text2: "sa", From: time.Date(2026, 10, 27, 8, 30, 0, 0, time.UTC)}
	if cat.gotSearch != want {
		t.Errorf("search %+v, want %+v", cat.gotSearch, want)
	}
	for in, want := range map[string]time.Time{
		"2026-10-27":          time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC),
		"2026-10-27T08:30:15": time.Date(2026, 10, 27, 8, 30, 15, 0, time.UTC),
	} {
		if _, _, err := runLog(t, cat, "--since", in); err != nil || !cat.gotSearch.From.Equal(want) {
			t.Errorf("--since %s: %v, from %v", in, err, cat.gotSearch.From)
		}
	}
}

func TestServerLogAgent(t *testing.T) {
	cat := &logCatalog{entries: logEntries(2)}
	res, _, err := runLog(t, cat, "--agent", "--archive", "3")
	if err != nil {
		t.Fatal(err)
	}
	if cat.gotType != gosmo.ErrorLogAgent || cat.gotArchive != 3 {
		t.Errorf("read %v %d", cat.gotType, cat.gotArchive)
	}
	set := res.Sets[0]
	if set.Columns[1] != (output.Column{Name: "level", Type: "int"}) {
		t.Errorf("middle column %+v", set.Columns[1])
	}
	if got := setRows(set); got[0][1] != "1" || got[1][1] != "2" {
		t.Errorf("levels %q", got)
	}
}

// TestServerLogArgumentErrors: each is exit 2 before connecting.
func TestServerLogArgumentErrors(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"--lines", "-1"}, "--lines"},
		{[]string{"--archive", "-1"}, "--archive"},
		{[]string{"--search", "a", "--search", "b", "--search", "c"}, "at most 2"},
		{[]string{"--search", ""}, "empty"},
		{[]string{"--since", "2026-10-27T00:00:00Z"}, "time zone"},
		{[]string{"--since", "2026-10-27T00:00:00+02:00"}, "time zone"},
		{[]string{"--since", "2026-10-27T00:00-05:00"}, "time zone"},
		{[]string{"--since", "yesterday"}, "not a date"},
		{[]string{"--since", "2026-10-27 08:00"}, "not a date"},
	} {
		_, dials, err := runLog(t, &logCatalog{}, tc.argv...)
		if errCode(err) != output.CodeUsageError || !strings.Contains(err.Error(), tc.want) || dials != 0 {
			t.Errorf("%q: %v (dials %d), want a usage error with %q", tc.argv, err, dials, tc.want)
		}
	}
}

func TestServerLogErrors(t *testing.T) {
	denied := fmt.Errorf("gosmo: read SQL Server error log 0: %w",
		mssql.Error{Number: 27219, Class: 16, Message: "Caller does not have permissions to execute the stored procedure."})
	_, _, err := runLog(t, &logCatalog{readErr: denied})
	if errCode(err) != output.CodePermissionDenied || !strings.Contains(err.(*output.Error).Hint, "VIEW ANY ERROR LOG") {
		t.Errorf("refused: %v", err)
	}

	noFile := fmt.Errorf("gosmo: read SQL Server error log 9: %w",
		mssql.Error{Number: 22004, Class: 16, Message: "xp_readerrorlog() returned error 2, 'The system cannot find the file specified.'"})
	files := []*gosmo.ErrorLogFile{{Number: 0}, {Number: 1}, {Number: 2}, {Number: 6}}
	_, _, err = runLog(t, &logCatalog{readErr: noFile, files: files}, "--archive", "9")
	if e, ok := err.(*output.Error); errCode(err) != output.CodeNotFound || !ok || !strings.Contains(e.Hint, "1 to 6") {
		t.Errorf("missing archive: %v", err)
	}
	_, _, err = runLog(t, &logCatalog{readErr: noFile, files: files[:1]}, "--archive", "1")
	if e, ok := err.(*output.Error); errCode(err) != output.CodeNotFound || !ok || !strings.Contains(e.Hint, "no archived") {
		t.Errorf("no archives: %v", err)
	}
	// An archive the server lists, or a list that cannot be read, keeps the
	// read's own error.
	for name, cat := range map[string]*logCatalog{
		"listed":      {readErr: noFile, files: files},
		"enum failed": {readErr: noFile, enumErr: fmt.Errorf("x")},
	} {
		_, _, err = runLog(t, cat, "--archive", "2")
		if errCode(err) != output.CodeSQLError {
			t.Errorf("%s: %v, want the read's SQL error", name, err)
		}
	}
	// The current log is never looked up as an archive.
	if _, _, err = runLog(t, &logCatalog{readErr: noFile, files: files}); errCode(err) != output.CodeSQLError {
		t.Errorf("archive 0: %v", err)
	}
}
