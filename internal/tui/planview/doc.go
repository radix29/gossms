// Package planview is a reusable TUI control that renders a parsed SQL
// Server execution plan (internal/showplan.Plan) as a tabbed view: a
// graphical operator plan, an expandable tree, and the raw plan XML.
//
// PlanView knows nothing about gossms' App: like every tuikit control it talks
// outward only through callbacks and getters, so it embeds in a query panel or
// a standalone panel.
package planview
