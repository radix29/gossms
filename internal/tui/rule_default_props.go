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

// rule_default_props.go is the read-only Properties for a standalone rule and
// a standalone default — the two objects CREATE RULE and CREATE DEFAULT make.
//
// Read-only deliberately rather than by omission: Microsoft has deprecated
// both since SQL Server 2008, sp_bindrule and sp_bindefault with them, and
// neither statement has an ALTER. A new tool offering a way to create one
// would be steering users onto a feature the server documents as going away;
// showing the ones a database already has is the whole job. Both pages are
// named in prop_page_requires_test.go's pagesThatOnlyRead.
//
// A default here is never a table's DF_… constraint: gosmo's Defaults read
// carries the parent_object_id = 0 predicate that separates the two families
// living under sys.objects type 'D'.

func findRule(ctx context.Context, sc *db.ServerConn, dbName, schema, name string) (*gosmo.Rule, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.RuleByNameContext(ctx, schema, name)
}

func findDefault(ctx context.Context, sc *db.ServerConn, dbName, schema, name string) (*gosmo.Default, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.DefaultByNameContext(ctx, schema, name)
}

func rulePropPages(sc *db.ServerConn, dbName, schema, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			r, err := findRule(ctx, sc, dbName, schema, name)
			if err != nil {
				return nil, nil, err
			}
			return bindableObjectForm("Rule", r.Name, r.Schema,
				formatSQLDate(r.CreateDate), formatSQLDate(r.ModifyDate), r.Definition,
				"Rules are deprecated by Microsoft — sp_bindrule since SQL Server 2008 — and CREATE RULE has no ALTER. Use a CHECK constraint on the column instead."), nil, nil
		},
	}}
}

func defaultPropPages(sc *db.ServerConn, dbName, schema, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			df, err := findDefault(ctx, sc, dbName, schema, name)
			if err != nil {
				return nil, nil, err
			}
			return bindableObjectForm("Default", df.Name, df.Schema,
				formatSQLDate(df.CreateDate), formatSQLDate(df.ModifyDate), df.Definition,
				"Defaults are deprecated by Microsoft — sp_bindefault since SQL Server 2008 — and CREATE DEFAULT has no ALTER. Use a DEFAULT constraint on the column instead."), nil, nil
		},
	}}
}

// bindableObjectForm builds the one page shape a rule and a default share.
// They are the same row in sys.objects under two type codes, differ in
// nothing this page shows but their heading, and writing the form twice would
// mean two places for a label to drift.
func bindableObjectForm(kind, name, schema, created, modified, definition, note string) *propsheet.Form {
	f := propsheet.NewForm(
		propsheet.Section(kind),
		propsheet.Static("Name", name),
		propsheet.Static("Schema", schema),
		propsheet.Section("Dates"),
		propsheet.Static("Created", created),
		propsheet.Static("Last modified", modified),
	)
	// An encrypted module reports no definition, and so does one the login
	// cannot see into. Saying so beats an empty editor, which reads as an
	// object with an empty body.
	if strings.TrimSpace(definition) == "" {
		f.Add(
			propsheet.Section("Definition"),
			propsheet.Note("The definition is not readable — the module is either encrypted (WITH ENCRYPTION) or invisible to this login, which needs VIEW DEFINITION on it."),
			propsheet.Note(note),
		)
		return f
	}

	ed := controls.NewEditor(controls.SQLHighlighter(theme.Active()))
	ed.SetText(definition)
	ed.SetReadOnly(true)
	f.Add(
		propsheet.Section("Definition"),
		propsheet.NewEditorRow("Body", ed, 10),
		propsheet.Note(note),
	)
	return f
}
