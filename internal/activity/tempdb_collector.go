package activity

import (
	"database/sql"
	"time"
)

// TempDBRetention is four hours, not thirty minutes: this tab ticks in tens of
// seconds, and what it shows (an uncleaned version store, a file filling over
// an afternoon) builds over hours.
const TempDBRetention = 4 * time.Hour

// TempDBDetailWindow is how many newest samples keep their file, object and
// session lists. Only the newest sample's lists are read, and session lists are
// the largest data here.
const TempDBDetailWindow = 10

// tempDBSampleTime reads a TempDBSample's timestamp for pruning.
func tempDBSampleTime(s TempDBSample) time.Time { return s.At }

// TempDBStore is the in-memory history of tempdb samples, oldest first.
type TempDBStore struct {
	sampleStore[TempDBSample]
}

// Append adds a sample, drops lists from samples leaving the detail window, and
// prunes anything older than TempDBRetention.
func (s *TempDBStore) Append(sample TempDBSample) {
	s.appendSample(sample, tempDBSampleTime, TempDBRetention, TempDBDetailWindow,
		func(old *TempDBSample) { old.Files, old.Sessions = nil, nil })
}

// TempDBCollector ticks tempdb readings against one connection. Separate from
// Collector because it runs at a slower rate and its object enumeration touches
// tempdb metadata, which shouldn't ride a 2-second tick. Shares collector's
// ticking half.
type TempDBCollector struct {
	collector[TempDBSample, tempdbSnapshot]
}

// NewTempDBCollector creates a collector. onSample is called per successful
// tick, onError per failed one; either may be nil.
func NewTempDBCollector(db *sql.DB, onSample func(TempDBSample), onError func(error)) *TempDBCollector {
	return new(TempDBCollector{newCollector(db, collectTempDB, deriveTempDB, onSample, onError)})
}
