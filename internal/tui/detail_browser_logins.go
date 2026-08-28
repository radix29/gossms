package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
)

// loadLoginsDetails lists every server login with its type, enabled state,
// default database, and creation date — all from the single
// Server.Logins() round trip, so unlike the Databases folder there's
// nothing to backfill progressively.
func (db *DetailBrowser) loadLoginsDetails(app *App, sc *dbconn.ServerConn, node *explorerNode, seq int) {
	// data, not node: this runs on a background goroutine and the UI goroutine
	// writes node.data underneath it (see explorerNode.snapshot). node stays
	// behind as the identity panicRepair and postFinal key off.
	data := node.data
	app.safegoRepair("loading login details", db.panicRepair(node, seq), func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()

		logins, err := sc.Server.LoginsContext(ctx)
		if err != nil {
			db.postFinal(app, node, seq, nil, nil, err)
			return
		}

		logins = filterObjects(data.Filter, logins, func(l *gosmo.Login) nodeData {
			return nodeData{Name: l.Name, CreateDate: l.CreateDate}
		})

		rows := make([][]string, 0, len(logins))
		objs := make([]nodeData, 0, len(logins))
		for _, l := range logins {
			status := "Enabled"
			if l.IsDisabled {
				status = "Disabled"
			}
			rows = append(rows, []string{l.Name, l.LoginType, status, l.DefaultDatabase, formatSQLDate(l.CreateDate)})
			objs = append(objs, nodeData{Type: NodeLogin, Name: l.Name})
		}
		cols := []string{"Name", "Type", "Status", "Default Database", "Created"}
		db.postFinalObjects(app, node, seq, cols, rows, objs, nil)
	})
}
