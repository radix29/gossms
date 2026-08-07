package activity

import (
	"database/sql"
	"time"
)

// TempDBRetention is how far back the tempdb store keeps samples. Four
// hours rather than Store's thirty minutes because this tab ticks in tens of
// seconds, not in seconds: at a 60-second rate a thirty-minute window is
// thirty columns, and the things it exists to show — a version store that
// never gets cleaned up, a file that fills over an afternoon — build over
// hours.
const TempDBRetention = 4 * time.Hour

// TempDBDetailWindow is how many of the newest samples keep their file,
// object, and session lists. Older samples keep the space and counter
// levels every chart plots; the lists are only ever read for the newest
// sample, and a session list on a busy server is the largest thing here.
const TempDBDetailWindow = 10

// tempDBSampleTime reads a TempDBSample's own timestamp, for sampleStore's
// pruning.
func tempDBSampleTime(s TempDBSample) time.Time { return s.At }

// TempDBStore is the in-memory history of tempdb samples, oldest first.
type TempDBStore struct {
	sampleStore[TempDBSample]
}

// Append adds a sample, drops the lists of any sample that has fallen out of
// the detail window, and prunes anything older than TempDBRetention.
func (s *TempDBStore) Append(sample TempDBSample) {
	s.appendSample(sample, tempDBSampleTime, TempDBRetention, TempDBDetailWindow,
		func(old *TempDBSample) { old.Files, old.Sessions = nil, nil })
}

// TempDBCollector ticks tempdb readings against one connection. It is a
// second collector rather than more work inside Collector because the two
// tabs run at different rates: tempdb is read in tens of seconds, and its
// object enumeration touches tempdb's own metadata, which is the last thing
// that should ride along on a 2-second tick. Both share collector's ticking
// half — see its doc comment.
type TempDBCollector struct {
	collector[TempDBSample, tempdbSnapshot]
}

// NewTempDBCollector creates a collector. onSample is called once per
// successful tick, onError once per failed one; either may be nil.
func NewTempDBCollector(db *sql.DB, onSample func(TempDBSample), onError func(error)) *TempDBCollector {
	return new(TempDBCollector{newCollector(db, collectTempDB, deriveTempDB, onSample, onError)})
}
