package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
)

// onlineDatabases is the subset of dbs a per-database page fetch can query.
// Every caller needs it, and none of them treats an offline database as an
// error: it simply has nothing to contribute to the page.
func onlineDatabases(dbs []*gosmo.Database) []*gosmo.Database {
	out := make([]*gosmo.Database, 0, len(dbs))
	for _, d := range dbs {
		if d.State() == "ONLINE" {
			out = append(out, d)
		}
	}
	return out
}

// eachDatabase runs fetch against every database in dbs and returns the
// results in order.
//
// A database whose fetch fails is dropped rather than failing the caller —
// one inaccessible-but-ONLINE database (no permission, an unreadable
// availability-group secondary) must not take down a page that has 49 others
// to show. That is the policy gosmo's Login.UserMappingsContext already
// applies on the same page, and it matches what these callers already do with
// a database that is merely offline: it drops out of the list.
//
// A cancelled context is not a drop. Every remaining database would fail the
// same way, so the error is returned instead of issuing a doomed query per
// database — again the rule UserMappingsContext states.
//
// Serial on purpose, and measured: fanning these queries across a worker pool
// was slower against the live instance (46 databases, 2026-08-14) at every
// width from 2 to 8, on all three runs. Each worker needs a pooled connection
// of its own, and a fresh TCP+TLS+login handshake costs far more than the few
// milliseconds of query latency it overlaps — 0.37-0.47s serial against
// 0.62-1.54s fanned out, on the cold pool a freshly opened dialog actually
// has. Concurrency only wins once eight connections are already idle, which is
// not the state these pages open in.
func eachDatabase[T any](ctx context.Context, dbs []*gosmo.Database, fetch func(context.Context, *gosmo.Database) (T, error)) ([]T, error) {
	out := make([]T, 0, len(dbs))
	for _, d := range dbs {
		v, err := fetch(ctx, d)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		out = append(out, v)
	}
	return out, nil
}
