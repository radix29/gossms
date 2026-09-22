package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// message_type_props.go is the read-only Properties for a Service Broker
// message type.
//
// Read-only although ALTER MESSAGE TYPE exists, and the reason is what the
// ALTER does: it changes the VALIDATION clause, which every conversation
// already using the type is measured against from the next message on. That
// is a change to a running application's wire contract, not a setting, and it
// belongs in the script the application ships — Script as ▸ CREATE is what
// this build offers for it. The page is named in
// prop_page_requires_test.go's pagesThatOnlyRead.

func findMessageType(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.MessageType, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.MessageTypeByName(ctx, name)
}

func messageTypePropPages(sc *db.ServerConn, dbName, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			mt, err := findMessageType(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("Message type"),
				propsheet.Static("Name", mt.Name),
				propsheet.Static("Owner", mt.Owner),
				propsheet.Static("System object", boolStr(mt.IsSystemObject)),
				propsheet.Section("Validation"),
				propsheet.Static("Validation", string(mt.Validation)),
				// dottedName, not gosmo's SchemaCollection(): that is the
				// bracket-quoted form a CREATE MESSAGE TYPE takes, and
				// "[dbo].[ClaimSchema]" in a property row reads as a name
				// with brackets in it.
				propsheet.Static("Schema collection", boundOrNone(dottedName(mt.SchemaCollectionSchema, mt.SchemaCollectionName))),
				propsheet.Note("VALIDATION is what the broker measures every message of this type against, from the next message on — an edit here would change a running application's wire contract, so it is made by script rather than by form."),
			)
			return f, nil, nil
		},
	}}
}
