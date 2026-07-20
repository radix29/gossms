package tui

import (
	"context"

	dbconn "github.com/radix29/gossms/internal/db"
)

// loadLoginsDetails lists every server login with its type, enabled state,
// default database, and creation date — all from the single
// Server.Logins() round trip, so unlike the Databases folder there's
// nothing to backfill progressively.
func (db *DetailBrowser) loadLoginsDetails(app *App, sc *dbconn.ServerConn, node *explorerNode, seq int) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), childFetchTimeout)
		defer cancel()

		logins, err := sc.Server.LoginsContext(ctx)
		if err != nil {
			db.postFinal(app, node, seq, nil, nil, err)
			return
		}

		rows := make([][]string, 0, len(logins))
		for _, l := range logins {
			status := "Enabled"
			if l.IsDisabled {
				status = "Disabled"
			}
			rows = append(rows, []string{l.Name, l.LoginType, status, l.DefaultDatabase, formatSQLDate(l.CreateDate)})
		}
		cols := []string{"Name", "Type", "Status", "Default Database", "Created"}
		db.postFinal(app, node, seq, cols, rows, nil)
	}()
}
