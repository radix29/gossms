package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// The Database Mail log in the Log Viewer: gosmo's own family, one "file"
// that cannot be cycled. The Recycle cell purges it instead — its own label,
// its own rights, and sysmail_delete_log_sp rather than a cycle.

// mailLogResponses script the mail log's enumeration and read, so the Refresh
// a purge ends in has something to come back to. The MAX read is first: the
// entry read's FROM would match it too.
func mailLogResponses() []fakeResponse {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	return []fakeResponse{
		{match: "SELECT MAX(log_date) FROM msdb.dbo.sysmail_event_log", cols: 1, rows: [][]driver.Value{{at}}},
		{match: "FROM   msdb.dbo.sysmail_event_log", cols: 3, rows: [][]driver.Value{
			{at.Add(-2 * time.Hour), "information", "DatabaseMail process is started"},
			{at.Add(-time.Hour), "error", "Could not connect to mail server."},
			{at, "information", "DatabaseMail process is shutting down"},
		}},
	}
}

// newMailLogTestViewer is a viewer on the Database Mail log, loaded through a
// real App so the prompt, the confirmation and the purge all run for real.
func newMailLogTestViewer(t *testing.T) (*App, *LogViewer, *fakeInstance) {
	t.Helper()
	a := newTestApp()
	sc, inst := newFakeConn(t, mailLogResponses()...)
	sc.Opts.Server = "FAKE\\SQL"
	a.connections = append(a.connections, sc)
	lv := newTestLogViewer()
	lv.app, lv.conn = a, sc
	lv.ShowLog(gosmo.ErrorLogDatabaseMail, 0)
	drainUntil(t, a, func() bool { return !lv.busy }, "the mail log to load")
	if len(lv.shown) != 3 {
		t.Fatalf("loaded %d mail log rows, want 3", len(lv.shown))
	}
	return a, lv, inst
}

// A login gosmo reads the views for sees only its own items' events, and the
// status line says so — an almost empty log otherwise reads as a quiet server
// (docs/decisions.md). A login reading the whole log, and one whose visibility could not be
// read, get no note.
func TestLogViewerSaysWhenTheMailLogIsOwnOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		vis  []fakeResponse
		note bool
	}{
		{"role member", []fakeResponse{mailVisibilityAnswer(false, false)}, true},
		{"base-table reader", []fakeResponse{mailVisibilityAnswer(true, true)}, false},
		{"visibility unread", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp()
			sc, _ := newFakeConn(t, append(tc.vis, mailLogResponses()...)...)
			a.connections = append(a.connections, sc)
			lv := newTestLogViewer()
			lv.app, lv.conn = a, sc
			lv.ShowLog(gosmo.ErrorLogDatabaseMail, 0)
			drainUntil(t, a, func() bool { return !lv.busy }, "the mail log to load")
			status := lv.grid.Status()
			if got := strings.Contains(status, "only your mail items' entries"); got != tc.note {
				t.Errorf("status %q: own-only note = %v, want %v", status, got, tc.note)
			}
			if !strings.Contains(status, "3 entries") {
				t.Errorf("status %q: want the entry count kept", status)
			}
		})
	}
}

func TestLogViewerOffersDeleteInsteadOfRecycleOnTheMailLog(t *testing.T) {
	lv := newTestLogViewer()
	if got := lv.tools.Cells[logToolRecycle].Label; got != "Recycle..." {
		t.Errorf("SQL Server log: cell = %q, want Recycle...", got)
	}
	lv.logType = gosmo.ErrorLogDatabaseMail
	lv.refreshToolLabels()
	if got := lv.tools.Cells[logToolRecycle].Label; got != "Delete..." {
		t.Errorf("Database Mail log: cell = %q, want Delete...", got)
	}
}

// Purging the mail log is msdb permission, not CONTROL SERVER (W8): a login
// in msdb db_owner and denied CONTROL SERVER may delete it, and still may not
// cycle the SQL Server log from the same cell.
func TestLogViewerMailLogDeleteIsGatedOnMsdbRights(t *testing.T) {
	a := newTestApp()
	owner, _ := newFakeConn(t, capabilityResponsesWithRoles(true, nil, []string{"CONTROL SERVER"}, []string{"db_owner"}, nil)...)
	owner.ProbeCapabilities()
	outsider, _ := newFakeConn(t, capabilityResponsesWithRoles(true, nil, []string{"CONTROL SERVER"}, nil, []string{"db_owner"})...)
	outsider.ProbeCapabilities()
	// The msdb answers are cached the way selecting the Database Mail node
	// primes them; unprobed, a membership right fails open.
	for _, sc := range []*db.ServerConn{owner, outsider} {
		sc.DatabaseCapabilities(context.Background(), "msdb")
	}
	a.connections = append(a.connections, owner, outsider)

	lv := newTestLogViewer()
	lv.app, lv.conn = a, owner
	lv.logType = gosmo.ErrorLogDatabaseMail
	if lv.toolDisabled(logToolRecycle) {
		t.Error("Delete was withheld from msdb db_owner")
	}
	lv.logType = gosmo.ErrorLogSQLServer
	if !lv.toolDisabled(logToolRecycle) {
		t.Error("msdb db_owner may Recycle the SQL Server log — the mail rights leaked to the other family")
	}

	lv.conn, lv.logType = outsider, gosmo.ErrorLogDatabaseMail
	if lv.runTool(logToolRecycle) {
		t.Fatal("Delete ran for a login in neither msdb db_owner nor CONTROL SERVER")
	}
	if a.promptDialog.Visible() {
		t.Error("a withheld Delete still asked for a date")
	}
	if st := lv.grid.Status(); !strings.Contains(st, "db_owner") || !strings.Contains(st, "CONTROL SERVER") {
		t.Errorf("status = %q, want both alternatives named", st)
	}
}

// The whole path: the cell, the prompt pre-filled from the selected row, the
// confirmation, the purge bounded at that time, and the reload.
func TestLogViewerDeletesMailLogEntriesBeforeTheSelectedRow(t *testing.T) {
	a, lv, inst := newMailLogTestViewer(t)
	// Newest first: row 1 is the 08:00 error, not the first row.
	lv.grid.SetSelectedRow(1)

	if !lv.runTool(logToolRecycle) {
		t.Fatal("the Delete cell refused to run on an idle toolbar")
	}
	if !a.promptDialog.Visible() {
		t.Fatal("Delete did not ask what to delete")
	}
	if got := a.promptDialog.Value(); got != "2026-10-01 08:00:00" {
		t.Errorf("prompt pre-filled with %q, want the selected row's time", got)
	}
	a.promptDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if !a.confirmDialog.Visible() {
		t.Fatal("Delete did not ask for confirmation")
	}
	answerConfirm(t, a, true)
	drainUntil(t, a, func() bool { return !lv.busy && len(inst.Statements()) > 0 }, "the purge to finish")

	want := "EXEC msdb.dbo.sysmail_delete_log_sp @logged_before = '2026-10-01T08:00:00.000'"
	if got := inst.Statements(); len(got) != 1 || got[0] != want {
		t.Fatalf("statements = %q, want exactly [%q]", got, want)
	}
}

func TestLogViewerDeletesTheWholeMailLogOnAll(t *testing.T) {
	a, lv, inst := newMailLogTestViewer(t)
	lv.runTool(logToolRecycle)
	answerPrompt(t, a, "ALL")
	answerConfirm(t, a, true)
	drainUntil(t, a, func() bool { return !lv.busy && len(inst.Statements()) > 0 }, "the purge to finish")
	want := "EXEC msdb.dbo.sysmail_delete_log_sp"
	if got := inst.Statements(); len(got) != 1 || got[0] != want {
		t.Fatalf("statements = %q, want exactly [%q]", got, want)
	}
}

// A value that is neither a time nor "all" keeps the prompt open with the
// reason, and nothing is asked or sent.
func TestLogViewerMailLogDeleteRefusesAnUnreadableTime(t *testing.T) {
	a, lv, inst := newMailLogTestViewer(t)
	lv.runTool(logToolRecycle)
	answerPrompt(t, a, "last week")
	if !a.promptDialog.Visible() {
		t.Error("the prompt closed on a value it cannot read")
	}
	if a.confirmDialog.Visible() || len(inst.Statements()) != 0 {
		t.Error("an unreadable time went on to the confirmation")
	}
	if lv.busy {
		t.Error("the toolbar was latched busy by a prompt still open")
	}
}

func TestParseMailLogCutoff(t *testing.T) {
	for in, want := range map[string]time.Time{
		"all":              {},
		" All ":            {},
		"2026-09-30":       time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
		"2026-09-30 14:05": time.Date(2026, 9, 30, 14, 5, 0, 0, time.UTC),
	} {
		got, err := parseMailLogCutoff(in)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseMailLogCutoff(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"yesterday", "30/09/2026"} {
		if _, err := parseMailLogCutoff(in); err == nil {
			t.Errorf("parseMailLogCutoff(%q) accepted it", in)
		}
	}
}
