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

// databaseTriggerPropPages builds the page set for Database Trigger
// Properties: General and Definition, both read-only.
//
// Nothing here writes, and that is the object's shape rather than an omission
// — the same reason serverTriggerPropPages gives one scope up. A database-scope
// DDL trigger has exactly three writes: ENABLE, DISABLE and DROP ... ON
// DATABASE, all three Object Explorer commands on the node. Enabling and
// disabling change what the database does the moment they run, which is not an
// Apply, and there is no ALTER a form could build — the body is edited as
// T-SQL, through Script Database Trigger as > ALTER To. Neither page declares
// requires for that reason; both are named in prop_page_requires_test.go's
// pagesThatOnlyRead.
func databaseTriggerPropPages(sc *db.ServerConn, dbName, trigName string) []propPage {
	return []propPage{
		pageDatabaseTriggerGeneral(sc, dbName, trigName),
		pageDatabaseTriggerDefinition(sc, dbName, trigName),
	}
}

// pageDatabaseTriggerGeneral is Database Trigger Properties > General.
func pageDatabaseTriggerGeneral(sc *db.ServerConn, dbName, trigName string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, err := loadDatabaseTrigger(ctx, sc, dbName, trigName)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("Trigger"),
				propsheet.Static("Name", t.Name),
				propsheet.Static("Status", enabledText(t.IsEnabled)),
				propsheet.Static("Scope", "Database"),
				propsheet.Static("Database", dbName),
				propsheet.Section("Events"),
				propsheet.Static("Fires on", strings.Join(t.Events, ", ")),
				propsheet.Section("Dates"),
				propsheet.Static("Created", formatSQLDate(t.CreateDate)),
				propsheet.Static("Last modified", formatSQLDate(t.ModifyDate)),
				propsheet.Note("Enable and Disable are on the trigger's Object Explorer menu — they take effect the moment they run, across the whole database. To change the body or the events, script the trigger as ALTER."),
			)
			return f, nil, nil
		},
	}
}

// pageDatabaseTriggerDefinition is Database Trigger Properties > Definition:
// the trigger body as sys.sql_modules stores it, in a read-only SQL editor.
//
// An encrypted trigger, and a CLR trigger (which has no row in that view at
// all), report the absence on the page rather than failing it — everything the
// catalog does know about the trigger is on the General page above.
func pageDatabaseTriggerDefinition(sc *db.ServerConn, dbName, trigName string) propPage {
	return propPage{
		title: "Definition",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, err := loadDatabaseTrigger(ctx, sc, dbName, trigName)
			if err != nil {
				return nil, nil, err
			}
			if strings.TrimSpace(t.Definition) == "" {
				return propsheet.NewForm(
					propsheet.Section("Definition"),
					propsheet.Note("The trigger's definition is not readable — it is either encrypted (WITH ENCRYPTION) or a CLR trigger, which stores no T-SQL body."),
				), nil, nil
			}

			ed := controls.NewEditor(controls.SQLHighlighter(theme.Active()))
			ed.SetText(t.Definition)
			ed.SetReadOnly(true)

			f := propsheet.NewForm(
				propsheet.Section("Definition"),
				propsheet.NewEditorRow("Body", ed, 16),
			)
			return f, nil, nil
		},
	}
}

// loadDatabaseTrigger reads one DDL trigger with every field populated.
// DatabaseByName, not Database: the pages show CreateDate and the definition,
// which the lightweight handle leaves zero-valued.
func loadDatabaseTrigger(ctx context.Context, sc *db.ServerConn, dbName, trigName string) (*gosmo.DatabaseTrigger, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return dbObj.DatabaseTriggerByNameContext(ctx, trigName)
}
