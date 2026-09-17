package tui

import (
	"fmt"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
)

// twoPaneConnectDialog opens the dialog on a terminal wide enough for the
// History pane, with saved connections for it to list.
func twoPaneConnectDialog(t *testing.T, saved ...config.Connection) *ConnectDialog {
	t.Helper()
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 120, h: 40}
	a.cfg.Connections = saved
	d := NewConnectDialog(a)
	d.Show()
	d.layoutFields()
	if !d.twoPane {
		t.Fatalf("a %d-column terminal did not get the History pane", 120)
	}
	return d
}

// A long server or database name used to fill the row's width, so the user —
// what tells two entries for one server apart — was clipped off. The budgets
// are cut for the 30-column History pane, narrower than the old autocomplete
// overlay's.
func TestMatchLabelClipsServerAndDatabase(t *testing.T) {
	cases := []struct {
		c    config.Connection
		want string
	}{
		{config.Connection{Server: "srv", Database: "db", User: "sa"}, "srv,1433,db,sa"},
		{config.Connection{Server: "sql01.corp.example.com", Port: 1444, Database: "AdventureWorks2022", User: "sa"},
			"sql01.corp.ex…,1444,Adventu…,sa"},
		{config.Connection{Server: "12345678901234", Database: "12345678", User: "sa"},
			"12345678901234,1433,12345678,sa"},
	}
	for _, c := range cases {
		if got := matchLabel(c.c); got != c.want {
			t.Errorf("matchLabel(%q, %q) = %q, want %q", c.c.Server, c.c.Database, got, c.want)
		}
	}
}

// The History pane lists every saved connection, most recent last-saved
// first — config.Config.MatchByServer("") is still the ordering source now
// that it is no longer a prefix filter.
func TestConnectHistoryListsSavedConnectionsMostRecentFirst(t *testing.T) {
	d := twoPaneConnectDialog(t,
		config.Connection{Server: "oldest", User: "sa"},
		config.Connection{Server: "middle", User: "sa"},
		config.Connection{Server: "newest", User: "sa"},
	)
	want := []string{"newest", "middle", "oldest"}
	if len(d.historyConns) != len(want) {
		t.Fatalf("History holds %d rows, want %d", len(d.historyConns), len(want))
	}
	for i, w := range want {
		if d.historyConns[i].Server != w {
			t.Errorf("History row %d = %q, want %q", i, d.historyConns[i].Server, w)
		}
	}
	// A connection made since the last showing is a new row.
	d.Hide()
	d.app.cfg.AddOrUpdate(config.Connection{Server: "brand-new", User: "sa"})
	d.Show()
	if len(d.historyConns) == 0 || d.historyConns[0].Server != "brand-new" {
		t.Errorf("reopening did not refill History: %+v", d.historyConns)
	}
}

// Selecting a row pre-fills the right pane. The list is the picker that
// replaced the server field's autocomplete overlay, so nothing else has to be
// typed to reach a saved connection.
func TestConnectHistorySelectionPreFillsTheRightPane(t *testing.T) {
	d := twoPaneConnectDialog(t,
		config.Connection{Server: "ubusql1", User: "sa", Database: "master"},
		config.Connection{Server: "win10cli", Port: 55253, User: "sa", Database: "tempdb"},
	)
	if got := d.focusable[0]; got != focusable(d.history) {
		t.Fatalf("focus ring starts at %T, want the History pane", got)
	}
	d.setFocus(0)
	// Down moves off row 0 (win10cli, the most recent) onto ubusql1.
	d.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	if got := d.fServer.Value(); got != "ubusql1" {
		t.Errorf("Server Name = %q after moving to the second row, want ubusql1", got)
	}
	if got := d.fDatabase.Value(); got != "master" {
		t.Errorf("Database Name = %q, want master", got)
	}
	d.HandleKey(tcell.NewEventKey(tcell.KeyUp, "", tcell.ModNone))
	if got := d.fServer.Value(); got != "win10cli:55253" {
		t.Errorf("Server Name = %q back on the first row, want the port folded in", got)
	}
}

// A terminal below the two-pane threshold drops the History pane rather than
// clipping it, and nothing it owned becomes unreachable: the pane is a picker
// for what the right pane can also be typed into (decision 3).
func TestConnectDialogNarrowTerminalDropsTheHistoryPane(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 80, h: 30}
	a.cfg.Connections = []config.Connection{{Server: "ubusql1", User: "sa"}}
	d := NewConnectDialog(a)
	d.Show()
	d.layoutFields()

	if d.twoPane {
		t.Fatalf("an 80-column terminal still drew the History pane")
	}
	if i := indexOfFocusable(d.focusable, d.history); i >= 0 {
		t.Errorf("the History pane is in the focus ring at %d in one-pane mode", i)
	}
	if got := d.Rect().W; got > 80 {
		t.Errorf("dialog width = %d on an 80-column terminal", got)
	}
	if d.focusable[0] != focusable(d.fServer) {
		t.Errorf("focus ring starts at %T, want the Server Name field", d.focusable[0])
	}

	// Widening brings it back, on the resize the host reports.
	a.screen.(*fakeSizedScreen).w = 120
	d.Relayout()
	d.layoutFields()
	if !d.twoPane {
		t.Fatalf("widening the terminal did not bring the History pane back")
	}
	if indexOfFocusable(d.focusable, d.history) < 0 {
		t.Errorf("the History pane is drawn but not in the focus ring")
	}
}

// Tab must never land on a control the hidden tab owns. The Custom Properties
// editor is on both tabs and stays reachable from either.
func TestConnectDialogTabRingHoldsOnlyTheVisibleTab(t *testing.T) {
	d := twoPaneConnectDialog(t)

	for _, w := range []focusable{d.fServer, d.ddAuth, d.fPassword, d.fExtraProps} {
		if indexOfFocusable(d.focusable, w) < 0 {
			t.Errorf("%T is not reachable on the Connection Properties tab", w)
		}
	}
	if indexOfFocusable(d.focusable, d.fConnStrPreview) >= 0 {
		t.Error("the connection-string editor is reachable from the properties tab")
	}

	d.setTab(connectTabString)
	if indexOfFocusable(d.focusable, d.fConnStrPreview) < 0 {
		t.Error("the connection-string editor is not reachable on its own tab")
	}
	if indexOfFocusable(d.focusable, d.fExtraProps) < 0 {
		t.Error("the Custom Properties editor is on both tabs but not in the ring")
	}
	for _, w := range []focusable{d.fServer, d.ddAuth, d.fPassword, d.cbRemember, d.ddEncrypt} {
		if i := indexOfFocusable(d.focusable, w); i >= 0 {
			t.Errorf("%T (properties tab) is reachable at %d from the connection-string tab", w, i)
		}
	}

	// Every step of a full cycle stays inside the ring.
	for range len(d.focusable) + 1 {
		d.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
		if d.focusIdx < 0 || d.focusIdx >= len(d.focusable) {
			t.Fatalf("Tab left focusIdx = %d, ring of %d", d.focusIdx, len(d.focusable))
		}
	}
}

// Clicking a tab switches the pane, and the columns hit-tested are the ones
// drawn — both come from the same TabStripSegments call.
func TestConnectDialogTabBarClickSwitchesPanes(t *testing.T) {
	d := twoPaneConnectDialog(t)
	segs := d.tabSegments()
	if len(segs) != len(connectTabLabels) {
		t.Fatalf("laid out %d tabs, want %d — the pane is too narrow for both", len(segs), len(connectTabLabels))
	}
	click := func(i int) {
		d.HandleMouse(tcell.NewEventMouse(segs[i][0].X+1, d.tabRect.Y, tcell.Button1, tcell.ModNone))
		d.HandleMouse(tcell.NewEventMouse(segs[i][0].X+1, d.tabRect.Y, tcell.ButtonNone, tcell.ModNone))
	}
	click(1)
	if d.tab != connectTabString {
		t.Errorf("clicking %q left the tab at %d", connectTabLabels[1], d.tab)
	}
	click(0)
	if d.tab != connectTabProperties {
		t.Errorf("clicking %q left the tab at %d", connectTabLabels[0], d.tab)
	}
}

// The dialog's own geometry must fit what it lays out: every field row, the
// tab bar and the Custom Properties section sit above the separator, and the
// widest label is exactly connectLabelWidth.
func TestConnectDialogRowsFitAboveTheButtonRow(t *testing.T) {
	d := twoPaneConnectDialog(t)
	bottom := d.ButtonRowY() - 2
	rows := map[string]int{
		"tab bar":           d.tabRect.Y,
		"Server Name":       d.fServer.RectY(),
		"Host Name In Cert": d.fHostCert.RectY(),
		"Custom Properties": d.extraPropsLabelY,
	}
	for name, y := range rows {
		if y < d.InnerRect().Y || y > bottom {
			t.Errorf("%s is drawn at row %d, outside the content band %d..%d",
				name, y, d.InnerRect().Y, bottom)
		}
	}
	if d.fHostCert.RectY() >= d.extraPropsLabelY {
		t.Errorf("the last field row (%d) collides with the Custom Properties section (%d)",
			d.fHostCert.RectY(), d.extraPropsLabelY)
	}
	if got := len(fmt.Sprintf("%s", d.fHostCert.Label())); got != connectLabelWidth {
		t.Errorf("the widest label is %d columns, want exactly %d", got, connectLabelWidth)
	}
}

// The dialog opens on the most recent saved connection, as SSMS does — the
// History pane's own first row. Reopening over a form that already has a
// server name in it must not replace it: the fields persist across Show/Hide,
// and a half-typed name is not something to overwrite.
func TestConnectDialogOpensOnTheMostRecentConnection(t *testing.T) {
	d := twoPaneConnectDialog(t,
		config.Connection{Server: "older", User: "sa"},
		config.Connection{Server: "newest", User: "sa", Database: "tempdb"},
	)
	if got := d.fServer.Value(); got != "newest" {
		t.Errorf("Server Name = %q on opening, want the most recent connection", got)
	}
	if got := d.fDatabase.Value(); got != "tempdb" {
		t.Errorf("Database Name = %q, want the row's own value", got)
	}

	d.Hide()
	d.fServer.SetValue("half-typed")
	d.Show()
	if got := d.fServer.Value(); got != "half-typed" {
		t.Errorf("reopening replaced a typed server name with %q", got)
	}
}
