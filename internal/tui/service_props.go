package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// service_props.go is the read-only Properties for a Service Broker service.
//
// ALTER SERVICE exists — it moves the service to another queue and adds or
// drops contracts — and this page still does not offer it, for the reason the
// whole family is read-only in this pass: a service, its queue and its
// contracts are authored together with the application that uses them, and
// moving a service to a different queue mid-conversation changes where its
// messages arrive. Script as ▸ CREATE is what this build offers. The page is
// named in prop_page_requires_test.go's pagesThatOnlyRead, and the deferral is
// recorded in docs/open-threads.md § Deferred scope.

func findBrokerService(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.BrokerService, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.BrokerServiceByNameContext(ctx, name)
}

func brokerServicePropPages(sc *db.ServerConn, dbName, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			s, err := findBrokerService(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("Service"),
				propsheet.Static("Name", s.Name),
				propsheet.Static("Owner", s.Owner),
				propsheet.Static("System object", boolStr(s.IsSystemObject)),
				propsheet.Section("Queue"),
				// The queue is a LEFT join in gosmo's read: a service whose
				// queue this login cannot see lists with an empty one rather
				// than disappearing, and "(none)" is what that looks like.
				propsheet.Static("Queue", boundOrNone(dottedName(s.QueueSchema, s.QueueName))),
				propsheet.Section("Contracts"),
			)
			if len(s.Contracts) == 0 {
				f.Add(propsheet.Note("This service names no contract, so only conversations using the DEFAULT contract can target it."))
			}
			for _, c := range s.Contracts {
				// The contract's name goes in the value for the reason
				// contract_props.go gives: a label clips silently at
				// propsheet.LabelWidth and these names are URIs.
				f.Add(propsheet.Static("Contract", c))
			}
			return f, nil, nil
		},
	}}
}
