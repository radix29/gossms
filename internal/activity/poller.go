package activity

import "context"

// Poller ticks a caller-supplied read on the Collector machinery: rate, Pause,
// Stop, the VIEW SERVER STATE prologue and failure backoff.
//
// It's for pre-aggregated sources, e.g. Managed Instance's
// sys.server_resource_stats (15-second history kept two weeks), where one tick
// reads everything and there's nothing to derive; Collector's probe/derive
// would double-average.
//
// probe takes only a context because the reads are the caller's own (gosmo).
// src is still required for the permission prologue.
type Poller[S any] struct {
	collector[S, S]
}

// NewPoller creates a poller. onSample is called per successful tick, onError
// per failed one; either may be nil. The interval is Run's argument.
func NewPoller[S any](src Source, probe func(context.Context) (*S, error),
	onSample func(S), onError func(error)) *Poller[S] {
	return new(Poller[S]{newCollector(src,
		func(ctx context.Context, _ Source) (*S, error) { return probe(ctx) },
		// The reading is the sample; nothing is derived.
		func(_, cur *S) S { return *cur },
		onSample, onError)})
}
