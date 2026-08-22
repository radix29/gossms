package tui

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
)

func newTestLogSearchDialog(t *testing.T) *LogSearchDialog {
	t.Helper()
	return NewLogSearchDialog(newTestApp())
}

// Every field on this dialog ends up as an argument to xp_readerrorlog, so
// what the buttons deliver is the whole behaviour.
func TestLogSearchDialogDeliversWhatWasTyped(t *testing.T) {
	d := newTestLogSearchDialog(t)
	var got *gosmo.LogSearch
	d.ShowLogSearch(gosmo.LogSearch{}, func(s gosmo.LogSearch) { got = &s })

	d.fText1.SetValue("login failed")
	d.fText2.SetValue("sa")
	d.fFrom.SetValue("2026-08-20")
	d.fTo.SetValue("2026-08-21 13:30:00")
	d.pressButton(0) // Search

	if got == nil {
		t.Fatal("Search delivered nothing")
	}
	if got.Text1 != "login failed" || got.Text2 != "sa" {
		t.Errorf("texts = %q / %q", got.Text1, got.Text2)
	}
	// A bare date is midnight — "From 2026-08-20" means the whole day.
	if want := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC); !got.From.Equal(want) {
		t.Errorf("From = %v, want %v", got.From, want)
	}
	if want := time.Date(2026, 8, 21, 13, 30, 0, 0, time.UTC); !got.To.Equal(want) {
		t.Errorf("To = %v, want %v", got.To, want)
	}
	if d.Visible() {
		t.Error("the dialog stayed open after Search")
	}
}

// Clear has to empty the fields as well as deliver the empty search: the
// dialog is seeded from what is in force, so fields left filled would put the
// cleared search straight back on the next Enter.
func TestLogSearchDialogClearEmptiesTheFieldsToo(t *testing.T) {
	d := newTestLogSearchDialog(t)
	var got *gosmo.LogSearch
	d.ShowLogSearch(gosmo.LogSearch{Text1: "boom", From: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)},
		func(s gosmo.LogSearch) { got = &s })

	if d.fText1.Value() != "boom" || d.fFrom.Value() != "2026-08-20 00:00:00" {
		t.Fatalf("the dialog was not seeded from the search in force: %q / %q", d.fText1.Value(), d.fFrom.Value())
	}
	d.pressButton(1) // Clear

	if got == nil {
		t.Fatal("Clear delivered nothing")
	}
	if got.Text1 != "" || !got.From.IsZero() {
		t.Errorf("Clear delivered %+v, want an empty search", *got)
	}
	for _, f := range d.inputFields() {
		if f.Value() != "" {
			t.Errorf("field %q still holds %q after Clear", f.Label(), f.Value())
		}
	}
}

// Cancel must deliver nothing at all — the panel keeps reading with whatever
// it had.
func TestLogSearchDialogCancelDeliversNothing(t *testing.T) {
	d := newTestLogSearchDialog(t)
	fired := false
	d.ShowLogSearch(gosmo.LogSearch{}, func(gosmo.LogSearch) { fired = true })
	d.fText1.SetValue("typed but abandoned")

	d.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))

	if fired {
		t.Error("Escape delivered a search")
	}
	if d.Visible() {
		t.Error("Escape left the dialog open")
	}
}

// A bad date is refused here rather than sent: xp_readerrorlog answers an
// unparseable one with a formatting lecture, and the panel would report that
// as a failed read.
func TestLogSearchDialogRefusesABadDate(t *testing.T) {
	for _, c := range []struct{ name, from, to, want string }{
		{"unparseable From", "yesterday", "", "From"},
		{"unparseable To", "", "20/08/2026", "To"},
		{"reversed range", "2026-08-21", "2026-08-20", "before"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := newTestLogSearchDialog(t)
			fired := false
			d.ShowLogSearch(gosmo.LogSearch{}, func(gosmo.LogSearch) { fired = true })
			d.fFrom.SetValue(c.from)
			d.fTo.SetValue(c.to)

			d.pressButton(0)

			if fired {
				t.Error("an unparseable search was delivered")
			}
			if !d.Visible() {
				t.Error("the dialog closed on a rejected search")
			}
			if !d.statusErr || !strings.Contains(d.status, c.want) {
				t.Errorf("status = %q (err %v), want it to name %q", d.status, d.statusErr, c.want)
			}
		})
	}
}

func TestParseLogSearchTime(t *testing.T) {
	for _, c := range []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{in: "", want: time.Time{}},
		{in: "  ", want: time.Time{}},
		{in: "2026-08-20", want: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)},
		{in: "2026-08-20 13:30", want: time.Date(2026, 8, 20, 13, 30, 0, 0, time.UTC)},
		{in: "2026-08-20 13:30:05", want: time.Date(2026, 8, 20, 13, 30, 5, 0, time.UTC)},
		{in: "2026-13-01", wantErr: true},
		{in: "tomorrow", wantErr: true},
	} {
		got, err := parseLogSearchTime(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseLogSearchTime(%q) = %v, want an error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseLogSearchTime(%q): %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("parseLogSearchTime(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// The status line has to say a search is in force: without it "no entries"
// on a searched read is indistinguishable from an empty log.
func TestLogViewerSummaryNamesTheSearch(t *testing.T) {
	lv := newTestLogViewer()
	if got := lv.searchSuffix(); got != "" {
		t.Errorf("searchSuffix with no search = %q, want empty", got)
	}
	if got := lv.summary(); strings.Contains(got, "searching") {
		t.Errorf("summary with no search mentions a search: %q", got)
	}

	lv.search = gosmo.LogSearch{Text1: "login failed", From: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)}
	got := lv.summary()
	if !strings.Contains(got, `"login failed"`) {
		t.Errorf("summary = %q, want it to name the search text", got)
	}
	if !strings.Contains(got, "2026-08-20 00:00:00..…") {
		t.Errorf("summary = %q, want it to show the open-ended range", got)
	}
	if !strings.Contains(got, "no entries") {
		t.Errorf("summary = %q, want it to still report the entry count", got)
	}
}

// The search has to reach the read itself, not just the panel's state. The
// fake answers a query by the parameters it carries, so a read that dropped
// the search gets the whole-file answer and the count says so.
func TestLogViewerLoadPassesTheSearchToTheServer(t *testing.T) {
	entry := func(text string) []driver.Value {
		return []driver.Value{time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC), "Server", text}
	}
	a := newTestApp()
	sc, _ := newFakeConn(t,
		// Scoped to the search text, and listed first: without the scoping
		// both reads get the same answer and the test passes either way.
		fakeResponse{match: "xp_readerrorlog", arg: "login failed", cols: 3, rows: [][]driver.Value{
			entry("Login failed for user 'sa'."),
		}},
		fakeResponse{match: "xp_readerrorlog", cols: 3, rows: [][]driver.Value{
			entry("Starting up database 'master'."),
			entry("Recovery is complete."),
			entry("Login failed for user 'sa'."),
		}},
		fakeResponse{match: "sp_enumerrorlogs", cols: 3, rows: [][]driver.Value{
			{int64(0), "08/20/2026 10:00", int64(4096)},
		}},
	)
	lv := NewLogViewer(a, sc, gosmo.ErrorLogSQLServer, 0)

	lv.Load()
	waitAndDrain(t, a)
	if len(lv.entries) != 3 {
		t.Fatalf("unsearched read returned %d entries, want the whole file (3)", len(lv.entries))
	}

	lv.search = gosmo.LogSearch{Text1: "login failed"}
	lv.Load()
	waitAndDrain(t, a)
	if len(lv.entries) != 1 {
		t.Fatalf("searched read returned %d entries, want the 1 the server was asked for", len(lv.entries))
	}
	if !strings.Contains(lv.grid.Status(), `"login failed"`) {
		t.Errorf("grid status = %q, want it to name the search", lv.grid.Status())
	}
}
