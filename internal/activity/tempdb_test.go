package activity

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
)

// tempdbSource answers every collectTempDB reading. The page-to-MB
// conversions are gosmo's, tested there; this pins what this package does
// with the readings.
func tempdbSource() *fakeSource {
	return &fakeSource{
		counters: []gosmo.PerformanceCounter{
			{Object: "General Statistics", Counter: "Active Temp Tables", Value: 9, Type: gosmo.CounterRawCount},
			{Object: "Transactions", Counter: "Version Store Size (KB)", Value: 2048, Type: gosmo.CounterRawCount},
		},
		tdSpace: gosmo.TempDBSpace{TotalMB: 100, FreeMB: 60, VersionStoreMB: 10, UserObjectMB: 20, InternalObjectMB: 8, MixedExtentMB: 2},
		tdFiles: []gosmo.TempDBFile{
			{FileID: 1, Name: "tempdev", Type: "ROWS", SizeMB: 64, UsedMB: 16, GrowthMB: 8},
			{FileID: 3, Name: "temp2", Type: "ROWS", SizeMB: 32, UsedMB: 4, GrowthMB: 10, PercentGrowth: true},
			{FileID: 2, Name: "templog", Type: "LOG", SizeMB: 8, GrowthMB: 2},
		},
		tdObjects: []gosmo.TempDBObjects{
			{Kind: gosmo.TempDBLocalTemp, Count: 3, ReservedMB: 5, UsedMB: 4, Rows: 700},
			{Kind: gosmo.TempDBUserTable, Count: 1, ReservedMB: 9, UsedMB: 6, Rows: 900},
			// An unknown kind is skipped, not written past the array.
			{Kind: 99, Count: 1},
		},
		tdSessions: []gosmo.TempDBSession{
			{SessionID: 57, Host: "wkstn", Program: "SSMS", Login: "sa", UserMB: 3, InternalMB: 2, TotalMB: 5},
		},
		info: &gosmo.ServerInfo{LogicalCPUCount: 8},
	}
}

func TestCollectTempDBReadsEveryPart(t *testing.T) {
	src := tempdbSource()

	snap, err := collectTempDB(context.Background(), src)
	if err != nil {
		t.Fatalf("collectTempDB: %v", err)
	}
	s := snap.sample
	if !slices.Equal(src.counterNames, tempdbCounterNames) {
		t.Errorf("counter filter = %q, want tempdbCounterNames", src.counterNames)
	}

	want := TempDBSpace{TotalMB: 100, FreeMB: 60, VersionStoreMB: 10,
		UserObjectMB: 20, InternalObjectMB: 8, MixedExtentMB: 2}
	if s.Space != want {
		t.Errorf("space = %+v, want %+v", s.Space, want)
	}

	if len(s.Files) != 3 {
		t.Fatalf("got %d files, want 3", len(s.Files))
	}
	byName := map[string]TempDBFile{}
	for _, f := range s.Files {
		byName[f.Name] = f
	}
	if n := len(s.DataFiles()); n != 2 {
		t.Errorf("DataFiles() = %d files, want 2 — the log file is not a data file", n)
	}

	local := s.Objects[TempDBUserTemp]
	if local.Count != 3 || local.ReservedMB != 5 || local.UsedMB != 4 || local.Rows != 700 {
		t.Errorf("local temp tables = %+v, want 3 objects, 5MB reserved, 4MB used, 700 rows", local)
	}
	if user := s.Objects[TempDBUserTable]; user.Count != 1 || user.ReservedMB != 9 {
		t.Errorf("user tables = %+v, want 1 object and 9MB reserved", user)
	}
	// Every slot names its kind even with no row, so series and legend stay
	// aligned.
	for i := range s.Objects {
		if s.Objects[i].Kind != TempDBObjectKind(i) {
			t.Errorf("Objects[%d].Kind = %v, want %v", i, s.Objects[i].Kind, TempDBObjectKind(i))
		}
	}

	if len(s.Sessions) != 1 || s.Sessions[0] != src.tdSessions[0] {
		t.Errorf("sessions = %+v, want the one the source read", s.Sessions)
	}

	if s.Cores != 8 {
		t.Errorf("Cores = %d, want 8 — the one-file-per-core rule needs it", s.Cores)
	}

	// Without server info (sys.dm_os_sys_info was unreadable at connect) the
	// core count is unknown, not a failed tick.
	src.info = nil
	if snap, err := collectTempDB(context.Background(), src); err != nil || snap.sample.Cores != 0 {
		t.Errorf("no server info: cores %v, err %v; want 0, nil", snap.sample.Cores, err)
	}
}

func TestCollectTempDBStopsAtAFailedRead(t *testing.T) {
	boom := errors.New("activity_test: tempdb DMV unavailable")
	for _, method := range []string{"PerformanceCounters", "TempDBSpace", "TempDBFiles", "TempDBObjects", "TempDBSessions"} {
		t.Run(method, func(t *testing.T) {
			src := tempdbSource()
			src.fail = map[string]error{method: boom}

			if snap, err := collectTempDB(context.Background(), src); err == nil {
				t.Fatalf("collectTempDB succeeded with %s failing: %+v", method, snap.sample)
			} else if !errors.Is(err, boom) {
				t.Errorf("error = %v, want the reading's own", err)
			}
		})
	}
}

// The first sample has no previous one, so rates are zero rather than the
// cumulative total.
func TestDeriveTempDBNeedsTwoSamplesForARate(t *testing.T) {
	src := tempdbSource()
	ctx := context.Background()

	first, err := collectTempDB(ctx, src)
	if err != nil {
		t.Fatalf("collectTempDB: %v", err)
	}
	one := deriveTempDB(nil, first)
	if one.Interval != 0 {
		t.Errorf("first sample Interval = %v, want 0", one.Interval)
	}
	// A per-second counter with no baseline reads 0; a point-in-time one is its
	// own value.
	if one.ActiveTempTables != 9 {
		t.Errorf("ActiveTempTables = %v, want 9 on the first sample", one.ActiveTempTables)
	}
	if one.VersionStoreMB != 2 {
		t.Errorf("VersionStoreMB = %v, want 2 (2048 KB)", one.VersionStoreMB)
	}

	second, err := collectTempDB(ctx, src)
	if err != nil {
		t.Fatalf("collectTempDB: %v", err)
	}
	second.at = first.at.Add(2 * time.Second)
	two := deriveTempDB(first, second)
	if two.Interval != 2*time.Second {
		t.Errorf("Interval = %v, want 2s", two.Interval)
	}
	if two.At != second.at {
		t.Errorf("At = %v, want the newer snapshot's time %v", two.At, second.at)
	}
	if two.Space != second.sample.Space {
		t.Errorf("space was not carried through: %+v", two.Space)
	}
}
