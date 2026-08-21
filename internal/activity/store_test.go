package activity

import (
	"testing"
	"time"
)

func sampleAt(t time.Time, batches float64) Sample {
	return Sample{At: t, BatchesSec: batches, Detail: &SampleDetail{
		PerDatabaseIO: []FileIO{{Database: "HealthClinic"}},
		Memory:        []MemoryComponent{{Name: memBuffer, MB: 1024}},
	}}
}

// Retention is time-based, not count-based: changing the refresh rate
// changes how many samples fit in the window, not how far back it reaches.
func TestStorePrunesByAge(t *testing.T) {
	var s Store
	start := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	// One sample every 2 seconds for 45 minutes.
	for i := range 45 * 30 {
		s.Append(sampleAt(start.Add(time.Duration(i)*2*time.Second), float64(i)))
	}

	if s.Len() > int(Retention/(2*time.Second))+1 {
		t.Errorf("store holds %d samples, more than %v at a 2-second rate", s.Len(), Retention)
	}
	oldest := s.Samples()[0]
	newest, _ := s.Latest()
	if newest.At.Sub(oldest.At) > Retention {
		t.Errorf("oldest sample is %v old, past the %v window", newest.At.Sub(oldest.At), Retention)
	}
}

// The full-fidelity Detail is what makes a 30-minute window expensive, so
// only the newest samples keep it. Every History chart plots the aggregate
// fields, which every sample keeps.
func TestStoreKeepsDetailOnlyForTheNewestSamples(t *testing.T) {
	var s Store
	start := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	for i := range DetailWindow * 3 {
		s.Append(sampleAt(start.Add(time.Duration(i)*2*time.Second), float64(i)))
	}

	samples := s.Samples()
	withDetail := 0
	for _, sample := range samples {
		if sample.Detail != nil {
			withDetail++
		}
	}
	if withDetail > DetailWindow+1 {
		t.Errorf("%d samples still carry Detail, want at most %d", withDetail, DetailWindow+1)
	}
	if latest, _ := s.Latest(); latest.Detail == nil {
		t.Error("the newest sample lost its Detail — the Sample tab draws exactly that one")
	}
	if samples[0].Detail != nil {
		t.Error("the oldest sample still carries its full-fidelity Detail")
	}
	// Dropping Detail must not touch what History plots.
	if samples[0].BatchesSec == 0 && samples[1].BatchesSec == 0 {
		t.Error("pruning Detail also cleared the aggregate series")
	}
}

func TestStoreSeriesIsOldestFirst(t *testing.T) {
	var s Store
	start := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	for i := range 5 {
		s.Append(sampleAt(start.Add(time.Duration(i)*time.Second), float64(i*10)))
	}

	got := s.Series(func(sample Sample) float64 { return sample.BatchesSec })
	want := []float64{0, 10, 20, 30, 40}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("series = %v, want %v", got, want)
		}
	}
}

func TestStoreEmptyAndReset(t *testing.T) {
	var s Store
	if _, ok := s.Latest(); ok {
		t.Error("an empty store reported a latest sample")
	}
	if len(s.Series(func(Sample) float64 { return 1 })) != 0 {
		t.Error("an empty store produced a non-empty series")
	}

	s.Append(sampleAt(time.Now(), 1))
	s.Reset()
	if s.Len() != 0 {
		t.Error("Reset left samples behind")
	}
}

func tempdbSampleAt(t time.Time, versionMB float64) TempDBSample {
	return TempDBSample{
		At:             t,
		VersionStoreMB: versionMB,
		Space:          TempDBSpace{TotalMB: 100, FreeMB: 60},
		Files:          []TempDBFile{{FileID: 1, Name: "tempdev", Type: "ROWS"}},
		Sessions:       []TempDBSession{{SessionID: 57, TotalMB: 5}},
	}
}

// The tempdb store keeps four hours where the main one keeps thirty
// minutes: this tab ticks in tens of seconds, and what it exists to show —
// a version store nothing cleans up, a file filling over an afternoon —
// builds over hours. Its window is time-based for the same reason.
func TestTempDBStorePrunesByAge(t *testing.T) {
	var s TempDBStore
	start := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)

	// One sample a minute for six hours, past the four-hour window.
	for i := range 6 * 60 {
		s.Append(tempdbSampleAt(start.Add(time.Duration(i)*time.Minute), float64(i)))
	}

	if s.Len() == 0 {
		t.Fatal("the store dropped everything")
	}
	oldest := s.Samples()[0]
	newest, _ := s.Latest()
	if newest.At.Sub(oldest.At) > TempDBRetention {
		t.Errorf("oldest sample is %v old, past the %v window", newest.At.Sub(oldest.At), TempDBRetention)
	}
	if newest.VersionStoreMB != float64(6*60-1) {
		t.Errorf("newest sample = %v, want the last one appended", newest.VersionStoreMB)
	}
}

// The file and session lists are only ever read for the newest sample, and
// a session list on a busy server is the largest thing this store holds.
// Older samples keep the space and counter levels every chart plots.
func TestTempDBStoreKeepsTheListsOnlyForTheNewestSamples(t *testing.T) {
	var s TempDBStore
	start := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	for i := range TempDBDetailWindow * 3 {
		s.Append(tempdbSampleAt(start.Add(time.Duration(i)*time.Minute), float64(i)))
	}

	samples := s.Samples()
	withLists := 0
	for _, sample := range samples {
		if sample.Files != nil || sample.Sessions != nil {
			withLists++
		}
		if sample.Space.TotalMB != 100 {
			t.Fatalf("a sample lost its space breakdown: %+v", sample.Space)
		}
	}
	if withLists > TempDBDetailWindow+1 {
		t.Errorf("%d samples still hold their file/session lists, want at most %d",
			withLists, TempDBDetailWindow+1)
	}
	// The newest sample is the one the grids read, so it must be among them.
	newest, ok := s.Latest()
	if !ok || newest.Files == nil || newest.Sessions == nil {
		t.Errorf("the newest sample lost its lists: %+v", newest)
	}
}
