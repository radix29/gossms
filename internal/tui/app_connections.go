package tui

import (
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

	go func() {
		sc, err := db.Connect(opts)
		a.postEvent(func() {
			if err != nil {
				if dbErr, ok := errors.AsType[*db.ConnectionError](err); ok {
					a.setStatus(fmt.Sprintf("Connection error [%s]: %s", dbErr.Server, dbErr.Cause))
				} else {
					a.setStatus(fmt.Sprintf("Connection failed: %v", err))
				}
				return
			}
			a.connections = append(a.connections, sc)
			a.explorer.AddRoot(sc.Label(), sc)
			info := sc.Server.Info()
			a.setStatus(fmt.Sprintf("Connected to %s  |  %s %s", opts.Server, info.Edition, info.ProductVersion))

			// Only a successful connection is worth remembering — save it
			// (auto-named "server,user,database", most-recently-used,
			// capped to config.MaxSavedConnections) for the Connect
			// dialog's server-field autocomplete.
			a.cfg.AddOrUpdate(opts)
			if err := a.cfg.Save(); err != nil {
				a.logStatus("save config: %v", err)
			}
		})
		a.wakeEventLoop()
	}()
}

// connectForQueryPanel opens a dedicated connection for qp, cloning sc's own
// connection options — SSMS gives every query window its own connection,
// distinct from (and outliving) whichever one Object Explorer used to
// resolve it. database, if non-empty, overrides which database the new
// connection starts in. Connecting is async, same as connectServer; qp.conn
// is nil (and the panel shows as disconnected) until it resolves.
func (a *App) connectForQueryPanel(qp *QueryPanel, sc *db.ServerConn, database string) {
	opts := sc.Opts
	if database != "" {
		opts.Database = database
	}
	qp.database = opts.Database
	a.setStatus(fmt.Sprintf("Connecting to %s...", opts.Server))

	go func() {
		newConn, err := db.Connect(opts)
		resolvedDB := opts.Database
		if err == nil && resolvedDB == "" {
			resolvedDB = defaultDatabaseName(newConn)
		}
		a.postEvent(func() {
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
			qp.conn = newConn
			qp.database = resolvedDB
			a.setStatus(fmt.Sprintf("Connected to %s", opts.Server))
		})
		a.wakeEventLoop()
	}()
}

// defaultDatabaseName resolves the database a connection actually landed
// in when config.Connection.Database was left empty — the login's real
// default database — so the query panel's connection bar and Execute both
// use it instead of showing/USE-ing nothing. Falls back to "master" if the
// server can't be asked (e.g. DB_NAME() fails for some reason).
func defaultDatabaseName(sc *db.ServerConn) string {
	name, err := sc.Server.CurrentDatabase()
	if err != nil || name == "" {
		return "master"
	}
	return name
}

func (a *App) disconnectActive() {
	node := a.explorer.Selected()
	if node == nil {
		a.setStatus("Select a connected server in Object Explorer first")
		return
	}
	sc := resolveConn(node)
	if sc == nil {
		a.setStatus("Select a connected server in Object Explorer first")
		return
	}
	a.disconnect(sc)
}

// disconnect closes sc and removes it from the connection list and the
// explorer tree. Query panels bound to sc keep their reference; they show
// "(disconnected)" in their title and refuse to execute (see runQuery).
func (a *App) disconnect(sc *db.ServerConn) {
	sc.Close()
	if i := slices.Index(a.connections, sc); i >= 0 {
		a.connections = slices.Delete(a.connections, i, i+1)
	}
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
