package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tui/planview"
)

// recording is a Live Query Statistics run captured from a real server: the
// in-flight plan (sys.dm_exec_query_statistics_xml), the
// sys.dm_exec_query_profiles rows read every IntervalMS while it ran, and the
// actual plan it finished with. testdata/live_crossjoin.json is a DOP-4 cross
// join of a 6000-row #temp table with itself, recorded on win10cli (SQL
// Server 2025) on 2026-10-06; its Compute Scalars never appear in the DMV, so
// it shows the "not reported" tiles too.
type recording struct {
	Query      string                  `json:"query"`
	IntervalMS int                     `json:"interval_ms"`
	Plan       string                  `json:"plan"`
	Snapshots  [][]showplan.ProfileRow `json:"snapshots"`
	Actual     string                  `json:"actual"`
}

// replay steps a PlanView through a recording at its recorded pace.
type replay struct {
	rec      recording
	plan     *showplan.Plan
	interval time.Duration
	next     int // the snapshot the next step shows; len(Snapshots) = the actual plan
	paused   bool
}

func loadReplay(path string) (*replay, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rp replay
	if err := json.Unmarshal(data, &rp.rec); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(rp.rec.Snapshots) == 0 {
		return nil, fmt.Errorf("%s: no snapshots", path)
	}
	if rp.plan, err = showplan.Parse([]byte(rp.rec.Plan)); err != nil {
		return nil, fmt.Errorf("%s: in-flight plan: %w", path, err)
	}
	rp.interval = time.Duration(max(rp.rec.IntervalMS, 100)) * time.Millisecond
	return &rp, nil
}

// restart shows the first snapshot again, running.
func (rp *replay) restart(v *planview.PlanView) {
	rp.next, rp.paused = 0, false
	rp.step(v)
}

// step shows the next snapshot — after the last, the actual plan, as the
// query panel does when the batch ends — and reports whether anything
// changed.
func (rp *replay) step(v *planview.PlanView) bool {
	switch {
	case rp.paused || rp.next > len(rp.rec.Snapshots):
		return false
	case rp.next == len(rp.rec.Snapshots):
		if rp.rec.Actual != "" {
			v.SetPlanXML(rp.rec.Actual)
		}
	default:
		v.SetLive(rp.plan, showplan.MergeProfiles(rp.rec.Snapshots[rp.next]))
	}
	rp.next++
	return true
}
