package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
)

// ---- Connection management ----

// connectServer dials opts in the background and passes the outcome to done on
// the UI goroutine before acting on it. done may be nil; it reports whether the
// caller still wants the attempt.
//
// Cancelling ctx aborts the dial (the Connect dialog's Cancel does, then
// answers false). A cancelled attempt that connected anyway is closed rather
// than added to Object Explorer, and a failure goes to the status bar without
// an alert.
//
// Sign-in methods run a sign-in phase first (see signInPhase); phase, if
// non-nil, receives each phase's label on the UI goroutine.
func (a *App) connectServer(ctx context.Context, opts config.Connection, phase func(label string), done func(err error) bool) {
	a.setStatus(fmt.Sprintf("Connecting to %s...", opts.Server))
	a.draw()

	a.safego("connecting to the server", func() {
		var sc *db.ServerConn
		err := a.signInPhase(ctx, opts, phase)
		if err == nil {
			sc, err = db.ConnectContext(ctx, opts, db.RoleExplorer)
		}
		a.postAndWake(func() {
			wanted := true
			if done != nil {
				wanted = done(err)
			}
			if err != nil && !wanted && errors.Is(err, context.Canceled) {
				a.setStatus(fmt.Sprintf("Cancelled connecting to %s", opts.Server))
				return
			}
			if err != nil {
				server, cause := opts.Server, err.Error()
				if dbErr, ok := errors.AsType[*db.ConnectionError](err); ok {
					server, cause = dbErr.Server, dbErr.Cause
				}
				a.setStatus(fmt.Sprintf("Connection error [%s]: %s", server, firstErrorLine(cause)))
				if strings.Contains(cause, "\n") {
					// The status bar has one line; the log and status history
					// keep the rest.
					a.logStatus("connect to %s: %s", server, cause)
				}
				if wanted {
					a.alertDialog.ShowAlert("Connection Error",
						fmt.Sprintf("Could not connect to %s: %s", server, tidyErrorText(cause)))
				}
				return
			}
			if !wanted {
				// Nothing else references sc; close it so a cancelled attempt
				// doesn't leak a session.
				sc.Close()
				a.setStatus(fmt.Sprintf("Cancelled connecting to %s", opts.Server))
				return
			}
			// Before the tree loads: an Always On folder expanded on the first
			// frame reaches its primary through Peer.
			sc.SetPeerCredentials(a.peerCredentialsFor)
			a.connections = append(a.connections, sc)
			// Expanded immediately, as SSMS opens a new connection.
			a.explorer.ExpandNode(a.explorer.AddRoot(sc.Label(), sc))
			info := sc.Server.Info()
			a.setStatus(fmt.Sprintf("Connected to %s  |  %s %s", opts.Server, info.Edition, info.ProductVersion))
			a.ensureSysCompletionInventory(sc)

			// Save the successful connection (auto-named, most recent first,
			// capped at config.MaxSavedConnections) for Connect's autocomplete.
			a.cfg.AddOrUpdate(opts)
			// Also remembered as how to reach that instance from others:
			// connecting to a replica once gives Peer its credentials.
			a.rememberPeerCredentials(opts)
			// A successful direct connect proves the instance is up,
			// contradicting other connections' negative peer caches.
			a.forgetPeerFailure(db.ConnectionAddress(opts))
			if err := a.cfg.Save(); err != nil {
				a.logStatus("save config: %v", err)
			}
		})
	})
}

// signInPhase runs db.SignIn before the dial for sign-in methods, showing it on
// the status bar and via phase (may be nil), then back to "Connecting".
// Otherwise returns nil at once. Runs on the connecting goroutine.
func (a *App) signInPhase(ctx context.Context, opts config.Connection, phase func(label string)) error {
	if !db.NeedsSignIn(opts.AuthMethod) {
		return nil
	}
	report := func(status, label string) {
		a.postAndWake(func() {
			a.setStatus(status)
			if phase != nil {
				phase(label)
			}
		})
	}
	where := "complete the sign-in in your browser"
	if opts.AuthMethod == config.AuthEntraDeviceCode {
		where = "enter the code shown at the sign-in page"
	}
	report(fmt.Sprintf("Signing in to Microsoft Entra for %s — %s...", opts.Server, where), "Signing in...")
	if err := db.SignIn(ctx, opts); err != nil {
		return err
	}
	report(fmt.Sprintf("Connecting to %s...", opts.Server), "Connecting...")
	return nil
}

// firstErrorLine is an error's first non-blank line for the one-row status bar,
// which would run a multi-line azidentity error (HTTP dump under a summary)
// together.
func firstErrorLine(msg string) string {
	for line := range strings.Lines(msg) {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return strings.TrimSpace(msg)
}

// tidyErrorText drops the dash rows azcore draws between sections, which an
// alert would wrap as hard-broken 80-column words.
func tidyErrorText(msg string) string {
	var kept []string
	for line := range strings.Lines(msg) {
		line = strings.TrimRight(line, "\r\n")
		if t := strings.TrimSpace(line); len(t) >= 3 && strings.Trim(t, "-") == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// connectForQueryPanel opens qp's own connection by cloning sc's options; every
// query window has its own, outliving Object Explorer's. database, if set, is
// where it starts. Async like connectServer: qp.conn is nil (disconnected)
// until resolved; onConnected, if set, runs once it is
// (openQueryWithTextAndExecute uses it to run immediately).
//
// The connection is a pool for IntelliSense and catalog reads plus one
// query.Session for the panel's lifetime, which every Execute uses (see
// query.Session: a pool loses temp tables, SET options and transactions).
// Catalog reads never queue behind a query. The panel starts in the session's
// DB_NAME() — the login default if none was asked.
func (a *App) connectForQueryPanel(qp *QueryPanel, sc *db.ServerConn, database string, onConnected func()) {
	opts := sc.Opts
	if database != "" {
		opts.Database = database
	}
	qp.database = opts.Database
	qp.connectingTo = opts.Server
	a.setStatus(fmt.Sprintf("Connecting to %s...", opts.Server))

	a.safego("connecting the query panel", func() {
		newConn, err := db.ConnectContext(context.Background(), opts, db.RoleQuery)
		var sess *query.Session
		var state query.SessionState
		if err == nil {
			ctx, cancel := context.WithTimeout(newConn.Context(), childFetchTimeout)
			sess, state, err = query.Open(ctx, newConn.Server.DB(), opts.Database)
			cancel()
			if err != nil {
				newConn.Close()
				err = fmt.Errorf("open a session: %w", err)
			}
		}
		a.postAndWake(func() {
			qp.connectingTo = ""
			if err != nil {
				a.setStatus("Connection failed: " + firstErrorLine(err.Error()))
				return
			}
			if !a.panelHosted(qp) {
				// qp closed while connecting; nothing else references it, so
				// close it here.
				sess.Close()
				newConn.Close()
				return
			}
			newConn.SetPeerCredentials(a.peerCredentialsFor)
			qp.conn = newConn
			qp.session = sess
			qp.tranCount = state.TranCount
			qp.database = state.Database
			a.setStatus(fmt.Sprintf("Connected to %s (SPID %d)", opts.Server, sess.SPID()))
			a.ensureSysCompletionInventory(newConn)
			if onConnected != nil {
				onConnected()
			}
		})
	})
}

// connectForActivityMonitor opens the Activity Monitor's own connection (cloned
// like connectForQueryPanel) and starts the collector. The panel owns and
// closes it; frequent DMV reads mustn't queue ahead of Object Explorer's work.
func (a *App) connectForActivityMonitor(am *ActivityMonitor, sc *db.ServerConn) {
	opts := sc.Opts
	a.safego("connecting Activity Monitor", func() {
		newConn, err := db.ConnectContext(context.Background(), opts, db.RoleActivityMonitor)
		a.postAndWake(func() {
			if err != nil {
				// Both feeds: neither collector will start, and TempDB would
				// otherwise wait forever for a sample.
				am.act.status = "Connection failed: " + firstErrorLine(err.Error())
				am.td.status = am.act.status
				return
			}
			if !a.panelHosted(am) {
				// Panel closed while connecting; close newConn here.
				newConn.Close()
				return
			}
			newConn.SetPeerCredentials(a.peerCredentialsFor)
			am.startCollector(newConn)
		})
	})
}

// clearEntraSignIns runs File > Clear Microsoft Entra Sign-ins: the next Entra
// connection signs in again, switching accounts without a restart.
func (a *App) clearEntraSignIns() {
	db.ClearEntraSignIns()
	a.setStatus("Cleared Microsoft Entra sign-ins — the next Entra connection signs in again")
}

func (a *App) disconnectActive() {
	sc := a.selectedServerConn()
	if sc == nil {
		a.setStatus("Select a connected server in Object Explorer first")
		return
	}
	a.disconnect(sc)
}

// selectedServerConn resolves the *db.ServerConn owning the selected Object
// Explorer node, or nil.
func (a *App) selectedServerConn() *db.ServerConn {
	node := a.explorer.Selected()
	if node == nil {
		return nil
	}
	return resolveConn(node)
}

// disconnect closes sc and removes it from the connection list and tree. Query
// panels keep their own connections (connectForQueryPanel), as SSMS query
// windows survive an Object Explorer disconnect.
//
// Purges the caches keyed off sc too: the Detail Browser's (before dropping the
// nodes it's keyed by) and the autocomplete inventories (keyed by
// server/database name).
func (a *App) disconnect(sc *db.ServerConn) {
	sc.Close()
	if i := slices.Index(a.connections, sc); i >= 0 {
		a.connections = slices.Delete(a.connections, i, i+1)
	}
	a.detailBrowser.PurgeConn(sc)
	a.purgeCompletionInventories(sc)
	a.explorer.RemoveRootByConn(sc)
	a.setStatus("Disconnected")
}

// isConnected reports whether sc is still open, without requiring it to be in
// a.connections.
func (a *App) isConnected(sc *db.ServerConn) bool {
	return sc.IsOpen()
}

// requireConn guards actions needing a live connection: reports whether sc is
// open, setting the standard status otherwise. Use as `if !a.requireConn(sc) {
// return }`.
func (a *App) requireConn(sc *db.ServerConn) bool {
	if a.isConnected(sc) {
		return true
	}
	a.setStatus(notConnectedMessage)
	return false
}

// notConnectedMessage is the single missing-connection wording (status bar and
// QueryPanel results).
const notConnectedMessage = "Not connected — use File > Connect"

// activeServerConn resolves the connection a Tools/toolbar action targets when
// none is named: the selection's, else the first open one; nil if none.
//
// Side-effect free, unlike connOrFirst (which sets a status), because Enabled
// predicates run while drawing. Gate on this and act on connOrFirst so the gate
// checks the connection the action will use.
func (a *App) activeServerConn() *db.ServerConn {
	if sc := a.selectedServerConn(); sc != nil {
		return sc
	}
	if len(a.connections) > 0 {
		return a.connections[0]
	}
	return nil
}

// connOrFirst is activeServerConn that reports a missing connection on the
// status bar: `sc := a.connOrFirst(); if sc == nil { return }`.
func (a *App) connOrFirst() *db.ServerConn {
	sc := a.activeServerConn()
	if sc == nil {
		a.setStatus(notConnectedMessage)
	}
	return sc
}

// selectedConnTarget resolves the connection and database of the selected node
// for a new query, falling back to the first open connection (default database)
// like showServerProperties. nil sc if nothing is connected.
func (a *App) selectedConnTarget() (sc *db.ServerConn, database string) {
	if node := a.explorer.Selected(); node != nil {
		if c := resolveConn(node); c != nil {
			return c, node.data.DBName
		}
	}
	if len(a.connections) > 0 {
		return a.connections[0], ""
	}
	return nil, ""
}
