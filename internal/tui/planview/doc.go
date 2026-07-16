// Package planview is a reusable TUI control that renders a parsed SQL
// Server execution plan (internal/showplan.Plan) as a tabbed view: a
// graphical operator plan, an expandable tree, and the raw plan XML.
//
// PlanView knows nothing about gossms' App — like every tuikit control it
// talks outward only through callbacks and getters, so it can be embedded
// in a query panel's results area or hosted in its own standalone panel.
// See todo/plan/planview-architecture.md for the full design.
package planview
