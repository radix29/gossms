package activity

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

// snapshotAnswers scripts one reply for every query Collect makes, each with
// values distinctive enough that a column read into the wrong field shows up
// as the wrong number rather than as a plausible one.
func snapshotAnswers() map[string]reply {
	return map[string]reply{
		counterQuery: {
			cols: []string{"object_name", "counter_name", "instance_name", "cntr_value", "cntr_type"},
			rows: [][]driver.Value{
				{"SQLServer:SQL Statistics", "Batch Requests/sec", "", int64(4200), int64(272696576)},
			},
		},
		waitQuery: {
			cols: []string{"wait_type", "wait_time_ms", "signal_wait_time_ms", "waiting_tasks_count"},
			rows: [][]driver.Value{
				{"PAGEIOLATCH_SH", int64(900), int64(30), int64(12)},
				{"LCK_M_X", int64(400), int64(10), int64(3)},
			},
		},
		fileIOQuery: {
			cols: []string{"database_id", "file_id", "name", "is_log",
				"reads", "bytes_read", "stall_read", "writes", "bytes_written", "stall_write"},
			rows: [][]driver.Value{
				{int64(5), int64(1), "AppDB", int64(0), int64(11), int64(12), int64(13), int64(14), int64(15), int64(16)},
				{int64(5), int64(2), nil, int64(1), int64(21), int64(22), int64(23), int64(24), int64(25), int64(26)},
			},
		},
		clerkQuery: {
			cols: []string{"type", "mb"},
			rows: [][]driver.Value{
				{"MEMORYCLERK_SQLBUFFERPOOL", float64(1024)},
				{"SOMETHING_NEW_IN_A_LATER_RELEASE", float64(7)},
			},
		},
		schedQuery: {
			cols: []string{"schedulers", "runnable", "current", "workers", "queue"},
			rows: [][]driver.Value{{int64(8), float64(2), float64(30), float64(40), float64(1)}},
		},
		sessionQuery: {
			cols: []string{"sessions", "requests", "runnable", "suspended", "blocked"},
			rows: [][]driver.Value{{float64(17), float64(5), float64(2), float64(1), float64(3)}},
		},
		cpuUsageQuery: {
			cols: []string{"sql_pct", "other_pct"},
			rows: [][]driver.Value{{float64(63), float64(11)}},
		},
		loadFactorQuery: {
			cols: []string{"cpu_id", "load_factor"},
			rows: [][]driver.Value{
				{int64(0), float64(12)},
				{int64(1), float64(34)},
			},
		},
	}
}

// TestCollectReadsEveryPartOfASnapshot drives the whole of Collect against
// scripted DMVs. Every one of these readers is a query plus a scan, and a
// scan that reads a column into the wrong field produces a plausible number
// and no error at all — so each field is asserted against a value only that
// column carries.
func TestCollectReadsEveryPartOfASnapshot(t *testing.T) {
	db, _ := scriptedDB(t, snapshotAnswers())

	snap, err := Collect(context.Background(), db)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if snap.At.IsZero() {
		t.Error("snapshot has no time: nothing can be a rate against it")
	}

	// Counters are keyed with the instance prefix stripped, or a named
	// instance's counters go missing silently.
	got := snap.Counters[counterKey{object: "SQL Statistics", counter: "Batch Requests/sec", instance: ""}]
	if got.value != 4200 {
		t.Errorf("Batch Requests/sec = %d, want 4200 (is the object name prefix being stripped?)", got.value)
	}

	if w, ok := snap.Waits["PAGEIOLATCH_SH"]; !ok {
		t.Error("PAGEIOLATCH_SH missing from the wait set")
	} else if w.waitMs != 900 || w.signalMs != 30 || w.tasks != 12 {
		t.Errorf("PAGEIOLATCH_SH = %+v, want waitMs 900, signalMs 30, tasks 12", w)
	}

	data, ok := snap.Files[fileKey{dbID: 5, fileID: 1}]
	if !ok {
		t.Fatal("the data file is missing from the file set")
	}
	if data.database != "AppDB" || data.isLog {
		t.Errorf("data file = %q isLog=%v, want AppDB, false", data.database, data.isLog)
	}
	if data.reads != 11 || data.bytesRead != 12 || data.stallRead != 13 ||
		data.writes != 14 || data.bytesWrit != 15 || data.stallWrit != 16 {
		t.Errorf("data file counters = %+v, want reads 11, bytesRead 12, stallRead 13, writes 14, bytesWrit 15, stallWrit 16", data)
	}
	// DB_NAME() is NULL for a database the connection cannot see; the row
	// still has to contribute its I/O, under a name a reader can live with.
	logFile, ok := snap.Files[fileKey{dbID: 5, fileID: 2}]
	if !ok {
		t.Fatal("the log file is missing from the file set")
	}
	if logFile.database != "(unknown)" || !logFile.isLog {
		t.Errorf("log file = %q isLog=%v, want (unknown), true", logFile.database, logFile.isLog)
	}

	// An unrecognized clerk must land in Other rather than vanishing.
	mem := map[string]float64{}
	for _, c := range snap.Memory {
		mem[c.Name] = c.MB
	}
	if mem[memBuffer] != 1024 {
		t.Errorf("%s = %v MB, want 1024", memBuffer, mem[memBuffer])
	}
	if mem[memOther] != 7 {
		t.Errorf("%s = %v MB, want 7 — an unknown clerk must still be counted", memOther, mem[memOther])
	}

	if snap.Sched != (SchedStats{Schedulers: 8, RunnableTasks: 2, CurrentTasks: 30, ActiveWorkers: 40, WorkQueue: 1}) {
		t.Errorf("SchedStats = %+v", snap.Sched)
	}
	if snap.Sessions != (SessionStats{UserSessions: 17, ActiveRequests: 5, RunnableRequests: 2, SuspendedTasks: 1, BlockedRequests: 3}) {
		t.Errorf("SessionStats = %+v", snap.Sessions)
	}
	if snap.CPU != (CPUUsage{SQLPct: 63, OtherPct: 11}) {
		t.Errorf("CPUUsage = %+v, want 63/11", snap.CPU)
	}
	want := []SchedulerLoad{{CPUID: 0, LoadFactor: 12}, {CPUID: 1, LoadFactor: 34}}
	if len(snap.Load) != len(want) || snap.Load[0] != want[0] || snap.Load[1] != want[1] {
		t.Errorf("SchedulerLoad = %+v, want %+v", snap.Load, want)
	}
}

// An instance that has just started has no scheduler-monitor record yet.
// That is a zero reading, not an error: one empty chart must not fail the
// whole tick and leave the panel with nothing.
func TestCollectCPUUsageWithNoRecordYet(t *testing.T) {
	answers := snapshotAnswers()
	answers[cpuUsageQuery] = reply{cols: []string{"sql_pct", "other_pct"}}
	db, _ := scriptedDB(t, answers)

	snap, err := Collect(context.Background(), db)
	if err != nil {
		t.Fatalf("Collect with no CPU record: %v", err)
	}
	if snap.CPU != (CPUUsage{}) {
		t.Errorf("CPUUsage = %+v, want the zero reading", snap.CPU)
	}
}

// A failing DMV read fails the tick. The alternative — carrying on with a
// zero-valued part — draws a server that looks idle in exactly the place the
// reading failed, which is indistinguishable from a real answer.
func TestCollectStopsAtAFailedRead(t *testing.T) {
	boom := errors.New("activity_test: DMV unavailable")
	for _, q := range []struct {
		name  string
		query string
	}{
		{"counters", counterQuery},
		{"waits", waitQuery},
		{"file I/O", fileIOQuery},
		{"memory", clerkQuery},
		{"schedulers", schedQuery},
		{"sessions", sessionQuery},
		{"CPU", cpuUsageQuery},
		{"scheduler load", loadFactorQuery},
	} {
		t.Run(q.name, func(t *testing.T) {
			answers := snapshotAnswers()
			answers[q.query] = reply{err: boom}
			db, _ := scriptedDB(t, answers)

			snap, err := Collect(context.Background(), db)
			if err == nil {
				t.Fatalf("Collect returned a snapshot with the %s read failing: %+v", q.name, snap)
			}
			if !errors.Is(err, boom) {
				t.Errorf("Collect error = %v, want the driver's own error", err)
			}
		})
	}
}

// The wait query must exclude the benign idle waits by name and by family.
// Left in, they are hours of accumulated sleep time and the chart shows
// nothing but their bar — and the ESCAPE clause is what stops a family
// pattern's underscores from being read as single-character wildcards,
// which would make "QDS\_%" match far more than the QDS family.
func TestWaitQueryExcludesTheBenignWaits(t *testing.T) {
	for _, name := range benignWaits {
		if !strings.Contains(waitQuery, "'"+name+"'") {
			t.Errorf("waitQuery does not exclude %s by name", name)
		}
	}
	for _, family := range benignFamilies {
		if !strings.Contains(waitQuery, "NOT LIKE '"+family+"' ESCAPE '\\'") {
			t.Errorf("waitQuery does not exclude the %s family with an ESCAPE clause", family)
		}
	}
}

// WaitCategory.String is what labels the chart's series, and it is indexed
// into a fixed array — an out-of-range category must name something rather
// than panicking the draw.
func TestWaitCategoryString(t *testing.T) {
	if got := WaitCPU.String(); got != "CPU" {
		t.Errorf("WaitCPU.String() = %q, want CPU", got)
	}
	if got := WaitOther.String(); got != "Other" {
		t.Errorf("WaitOther.String() = %q, want Other", got)
	}
	for _, c := range []WaitCategory{-1, waitCategoryCount, waitCategoryCount + 5} {
		if got := c.String(); got != "Other" {
			t.Errorf("WaitCategory(%d).String() = %q, want Other", c, got)
		}
	}
	for i := range waitCategoryCount {
		if WaitCategoryNames[i] == "" {
			t.Errorf("WaitCategory(%d) has no name; the chart legend would be blank", i)
		}
	}
}
