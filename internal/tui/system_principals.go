package tui

import (
	"strings"

	gosmo "github.com/radix29/gosmo"
)

// system_principals.go decides which of the principals SQL Server creates for
// itself the UI treats as built-in: Object Explorer offers neither Delete nor
// Rename on one (nodeData.IsSystem, gated in objectOpsMenuItems), and the
// properties pages draw their identity fields read-only.
//
// The Security folders have no "System *" sub-folder to separate them the way
// Databases, Views, Stored Procedures and Functions do, so every one of these
// arrives in the same folder as the user's own principals and has to be told
// apart by what it is rather than by which loader produced it.
//
// # The rule, and why it isn't "everything SQL Server made"
//
// A principal is treated as built-in when the server refuses to drop or rename
// it, so that the UI never offers an action that cannot work. Verified on
// win10cli (SQL Server 17.0) by attempting each one in a throwaway database:
//
//	dbo, guest, sys, INFORMATION_SCHEMA   both refused, as users and as schemas
//	the fixed database roles              both refused
//	public                                both refused
//	the fixed server roles, incl. ##MS_*  both refused
//	sa                                    drop refused; RENAME IS ALLOWED
//	the db_* fixed-role schemas           BOTH ALLOWED
//
// The last two lines are why this file is a set of predicates and not one
// "created by SQL Server" test. Renaming `sa` is a documented hardening step
// that SSMS offers, and the `db_owner`/`db_datareader`/... schemas are
// vestigial and really can be dropped — gating either would take away
// something legitimate. Do not "tidy" them into the general rule.
//
// isSystemLogin is the one deliberate exception to the refusal rule; its own
// comment says why.
//
// The trap in the table above: **public is not is_fixed_role in either
// family** — it is principal_id 0 (database) and 2 (server) with the flag
// clear — so IsFixedRole alone silently misses the one role every principal
// belongs to. Do not simplify those two predicates down to the flag.

// isSystemUser reports whether a user's name/login/default schema can't be
// changed: ALTER USER on any of these fails outright ("Cannot rename the
// user 'guest'.", "Cannot alter the user 'dbo'.", same for
// sys/INFORMATION_SCHEMA), unlike the ordinary permission errors other
// ALTER USER failures produce. DROP USER is refused for the same four.
func isSystemUser(name string) bool {
	switch name {
	case "dbo", "guest", "sys", "INFORMATION_SCHEMA":
		return true
	default:
		return false
	}
}

// isSystemSchema reports whether a schema can be neither dropped nor
// re-owned — the same fixed four isSystemUser covers, for the same reason.
//
// Note this is *not* every schema SQL Server created: the schema behind each
// fixed database role (db_owner, db_datareader, ...) drops and re-owns
// perfectly happily, verified live, so those are ordinary schemas here.
func isSystemSchema(name string) bool {
	switch name {
	case "dbo", "guest", "sys", "INFORMATION_SCHEMA":
		return true
	default:
		return false
	}
}

// isSystemDatabaseRole reports whether r is a fixed database role or public —
// neither its name nor its owner can be changed. DROP ROLE is refused, and
// both ALTER ROLE public WITH NAME=... and ALTER AUTHORIZATION ON ROLE::public
// are syntax errors rather than permission failures.
func isSystemDatabaseRole(r *gosmo.DatabaseRole) bool {
	return r.IsFixedRole || r.Name == publicRoleName
}

// isSystemServerRole reports whether r is a fixed server role or public, with
// the same consequence as isSystemDatabaseRole: ALTER SERVER ROLE public WITH
// NAME=... and ALTER AUTHORIZATION ON SERVER ROLE::public are both syntax
// errors, "public" being a reserved keyword in that position. The ##MS_*##
// server roles report IsFixedRole, so they need no name test.
func isSystemServerRole(r *gosmo.ServerRole) bool {
	return r.IsFixedRole || r.Name == publicRoleName
}

// publicRoleName is the one role in each family that every principal belongs
// to and that carries is_fixed_role = 0 despite being undroppable.
const publicRoleName = "public"

// isSystemLogin reports whether l is one SQL Server manages for itself.
//
// The one place this file gates something the server would permit. Dropping
// [##MS_PolicyEventProcessingLogin##] *succeeds* — verified the hard way on
// win10cli — and takes Policy-Based Management's execution identity with it,
// orphaning the matching users in master and msdb; nothing warns and nothing
// fails until a policy runs. That is worth refusing even though the server
// won't.
//
// Deliberately narrow: it matches only the internal ## names, so `sa` and the
// NT SERVICE\* logins stay editable. Renaming `sa` is a documented hardening
// step, and the service logins are ordinary Windows logins an administrator
// may well want gone.
func isSystemLogin(l *gosmo.Login) bool {
	return strings.HasPrefix(l.Name, "##")
}
