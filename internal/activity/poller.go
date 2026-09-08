package activity

import (
	"context"
	"database/sql"
)

// Poller ticks a caller-supplied read against one connection, on the same
// machinery Collector and TempDBCollector run on: the rate selector, Pause,
// Stop, the VIEW SERVER STATE prologue, and the failure backoff that stops a
// panel left open against an unreachable server from hammering it.
//
// It exists for a feed whose source is already *pre-aggregated* — Azure SQL
// Managed Instance's sys.server_resource_stats is a 15-second history the
// server keeps for two weeks, so one tick reads the whole thing and there is
// nothing to derive from the previous reading. Collector's probe/derive pair
// answers the other question, "what changed since last time", which is what
// every per-second rate in this package is; asking it of a view that is
// already an average would double-average it.
//
// probe takes only a context: the reads behind a Poller are the caller's own
// (gossms drives gosmo through it), not raw SQL over the *sql.DB this package
// owns. The db is still required, and is not decoration — it is what the
// permission prologue runs on, and without VIEW SERVER STATE these views
// return empty rather than failing, which is the one thing this package
// exists to keep from looking like an idle server.
type Poller[S any] struct {
	collector[S, S]
}

// NewPoller creates a poller. onSample is called once per successful tick,
// onError once per failed one; either may be nil. The interval is Run's
// argument, not this one's.
func NewPoller[S any](db *sql.DB, probe func(context.Context) (*S, error),
	onSample func(S), onError func(error)) *Poller[S] {
	return new(Poller[S]{newCollector(db,
		func(ctx context.Context, _ *sql.DB) (*S, error) { return probe(ctx) },
		// The reading *is* the sample: nothing is derived, which is the whole
		// reason this type exists rather than a second Collector.
		func(_, cur *S) S { return *cur },
		onSample, onError)})
}
