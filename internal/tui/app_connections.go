package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
)

// ---- Connection management ----

// connectServer dials opts on a background goroutine and, once it resolves,
// hands the outcome to done on the UI goroutine, ahead of acting on it. done
// may be nil; it reports whether the caller still wants the attempt.
//
// Cancelling ctx aborts the dial in flight — the Connect dialog's Cancel does,
// and then answers false here. A cancelled attempt can still have finished
// connecting in the moment before the cancel landed, so an unwanted one is
// wound back rather than half-applied: a connection that arrived anyway is
// closed instead of appearing in Object Explorer under a dialog the user
// dismissed, and a failure is left on the status bar without an alert popping
// over whatever they moved on to.
func (a *App) connectServer(ctx context.Context, opts config.Connection, done func(err error) bool) {
	a.setStatus(fmt.Sprintf("Connecting to %s...", opts.Server))
	a.draw()

	a.safego("connecting to the server", func() {
		sc, err := db.ConnectContext(ctx, opts, db.RoleExplorer)
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
				if dbErr, ok := errors.AsType[*db.ConnectionError](err); ok {
					a.setStatus(fmt.Sprintf("Connection error [%s]: %s", dbErr.Server, dbErr.Cause))
					if wanted {
						a.alertDialog.ShowAlert("Connection Error", fmt.Sprintf("Could not connect to %s: %s", dbErr.Server, dbErr.Cause))
					}
				} else {
					a.setStatus(fmt.Sprintf("Connection failed: %v", err))
					if wanted {
						a.alertDialog.ShowAlert("Connection Error", fmt.Sprintf("Could not connect to %s: %v", opts.Server, err))
					}
				}
				return
			}
			if !wanted {
				// Nothing else references sc — closing it here is what keeps
				// a cancelled attempt from leaking a live session for the
				// rest of the process's lifetime.
				sc.Close()
				a.setStatus(fmt.Sprintf("Cancelled connecting to %s", opts.Server))
				return
			}
			// Before the tree can start loading off it: an Always On folder
			// expanded on the first frame reaches its primary through Peer.
			sc.SetPeerCredentials(a.peerCredentialsFor)
			a.connections = append(a.connections, sc)
			// Expanded straight away, the way SSMS opens a new connection:
			// the server node alone says nothing, and the first thing anyone
			// does with it is open it.
			a.explorer.ExpandNode(a.explorer.AddRoot(sc.Label(), sc))
			info := sc.Server.Info()
			a.setStatus(fmt.Sprintf("Connected to %s  |  %s %s", opts.Server, info.Edition, info.ProductVersion))
			a.ensureSysCompletionInventory(sc)

			// Only a successful connection is worth remembering — save it
			// (auto-named "server,user,database", most-recently-used,
			// capped to config.MaxSavedConnections) for the Connect
			// dialog's server-field autocomplete.
			a.cfg.AddOrUpdate(opts)
			// The same connection, remembered as the way to reach that
			// instance from any other one: connecting to a replica once is
			// how the user gives Peer credentials for it.
			a.rememberPeerCredentials(opts)
			// A direct connect that succeeded is proof the instance is up,
			// which is exactly what the negative peer cache on every other
			// connection is claiming otherwise for up to peerFailureTTL.
			a.forgetPeerFailure(opts.Server)
			if err := a.cfg.Save(); err != nil {
				a.logStatus("save config: %v", err)
			}
		})
	})
}

// connectForQueryPanel opens a dedicated connection for qp, cloning sc's own
// connection options — every query window gets its own connection, distinct
// from (and outliving) whichever one Object Explorer used to resolve it.
// database, if non-empty, overrides which database the new connection starts
// in. Connecting is async, same as connectServer; qp.conn is nil (and the
// panel shows as disconnected) until it resolves. onConnected, if non-nil,
// runs once qp.conn is set — openQueryWithTextAndExecute uses it to run the
// panel's query as soon as the connection is usable.
//
// The connection is two things: a pool, for IntelliSense and catalog reads,
// and one query.Session taken out of it for the panel's lifetime, which every
// Execute runs on — see query.Session for why a pool alone loses temp tables,
// SET options and transactions between runs. Catalog reads stay on the pool
// so they never queue behind a running query. The session's own DB_NAME() is
// where the panel starts, which is the login's default database when none was
// asked for.
func (a *App) connectForQueryPanel(qp *QueryPanel, sc *db.ServerConn, database string, onConnected func()) {
	opts := sc.Opts
	if database != "" {
		opts.Database = database
	}
	qp.database = opts.Database
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
			if err != nil {
				a.setStatus(fmt.Sprintf("Connection failed: %v", err))
				return
			}
			if !a.panelHosted(qp) {
				// qp was closed while this connection was still resolving —
				// nothing else references newConn, so close it here or it
				// leaks for the rest of the process's lifetime.
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

// connectForActivityMonitor opens the Activity Monitor's own connection,
// cloning sc's options the way connectForQueryPanel does, and starts the
// collector on it. The panel owns this connection and closes it on
// teardown; collection must not share App's connection, since a DMV read
// every couple of seconds would sit in front of whatever Object Explorer is
// doing on it.
func (a *App) connectForActivityMonitor(am *ActivityMonitor, sc *db.ServerConn) {
	opts := sc.Opts
	a.safego("connecting Activity Monitor", func() {
		newConn, err := db.ConnectContext(context.Background(), opts, db.RoleActivityMonitor)
		a.postAndWake(func() {
			if err != nil {
				// Both feeds, not just the activity one: neither collector
				// will ever start, and the TempDB tab would otherwise keep
				// saying it was waiting for its first sample.
				am.act.status = fmt.Sprintf("Connection failed: %v", err)
				am.td.status = am.act.status
				return
			}
			if !a.panelHosted(am) {
				// The panel was closed while this was resolving — nothing
				// else references newConn, so close it here or it leaks for
				// the rest of the process's lifetime.
				newConn.Close()
				return
			}
			newConn.SetPeerCredentials(a.peerCredentialsFor)
			am.startCollector(newConn)
		})
	})
}

func (a *App) disconnectActive() {
	sc := a.selectedServerConn()
	if sc == nil {
		a.setStatus("Select a connected server in Object Explorer first")
		return
	}
	a.disconnect(sc)
}

// selectedServerConn resolves the *db.ServerConn owning the currently
// selected Object Explorer node, or nil if nothing is selected or the
// selection doesn't resolve to one.
func (a *App) selectedServerConn() *db.ServerConn {
	node := a.explorer.Selected()
	if node == nil {
		return nil
	}
	return resolveConn(node)
}

// disconnect closes sc and removes it from the connection list and the
// explorer tree. Query panels are unaffected: each owns a connection of its
// own (connectForQueryPanel), never sc, so they stay connected — as SSMS's
// query windows do when Object Explorer disconnects.
//
// Both caches keyed off sc are purged too: the Detail Browser's, before
// the tree nodes it's keyed by are dropped, and the autocomplete
// inventories, which are keyed by server/database name rather than by
// connection (see their doc comments).
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

// isConnected reports whether sc is still open. A query panel can outlive
// its connection (or never share one with Object Explorer at all — see
// connectForQueryPanel); this is how it tells, without needing sc to be
// tracked in a.connections.
func (a *App) isConnected(sc *db.ServerConn) bool {
	return sc.IsOpen()
}

// requireConn is the guard every action needing a live connection opens
// with: it reports whether sc is still open, setting the standard status
// message when it isn't. Callers read as
// `if !a.requireConn(sc) { return }`.
func (a *App) requireConn(sc *db.ServerConn) bool {
	if a.isConnected(sc) {
		return true
	}
	a.setStatus(notConnectedMessage)
	return false
}

// notConnectedMessage is the one wording used everywhere a connection is
// missing — status bar and QueryPanel's results notice alike.
const notConnectedMessage = "Not connected — use File > Connect"

// activeServerConn resolves the connection a Tools-menu/toolbar action acts on
// when it names none: whichever the Object Explorer selection belongs to,
// falling back to the first open connection when nothing relevant is selected.
// nil when nothing is connected at all.
//
// Side-effect free, which is the whole reason it exists next to connOrFirst: a
// menu item's Enabled predicate runs while the menu is being drawn, and
// connOrFirst sets a status message on its way out. Gate on this and act on
// connOrFirst, so the item is offered for the connection it would open —
// gating on selectedServerConn instead left the gate reading nil (fail-open)
// whenever nothing was selected, while the action went to connections[0].
func (a *App) activeServerConn() *db.ServerConn {
	if sc := a.selectedServerConn(); sc != nil {
		return sc
	}
	if len(a.connections) > 0 {
		return a.connections[0]
	}
	return nil
}

// connOrFirst is activeServerConn for a caller about to act: it reports the
// missing connection on the status bar, so callers read as
// `sc := a.connOrFirst(); if sc == nil { return }`.
func (a *App) connOrFirst() *db.ServerConn {
	sc := a.activeServerConn()
	if sc == nil {
		a.setStatus(notConnectedMessage)
	}
	return sc
}

// selectedConnTarget resolves what "the current Object Explorer selection"
// means for opening a new query: the connection and database the selected
// node belongs to, falling back to the first open connection (server
// default database) when nothing is selected — same fallback
// showServerProperties uses. Returns a nil sc if there's nothing to connect
// to at all.
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
