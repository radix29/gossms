package tui

import (
	"context"
	"strings"

	"github.com/radix29/gosmo"
)

// The four states a permission cell can hold, spelled the way
// sys.database_permissions/sys.server_permissions report them so a state
// read back from the server needs no translation. The empty string is
// "no explicit entry" — what a REVOKE leaves behind.
const (
	permStateNone      = ""
	permStateGrant     = "GRANT"
	permStateGrantWith = "GRANT_WITH_GRANT_OPTION"
	permStateDeny      = "DENY"
)

// nextPermState advances a permission cell one step round the cycle
// (none) -> Grant -> Grant With Grant -> Deny -> (none), which is the order
// SSMS's own checkbox columns read down the page.
func nextPermState(s string) string {
	switch s {
	case permStateGrant:
		return permStateGrantWith
	case permStateGrantWith:
		return permStateDeny
	case permStateDeny:
		return permStateNone
	default:
		return permStateGrant
	}
}

// displayPermState renders a state for the grid's State column.
func displayPermState(s string) string {
	switch s {
	case permStateGrant:
		return "Grant"
	case permStateGrantWith:
		return "Grant With Grant"
	case permStateDeny:
		return "Deny"
	default:
		return "(none)"
	}
}

// permStateCycleNote is the hint every permissions grid carries under it.
const permStateCycleNote = "Space/Enter (or click) on State cycles Grant → Grant With Grant → Deny → (none)."

// permApplyFn issues one GRANT/DENY/REVOKE at whatever scope the page
// edits. verb is "GRANT", "DENY" or "REVOKE"; opts carries the WITH GRANT
// OPTION / CASCADE / GRANT OPTION FOR modifiers permTransition worked out.
// One function rather than a grant/deny/revoke triple, because the modifiers
// are decided from the *pair* of states and a three-way split has nowhere to
// put that.
type permApplyFn func(ctx context.Context, verb string, opts gosmo.PermissionOptions, permission, principal string) error

// permTransition returns the single statement that moves a permission from
// orig to current, and the modifiers it needs.
//
// The modifiers are not cosmetic. SQL Server refuses to revoke or deny a
// permission that was granted WITH GRANT OPTION unless CASCADE is present
// ("...because the permission was granted WITH GRANT OPTION"), so every
// transition *out of* Grant With Grant carries it — which is why orig has to
// be consulted at all, and why a grid that only knew the new state could not
// build these statements. The Grant With Grant -> Grant step is the one that
// is not a plain re-grant: REVOKE GRANT OPTION FOR takes away the right to
// re-grant and leaves the underlying GRANT standing, where a bare
// GRANT would leave the grant option in place and change nothing.
func permTransition(orig, current string) (string, gosmo.PermissionOptions) {
	hadGrantOption := orig == permStateGrantWith
	switch current {
	case permStateGrant:
		if hadGrantOption {
			return "REVOKE", gosmo.PermissionOptions{GrantOptionOnly: true}
		}
		return "GRANT", gosmo.PermissionOptions{}
	case permStateGrantWith:
		return "GRANT", gosmo.PermissionOptions{WithGrantOption: true}
	case permStateDeny:
		return "DENY", gosmo.PermissionOptions{Cascade: hadGrantOption}
	default:
		return "REVOKE", gosmo.PermissionOptions{Cascade: hadGrantOption}
	}
}

// applyPermChange routes one orig->current change through apply. A no-op
// change issues nothing.
//
// It never moves orig onto current, even once the write has landed: an apply
// closure runs off the UI goroutine and under Script Changes, so it writes no
// page state (docs/ui-rules.md). The baseline is put right by the reload that
// follows instead. A full Apply reloads every page; one that fails part-way
// reloads each page whose statements reached the server (PropDialog.
// applyFailed). That reload is what keeps the data-losing case honest: cell X
// dropped from Grant With Grant to Grant, REVOKE GRANT OPTION FOR landed, a
// later cell failed — and the user then putting X back to Grant With Grant,
// the state the page had loaded with, must still read as a change to send.
func applyPermChange(ctx context.Context, apply permApplyFn, orig, current, permission, principal string) error {
	if orig == current {
		return nil
	}
	verb, opts := permTransition(orig, current)
	return apply(ctx, verb, opts, permission, principal)
}

// databasePermApply adapts a database-scoped permission edit to gosmo.
func databasePermApply(d *gosmo.Database) permApplyFn {
	return func(ctx context.Context, verb string, opts gosmo.PermissionOptions, permission, principal string) error {
		p := gosmo.DatabasePermission(permission)
		switch verb {
		case "GRANT":
			return d.GrantDatabasePermission(ctx, p, principal, opts)
		case "DENY":
			return d.DenyDatabasePermission(ctx, p, principal, opts)
		default:
			return d.RevokeDatabasePermission(ctx, p, principal, opts)
		}
	}
}

// objectPermApply adapts a table/view permission edit to gosmo.
func objectPermApply(d *gosmo.Database, schema, name string) permApplyFn {
	return func(ctx context.Context, verb string, opts gosmo.PermissionOptions, permission, principal string) error {
		p := gosmo.ObjectPermission(permission)
		switch verb {
		case "GRANT":
			return d.GrantPermission(ctx, schema, name, p, principal, opts)
		case "DENY":
			return d.DenyPermission(ctx, schema, name, p, principal, opts)
		default:
			return d.RevokePermission(ctx, schema, name, p, principal, opts)
		}
	}
}

// schemaPermApply adapts a schema permission edit to gosmo.
func schemaPermApply(d *gosmo.Database, schemaName string) permApplyFn {
	return func(ctx context.Context, verb string, opts gosmo.PermissionOptions, permission, principal string) error {
		p := gosmo.ObjectPermission(permission)
		switch verb {
		case "GRANT":
			return d.GrantSchemaPermission(ctx, schemaName, p, principal, opts)
		case "DENY":
			return d.DenySchemaPermission(ctx, schemaName, p, principal, opts)
		default:
			return d.RevokeSchemaPermission(ctx, schemaName, p, principal, opts)
		}
	}
}

// serverPermApply adapts a server-scoped permission edit to gosmo.
func serverPermApply(srv *gosmo.Server) permApplyFn {
	return func(ctx context.Context, verb string, opts gosmo.PermissionOptions, permission, principal string) error {
		p := gosmo.ServerPermission(permission)
		switch verb {
		case "GRANT":
			return srv.GrantServerPermission(ctx, p, principal, opts)
		case "DENY":
			return srv.DenyServerPermission(ctx, p, principal, opts)
		default:
			return srv.RevokeServerPermission(ctx, p, principal, opts)
		}
	}
}

// matchesFilter reports whether any of fields contains term,
// case-insensitively. An empty term matches everything, which is what makes
// a filter box start out showing the whole list.
func matchesFilter(term string, fields ...string) bool {
	term = strings.TrimSpace(strings.ToLower(term))
	if term == "" {
		return true
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), term) {
			return true
		}
	}
	return false
}
