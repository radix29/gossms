package activity

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"
)

// tempdbAnswers scripts every query collectTempDB makes. Page counts are
// chosen so each converts to a distinct number of megabytes — 128 pages to
// the MB — and no two fields share a value, so a column scanned into the
// wrong field is a wrong number rather than a plausible one.
func tempdbAnswers() map[string]reply {
	return map[string]reply{
		tempdbCounterQuery: {
			cols: []string{"object_name", "counter_name", "instance_name", "cntr_value", "cntr_type"},
			rows: [][]driver.Value{
				{"SQLServer:General Statistics", "Active Temp Tables", "", int64(9), int64(65792)},
				{"SQLServer:Transactions", "Version Store Size (KB)", "", int64(2048), int64(65792)},
			},
		},
		tempdbSpaceQuery: {
			cols: []string{"total", "free", "version", "user", "internal", "mixed"},
			rows: [][]driver.Value{{int64(128 * 100), int64(128 * 60), int64(128 * 10),
				int64(128 * 20), int64(128 * 8), int64(128 * 2)}},
		},
		tempdbFileQuery: {
			cols: []string{"file_id", "name", "type_desc", "size", "used", "growth", "is_percent_growth"},
			rows: [][]driver.Value{
				{int64(1), "tempdev", "ROWS", int64(128 * 64), int64(128 * 16), int64(128 * 8), false},
				{int64(3), "temp2", "ROWS", int64(128 * 32), int64(128 * 4), int64(10), true},
				{int64(2), "templog", "LOG", int64(128 * 8), int64(0), int64(128 * 2), false},
			},
		},
		tempdbObjectQuery: {
			cols: []string{"kind", "count", "reserved", "used", "rows"},
			rows: [][]driver.Value{
				{int64(0), int64(3), int64(128 * 5), int64(128 * 4), int64(700)},
				{int64(2), int64(1), int64(128 * 9), int64(128 * 6), int64(900)},
				// A kind the query could grow but this build doesn't know:
				// skipped, not written past the end of the array.
				{int64(99), int64(1), int64(128), int64(128), int64(1)},
			},
		},
		tempdbSessionQuery: {
			cols: []string{"session_id", "host", "program", "login", "user_pages", "internal_pages"},
			rows: [][]driver.Value{
				{int64(57), "wkstn", "SSMS", "sa", int64(128 * 3), int64(128 * 2)},
				// Task and session usage are summed, and a task releasing
				// pages its session already accounted for can drive one part
				// negative. "Holding -0.4 MB" is not actionable: it reads as
				// holding none.
				{int64(58), "", "", "", int64(-256), int64(128 * 1)},
			},
		},
		tempdbCoreQuery: {
			cols: []string{"cpu_count"},
			rows: [][]driver.Value{{int64(8)}},
		},
	}
}

func TestCollectTempDBReadsEveryPart(t *testing.T) {
	db, _ := scriptedDB(t, tempdbAnswers())

	snap, err := collectTempDB(context.Background(), db)
	if err != nil {
		t.Fatalf("collectTempDB: %v", err)
	}
	s := snap.sample

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
	if f := byName["tempdev"]; f.SizeMB != 64 || f.UsedMB != 16 || f.GrowthMB != 8 || f.PercentGrowth {
		t.Errorf("tempdev = %+v, want 64MB size, 16MB used, 8MB growth", f)
	}
	// A percentage growth is a percentage, not a page count: dividing it by
	// 128 would report "10% growth" as 0.08 MB.
	if f := byName["temp2"]; !f.PercentGrowth || f.GrowthMB != 10 {
		t.Errorf("temp2 = %+v, want a 10 percent growth kept as 10", f)
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
	// Every slot must name its own kind even where the server sent no row,
	// or a chart's series and its legend drift apart.
	for i := range s.Objects {
		if s.Objects[i].Kind != TempDBObjectKind(i) {
			t.Errorf("Objects[%d].Kind = %v, want %v", i, s.Objects[i].Kind, TempDBObjectKind(i))
		}
	}

	if len(s.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(s.Sessions))
	}
	if got := s.Sessions[0]; got.SessionID != 57 || got.Host != "wkstn" || got.Program != "SSMS" ||
		got.Login != "sa" || got.UserMB != 3 || got.InternalMB != 2 || got.TotalMB != 5 {
		t.Errorf("session 57 = %+v, want wkstn/SSMS/sa holding 3 + 2 MB", got)
	}
	if got := s.Sessions[1]; got.UserMB != 0 || got.InternalMB != 1 || got.TotalMB != 1 {
		t.Errorf("session 58 = %+v, want the negative half clamped to 0 and a 1MB total", got)
	}

	if s.Cores != 8 {
		t.Errorf("Cores = %d, want 8 — the one-file-per-core rule needs it", s.Cores)
	}
}

func TestCollectTempDBStopsAtAFailedRead(t *testing.T) {
	boom := errors.New("activity_test: tempdb DMV unavailable")
	for name, q := range map[string]string{
		"counters": tempdbCounterQuery,
		"space":    tempdbSpaceQuery,
		"files":    tempdbFileQuery,
		"objects":  tempdbObjectQuery,
		"sessions": tempdbSessionQuery,
		"cores":    tempdbCoreQuery,
	} {
		t.Run(name, func(t *testing.T) {
			answers := tempdbAnswers()
			answers[q] = reply{err: boom}
			db, _ := scriptedDB(t, answers)

			if snap, err := collectTempDB(context.Background(), db); err == nil {
				t.Fatalf("collectTempDB succeeded with the %s read failing: %+v", name, snap.sample)
			} else if !errors.Is(err, boom) {
				t.Errorf("error = %v, want the driver's own", err)
			}
		})
	}
}

// deriveTempDB decodes the counters against the previous reading. The first
// sample has no previous one, and a rate needs two: it must read zero rather
// than treating the cumulative total as one interval's worth.
func TestDeriveTempDBNeedsTwoSamplesForARate(t *testing.T) {
	db, _ := scriptedDB(t, tempdbAnswers())
	ctx := context.Background()

	first, err := collectTempDB(ctx, db)
	if err != nil {
		t.Fatalf("collectTempDB: %v", err)
	}
	one := deriveTempDB(nil, first)
	if one.Interval != 0 {
		t.Errorf("first sample Interval = %v, want 0", one.Interval)
	}
	// A per-second counter with nothing to compare against reads 0; a
	// point-in-time one is its own value either way.
	if one.ActiveTempTables != 9 {
		t.Errorf("ActiveTempTables = %v, want 9 on the first sample", one.ActiveTempTables)
	}
	if one.VersionStoreMB != 2 {
		t.Errorf("VersionStoreMB = %v, want 2 (2048 KB)", one.VersionStoreMB)
	}

	second, err := collectTempDB(ctx, db)
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

func TestNonNegativeMB(t *testing.T) {
	for _, tc := range []struct {
		pages int64
		want  float64
	}{
		{0, 0},
		{-1, 0},
		{-128 * 4, 0},
		{128, 1},
		{64, 0.5},
	} {
		if got := nonNegativeMB(tc.pages); got != tc.want {
			t.Errorf("nonNegativeMB(%d) = %v, want %v", tc.pages, got, tc.want)
		}
	}
}
