package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Send Test E-Mail: the page built for each kind of login, what reaches
// sp_send_dbmail, and how the wait reads an item that sent, failed, logged
// an error while still retrying, or never moved.

const (
	mailItemByIDRead = "FROM msdb.dbo.sysmail_allitems WHERE mailitem_id"
	mailEventsRead   = "FROM   msdb.dbo.sysmail_event_log"
	mailSendCall     = "sp_send_dbmail"
)

// mailItem answers MailItemByID with item 42 in status.
func mailItem(status string) fakeResponse {
	at := time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)
	return fakeResponse{match: mailItemByIDRead, cols: 17, rows: [][]driver.Value{
		{int64(42), int64(1), "dba@example.com", "", "", "Database Mail Test", "", "TEXT", "NORMAL", "NORMAL", "",
			at, "sa", int64(0), status, nil, at},
	}}
}

// mailErrorEvent answers the event read with one error row for item 42.
func mailErrorEvent(desc string) fakeResponse {
	at := time.Date(2026, 10, 1, 9, 30, 20, 0, time.UTC)
	return fakeResponse{match: mailEventsRead, cols: 9, rows: [][]driver.Value{
		{int64(7), "error", at, desc, int64(1234), int64(42), int64(10), at, "sa"},
	}}
}

func noMailEvents() fakeResponse { return fakeResponse{match: mailEventsRead, cols: 9} }

// An unreachable server's error row as SQL Server 17 logs it (live,
// 2026-10-01): a summary sentence ending in an empty "Exception Message:",
// then .NET exception blocks with stack traces.
const mailConnectError = "The mail could not be sent to the recipients because of the mail server failure. (Sending Mail using Account 41 (2026-10-01T12:30:21). Exception Message: \r\n" +
	"\r\n" +
	"1) Exception Information\r\n" +
	"===================\r\n" +
	"Exception Type: Microsoft.SqlServer.Management.SqlIMail.MailFramework.Exceptions.BaseMailFrameworkException\r\n" +
	"Message: Could not connect to mail server. (No connection could be made because the target machine actively refused it 127.0.0.1:1)\r\n" +
	"Data: System.Collections.ListDictionaryInternal\r\n" +
	"TargetSite: Void CheckServerValidity()\r\n" +
	"\r\n" +
	"2) Exception Information\r\n" +
	"===================\r\n" +
	"Exception Type: System.Net.Sockets.SocketException\r\n" +
	"Message: No connection could be made because the target machine actively refused it 127.0.0.1:1\r\n"

const mailConnectCause = "Could not connect to mail server. (No connection could be made because the target machine actively refused it 127.0.0.1:1)"

func TestMailErrorSummaryLeadsWithTheFirstMessageLine(t *testing.T) {
	if got := mailErrorSummary(mailConnectError); got != mailConnectCause {
		t.Errorf("summary = %q, want %q", got, mailConnectCause)
	}
	// No Message: line — the summary sentence, without its account tail.
	desc := "The mail could not be sent to the recipients because of the mail server failure. (Sending Mail using Account 41 (2026-10-01T12:30:21). Exception Message: \r\n"
	if got := mailErrorSummary(desc); got != "The mail could not be sent to the recipients because of the mail server failure." {
		t.Errorf("summary without a Message: line = %q", got)
	}
	if got := mailErrorSummary("Just one line.\r\n"); got != "Just one line." {
		t.Errorf("a one-line description = %q", got)
	}
}

func TestWaitForTestMail(t *testing.T) {
	for _, tc := range []struct {
		name      string
		responses []fakeResponse
		wantText  string
		wantErr   bool
		// wantPolls is how many times the item was read: the wait must end on
		// the first read that settles it, and keep reading one that does not.
		wantPolls func(int) bool
	}{
		{"sent", []fakeResponse{mailItem("sent")},
			"Test e-mail sent (mail item 42).", false, func(n int) bool { return n == 1 }},
		// Windows: the error row is logged ~20 s in, a minute before
		// "failed" (W8). A wait for "failed" would never report it.
		{"retrying with an error logged", []fakeResponse{mailItem("retrying"), mailErrorEvent(mailConnectError)},
			"Mail item 42 is retrying after an error: " + mailConnectCause, true, func(n int) bool { return n == 1 }},
		{"failed with an error logged", []fakeResponse{mailItem("failed"), mailErrorEvent(mailConnectError)},
			"Mail item 42 failed: " + mailConnectCause, true, func(n int) bool { return n == 1 }},
		{"failed with nothing logged", []fakeResponse{mailItem("failed"), noMailEvents()},
			"Mail item 42 failed — see the Database Mail log.", true, func(n int) bool { return n == 1 }},
		// Linux: nothing for over two minutes.
		{"never moves", []fakeResponse{mailItem("unsent"), noMailEvents()},
			"Mail item 42 is still unsent after 30 seconds", false, func(n int) bool { return n > 1 }},
		{"item refused", []fakeResponse{mailRefused(mailItemByIDRead, "sysmail_allitems")},
			"Mail item 42 was queued, but reading its status failed", true, func(n int) bool { return n == 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, inst := newFakeConn(t, tc.responses...)
			res := waitForTestMail(context.Background(), sc, 42, 40*time.Millisecond, 5*time.Millisecond)
			text, isErr := res.message()
			if !strings.HasPrefix(text, tc.wantText) {
				t.Errorf("message = %q, want it to start %q", text, tc.wantText)
			}
			if isErr != tc.wantErr {
				t.Errorf("isErr = %v, want %v", isErr, tc.wantErr)
			}
			if n := len(inst.Reads(mailItemByIDRead)); !tc.wantPolls(n) {
				t.Errorf("the item was read %d times", n)
			}
			// The item asked about is the one sent, not the newest.
			if args, ok := inst.ReadArgs(mailItemByIDRead); !ok || len(args) != 1 || args[0].Value != int64(42) {
				t.Errorf("item read with %v, want mail item 42", args)
			}
		})
	}
}

func TestWaitForTestMailStopsWhenCancelled(t *testing.T) {
	sc, _ := newFakeConn(t, mailItem("unsent"), noMailEvents())
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	start := time.Now()
	res := waitForTestMail(ctx, sc, 42, time.Minute, 5*time.Millisecond)
	if time.Since(start) > 5*time.Second {
		t.Fatal("the wait outlived its context")
	}
	text, isErr := res.message()
	if !res.stopped || isErr || !strings.HasPrefix(text, "Stopped waiting: mail item 42 is unsent") {
		t.Errorf("cancelled wait = %+v, message %q", res, text)
	}
}

// loadSendTestMail opens the dialog on sc and waits for its page.
func loadSendTestMail(t *testing.T, sc *db.ServerConn) (*SendTestMailDialog, *App, *propsheet.Form) {
	t.Helper()
	a := newTestApp()
	d := NewSendTestMailDialog(a)
	d.show(sc)
	drainUntil(t, a, func() bool { return d.PageState(0) == propsheet.PageReady || d.PageState(0) == propsheet.PageError },
		"the page to load")
	if d.PageForm(0) == nil {
		t.Fatal("the page failed to load")
	}
	return d, a, d.PageForm(0)
}

func TestSendTestMailOffersTheProfilesWithThePublicDefaultFirstChosen(t *testing.T) {
	sc, _ := newFakeConn(t, append([]fakeResponse{mailStatus("STARTED")}, mailConfig()...)...)
	_, _, f := loadSendTestMail(t, sc)
	pick := selectRow(t, f, "Profile")
	if got := pick.Items(); len(got) != 2 || got[0] != "alerts" || got[1] != "ops" {
		t.Fatalf("profiles = %v, want alerts and ops", got)
	}
	// ops is the public default, and not first in the list.
	if pick.Value() != "ops" {
		t.Errorf("profile = %q, want the public default ops", pick.Value())
	}
}

// A DatabaseMailUserRole member may send but not list profiles (W8): the
// profile is typed, and blank sends through their default — no
// @profile_name at all.
func TestSendTestMailForARoleOnlyLoginTypesTheProfile(t *testing.T) {
	sc, inst := newFakeConn(t, mailStatus("STARTED"),
		mailRefused(mailProfileRead, "sysmail_profile"),
		fakeResponse{match: mailSendCall, cols: 1, rows: [][]driver.Value{{int64(42)}}},
		mailItem("sent"))
	d, a, f := loadSendTestMail(t, sc)
	textRow(t, f, "Profile") // typed, not a dropdown
	if !hasNoteMentioning(f, "may not list profiles") {
		t.Error("no note says why the profile is typed")
	}
	editText(t, f, "To", "dba@example.com")
	d.send(false)
	drainUntil(t, a, func() bool { return !d.Applying() && d.Message() != "" }, "the outcome")
	reads := inst.Reads(mailSendCall)
	if len(reads) != 1 {
		t.Fatalf("sp_send_dbmail ran %d times", len(reads))
	}
	if strings.Contains(reads[0], "@profile_name") {
		t.Errorf("a blank profile sent @profile_name: %s", reads[0])
	}
	if !strings.Contains(reads[0], "@recipients = N'dba@example.com'") {
		t.Errorf("recipient not sent: %s", reads[0])
	}
	if got := d.Message(); got != "Test e-mail sent (mail item 42)." {
		t.Errorf("message = %q", got)
	}
}

func TestSendTestMailSendsThroughTheChosenProfile(t *testing.T) {
	sc, inst := newFakeConn(t, append([]fakeResponse{mailStatus("STOPPED"),
		{match: mailSendCall, cols: 1, rows: [][]driver.Value{{int64(42)}}},
		mailItem("sent")}, mailConfig()...)...)
	d, a, f := loadSendTestMail(t, sc)
	// Opened from a stale menu: the server refuses the send (Msg 14641),
	// and the page says so up front.
	if !hasNoteMentioning(f, "Database Mail is stopped, and refuses mail") {
		t.Error("a stopped Database Mail is not mentioned")
	}
	editSelect(t, f, "Profile", "alerts")
	editText(t, f, "To", "  dba@example.com  ")
	editText(t, f, "Subject", "hello")
	d.send(true) // OK: closes once queued, and the status bar follows the item
	drainUntil(t, a, func() bool { return !d.Visible() }, "the dialog to close")
	drainUntil(t, a, func() bool { return a.statusText == "Test e-mail sent (mail item 42)." }, "the outcome on the status bar")
	reads := inst.Reads(mailSendCall)
	if len(reads) != 1 {
		t.Fatalf("sp_send_dbmail ran %d times", len(reads))
	}
	for _, want := range []string{"@profile_name = N'alerts'", "@recipients = N'dba@example.com'", "@subject = N'hello'"} {
		if !strings.Contains(reads[0], want) {
			t.Errorf("send lacks %s: %s", want, reads[0])
		}
	}
}

func TestSendTestMailRefusesBeforeSending(t *testing.T) {
	sc, inst := newFakeConn(t, append([]fakeResponse{mailStatus("STARTED")}, mailConfig()...)...)
	d, _, _ := loadSendTestMail(t, sc)
	d.send(false)
	if d.Message() != "To is required" || d.Applying() {
		t.Errorf("blank To: message %q, applying %v", d.Message(), d.Applying())
	}

	empty, inst2 := newFakeConn(t, mailStatus("STARTED"),
		fakeResponse{match: mailProfileRead, cols: 6}, fakeResponse{match: mailProfAcctRead, cols: 4})
	d2, _, f2 := loadSendTestMail(t, empty)
	editText(t, f2, "To", "dba@example.com")
	d2.send(false)
	if !strings.Contains(d2.Message(), "no Database Mail profile") {
		t.Errorf("no profiles: message %q", d2.Message())
	}
	for _, i := range []*fakeInstance{inst, inst2} {
		if len(i.Reads(mailSendCall)) != 0 {
			t.Error("sp_send_dbmail ran")
		}
	}
}

// A refused send keeps the dialog open, on OK too, with what was typed.
func TestSendTestMailKeepsTheDialogWhenTheSendIsRefused(t *testing.T) {
	sc, _ := newFakeConn(t, append([]fakeResponse{mailStatus("STARTED"),
		mailRefused(mailSendCall, "sp_send_dbmail")}, mailConfig()...)...)
	d, a, f := loadSendTestMail(t, sc)
	editText(t, f, "To", "dba@example.com")
	d.send(true)
	drainUntil(t, a, func() bool { return !d.Applying() }, "the send to fail")
	if !d.Visible() {
		t.Error("the dialog closed on a refused send")
	}
	if !strings.Contains(d.Message(), "permission was denied") {
		t.Errorf("message = %q", d.Message())
	}
}

// The errors sp_send_dbmail refuses a message with read as a plain sentence
// rather than "gosmo: send test mail: mssql: profile name is not valid"
// (N10); the numbers and texts are the server's own, live on 17.
func TestSendTestMailExplainsTheRefusalsItKnows(t *testing.T) {
	for _, tc := range []struct {
		number  int32
		message string
		want    string
	}{
		{14607, "profile name is not valid", `Profile "alerts" does not exist or is not granted to you.`},
		{14636, "No global profile is configured. Specify a profile name in the @profile_name parameter.", "No default profile for this login"},
		{14641, "Mail not queued. Database Mail is stopped. Use sysmail_start_sp to start Database Mail.", "stopped and queued nothing"},
		{15281, "SQL Server blocked access to procedure 'dbo.sp_send_dbmail' of component 'Database Mail XPs' because this component is turned off as part of the security configuration for this server.", "Database Mail XPs is off"},
	} {
		sc, _ := newFakeConn(t, append([]fakeResponse{mailStatus("STARTED"),
			{match: mailSendCall, err: mssql.Error{Number: tc.number, Class: 16, Message: tc.message}}}, mailConfig()...)...)
		d, a, f := loadSendTestMail(t, sc)
		editSelect(t, f, "Profile", "alerts")
		editText(t, f, "To", "dba@example.com")
		d.send(false)
		drainUntil(t, a, func() bool { return !d.Applying() }, "the send to fail")
		if msg := d.Message(); !strings.Contains(msg, tc.want) || strings.Contains(msg, "mssql") {
			t.Errorf("Msg %d: message %q, want it to contain %q", tc.number, msg, tc.want)
		}
		// The message line hard-clips, and an 80-column screen leaves it
		// about 68 columns (live).
		if w := core.DisplayWidth(d.Message()); w > 66 {
			t.Errorf("Msg %d: message is %d columns, clipped at 80: %q", tc.number, w, d.Message())
		}
	}
}
