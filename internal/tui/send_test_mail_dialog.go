package tui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// send_test_mail_dialog.go is Database Mail's Send Test E-Mail: profile, To,
// Subject and Body, then sp_send_dbmail — and, what the dialog is for, the
// outcome. sp_send_dbmail only queues the message, so success there says
// nothing about the mail server; the dialog follows the item and its log
// rows until it is sent, an error is logged, or it gives up.
//
// It is a one-page sheet but not a New-object dialog: it creates nothing to
// open the Properties of afterwards, and sending twice is sending twice, so
// newObjectDialog's "already created" latch would be wrong. Apply sends and
// waits in the dialog; OK sends, closes once the message is queued, and
// reports the outcome on the status bar; Script Changes shows the
// sp_send_dbmail call.

// How long the dialog follows a test message, and how often it reads it. On
// Windows an unreachable server's error is logged after ~20 s, while the item
// still reads "retrying" — so the wait ends on the first error row, not on
// "failed", which took ~80 s; on Linux nothing is logged for over two
// minutes, the TCP connect timeout, and the item is reported still queued
// (W8).
const (
	mailTestWait      = 30 * time.Second
	mailTestPollEvery = 2 * time.Second
)

// sendTestMailPrefetch is what the page is built from.
type sendTestMailPrefetch struct {
	state      gosmo.MailState
	stateKnown bool
	// profiles is nil when the login may not list them — a
	// DatabaseMailUserRole member (Msg 229, W8) — and the profile is then
	// typed, blank meaning the sender's default.
	profiles []string
	// defaultProfile indexes the public default profile in profiles, or -1.
	defaultProfile int
}

func fetchSendTestMail(ctx context.Context, sc *db.ServerConn) (*sendTestMailPrefetch, error) {
	pf := &sendTestMailPrefetch{defaultProfile: -1}
	switch st, err := sc.Server.MailStatus(ctx); {
	case err == nil:
		pf.state, pf.stateKnown = st, true
	case !isRefusal(err):
		return nil, err
	}
	profiles, err := sc.Server.MailProfiles(ctx)
	switch {
	case isRefusal(err):
		return pf, nil
	case err != nil:
		return nil, err
	}
	pf.profiles = []string{}
	for _, p := range profiles {
		if p.IsDefault {
			pf.defaultProfile = len(pf.profiles)
		}
		pf.profiles = append(pf.profiles, p.Name)
	}
	return pf, nil
}

// mailTestRequest is one message, read off the page on the UI goroutine.
type mailTestRequest struct {
	profile, to, subject, body string
}

// SendTestMailDialog is Send Test E-Mail.
type SendTestMailDialog struct {
	*propsheet.PropertySheet

	app *App
	sc  *db.ServerConn

	// ctx spans one showing; closing the dialog cancels it, and with it a
	// load or an Apply's wait in flight.
	ctx    context.Context
	cancel context.CancelFunc
	load   latest
	run    applyRun

	// request reads the page; nil until it has loaded.
	request func() (mailTestRequest, error)
}

// NewSendTestMailDialog creates the dialog and wires its callbacks.
func NewSendTestMailDialog(app *App) *SendTestMailDialog {
	d := &SendTestMailDialog{app: app}
	d.PropertySheet = propsheet.NewPropertySheet(app.screen, "Send Test E-Mail")
	d.OnLoadPage = d.onLoadPage
	d.OnApply = func() { d.send(false) }
	d.OnOK = func() { d.send(true) }
	d.OnClose = func() { cancelIfSet(d.cancel) }
	d.OnScript = d.script
	d.OnCancelApply = d.run.cancel
	return d
}

// showSendTestMailFor opens Send Test E-Mail on sc.
func (a *App) showSendTestMailFor(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	a.sendTestMailDialog.show(sc)
}

func (d *SendTestMailDialog) show(sc *db.ServerConn) {
	cancelIfSet(d.cancel)
	d.load.Abandon()
	d.ctx, d.cancel = context.WithCancel(sc.Context())
	d.sc = sc
	d.request = nil
	d.SetHeader("Instance: "+sc.Opts.Server, "Connected: yes")
	d.SetPages([]string{"General"})
	d.Show()
}

func (d *SendTestMailDialog) onLoadPage(page, seq int) {
	sc := d.sc
	ctx, token := d.load.BeginTimeout(d.ctx, propFetchTimeout)
	d.app.safegoRepair("loading Send Test E-Mail", func() {
		if d.load.Done(token) {
			d.SetPageError(page, seq, errPageLoadPanicked)
		}
	}, func() {
		pf, err := fetchSendTestMail(ctx, sc)
		d.app.postAndWake(func() {
			if !d.load.Done(token) {
				return
			}
			if err != nil {
				d.SetPageError(page, seq, displayError(err))
				return
			}
			d.SetPageForm(page, seq, d.build(pf))
		})
	})
}

// build is the page, and sets d.request to read it.
func (d *SendTestMailDialog) build(pf *sendTestMailPrefetch) *propsheet.Form {
	rows := []propsheet.Row{propsheet.Section("Test e-mail")}
	if pf.stateKnown {
		rows = append(rows, propsheet.Static("Database Mail", pf.state.String()))
		if pf.state == gosmo.MailStopped {
			rows = append(rows, propsheet.Note("Database Mail is stopped, and refuses mail until it is started (Start Database Mail on the node's menu)."))
		}
	}

	var profile func() (string, error)
	switch {
	case pf.profiles == nil:
		typed := propsheet.Text("Profile", "", 40)
		rows = append(rows, typed, propsheet.Note(
			"This login may not list profiles. Leave Profile blank to send through your default profile, or type one granted to you."))
		profile = func() (string, error) { return strings.TrimSpace(typed.Value()), nil }
	case len(pf.profiles) == 0:
		rows = append(rows, propsheet.Static("Profile", "(none)"), propsheet.Note(
			"No Database Mail profile exists. Create an account and a profile in Configure Database Mail first."))
		profile = func() (string, error) { return "", errors.New("no Database Mail profile exists to send through") }
	default:
		sel := max(pf.defaultProfile, 0)
		pick := propsheet.Select("Profile", pf.profiles, sel)
		rows = append(rows, pick)
		profile = func() (string, error) { return pick.Value(), nil }
	}

	server := d.sc.Opts.Server
	if info := d.sc.Server.Info(); info != nil && info.Name != "" {
		server = info.Name
	}
	// 46 columns fills the form at the sheet's full width (50 drew over its
	// border there); a narrower sheet narrows them (TextRow.Layout). Subject
	// and Body are pre-filled, and read from their start.
	to := propsheet.Text("To", "", 46)
	subject := propsheet.Text("Subject", "Database Mail Test", 46)
	body := propsheet.Text("Body", "This is a test e-mail sent from Database Mail on "+server+".", 46)
	subject.ShowFromStart()
	body.ShowFromStart()
	rows = append(rows, to, subject, body,
		propsheet.Note("Apply sends and waits up to 30 seconds for the outcome; OK sends and reports it on the status bar. Separate several recipients with semicolons. Every attempt is in the Database Mail log."),
	)

	d.request = func() (mailTestRequest, error) {
		p, err := profile()
		if err != nil {
			return mailTestRequest{}, err
		}
		r := mailTestRequest{profile: p, to: strings.TrimSpace(to.Value()), subject: subject.Value(), body: body.Value()}
		if r.to == "" {
			//lint:ignore ST1005 the capital is the "To" field's label
			return mailTestRequest{}, errors.New("To is required")
		}
		return r, nil
	}
	return propsheet.NewForm(rows...)
}

// readRequest is the page's message, or reports on the message line why
// there is none.
func (d *SendTestMailDialog) readRequest() (mailTestRequest, bool) {
	if d.request == nil {
		d.SetMessage("Still loading — try again in a moment.", true)
		return mailTestRequest{}, false
	}
	r, err := d.request()
	if err != nil {
		d.SetMessage(err.Error(), true)
		return mailTestRequest{}, false
	}
	return r, true
}

// send queues the message. On Apply it then follows it here; on OK the
// dialog closes once it is queued and the status bar carries the outcome. A
// refused send keeps the dialog open either way, with what was typed.
func (d *SendTestMailDialog) send(closeOnQueued bool) {
	req, ok := d.readRequest()
	if !ok {
		return
	}
	sc := d.sc
	d.StartApplying("Sending...")
	d.SetMessage("", false)
	runCtx := d.run.start(d.ctx)
	stop := d.run.stop
	done := make(chan struct{})
	d.app.animateUntil("animating the Send Test E-Mail spinner", propsheet.ApplyingSpinner.Period, done)
	d.app.safegoRepair("sending a test e-mail", func() {
		d.SetApplying(false)
		d.SetMessage("Sending stopped unexpectedly — see the log for details.", true)
	}, func() {
		defer close(done)
		defer stop()
		id, err := sc.Server.SendTestMail(runCtx, req.profile, req.to, req.subject, req.body)
		if err != nil || closeOnQueued {
			d.app.postAndWake(func() {
				d.SetApplying(false)
				switch {
				case err != nil && d.run.cancelled:
					d.SetMessage("Send cancelled. It may have been queued already — see the Database Mail log.", false)
				case err != nil:
					d.SetMessage(mailSendErrorText(err, req.profile), true)
				default:
					d.Dismiss()
					d.app.followTestMail(sc, id)
				}
			})
			return
		}
		d.app.postAndWake(func() { d.StartApplying(fmt.Sprintf("Waiting for mail item %d...", id)) })
		res := waitForTestMail(runCtx, sc, id, mailTestWait, mailTestPollEvery)
		d.app.postAndWake(func() {
			d.SetApplying(false)
			text, isErr := res.message()
			d.SetMessage(text, isErr)
			d.app.testMailSettled(sc)
		})
	})
}

// The errors sp_send_dbmail refuses a message with, each captured live on
// 17 (2026-10-01). Their text is the procedure's own — "profile name is not
// valid" — behind gosmo's and the driver's prefixes, which said nothing the
// user could act on (N10).
const (
	errMailProfileInvalid = 14607 // no such profile, or not granted to the sender
	errMailNoDefault      = 14636 // no profile named and no default to fall back on
	errMailStopped        = 14641 // Database Mail stopped; nothing is queued
	errMailXPsOff         = 15281 // 'Database Mail XPs' is off
)

// mailSendErrorText is a refused send as the message line shows it: a plain
// sentence for the errors sp_send_dbmail is known to raise, the raw error —
// with any permission advice — otherwise. A mapped error's raw text goes to
// the log, so nothing the server said is lost. The message line hard-clips,
// so each sentence is kept short enough for an 80-column dialog.
func mailSendErrorText(err error, profile string) string {
	var text string
	if se, ok := gosmo.AsSQLError(err); ok {
		switch se.Number {
		case errMailProfileInvalid:
			text = fmt.Sprintf("Profile %q does not exist or is not granted to you.", profile)
		case errMailNoDefault:
			text = "No default profile for this login — type a profile name."
		case errMailStopped:
			text = "Database Mail is stopped and queued nothing — start it first."
		case errMailXPsOff:
			text = "Database Mail XPs is off — enable it in Configure Database Mail."
		}
	}
	if text == "" {
		return withPermissionAdvice(err).Error()
	}
	log.Printf("Send Test E-Mail: %v", err)
	return text
}

// followTestMail follows a test message sent from a dialog that has since
// closed, reporting on the status bar. It runs under the connection's
// context, not the dialog's, which closing has cancelled.
func (a *App) followTestMail(sc *db.ServerConn, id int) {
	a.setStatus(fmt.Sprintf("Test e-mail queued as mail item %d — waiting for the outcome...", id))
	a.safego("following a test e-mail", func() {
		res := waitForTestMail(sc.Context(), sc, id, mailTestWait, mailTestPollEvery)
		a.postAndWake(func() {
			text, _ := res.message()
			a.setStatus(text)
			a.testMailSettled(sc)
		})
	})
}

// testMailSettled refreshes the Database Mail node's Details, whose failed
// items a test message may have joined.
func (a *App) testMailSettled(sc *db.ServerConn) {
	if node := a.databaseMailNodeOf(sc); node != nil {
		a.detailBrowser.Invalidate(a, node)
	}
}

// script shows the sp_send_dbmail call the page would make. gosmo records
// it under a script context without sending anything.
func (d *SendTestMailDialog) script() {
	req, ok := d.readRequest()
	if !ok {
		return
	}
	text, err := collectScript(d.ctx, func(ctx context.Context) error {
		_, err := d.sc.Server.SendTestMail(ctx, req.profile, req.to, req.subject, req.body)
		return err
	})
	if err != nil {
		d.SetMessage(err.Error(), true)
		return
	}
	d.app.openQueryWithText(d.sc, "msdb", text)
}

// mailTestResult is how far a test message got while it was followed.
type mailTestResult struct {
	id int
	// status is the item's sent_status at the last read; "" if none
	// succeeded.
	status gosmo.MailSentStatus
	// logged is the item's newest error row, condensed (mailErrorSummary);
	// "" when none was logged.
	logged string
	// readErr is a read of the item that failed for another reason than
	// the wait being stopped.
	readErr error
	// stopped is a wait ended by the caller rather than by an outcome or
	// the time limit.
	stopped bool
}

// waitForTestMail follows mail item id until it is sent, an error is logged
// for it, it is failed, or wait has passed, reading it every every.
//
// The error row is what ends a failing wait, not the status: on Windows it
// is logged while the item still reads "retrying", a minute before "failed"
// (W8). The reads are the sender's own — a DatabaseMailUserRole member sees
// their own items and those items' log rows — so no further right is needed.
func waitForTestMail(ctx context.Context, sc *db.ServerConn, id int, wait, every time.Duration) mailTestResult {
	res := mailTestResult{id: id}
	deadline := time.Now().Add(wait)
	for {
		item, err := sc.Server.MailItemByID(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				res.stopped = true
			} else {
				res.readErr = err
			}
			return res
		}
		res.status = item.SentStatus
		if res.status == gosmo.MailSent {
			return res
		}
		events, err := sc.Server.MailEvents(ctx, gosmo.MailEventFilter{MailItemID: id, EventType: gosmo.MailEventError, Max: 1})
		if err == nil && len(events) > 0 {
			res.logged = mailErrorSummary(events[0].Description)
			return res
		}
		if res.status == gosmo.MailFailed {
			return res
		}
		left := time.Until(deadline)
		if left <= 0 {
			return res
		}
		t := time.NewTimer(min(every, left))
		select {
		case <-ctx.Done():
			t.Stop()
			res.stopped = true
			return res
		case <-t.C:
		}
	}
}

// message is the outcome as one line, and whether it is a failure.
func (r mailTestResult) message() (string, bool) {
	item := fmt.Sprintf("Mail item %d", r.id)
	switch {
	case r.readErr != nil:
		return fmt.Sprintf("%s was queued, but reading its status failed: %v", item, withPermissionAdvice(r.readErr)), true
	case r.status == gosmo.MailSent:
		return fmt.Sprintf("Test e-mail sent (mail item %d).", r.id), false
	case r.logged != "" && r.status == gosmo.MailFailed:
		return item + " failed: " + r.logged, true
	case r.logged != "":
		return fmt.Sprintf("%s is %s after an error: %s", item, r.status, r.logged), true
	case r.status == gosmo.MailFailed:
		return item + " failed — see the Database Mail log.", true
	case r.stopped && r.status == "":
		return fmt.Sprintf("Stopped waiting for mail item %d — see the Database Mail log.", r.id), false
	case r.stopped:
		return fmt.Sprintf("Stopped waiting: mail item %d is %s — see the Database Mail log.", r.id, r.status), false
	}
	return fmt.Sprintf("%s is still %s after %d seconds — see the Database Mail log.", item, r.status, int(mailTestWait/time.Second)), false
}

// mailErrorSummary condenses a Database Mail error row to one line: the
// first non-empty "Message:" line of its exception text — "Could not connect
// to mail server. (… 127.0.0.1:1)", the part that says what went wrong —
// or, failing one, its summary sentence. The summary leads the row but is
// the same for every SMTP failure ("… because of the mail server failure.
// (Sending Mail using Account 41 (…). Exception Message:", the message
// itself on later lines), and leading with it pushed the cause off the
// message line, which hard-clips (live on 17). The whole text, several KB
// with .NET stack traces, is in the Log Viewer's details pane (W8).
func mailErrorSummary(desc string) string {
	var summary string
	for line := range strings.Lines(desc) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if summary == "" {
			summary = line
		}
		if i := strings.Index(line, "Message:"); i >= 0 {
			if msg := strings.TrimSpace(line[i+len("Message:"):]); msg != "" {
				return msg
			}
		}
	}
	if i := strings.Index(summary, " (Sending Mail using"); i > 0 {
		return summary[:i]
	}
	return summary
}
