package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// contract_props.go is the read-only Properties for a Service Broker
// contract.
//
// Read-only with no argument to make: there is no ALTER CONTRACT at all. A
// contract's message types and the end allowed to send each are fixed at
// CREATE, and changing either means dropping the contract — which the server
// refuses (Msg 3716) while a service or a conversation priority still names
// it. The page is named in prop_page_requires_test.go's pagesThatOnlyRead.

func findContract(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.ServiceContract, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.ContractByNameContext(ctx, name)
}

func contractPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			c, err := findContract(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("Contract"),
				propsheet.Static("Name", c.Name),
				propsheet.Static("Owner", c.Owner),
				propsheet.Static("System object", boolStr(c.IsSystemObject)),
				propsheet.Section("Message types"),
			)
			// is_sent_by_initiator and is_sent_by_target are two bits, and
			// both set is SENT BY ANY — the case a one-column reading gets
			// wrong. Each is shown with its message type rather than as a
			// bare list, since the pair is what the contract says.
			//
			// The message type goes in the *value*, not the label: a label
			// clips at propsheet.LabelWidth with no ellipsis, and these names
			// are URIs — "//schemas.microsoft.com/SQL/ServiceBroker/…" would
			// render as a different, shorter name for every one of them.
			if len(c.Messages) == 0 {
				f.Add(propsheet.Note("This contract names no message type, which CREATE CONTRACT does not allow — the rows are either invisible to this login or the contract is one SQL Server ships."))
			}
			for _, m := range c.Messages {
				f.Add(propsheet.Static("Message type", m.MessageType+" (SENT BY "+string(m.SentBy)+")"))
			}
			f.Add(propsheet.Note("There is no ALTER CONTRACT: a contract's message types and their senders are fixed when it is created."))
			return f, nil, nil
		},
	}}
}
