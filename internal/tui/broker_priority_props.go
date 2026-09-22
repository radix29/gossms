package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// broker_priority_props.go is the read-only Properties for a conversation
// priority.
//
// ALTER BROKER PRIORITY exists and can change the level and the three
// criteria. It is not offered here because the permission that would gate it
// cannot be read: SQL Server enforces a BROKER PRIORITY permission it does not
// publish — HAS_PERMS_BY_NAME answers NULL for every spelling of one — so the
// only right the gate can ask about is ALTER on the database, and a page
// gating a narrow write on the widest right there is would show a read-only
// banner to everyone else who can in fact perform it. The page is named in
// prop_page_requires_test.go's pagesThatOnlyRead.

func findBrokerPriority(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.BrokerPriority, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.BrokerPriorityByName(ctx, name)
}

func brokerPriorityPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			p, err := findBrokerPriority(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("Broker priority"),
				propsheet.Static("Name", p.Name),
				propsheet.Static("Priority level", strconv.Itoa(p.Level)),
				propsheet.Section("Applies to"),
				// An empty criterion is ANY, which is how CREATE BROKER
				// PRIORITY spells one it was not given — a blank value here
				// would read as a row the page failed to load.
				propsheet.Static("Contract", anyOrName(p.Contract)),
				propsheet.Static("Local service", anyOrName(p.LocalService)),
				propsheet.Static("Remote service", anyOrName(p.RemoteService)),
				propsheet.Note("A priority applies to a conversation matching all three criteria. 1 is the lowest priority and 10 the highest; a conversation matching no priority runs at 5."),
			)
			return f, nil, nil
		},
	}}
}
