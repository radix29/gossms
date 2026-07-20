package tui

import (
	"context"
	"fmt"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
)

// loadServerDetails shows the server's connect-time-cached info (version,
// edition, paths, CPU count, physical memory — all free, no extra round
// trip, since gosmo.Server.Info() just returns what Connect already
// loaded) immediately, then backfills available memory and NUMA node count
// (one DMV query each) followed by per-volume disk free space (another
// query, appended once it lands) — the same "System Information" facts
// already surfaced on Server Properties' General/Memory/Processors pages.
func (db *DetailBrowser) loadServerDetails(app *App, sc *dbconn.ServerConn, node *explorerNode, seq int) {
	// Everything below, including the "instant" first stage, runs on a
	// background goroutine — never call postPartial/postFinal (and the
	// wakeEventLoop they trigger) directly from ShowNodeDetails' own
	// goroutine (the UI goroutine): see wakeEventLoop's doc comment in
	// app.go for why that's unsafe.
	go func() {
		info := sc.Server.Info()
		const availMemRow, numaRow = 9, 10
		rows := [][]string{
			{"Server", sc.Opts.Server},
			{"Version", info.ProductVersion},
			{"Edition", info.Edition},
			{"OS Version", info.OSVersion},
			{"Collation", info.Collation},
			{"Data Path", info.DefaultDataPath},
			{"Log Path", info.DefaultLogPath},
			{"CPU Count", strconv.Itoa(info.LogicalCPUCount)},
			{"Memory (MB)", formatMB(float64(info.PhysicalMemoryMB))},
			{"Available Memory (MB)", "Loading..."},
			{"NUMA Nodes", "Loading..."},
		}
		cols := []string{"Property", "Value"}
		db.postPartial(app, seq, cols, rows)

		ctx, cancel := context.WithTimeout(context.Background(), childFetchTimeout)
		defer cancel()

		if mem, err := sc.Server.MemoryStatsContext(ctx); err == nil {
			rows[availMemRow][1] = formatMB(float64(mem.AvailableMemoryMB))
		} else {
			rows[availMemRow][1] = "N/A"
		}
		if proc, err := sc.Server.ProcessorInfoContext(ctx); err == nil {
			rows[numaRow][1] = strconv.Itoa(proc.NUMANodeCount)
		} else {
			rows[numaRow][1] = "N/A"
		}
		app.postEvent(func() {
			if seq == db.seq {
				db.grid.RefreshColumnWidths()
			}
		})
		app.wakeEventLoop()

		// Cross-platform disk free space (sys.dm_os_volume_stats works
		// identically on Windows and Linux). Appended once it lands, rather
		// than backfilled in place like the rows above, since the row count
		// itself is only known now.
		if vols, err := sc.Server.DiskVolumesContext(ctx); err == nil {
			for i, v := range vols {
				rows = append(rows, []string{diskVolumeLabel(i, v), diskVolumeValue(v)})
			}
		}
		db.postFinal(app, node, seq, cols, rows, nil)
	}()
}

// diskVolumeLabel names a disk volume row: the mount point/drive letter
// when SQL Server reports one, else the OS volume label, else a numbered
// fallback (some containerized Linux hosts report both blank).
func diskVolumeLabel(i int, v gosmo.DiskVolumeInfo) string {
	switch {
	case v.MountPoint != "":
		return "Disk (" + v.MountPoint + ")"
	case v.VolumeName != "":
		return "Disk (" + v.VolumeName + ")"
	default:
		return fmt.Sprintf("Disk %d", i+1)
	}
}

// diskVolumeValue formats a disk volume's free/total space, appending a
// sample database file path when the volume itself couldn't be named.
func diskVolumeValue(v gosmo.DiskVolumeInfo) string {
	val := formatMB(v.AvailableMB) + " free of " + formatMB(v.TotalMB)
	if v.MountPoint == "" && v.VolumeName == "" && v.SamplePath != "" {
		val += " (" + v.SamplePath + ")"
	}
	return val
}
