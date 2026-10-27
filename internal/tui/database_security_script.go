package tui

import (
	"context"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// database_security_script.go is Object Explorer's database-node "Script
// Database Security": gosmo's Scripter.ScriptDatabaseSecurity — the T-SQL
// recreating the database's owner, roles, users, schemas, role memberships
// and explicit permissions — opened in a new query window on that database.
// It is opened, never executed: running it is the user's call.
//
// Until 2026-10 this was a generator script the user ran to produce that
// T-SQL. gosmo now builds it, sharing ScriptUser's and ScriptDatabaseRole's
// forms and its password placeholder, with a live round trip behind it.

// scriptDatabaseSecurity reads dbName's security in the background and opens
// its script in a new query window connected to that database.
func (a *App) scriptDatabaseSecurity(sc *db.ServerConn, dbName string) {
	n := nodeData{Type: NodeDatabase, DBName: dbName, Name: dbName}
	a.generateScript(sc, n, scriptVerb{gen: databaseSecurityScript}, func(text string) {
		a.openQueryWithText(sc, dbName, text)
	})
}

// databaseSecurityScript is n's database security script, opening with USE
// [db]: the script itself names no database and applies to whichever one it
// runs in.
func databaseSecurityScript(ctx context.Context, sc *db.ServerConn, n nodeData) (string, error) {
	d, err := sc.Server.DatabaseByName(ctx, n.DBName)
	if err != nil {
		return "", err
	}
	s := gosmo.NewScripter(d, gosmo.DefaultScriptOptions())
	text, err := s.ScriptDatabaseSecurity(ctx)
	if err != nil {
		return "", err
	}
	return s.DatabaseContext() + text, nil
}
