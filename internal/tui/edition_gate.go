package tui

import (
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// edition_gate.go withholds what the *engine edition* refuses, as
// permission_gate.go withholds what the login may not do. The two are
// deliberately separate questions asked in the same shape — a disabled item
// carrying a short note — and they compose: gateAzure wraps the result of
// gate, and the edition's note wins, because no permission gets a user past
// an edition that does not implement the statement at all.
//
// Every entry here is a refusal driven live against a Managed Instance
// (t-qmi-01, EngineEdition 8, 2026-09-08), written up in
// docs/plan-azure-managed-instance.md § 7:
//
//	Detach Database          Could not find stored procedure 'sp_detach_db'
//	Attach Database          same class
//	Take Database Offline    Msg 5008, ALTER DATABASE statement is not supported
//	Recovery model           Msg 5008 — user databases are FULL only
//	CREATE DATABASE files    Msg 41918, specifying files and filegroups is not
//	                         supported
//
// The last of those is why New Database is gated row by row rather than
// wholesale: CREATE DATABASE itself succeeds on an MI as long as the file and
// filegroup clauses are left off, which is the documented "server default"
// path, so blocking the dialog would withhold something that works.

// serverIsAzure reports whether sc is connected to an Azure engine edition —
// SQL Database, Managed Instance, Synapse or SQL Edge. A connection with no
// server info answers no, so an unreachable probe never withholds an action,
// matching allowsActionOn's fail-open rule.
func serverIsAzure(sc *db.ServerConn) bool {
	return sc != nil && sc.Server != nil && sc.Server.Info().IsAzure()
}

// editionNote is the note an edition-gated item carries: "not on Managed
// Instance", naming the edition rather than "Azure" so the sentence stays
// true of whichever one the user is actually connected to.
func editionNote(sc *db.ServerConn) string {
	if !serverIsAzure(sc) {
		return ""
	}
	return "not on " + engineEditionName(sc.Server.Info().EngineEdition)
}

// gateAzure disables item when the connected instance is an Azure engine
// edition, which refuses the statement it emits.
//
// It is applied outside gate, never instead of it: an action is withheld from
// a login without the rights on every edition, and the permission note stays
// right on the ones that do implement it. NoteWhen is unconditional here
// because the edition is the reason whenever it applies at all — the note
// gate replaces sets is the one that would have sent the user after a
// permission that cannot help.
func gateAzure(item controls.MenuItem, sc *db.ServerConn) controls.MenuItem {
	if !serverIsAzure(sc) {
		return item
	}
	item.Enabled = func() bool { return false }
	item.Note = editionNote(sc)
	item.NoteWhen = func() bool { return true }
	return item
}
