package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// ---- Connection management ----

func (a *App) connectServer(opts config.Connection) {
	a.setStatus(fmt.Sprintf("Connecting to %s...", opts.Server))
	a.draw()

	a.safego("connecting to the server", func() {
		sc, err := db.Connect(opts)
		a.postAndWake(func() {
			if err != nil {
				if dbErr, ok := errors.AsType[*db.ConnectionError](err); ok {
					a.setStatus(fmt.Sprintf("Connection error [%s]: %s", dbErr.Server, dbErr.Cause))
					a.alertDialog.ShowAlert("Connection Error", fmt.Sprintf("Could not connect to %s: %s", dbErr.Server, dbErr.Cause))
				} else {
					a.setStatus(fmt.Sprintf("Connection failed: %v", err))
					a.alertDialog.ShowAlert("Connection Error", fmt.Sprintf("Could not connect to %s: %v", opts.Server, err))
				}
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
func (a *App) connectForQueryPanel(qp *QueryPanel, sc *db.ServerConn, database string, onConnected func()) {
	opts := sc.Opts
	if database != "" {
		opts.Database = database
	}
	qp.database = opts.Database
	a.setStatus(fmt.Sprintf("Connecting to %s...", opts.Server))

	a.safego("connecting the query panel", func() {
		newConn, err := db.Connect(opts)
		resolvedDB := opts.Database
		if err == nil && resolvedDB == "" {
			resolvedDB = defaultDatabaseName(newConn)
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
				newConn.Close()
				return
			}
			newConn.SetPeerCredentials(a.peerCredentialsFor)
			qp.conn = newConn
			qp.database = resolvedDB
			a.setStatus(fmt.Sprintf("Connected to %s", opts.Server))
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
		newConn, err := db.Connect(opts)
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

// defaultDatabaseName resolves the database a connection actually landed
// in when config.Connection.Database was left empty — the login's real
// default database — so the query panel's connection bar and Execute both
// use it. Falls back to "master" if the server can't be asked. Bounded by
// childFetchTimeout so a hung server can't block this background goroutine
// forever.
func defaultDatabaseName(sc *db.ServerConn) string {
	ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
	defer cancel()
	name, err := sc.Server.CurrentDatabaseContext(ctx)
	if err != nil || name == "" {
		return "master"
	}
	return name
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
// explorer tree. Query panels bound to sc keep their reference; they show
// "(disconnected)" in their title and refuse to execute (see runQuery).
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
