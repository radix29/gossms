package tui

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// The Object Explorer and Details wiring for Management ▸ Database Mail.
// Statement text is gosmo's to test; these pin what the tree and the pane do
// with the answers — the state label, the Azure SQL Database gate, and
// sections that each go "not visible" alone rather than failing the view.

// Match text for gosmo's Database Mail reads. The account list is matched
// with its FROM, since the profile-account read joins the same table.
const (
	mailStatusRead   = "N'Database Mail XPs'"
	mailQueueRead    = "sysmail_help_queue_sp"
	mailProfileRead  = "FROM   msdb.dbo.sysmail_profile p"
	mailProfAcctRead = "FROM   msdb.dbo.sysmail_profileaccount pa"
	mailAccountRead  = "FROM   msdb.dbo.sysmail_account a"
	mailItemsRead    = "FROM msdb.dbo.sysmail_allitems"
	// mailVisibilityRead is MailVisibility's query; the item and event
	// batches name IS_SRVROLEMEMBER after IF, not after CASE WHEN.
	mailVisibilityRead = "SELECT CAST(CASE WHEN ISNULL(IS_SRVROLEMEMBER"
)

// mailStatus answers the status read as gosmo's batch does: DISABLED from
// sys.configurations, or the procedure's STOPPED / STARTED.
func mailStatus(status string) fakeResponse {
	return fakeResponse{match: mailStatusRead, cols: 1, rows: [][]driver.Value{{status}}}
}

// mailRefused is the Msg 229 an msdb user outside the right role gets on a
// sysmail_* object (W8).
func mailRefused(match, object string) fakeResponse {
	return fakeResponse{match: match, err: mssql.Error{Number: 229, Class: 14,
		Message: "The SELECT permission was denied on the object '" + object + "', database 'msdb', schema 'dbo'."}}
}

// mailConfig answers queues, two profiles (the second the public default,
// its two accounts out of name order), two accounts and one failed item.
func mailConfig() []fakeResponse {
	at := time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)
	return []fakeResponse{
		{match: mailQueueRead, cols: 5, rows: [][]driver.Value{
			{"mail", int64(3), "INACTIVE", at, at},
			{"status", int64(0), "INACTIVE", at, at},
		}},
		{match: mailProfileRead, cols: 6, rows: [][]driver.Value{
			{int64(2), "alerts", "", false, false, at},
			{int64(1), "ops", "", true, true, at},
		}},
		{match: mailProfAcctRead, cols: 4, rows: [][]driver.Value{
			{int64(1), int64(11), "relay2", int64(1)},
			{int64(1), int64(10), "relay1", int64(2)},
		}},
		{match: mailAccountRead, cols: 15, rows: [][]driver.Value{
			{int64(10), "relay1", "", "ops@example.com", "", "", "SMTP", "smtp.example.com", int64(587),
				true, "mailer", int64(65536), false, int64(0), at},
			{int64(11), "relay2", "", "ops@example.com", "", "", "SMTP", "10.0.0.5", int64(25),
				false, "", int64(0), false, int64(0), at},
		}},
		{match: mailItemsRead, cols: 17, rows: [][]driver.Value{
			{int64(42), int64(1), "dba@example.com", "", "", "Job failed", "", "TEXT", "NORMAL", "NORMAL", "",
				at, "sa", int64(0), "failed", nil, at},
		}},
	}
}

func TestDatabaseMailLabelCarriesItsState(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		responses []fakeResponse
		want      mailNodeState
	}{
		{"XPs off", []fakeResponse{mailStatus("DISABLED")}, mailNodeState{"Database Mail (Disabled)", gosmo.MailDisabled, true}},
		{"stopped", []fakeResponse{mailStatus("STOPPED")}, mailNodeState{"Database Mail (Stopped)", gosmo.MailStopped, true}},
		{"started", []fakeResponse{mailStatus("STARTED")}, mailNodeState{"Database Mail", gosmo.MailStarted, true}},
		// An msdb user outside DatabaseMailUserRole: the folder must not fail,
		// and the state is unknown — not MailDisabled, its zero value.
		{"status refused", []fakeResponse{mailRefused(mailStatusRead, "sysmail_help_status_sp")}, mailNodeState{label: "Database Mail"}},
	} {
		sc, _ := newFakeConn(t, tc.responses...)
		if got := databaseMailState(ctx, sc); got != tc.want {
			t.Errorf("%s: state = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestManagementListsDatabaseMailExceptOnAzureSQLDatabase(t *testing.T) {
	ctx := context.Background()
	mailNode := func(sc *db.ServerConn) *explorerNode {
		children, err := loadManagementChildren(loaderCtx{ctx: ctx, sc: sc}, &explorerNode{})
		if err != nil {
			t.Fatal(err)
		}
		if i := slices.IndexFunc(children, func(n *explorerNode) bool { return n.data.Type == NodeDatabaseMail }); i >= 0 {
			if i != len(children)-1 {
				t.Errorf("Database Mail is child %d of %d; SSMS puts it after SQL Server Logs", i, len(children))
			}
			return children[i]
		}
		return nil
	}
	if n := mailNode(newFakeConnEdition(t, 3, "16.0.4085.2", mailStatus("STOPPED"))); n == nil {
		t.Error("Developer: Management has no Database Mail node")
	} else if n.label != "Database Mail (Stopped)" {
		t.Errorf("Developer: label = %q, want the state the loader read", n.label)
	}
	if mailNode(newFakeConnEdition(t, 8, "12.0.2000.8")) == nil {
		t.Error("Managed Instance: Management has no Database Mail node — D5 ships it shown")
	}
	if mailNode(newFakeConnEdition(t, 5, "12.0.2000.8")) != nil {
		t.Error("Azure SQL Database: Management lists Database Mail, which has no msdb there")
	}
}

// detailValue is the Value of the first row whose Property is prop.
func detailValue(t *testing.T, rows [][]string, prop string) string {
	t.Helper()
	for _, r := range rows {
		if r[0] == prop {
			return r[1]
		}
	}
	t.Fatalf("no %q row in %v", prop, rows)
	return ""
}

func TestDatabaseMailDetailListsEachSection(t *testing.T) {
	sc, _ := newFakeConn(t, append([]fakeResponse{mailStatus("STARTED")}, mailConfig()...)...)
	_, rows, err := databaseMailDetail(context.Background(), sc)
	if err != nil {
		t.Fatal(err)
	}
	for prop, want := range map[string]string{
		"Status":                     "Started",
		"Database Mail XPs":          "1",
		"Mail queue":                 "3 (inactive)",
		"Profiles":                   "2",
		"  alerts":                   "no accounts",
		"  ops":                      "relay2, relay1 · public, default",
		"Accounts":                   "2",
		"  relay1":                   "ops@example.com · smtp.example.com:587 · SSL · Basic (mailer)",
		"  relay2":                   "ops@example.com · 10.0.0.5:25 · Anonymous",
		"Failed items (latest 20)":   "1",
		"  #42  2026-10-01 09:30:00": "dba@example.com · Job failed",
	} {
		if got := detailValue(t, rows, prop); got != want {
			t.Errorf("%s = %q, want %q", prop, got, want)
		}
	}
}

func TestDatabaseMailDetailSaysDisabledAndHowToEnable(t *testing.T) {
	sc, _ := newFakeConn(t, append([]fakeResponse{mailStatus("DISABLED")}, mailConfig()...)...)
	_, rows, err := databaseMailDetail(context.Background(), sc)
	if err != nil {
		t.Fatal(err)
	}
	if got := detailValue(t, rows, "Status"); got != "Disabled" {
		t.Errorf("Status = %q, want Disabled", got)
	}
	if got := detailValue(t, rows, "Database Mail XPs"); !strings.HasPrefix(got, "0") || !strings.Contains(got, "Server Properties") {
		t.Errorf("Database Mail XPs = %q, want 0 and where to turn it on", got)
	}
}

// A DatabaseMailUserRole-only login (W8): status and its own items, Msg 229
// on the configuration reads and on the queue procedure. Each refused section
// names its right; none fails the view.
func TestDatabaseMailDetailDegradesPerSection(t *testing.T) {
	queueRefused := mailRefused(mailQueueRead, "sysmail_help_queue_sp")
	sc, _ := newFakeConn(t,
		mailStatus("STARTED"),
		queueRefused,
		mailRefused(mailProfileRead, "sysmail_profile"),
		mailRefused(mailAccountRead, "sysmail_account"),
		mailConfig()[4], // items: the server filters them to the caller's own
	)
	_, rows, err := databaseMailDetail(context.Background(), sc)
	if err != nil {
		t.Fatalf("a refused section failed the whole view: %v", err)
	}
	for prop, want := range map[string]string{
		"Status":                   "Started",
		"Queues":                   mailQueueNoExecute,
		"Profiles":                 mailConfigNotVisible,
		"Accounts":                 mailConfigNotVisible,
		"Failed items (latest 20)": "1",
	} {
		if got := detailValue(t, rows, prop); got != want {
			t.Errorf("%s = %q, want %q", prop, got, want)
		}
	}
}

// Anything that is not a refusal is a real failure and fails the view, as
// every other Details arm does — "not visible" must not hide a broken msdb.
func TestDatabaseMailDetailFailsOnAnErrorThatIsNotARefusal(t *testing.T) {
	sc, _ := newFakeConn(t, mailStatus("STARTED"),
		fakeResponse{match: mailQueueRead, err: errors.New("connection reset")})
	if _, _, err := databaseMailDetail(context.Background(), sc); err == nil {
		t.Error("a broken queue read was shown as a section rather than failing")
	}
}

// A label re-read in place lands after the Refresh that re-titled the pane
// with the old one; Retitle brings the title along, for the shown node only.
func TestDetailBrowserRetitlesOnlyTheShownNode(t *testing.T) {
	db := NewDetailBrowser("Object Explorer Details")
	shown := &explorerNode{label: "Database Mail (Disabled)"}
	db.currentNode, db.title = shown, "Object Explorer Details — Database Mail (Disabled)"

	other := &explorerNode{label: "Resource Governor"}
	db.Retitle(other)
	shown.label = "Database Mail"
	if db.title != "Object Explorer Details — Database Mail (Disabled)" {
		t.Errorf("title = %q after retitling a node not shown", db.title)
	}
	db.Retitle(shown)
	if db.title != "Object Explorer Details — Database Mail" {
		t.Errorf("title = %q, want the node's new label", db.title)
	}
}

// msdb db_owner without VIEW SERVER STATE may execute the queue procedure and
// is refused Msg 300 inside it (W8): that, and only that, names the server
// right.
func TestDatabaseMailDetailNamesViewServerStateOnMsg300(t *testing.T) {
	queueRefused := fakeResponse{match: mailQueueRead, err: mssql.Error{Number: 300, Class: 14,
		Message: "VIEW SERVER STATE permission was denied on object 'server', database 'master'."}}
	sc, _ := newFakeConn(t, append([]fakeResponse{mailStatus("STARTED"), queueRefused}, mailConfig()[1:]...)...)
	_, rows, err := databaseMailDetail(context.Background(), sc)
	if err != nil {
		t.Fatal(err)
	}
	if got := detailValue(t, rows, "Queues"); got != mailQueueNotVisible {
		t.Errorf("Queues = %q, want %q", got, mailQueueNotVisible)
	}
}

// A login with no msdb access is refused the status procedure, which gosmo
// calls only with 'Database Mail XPs' on — so the row still says it is (W14:
// VIEW SERVER STATE alone showed no XPs row at all).
func TestDatabaseMailDetailShowsXPsWhenStatusIsRefused(t *testing.T) {
	sc, _ := newFakeConn(t,
		mailRefused(mailStatusRead, "sysmail_help_status_sp"),
		mailRefused(mailQueueRead, "sysmail_help_queue_sp"),
		mailRefused(mailProfileRead, "sysmail_profile"),
		mailRefused(mailAccountRead, "sysmail_account"),
		mailRefused(mailItemsRead, "sysmail_allitems"),
	)
	_, rows, err := databaseMailDetail(context.Background(), sc)
	if err != nil {
		t.Fatal(err)
	}
	if got := detailValue(t, rows, "Status"); got != mailItemsNotVisible {
		t.Errorf("Status = %q, want %q", got, mailItemsNotVisible)
	}
	if got := detailValue(t, rows, "Database Mail XPs"); got != "1" {
		t.Errorf("Database Mail XPs = %q, want 1", got)
	}
}

// A DatabaseMailUserRole member reads sysmail_allitems, which shows it its
// own items only, so the heading says whose they are; a login gosmo reads
// the base table for — sysmail, msdb db_owner or db_datareader, CONTROL
// SERVER (docs/decisions.md) — and one whose visibility could not be read keep the plain
// heading.
func TestDatabaseMailDetailSaysWhoseFailedItems(t *testing.T) {
	conn := func(vis ...fakeResponse) *db.ServerConn {
		sc, _ := newFakeConn(t, append(append(vis, mailStatus("STARTED")), mailConfig()...)...)
		return sc
	}
	for _, tc := range []struct {
		name    string
		sc      *db.ServerConn
		heading string
	}{
		{"role member", conn(mailVisibilityAnswer(false, false)), "Your failed items (latest 20)"},
		{"base-table reader", conn(mailVisibilityAnswer(true, true)), "Failed items (latest 20)"},
		{"visibility unread", conn(), "Failed items (latest 20)"},
	} {
		_, rows, err := databaseMailDetail(context.Background(), tc.sc)
		if err != nil {
			t.Fatal(err)
		}
		if got := detailValue(t, rows, tc.heading); got != "1" {
			t.Errorf("%s: %q = %q, want 1", tc.name, tc.heading, got)
		}
	}
}

// mailVisibilityAnswer answers gosmo's MailVisibility. It must come before any
// capability response: its query names IS_SRVROLEMEMBER too.
func mailVisibilityAnswer(items, events bool) fakeResponse {
	return fakeResponse{match: mailVisibilityRead, cols: 2, rows: [][]driver.Value{{items, events}}}
}

// mailMenuNode is a Database Mail node in state; known false is a status
// read that was refused.
func mailMenuNode(sc *db.ServerConn, st gosmo.MailState, known bool) *explorerNode {
	return &explorerNode{label: databaseMailRootLabel, data: nodeData{Type: NodeDatabaseMail, conn: sc,
		MailState: st, MailStateKnown: known}}
}

// Start or Stop by the state the label shows, both when it is unknown — an
// unread state is not MailDisabled, its zero value — and Send Test E-Mail,
// Start and Stop withheld with 'Database Mail XPs' off, saying so.
func TestDatabaseMailMenuFollowsTheState(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t)
	for _, tc := range []struct {
		name        string
		state       gosmo.MailState
		known       bool
		start, stop bool
		xpsOff      bool
		// sendNote is Send Test E-Mail's withholding note, "" when offered:
		// a stopped Database Mail refuses the send, Msg 14641, live on 17.
		sendNote string
	}{
		{"started", gosmo.MailStarted, true, false, true, false, ""},
		{"stopped", gosmo.MailStopped, true, true, false, false, "stopped"},
		{"disabled", gosmo.MailDisabled, true, true, false, true, "XPs off"},
		{"unknown", gosmo.MailDisabled, false, true, true, false, ""},
	} {
		items := a.contextMenuItemsForNode(mailMenuNode(sc, tc.state, tc.known))
		start, stop := findMenuItem(items, "Start Database Mail"), findMenuItem(items, "Stop Database Mail")
		if (start != nil) != tc.start || (stop != nil) != tc.stop {
			t.Errorf("%s: Start offered %v, Stop %v; want %v, %v", tc.name, start != nil, stop != nil, tc.start, tc.stop)
		}
		send := findMenuItem(items, "Send Test E-Mail...")
		if send == nil {
			t.Fatalf("%s: no Send Test E-Mail...", tc.name)
		}
		if withheld := !send.Enabled() && send.NoteWhen(); withheld != (tc.sendNote != "") || withheld && send.Note != tc.sendNote {
			t.Errorf("%s: Send Test E-Mail withheld %v with note %q, want %q", tc.name, withheld, send.Note, tc.sendNote)
		}
		for _, it := range []*controls.MenuItem{start, stop} {
			if it == nil {
				continue
			}
			withheld := !it.Enabled() && it.NoteWhen() && it.Note == "XPs off"
			if withheld != tc.xpsOff {
				t.Errorf("%s: %q withheld for XPs = %v, want %v (note %q)", tc.name, it.Label, withheld, tc.xpsOff, it.Note)
			}
		}
		for _, label := range []string{"Configure Database Mail...", "View Database Mail Log", "Properties..."} {
			if findMenuItem(items, label) == nil {
				t.Errorf("%s: no %s", tc.name, label)
			}
		}
		if findMenuItem(items, "Script Database Mail as") == nil {
			t.Errorf("%s: no Script Database Mail as", tc.name)
		}
	}
}

// The gates wired to the items, not just the sets: Start/Stop are
// configuration ({db_owner in msdb, CONTROL SERVER}), Send Test E-Mail
// also admits DatabaseMailUserRole (W8), and View Database Mail Log also
// msdb db_datareader — public alone is refused Msg 229 (live on 17).
func TestDatabaseMailMenuGates(t *testing.T) {
	a := newTestApp()
	for _, tc := range []struct {
		name           string
		granted        []string
		roleIn         []string
		send, startOrS bool
		log            bool
	}{
		{"role only", nil, []string{"DatabaseMailUserRole"}, true, false, true},
		{"msdb db_datareader", nil, []string{"db_datareader"}, false, false, true},
		{"msdb db_owner", nil, []string{"db_owner"}, true, true, true},
		{"CONTROL SERVER", []string{"CONTROL SERVER"}, nil, true, true, true},
		{"nothing", nil, nil, false, false, false},
	} {
		var denied []string
		if len(tc.granted) == 0 {
			denied = []string{"CONTROL SERVER"}
		}
		var roleNotIn []string
		for _, r := range []string{"DatabaseMailUserRole", "db_owner", "db_datareader", "SQLAgentUserRole"} {
			if !slices.Contains(tc.roleIn, r) {
				roleNotIn = append(roleNotIn, r)
			}
		}
		sc := agentConn(t, tc.granted, denied, tc.roleIn, roleNotIn)
		for _, st := range []gosmo.MailState{gosmo.MailStarted, gosmo.MailStopped} {
			items := a.contextMenuItemsForNode(mailMenuNode(sc, st, true))
			if got := findMenuItem(items, "Send Test E-Mail...").Enabled(); st == gosmo.MailStarted && got != tc.send {
				t.Errorf("%s: Send Test E-Mail offered %v, want %v", tc.name, got, tc.send)
			}
			toggle := findMenuItem(items, "Stop Database Mail")
			if st == gosmo.MailStopped {
				toggle = findMenuItem(items, "Start Database Mail")
			}
			if got := toggle.Enabled(); got != tc.startOrS {
				t.Errorf("%s: %s offered %v, want %v", tc.name, toggle.Label, got, tc.startOrS)
			}
			if got := findMenuItem(items, "View Database Mail Log").Enabled(); got != tc.log {
				t.Errorf("%s: View Database Mail Log offered %v, want %v", tc.name, got, tc.log)
			}
		}
	}
}

// Start runs sysmail_start_sp and re-reads the label in place; Stop is
// confirmed first.
func TestStartAndStopDatabaseMail(t *testing.T) {
	a := newTestApp()
	sc, inst := newFakeConn(t, mailStatus("STARTED"))
	root := a.explorer.AddRoot("srv", sc)
	node := mailMenuNode(sc, gosmo.MailStopped, true)
	node.label = "Database Mail (Stopped)"
	a.explorer.SetChildren(root, []*explorerNode{node})
	a.startDatabaseMail(sc, node)
	drainUntil(t, a, func() bool { return node.label == "Database Mail" }, "the label to be re-read")
	if node.data.MailState != gosmo.MailStarted {
		t.Errorf("state = %v after the re-read", node.data.MailState)
	}
	if got := inst.Statements(); !slices.Contains(got, "EXEC msdb.dbo.sysmail_start_sp") {
		t.Errorf("statements = %q", got)
	}

	a.stopDatabaseMail(sc, node)
	if slices.Contains(inst.Statements(), "EXEC msdb.dbo.sysmail_stop_sp") {
		t.Error("Stop ran before it was confirmed")
	}
	if !a.confirmDialog.Visible() {
		t.Error("Stop asked no confirmation")
	}
}
