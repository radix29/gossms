// Package gate answers one question for the application layer: may this
// connection be offered this action?
//
// A [Right] is one permission (or one fixed-role membership) an action needs;
// [RightsAllow] is the whole of the rule, and [Item], [ItemOn] and [ItemOnAll]
// wrap a menu item in it. The rule fails *open*: an action is withheld only
// when the server answered "no" to every right that would permit it, so an
// unprobed connection, a failed probe and a permission this instance does not
// define all leave the action offered. The one answer that withholds rather
// than adds is an explicit DENY — see [ObjectDenial].
//
// The package knows nothing about App, and imports only gosmo,
// internal/db and tuikit/controls. It is not a tuikit-style leaf: the
// capability probe it reads lives on db.ServerConn. The layering rule is the
// usual one — gate never imports tui.
//
// docs/db-rules.md § Permission gating is the authority on what a right may
// be declared for; docs/decisions.md § Permission gating records the questions
// already settled.
package gate
