package tui

import (
	"context"
	"strings"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// serverTriggerPropPages builds the page set for Server Trigger Properties:
// General and Definition, both read-only.
//
// Nothing here writes, and that is the object's shape rather than an omission.
// A server-scope trigger has exactly three writes — ENABLE, DISABLE and DROP
// ... ON ALL SERVER — and all three are Object Explorer commands on the node:
// enabling and disabling change what the instance does the moment they run,
// which is not an Apply, and there is no ALTER a form could build (the body is
// edited as T-SQL, through Script Server Trigger as > ALTER To). Neither page
// declares requires for that reason — both are named in
// prop_page_requires_test.go's pagesThatOnlyRead.
func serverTriggerPropPages(sc *db.ServerConn, trigName string) []propPage {
	return []propPage{
		pageServerTriggerGeneral(sc, trigName),
		pageServerTriggerDefinition(sc, trigName),
	}
}

// pageServerTriggerGeneral is Server Trigger Properties > General.
func pageServerTriggerGeneral(sc *db.ServerConn, trigName string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, err := sc.Server.ServerTriggerByNameContext(ctx, trigName)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("Trigger"),
				propsheet.Static("Name", t.Name),
				propsheet.Static("Status", enabledText(t.IsEnabled)),
				propsheet.Static("Scope", "All Server"),
				propsheet.Section("Events"),
				propsheet.Static("Fires on", strings.Join(t.Events, ", ")),
				propsheet.Section("Dates"),
				propsheet.Static("Created", formatSQLDate(t.CreateDate)),
				propsheet.Static("Last modified", formatSQLDate(t.ModifyDate)),
				propsheet.Note("Enable and Disable are on the trigger's Object Explorer menu — they take effect the moment they run, server-wide. To change the body or the events, script the trigger as ALTER."),
			)
			return f, nil, nil
		},
	}
}

// pageServerTriggerDefinition is Server Trigger Properties > Definition: the
// trigger body as sys.server_sql_modules stores it, in a read-only SQL editor.
//
// An encrypted trigger, and a CLR trigger (which has no row in that view at
// all), report the absence on the page rather than failing it — everything the
// catalog does know about the trigger is on the General page above.
func pageServerTriggerDefinition(sc *db.ServerConn, trigName string) propPage {
	return propPage{
		title: "Definition",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, err := sc.Server.ServerTriggerByNameContext(ctx, trigName)
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
