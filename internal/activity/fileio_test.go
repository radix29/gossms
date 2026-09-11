package activity

import "testing"

func files(rows ...fileRow) fileSet {
	set := make(fileSet, len(rows))
	for i, r := range rows {
		set[fileKey{dbID: i/2 + 5, fileID: i%2 + 1}] = r
	}
	return set
}

// Latency divides by the operation-count delta: two slow reads and two hundred
// fast ones can have the same total stall.
func TestFileDeltasComputeThroughputAndLatency(t *testing.T) {
	prev := files(fileRow{database: "HealthClinic", reads: 100, bytesRead: 0, stallRead: 1000,
		writes: 10, bytesWrit: 0, stallWrit: 100})
	cur := files(fileRow{database: "HealthClinic", reads: 200, bytesRead: 200 * bytesPerMB, stallRead: 2000,
		writes: 20, bytesWrit: 40 * bytesPerMB, stallWrit: 300})

	perDB, total := fileDeltas(prev, cur, 2)

	if len(perDB) != 1 || perDB[0].Database != "HealthClinic" {
		t.Fatalf("per-database I/O = %+v, want one HealthClinic row", perDB)
	}
	if total.ReadMBSec != 100 {
		t.Errorf("read throughput = %v MB/sec, want 200MB over 2s", total.ReadMBSec)
	}
	if total.WriteMBSec != 20 {
		t.Errorf("write throughput = %v MB/sec, want 40MB over 2s", total.WriteMBSec)
	}
	if total.MsPerRead != 10 {
		t.Errorf("read latency = %v ms, want 1000ms over 100 reads", total.MsPerRead)
	}
	if total.MsPerWrite != 20 {
		t.Errorf("write latency = %v ms, want 200ms over 10 writes", total.MsPerWrite)
	}
}

// Log and data files are separate rows; combined, a latency spike's source is
// hidden.
func TestFileDeltasSeparateLogFromDataFiles(t *testing.T) {
	prev := files(
		fileRow{database: "Shop", writes: 0, stallWrit: 0},
		fileRow{database: "Shop", isLog: true, writes: 0, stallWrit: 0},
	)
	cur := files(
		fileRow{database: "Shop", writes: 10, bytesWrit: 20 * bytesPerMB, stallWrit: 400},
		fileRow{database: "Shop", isLog: true, writes: 100, bytesWrit: 2 * bytesPerMB, stallWrit: 100},
	)

	perDB, total := fileDeltas(prev, cur, 2)
	if len(perDB) != 2 {
		t.Fatalf("rows = %+v, want one data row and one log row", perDB)
	}
	var data, log FileIO
	for _, io := range perDB {
		if io.IsLog {
			log = io
		} else {
			data = io
		}
	}
	if data.MsPerWrite != 40 {
		t.Errorf("data write latency = %v ms, want 400ms over 10 writes", data.MsPerWrite)
	}
	if log.MsPerWrite != 1 {
		t.Errorf("log write latency = %v ms, want 100ms over 100 writes", log.MsPerWrite)
	}
	if log.Label() != "Shop (log)" || data.Label() != "Shop" {
		t.Errorf("labels = %q / %q, want %q / %q", data.Label(), log.Label(), "Shop", "Shop (log)")
	}
	// The total covers log and data files.
	if total.WriteMBSec != 11 {
		t.Errorf("total write throughput = %v MB/sec, want 22MB over 2s", total.WriteMBSec)
	}
}

// No reads means no read latency, not an infinity that rescales the chart.
func TestFileDeltasWithNoOperations(t *testing.T) {
	prev := files(fileRow{database: "Idle", reads: 5, stallRead: 50})
	cur := files(fileRow{database: "Idle", reads: 5, stallRead: 50})

	_, total := fileDeltas(prev, cur, 2)
	if total.MsPerRead != 0 || total.MsPerWrite != 0 {
		t.Errorf("latency over an idle interval = %v/%v, want 0", total.MsPerRead, total.MsPerWrite)
	}
}

// Detach/reattach or restart resets totals; a negative difference isn't
// throughput.
func TestFileDeltasIgnoreCountersThatWentBackwards(t *testing.T) {
	prev := files(fileRow{database: "Restarted", reads: 1_000_000, bytesRead: 9 << 30})
	cur := files(fileRow{database: "Restarted", reads: 3, bytesRead: 4096})

	perDB, total := fileDeltas(prev, cur, 2)
	if len(perDB) != 0 || total.ReadMBSec != 0 {
		t.Errorf("I/O across a reset = %+v / %v MB/sec, want nothing", perDB, total.ReadMBSec)
	}
}

// The total includes databases the connection can't name.
func TestFileDeltasTotalCoversEveryDatabase(t *testing.T) {
	prev := files(
		fileRow{database: "A", reads: 0, bytesRead: 0},
		fileRow{database: "B", reads: 0, bytesRead: 0},
	)
	cur := files(
		fileRow{database: "A", reads: 10, bytesRead: 10 * bytesPerMB},
		fileRow{database: "B", reads: 10, bytesRead: 30 * bytesPerMB},
	)

	perDB, total := fileDeltas(prev, cur, 1)
	if len(perDB) != 2 {
		t.Fatalf("per-database rows = %d, want 2", len(perDB))
	}
	if total.ReadMBSec != 40 {
		t.Errorf("total read throughput = %v MB/sec, want 40", total.ReadMBSec)
	}
	if total.Database != "Total" {
		t.Errorf("total row is named %q, want %q", total.Database, "Total")
	}
}
