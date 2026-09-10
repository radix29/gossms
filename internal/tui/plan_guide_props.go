package tui

import (
	"context"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// plan_guide_props.go is Plan Guide Properties: General, which can enable and
// disable the guide, and Query, which shows the statement it matches and the
// hints it applies.
//
// The one writable page in the Phase 3 tree families. sp_control_plan_guide is
// the whole of what a form can do to a plan guide — sp_create_plan_guide has
// no ALTER, and its arguments (the query text, verbatim to the character, and
// the OPTION clause) are text a user writes rather than fields a form fills.
// Enable/Disable is a real write nonetheless: a disabled guide shapes no plan,
// and it is invisible in the tree except for the label suffix.
//
// The right is rightAlterDatabase, which is what sp_control_plan_guide
// actually checks — there is no "ALTER ANY PLAN GUIDE" for it to take.

func findPlanGuide(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.PlanGuide, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.PlanGuideByNameContext(ctx, name)
}

// scopeSchema and scopeName are the routine an OBJECT-scoped guide is bound
// to, empty for the other scopes — see planGuideRights.
func planGuidePropPages(sc *db.ServerConn, dbName, name, scopeSchema, scopeName string) []propPage {
	return []propPage{
		withRequiresOn(pagePlanGuideGeneral(sc, dbName, name), dbName, scopeSchema, scopeName,
			planGuideRights(scopeName)...),
		pagePlanGuideQuery(sc, dbName, name),
	}
}

// planGuideWriteRights are what permits sp_control_plan_guide on a SQL or
// TEMPLATE guide: ALTER on the database, and the two rights that subsume it.
// There is no plan-guide-scoped right — the procedure's own documentation
// names ALTER DATABASE — so inventing one here would gate the page on a
// permission HAS_PERMS_BY_NAME answers NULL for, which reads as "unknown"
// forever.
func planGuideWriteRights() []requiredRight {
	return []requiredRight{rightAlterDatabase, rightControlDB, rightAlterAnyDatabase}
}

// planGuideRights is what permits sp_control_plan_guide — DROP, ENABLE and
// DISABLE alike — on one guide. An OBJECT-scoped guide is also controlled
// under ALTER on the routine it is bound to, which the schema-object rights
// ask about when handed that routine as the securable; the server's own
// refusal (Msg 10518) names both: "Alter permission on object referenced by
// plan guide, or alter database permission required." Verified live
// 2026-09-10 on majors 13, 14 and 17: db_ddladmin, ALTER ANY SCHEMA and ALTER
// on the routine each drop and disable an OBJECT guide and are refused a SQL
// one, and a DENY of ALTER on the routine or its schema refuses the OBJECT
// guide even to a principal holding ALTER on the database.
func planGuideRights(scopeName string) []requiredRight {
	if scopeName != "" {
		return objectWriteRights()
	}
	return planGuideWriteRights()
}

func pagePlanGuideGeneral(sc *db.ServerConn, dbName, name string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			g, err := findPlanGuide(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			enabled := propsheet.Check("Enabled", !g.IsDisabled)

			f := propsheet.NewForm(
				propsheet.Section("Plan guide"),
				propsheet.Static("Name", g.Name),
				enabled,
				propsheet.Section("Scope"),
				propsheet.Static("Scope type", string(g.Scope)),
				propsheet.Static("Scope object", planGuideScopeText(g)),
				propsheet.Static("Parameters", boundOrNone(g.Parameters)),
				propsheet.Section("Dates"),
				propsheet.Static("Created", formatSQLDate(g.CreateDate)),
				propsheet.Static("Last modified", formatSQLDate(g.ModifyDate)),
				propsheet.Note("Enabling and disabling is the only change this dialog makes: sp_create_plan_guide has no ALTER, and the query text it matches is compared character for character, so an edit here would silently stop matching."),
			)

			apply := func(ctx context.Context) error {
				if !enabled.Dirty() {
					return nil
				}
				// Re-read rather than reusing g: the apply runs after the
				// form was built, and a guide dropped in between must fail
				// here rather than have a control statement sent for it.
				guide, err := findPlanGuide(ctx, sc, dbName, name)
				if err != nil {
					return err
				}
				if enabled.Checked() {
					return guide.EnableContext(ctx)
				}
				return guide.DisableContext(ctx)
			}
			return f, apply, nil
		},
	}
}

// planGuideScopeText renders what an OBJECT-scoped guide is attached to. The
// other two scopes name no object, and an empty value there reads as a page
// that failed to load one.
func planGuideScopeText(g *gosmo.PlanGuide) string {
	if g.Scope == gosmo.PlanGuideScopeObject {
		return boundOrNone(g.ScopeObject)
	}
	return "(not object-scoped)"
}

// pagePlanGuideQuery is the guide's two texts: the statement it matches and
// the hints it applies. Both read-only, both in a SQL editor rather than a
// Static row — the query text is matched verbatim, whitespace included, and a
// row that clipped it would hide exactly the character that decides whether
// the guide matches anything at all.
//
// A guide created from a plan handle carries an XML showplan in Hints rather
// than an OPTION clause; it is shown as it is stored, since that is what
// sp_create_plan_guide was given.
func pagePlanGuideQuery(sc *db.ServerConn, dbName, name string) propPage {
	return propPage{
		title: "Query",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			g, err := findPlanGuide(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(propsheet.Section("Statement matched"))
			f.Add(readOnlySQLRow("Query text", g.QueryText,
				"This guide matches no statement text — it is scoped to a batch or a template instead."))

			if g.Scope == gosmo.PlanGuideScopeSQL && strings.TrimSpace(g.ScopeBatch) != "" {
				f.Add(propsheet.Section("Batch"))
				f.Add(readOnlySQLRow("Batch text", g.ScopeBatch, ""))
			}

			f.Add(propsheet.Section("Hints applied"))
			f.Add(readOnlySQLRow("Hints", g.Hints,
				"This guide applies no hints — it exists to pin a plan rather than to add an OPTION clause."))
			return f, nil, nil
		},
	}
}

// readOnlySQLRow is an editor row for one of a plan guide's texts, or a note
// in its place when the text is empty. empty may be "", which drops the row
// entirely — the batch section is only added when there is a batch.
func readOnlySQLRow(label, text, empty string) propsheet.Row {
	if strings.TrimSpace(text) == "" {
		return propsheet.Note(empty)
	}
	ed := controls.NewEditor(controls.SQLHighlighter(theme.Active()))
	ed.SetText(text)
	ed.SetReadOnly(true)
	return propsheet.NewEditorRow(label, ed, 10)
}
