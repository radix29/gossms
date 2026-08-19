package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// security_policy_props.go is the read-only Properties for a row-level
// security policy. Read-only because the only thing about a policy that can
// be changed in place is its state, and that is the context menu's
// Enable/Disable — every other part needs the policy recreated.

// findSecurityPolicy resolves a policy by schema-qualified name. gosmo's
// finder requires the schema, where this used to accept an empty one and
// match any: every security policy node carries a schema, since
// loadSecurityPoliciesChildren reads it from SCHEMA_NAME.
func findSecurityPolicy(ctx context.Context, sc *db.ServerConn, dbName, schema, name string) (*gosmo.SecurityPolicy, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.SecurityPolicyByNameContext(ctx, schema, name)
}

func securityPolicyPropPages(sc *db.ServerConn, dbName, schema, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			p, err := findSecurityPolicy(ctx, sc, dbName, schema, name)
			if err != nil {
				return nil, nil, err
			}
			rows := make([][]string, len(p.Predicates))
			for i, pred := range p.Predicates {
				rows[i] = []string{pred.PredicateType, fqn(pred.TargetSchema, pred.TargetTable),
					pred.Operation, pred.PredicateDefinition}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Type", "Target", "Operation", "Predicate"}, rows)
			grid.SetCellCursor(true)

			formRows := []propsheet.Row{
				propsheet.Section("Policy"),
				propsheet.Static("Name", p.Name),
				propsheet.Static("Schema", p.Schema),
				propsheet.Static("Enabled", boolStr(p.IsEnabled)),
				propsheet.Static("Schema bound", boolStr(p.IsSchemaBound)),
				propsheet.Static("Not for replication", boolStr(p.IsNotForReplication)),
				propsheet.Section("Predicates"),
				propsheet.NewGridRow(grid, 10),
			}
			if !p.IsEnabled {
				formRows = append(formRows,
					propsheet.Section("Note"),
					propsheet.Note("A disabled policy filters and blocks nothing — every row is visible to every user."),
				)
			}
			return propsheet.NewForm(formRows...), nil, nil
		},
	}}
}
