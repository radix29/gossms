package tui

import (
	"context"
	"fmt"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// explorer_database_mail.go is Management ▸ Database Mail: one leaf whose
// label carries Database Mail's state. Accounts and profiles have no nodes of
// their own (docs/decisions.md) — a profile is an ordered list of accounts and
// its security a (principal, profile, default) triple, so they are edited
// together in Database Mail Properties. The Details pane's view is in
// detail_browser_database_mail.go; the log opens in the Log Viewer as its
// own family (gosmo.ErrorLogDatabaseMail); Send Test E-Mail is
// send_test_mail_dialog.go.

// databaseMailRootLabel is the Database Mail node's base label, before
// databaseMailState's suffix.
const databaseMailRootLabel = "Database Mail"

// databaseMailNode builds Management's Database Mail node, its label carrying
// the state the way the Resource Governor node carries "(Disabled)".
func databaseMailNode(l loaderCtx) *explorerNode {
	st := databaseMailState(l.ctx, l.sc)
	n := l.node(st.label, NodeDatabaseMail, "", "", "")
	st.applyTo(n)
	return n
}

// mailNodeState is what the Database Mail node shows of Database Mail: its
// label, and the state its menu reads.
type mailNodeState struct {
	label string
	state gosmo.MailState
	known bool
}

func (st mailNodeState) applyTo(n *explorerNode) {
	n.label, n.data.MailState, n.data.MailStateKnown = st.label, st.state, st.known
}

// databaseMailState is the Database Mail node's label and state: "(Disabled)"
// while 'Database Mail XPs' is 0, "(Stopped)" while the mail queue is not
// receiving, and bare when started.
//
// A failed read leaves the label bare and the state unknown, as the Resource
// Governor node's does: sysmail_help_status_sp is refused to an msdb user
// outside DatabaseMailUserRole and db_owner (Msg 229, W8), and that is no
// reason to fail the Management folder. gosmo answers "disabled" itself, from
// sys.configurations, without calling the procedure that would refuse it.
func databaseMailState(ctx context.Context, sc *db.ServerConn) mailNodeState {
	bare := mailNodeState{label: databaseMailRootLabel}
	if sc == nil || sc.Server == nil {
		return bare
	}
	st, err := sc.Server.MailStatus(ctx)
	if err != nil {
		return bare
	}
	out := mailNodeState{label: databaseMailRootLabel, state: st, known: true}
	switch st {
	case gosmo.MailDisabled, gosmo.MailStopped:
		out.label += " (" + st.String() + ")"
	}
	return out
}

// databaseMailNodeOf is sc's Database Mail node, or nil when the tree has not
// loaded one.
func (a *App) databaseMailNodeOf(sc *db.ServerConn) *explorerNode {
	for _, r := range a.explorer.roots {
		if r.data.conn == sc {
			return findDescendantByType(r, NodeDatabaseMail)
		}
	}
	return nil
}

// refreshDatabaseMailLabel re-reads the Database Mail node's state in place.
// Management's loader is what reads it, so without this a Refresh of the node
// itself would leave a stale "(Stopped)" until Management was refreshed.
func (a *App) refreshDatabaseMailLabel(sc *db.ServerConn) {
	node := a.databaseMailNodeOf(sc)
	if node == nil {
		return
	}
	a.safego("refreshing the Database Mail node", func() {
		ctx, cancel := context.WithTimeout(sc.Server.Context(), childFetchTimeout)
		defer cancel()
		st := databaseMailState(ctx, sc)
		a.postAndWake(func() {
			if node.retired {
				return
			}
			st.applyTo(node)
			a.explorer.rebuild()
			a.detailBrowser.Retitle(node)
		})
	})
}

// databaseMailMenuItems is the Database Mail node's context menu, looked up
// through nodeMenus (explorer_loaders.go). Script as is spliced in from
// scriptables.
//
// Configure Database Mail... is Properties under the name SSMS gives it; both open
// the same dialog. Neither is gated: the dialog's pages come up read-only, each
// naming the rights it needs, for a login that may read but not change them. View
// Database Mail Log is gated — a login that may not read sysmail_event_log would
// open the viewer only on Msg 229 — while a DatabaseMailUserRole member is offered
// it and reads the events of their own items (the viewer says so).
//
// Start or Stop is offered by the state the label shows, both when it could not be
// read. With 'Database Mail XPs' off the server refuses Send Test E-Mail, Start
// and Stop (Msg 15281, W8), and the General page is where it is turned on; while
// stopped it refuses the send too (Msg 14641 — nothing is queued), so the item is
// withheld until Start.
func databaseMailMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	props := func() { a.showDatabaseMailPropertiesFor(sc, mailPageGeneral) }
	send := mailNeedsStarted(node, gate.Item(controls.MenuItem{Label: "Send Test E-Mail...",
		Action: func() { a.showSendTestMailFor(sc) }}, sc, "", gate.DatabaseMailSendRights()...))
	items := []controls.MenuItem{
		{Label: "Configure Database Mail...", Action: props},
		send,
		gate.Item(controls.MenuItem{Label: "View Database Mail Log", Action: func() {
			a.showLogViewerFor(sc, gosmo.ErrorLogDatabaseMail, 0)
		}}, sc, "", gate.DatabaseMailLogReadRights()...),
		{Divider: true},
	}
	start := mailNeedsXPs(node, gate.Item(controls.MenuItem{Label: "Start Database Mail",
		Action: func() { a.startDatabaseMail(sc, node) }}, sc, "", gate.DatabaseMailConfigRights()...))
	stop := mailNeedsXPs(node, gate.Item(controls.MenuItem{Label: "Stop Database Mail",
		Action: func() { a.stopDatabaseMail(sc, node) }}, sc, "", gate.DatabaseMailConfigRights()...))
	switch {
	case !node.data.MailStateKnown:
		items = append(items, start, stop)
	case node.data.MailState == gosmo.MailStarted:
		items = append(items, stop)
	default:
		items = append(items, start)
	}
	return append(items,
		controls.MenuItem{Divider: true},
		newQuery,
		controls.MenuItem{Divider: true},
		refresh,
		controls.MenuItem{Label: "Properties...", Action: props},
	)
}

// mailNeedsXPs withholds item while the node shows 'Database Mail XPs' off.
// Applied over the permission gate, whose note it replaces: with the option
// off nobody may run it, and naming a right would send the user after one
// they may already hold.
func mailNeedsXPs(node *explorerNode, item controls.MenuItem) controls.MenuItem {
	if !node.data.MailStateKnown || node.data.MailState != gosmo.MailDisabled {
		return item
	}
	return mailWithheld(item, "XPs off")
}

// mailNeedsStarted is mailNeedsXPs for sending, which a stopped Database
// Mail refuses as well.
func mailNeedsStarted(node *explorerNode, item controls.MenuItem) controls.MenuItem {
	if node.data.MailStateKnown && node.data.MailState == gosmo.MailStopped {
		return mailWithheld(item, "stopped")
	}
	return mailNeedsXPs(node, item)
}

func mailWithheld(item controls.MenuItem, note string) controls.MenuItem {
	item.Enabled = func() bool { return false }
	item.Note = note
	item.NoteWhen = func() bool { return true }
	return item
}

// startDatabaseMail starts the mail queue. Not confirmed, as no Start is; it
// starts no process either — DatabaseMail.exe starts on the first message.
func (a *App) startDatabaseMail(sc *db.ServerConn, node *explorerNode) {
	a.runDatabaseMailCommand(sc, node, "Start Database Mail", "Starting Database Mail...", "Database Mail started",
		func(ctx context.Context) error { return sc.Server.StartDatabaseMail(ctx) })
}

// stopDatabaseMail stops the mail queue, confirmed: sp_send_dbmail then
// refuses every message (Msg 14641) rather than queueing it — whatever sends
// mail through Database Mail on this server fails until Start.
func (a *App) stopDatabaseMail(sc *db.ServerConn, node *explorerNode) {
	a.confirmDialog.ShowConfirm("Stop Database Mail",
		"Stop Database Mail? Until it is started again, every message sent through it is refused, not queued — whatever this server sends by Database Mail, such as Agent alerts and job notifications, fails meanwhile.",
		func(ok bool) {
			if ok {
				a.runDatabaseMailCommand(sc, node, "Stop Database Mail", "Stopping Database Mail...", "Database Mail stopped",
					func(ctx context.Context) error { return sc.Server.StopDatabaseMail(ctx) })
			}
		})
}

// runDatabaseMailCommand runs one of Database Mail's own procedures behind the
// progress dialog, then re-reads the node's label and the Details pane's view
// of it. A failure re-reads too: a cancel can land after the procedure ran.
func (a *App) runDatabaseMailCommand(sc *db.ServerConn, node *explorerNode, title, message, done string,
	run func(ctx context.Context) error) {
	if !a.requireConn(sc) {
		return
	}
	a.runWithProgress(progressJob{title: title, message: message, what: "running a Database Mail command", sc: sc},
		func(ctx context.Context, _ progressReport) error { return run(ctx) },
		func(err error, cancelled bool) {
			switch {
			case cancelled:
				a.setStatus(title + " cancelled")
			case err != nil:
				a.setStatus(fmt.Sprintf("%s failed: %v", title, withPermissionAdvice(err)))
			default:
				a.setStatus(done)
			}
			a.refreshDatabaseMailLabel(sc)
			a.detailBrowser.Invalidate(a, node)
		})
}
