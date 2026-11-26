package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/tuikit/controls"
)

// onExplorerConnection reports whether p is connected under the same identity
// (server, login, auth) as Object Explorer's sc, so a database node of sc
// names a database p could be in. Identity, not pointer: a query panel opens
// its own connection from the same options. A nil p is never on it.
func (p *QueryPanel) onExplorerConnection(sc *db.ServerConn) bool {
	return p != nil && p.connected() && sc != nil &&
		p.conn.Opts.IdentityKey() == sc.Opts.IdentityKey()
}

// activeDatabase reports whether the database name on Object Explorer's
// connection sc is p's current database: the one row draws emphasized and Use
// Database shows checked.
func activeDatabase(p *QueryPanel, sc *db.ServerConn, name string) bool {
	return p.onExplorerConnection(sc) && p.database == name
}

// databaseChangeRequiresConnection reports whether the engine behind info
// cannot switch databases with USE (Azure SQL Database and Synapse): there a
// database change means a new connection, so Use Database stays disabled.
func databaseChangeRequiresConnection(info *gosmo.ServerInfo) bool {
	return info.IsDatabaseScoped() || info != nil && info.EngineEdition == gosmo.EngineAzureSynapse
}

// canUseDatabase reports whether Use Database on node can run in p now: p is
// idle on sc's identity and open, node is an online database other than p's
// current one, and the engine switches databases with USE.
func (p *QueryPanel) canUseDatabase(sc *db.ServerConn, node *explorerNode) bool {
	return p.onExplorerConnection(sc) && sc.IsOpen() && !p.executing &&
		p.connectingTo == "" && !node.data.IsOffline && node.data.DBName != "" &&
		p.database != node.data.DBName && p.conn.Server != nil &&
		!databaseChangeRequiresConnection(p.conn.Server.Info())
}

// useDatabaseMenuItem is a database node's Use Database context-menu item: it
// switches the active query panel to node's database, checked while that is
// already the panel's database.
func (a *App) useDatabaseMenuItem(sc *db.ServerConn, node *explorerNode) controls.MenuItem {
	return controls.MenuItem{
		Label: "Use Database",
		Enabled: func() bool {
			return a.activeQueryPanel().canUseDatabase(sc, node)
		},
		Checked: func() bool {
			return activeDatabase(a.activeQueryPanel(), sc, node.data.DBName)
		},
		Note: "Changing databases requires a new connection on this engine.",
		NoteWhen: func() bool {
			return sc != nil && sc.Server != nil && databaseChangeRequiresConnection(sc.Server.Info())
		},
		Action: func() {
			p := a.activeQueryPanel()
			if !p.canUseDatabase(sc, node) {
				a.setStatus("Database context cannot be changed for the active query panel")
				return
			}
			p.changeDatabase(node.data.DBName)
		},
	}
}

// changeDatabase runs USE database on p's session and takes the database the
// server reports afterwards as p's, reporting a failure in an alert.
func (p *QueryPanel) changeDatabase(database string) {
	p.editor.CloseCompletion()
	p.launchSession("changing the query database", fmt.Sprintf("Changing database to %s...", database), nil,
		func(ctx context.Context, sess *query.Session) *query.Result {
			ctx, cancel := context.WithTimeout(ctx, serverWriteTimeout)
			defer cancel()
			return sess.Execute(ctx, "USE "+gosmo.QuoteName(database)+";")
		},
		func(res *query.Result, cancelled bool) {
			if res.SessionLost {
				return
			}
			if res.Database != "" {
				p.database = res.Database
			} else if !res.HasErrors() && !cancelled {
				// USE succeeded even if its post-run state read was unavailable.
				p.database = database
			}
			switch {
			case cancelled:
				p.app.setStatus("Database change cancelled")
			case res.HasErrors():
				var errors []string
				for _, msg := range res.Messages {
					if msg.IsError {
						errors = append(errors, msg.Text)
					}
				}
				p.app.setStatus("Could not change database")
				p.app.alertDialog().ShowAlert("Change Database", strings.Join(errors, "\n"))
			default:
				p.app.setStatus(fmt.Sprintf("Current database: %s", p.database))
			}
		})
}
