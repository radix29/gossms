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
func applyPermChange(ctx context.Context, apply permApplyFn, orig, current, permission, principal string) error {
	if orig == current {
		return nil
	}
	verb, opts := permTransition(orig, current)
	return apply(ctx, verb, opts, permission, principal)
}

// commitApplied moves a cell's baseline onto the state that was just written.
// A propApply that returns an error leaves the dialog open with every edit
// intact, and the next Apply re-runs the whole closure from the top — so
// without this, orig still names the state the cell had *before* the partial
// apply, and the page is then lying about what the server holds.
//
// The failure that costs data is the user undoing an edit that already
// landed. Cell X sits at Grant With Grant, the user drops it to Grant, Apply
// issues REVOKE GRANT OPTION FOR and then fails on some later cell. The
// server now holds a plain GRANT. The user puts X back to Grant With Grant —
// the state the page loaded with — and presses Apply: with a stale orig the
// cell reads clean, nothing is issued at all, and the grid claims a grant
// option the server no longer has. Dirty() and Revert() are wrong for the
// same reason, both being orig-versus-current comparisons.
//
// Re-issuing the statements themselves is only wasteful, not destructive:
// REVOKE GRANT OPTION FOR, DENY ... CASCADE and REVOKE ... CASCADE were each
// checked against a live server and are idempotent, so a replay leaves the
// permission where the first attempt put it.
//
// Under gosmo.Scripting the statement was captured rather than executed, so
// committing there would mark the page clean and leave the real Apply that
// follows with nothing to do — the same trap commitRename documents.
func commitApplied(ctx context.Context, orig *string, current string) {
	if !gosmo.Scripting(ctx) {
		*orig = current
	}
}

// applyPermEdit applies one grid row's pending change and commits its
// baseline on success — the pairing every permissions grid needs, kept in one
// place so a caller can't do the first half and forget the second.
func applyPermEdit(ctx context.Context, apply permApplyFn, e *permEdit, principal string) error {
	if err := applyPermChange(ctx, apply, e.orig, e.current, e.entry.Permission, principal); err != nil {
		return err
	}
	commitApplied(ctx, &e.orig, e.current)
	return nil
}

// databasePermApply adapts a database-scoped permission edit to gosmo.
func databasePermApply(d *gosmo.Database) permApplyFn {
	return func(ctx context.Context, verb string, opts gosmo.PermissionOptions, permission, principal string) error {
		switch verb {
		case "GRANT":
			return d.GrantDatabasePermissionWithOptionsContext(ctx, permission, principal, opts)
		case "DENY":
			return d.DenyDatabasePermissionWithOptionsContext(ctx, permission, principal, opts)
		default:
			return d.RevokeDatabasePermissionWithOptionsContext(ctx, permission, principal, opts)
		}
	}
}

// objectPermApply adapts a table/view permission edit to gosmo.
func objectPermApply(d *gosmo.Database, schema, name string) permApplyFn {
	return func(ctx context.Context, verb string, opts gosmo.PermissionOptions, permission, principal string) error {
		p := gosmo.ObjectPermission(permission)
		switch verb {
		case "GRANT":
			return d.GrantPermissionWithOptionsContext(ctx, schema, name, p, principal, opts)
		case "DENY":
			return d.DenyPermissionWithOptionsContext(ctx, schema, name, p, principal, opts)
		default:
			return d.RevokePermissionWithOptionsContext(ctx, schema, name, p, principal, opts)
		}
	}
}

// schemaPermApply adapts a schema permission edit to gosmo.
func schemaPermApply(d *gosmo.Database, schemaName string) permApplyFn {
	return func(ctx context.Context, verb string, opts gosmo.PermissionOptions, permission, principal string) error {
		p := gosmo.ObjectPermission(permission)
		switch verb {
		case "GRANT":
			return d.GrantSchemaPermissionWithOptionsContext(ctx, schemaName, p, principal, opts)
		case "DENY":
			return d.DenySchemaPermissionWithOptionsContext(ctx, schemaName, p, principal, opts)
		default:
			return d.RevokeSchemaPermissionWithOptionsContext(ctx, schemaName, p, principal, opts)
		}
	}
}

// serverPermApply adapts a server-scoped permission edit to gosmo.
func serverPermApply(srv *gosmo.Server) permApplyFn {
	return func(ctx context.Context, verb string, opts gosmo.PermissionOptions, permission, principal string) error {
		switch verb {
		case "GRANT":
			return srv.GrantServerPermissionWithOptionsContext(ctx, permission, principal, opts)
		case "DENY":
			return srv.DenyServerPermissionWithOptionsContext(ctx, permission, principal, opts)
		default:
			return srv.RevokeServerPermissionWithOptionsContext(ctx, permission, principal, opts)
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
