package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
)

// loadServerDetails shows the server's connect-time-cached info (version,
// edition, paths, CPU count, physical memory — no extra round trip, since
// gosmo.Server.Info() returns what Connect already loaded) immediately,
// then backfills available memory and NUMA node count (one DMV query each)
// followed by per-volume disk free space, appended once it lands.
func (db *DetailBrowser) loadServerDetails(app *App, sc *dbconn.ServerConn, node *explorerNode, seq int) {
	// Everything below, including the "instant" first stage, runs on a
	// background goroutine — never call postPartial/postFinal (and the
	// wakeEventLoop they trigger) directly from ShowNodeDetails' own
	// goroutine (the UI goroutine): see wakeEventLoop's doc comment in
	// app.go for why that's unsafe.
	app.safegoRepair("loading server details", db.panicRepair(node, seq), func() {
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
			{"CPU Count", sysInfoInt(info, int64(info.LogicalCPUCount))},
			{"Memory (MB)", sysInfoMB(info)},
			{"Available Memory (MB)", "Loading..."},
			{"NUMA Nodes", "Loading..."},
		}
		cols := propertyValueColumns
		db.postPartial(app, seq, cols, rows)

		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
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
		app.postAndWake(func() {
			if seq == db.seq {
				db.grid.RefreshColumnWidths()
			}
		})

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
	})
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

// errorLogFilesDetail builds the SQL Server Logs / Agent Error Logs folder's
// detail view: one row per log file, matching what the folder's own children
// list.
func errorLogFilesDetail(ctx context.Context, sc *dbconn.ServerConn, logType gosmo.ErrorLogType) ([]string, [][]string, error) {
	files, err := sc.Server.EnumErrorLogsContext(ctx, logType)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0, len(files))
	for _, f := range files {
		rows = append(rows, []string{errorLogFileLabel(f), formatBytes(f.SizeBytes)})
	}
	return []string{"Log", "Size"}, rows, nil
}

// errorLogFileDetail builds one log file leaf's detail view. The entry count
// is what makes it worth a round trip — the size and date are already in the
// label — so the log is read here as well as enumerated.
func errorLogFileDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	logType, logNum := node.data.LogType, node.data.LogNumber
	rows := [][]string{
		{"Log", logType.String()},
		{"File", node.label},
	}
	if files, err := sc.Server.EnumErrorLogsContext(ctx, logType); err == nil {
		for _, f := range files {
			if f.Number == logNum {
				rows = append(rows,
					[]string{"Last written", formatSQLDate(f.LastWritten)},
					[]string{"Size", formatBytes(f.SizeBytes)})
				break
			}
		}
	}
	entries, err := sc.Server.ReadLogContext(ctx, logType, logNum)
	if err != nil {
		return nil, nil, err
	}
	rows = append(rows, []string{"Entries", strconv.Itoa(len(entries))})
	if len(entries) > 0 {
		rows = append(rows,
			[]string{"First entry", formatSQLDate(entries[0].Date)},
			[]string{"Last entry", formatSQLDate(entries[len(entries)-1].Date)})
	}
	return propertyValueColumns, rows, nil
}

// backupDevicesFolderDetail lists every logical backup device. It reads gosmo
// independently of the tree, so the folder's filter is applied here too — over
// the gosmo objects, before the rows are built.
func backupDevicesFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	devices, err := sc.Server.BackupDevicesContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	devices = filterObjects(node.data.Filter, devices, func(d *gosmo.BackupDevice) nodeData {
		return nodeData{Name: d.Name}
	})

	rows := make([][]string, 0, len(devices))
	out := make([]nodeData, 0, len(devices))
	for _, d := range devices {
		rows = append(rows, []string{d.Name, d.Type, d.PhysicalName})
		out = append(out, nodeData{Type: NodeBackupDevice, Name: d.Name})
	}
	*objs = out
	return []string{"Name", "Type", "Physical Location"}, rows, nil
}

// backupDeviceDetail is one backup device's Property/Value view. What the
// device *holds* is not read here: RESTORE HEADERONLY opens the media, which
// on a missing file or an offline tape is a failure the Details pane would
// report as the device being unreadable. The Media Contents page of Backup
// Device Properties is where that read belongs.
func backupDeviceDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	d, err := sc.Server.BackupDeviceByNameContext(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	return propertyRows(
		"Name", d.Name,
		"Type", d.Type,
		"Physical Location", d.PhysicalName,
	)
}

// serverTriggersFolderDetail lists every server-scope DDL and logon trigger.
// It reads gosmo independently of the tree, so the folder's filter is applied
// here too — over the gosmo objects, before the rows are built.
func serverTriggersFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	triggers, err := sc.Server.ServerTriggersContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	triggers = filterObjects(node.data.Filter, triggers, func(t *gosmo.ServerTrigger) nodeData {
		return nodeData{Name: t.Name, CreateDate: t.CreateDate}
	})

	rows := make([][]string, 0, len(triggers))
	out := make([]nodeData, 0, len(triggers))
	for _, t := range triggers {
		rows = append(rows, []string{
			t.Name, enabledText(t.IsEnabled), strings.Join(t.Events, ", "),
			formatSQLDate(t.CreateDate), formatSQLDate(t.ModifyDate),
		})
		out = append(out, nodeData{Type: NodeServerTrigger, Name: t.Name})
	}
	*objs = out
	return []string{"Name", "Status", "Events", "Created", "Modified"}, rows, nil
}

// serverTriggerDetail is one server trigger's Property/Value view. The
// definition is not shown here — it is multi-line, which a grid row flattens;
// the Properties dialog's Definition page is where it belongs.
func serverTriggerDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	t, err := sc.Server.ServerTriggerByNameContext(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	return propertyRows(
		"Name", t.Name,
		"Status", enabledText(t.IsEnabled),
		"Events", strings.Join(t.Events, ", "),
		"Created", formatSQLDate(t.CreateDate),
		"Modified", formatSQLDate(t.ModifyDate),
	)
}

// endpointsFolderDetail lists every endpoint on the server. It reads gosmo
// independently of the tree, so the folder's filter is applied here too — over
// the gosmo objects, before the rows are built.
func endpointsFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	endpoints, err := sc.Server.EndpointsContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	endpoints = filterObjects(node.data.Filter, endpoints, func(e *gosmo.Endpoint) nodeData {
		return nodeData{Name: e.Name}
	})

	rows := make([][]string, 0, len(endpoints))
	out := make([]nodeData, 0, len(endpoints))
	for _, e := range endpoints {
		rows = append(rows, []string{
			e.Name, e.Protocol, e.Type, endpointStateLabel(e.State),
			endpointPortText(e), e.Owner,
		})
		// IsSystem travels with the row object, not just the tree node: the
		// pane's own Delete reads these, and a built-in endpoint deleted from
		// here would fail on a statement SQL Server refuses.
		out = append(out, nodeData{Type: NodeEndpoint, Name: e.Name, IsSystem: e.IsSystem})
	}
	*objs = out
	return []string{"Name", "Protocol", "Type", "State", "Port", "Owner"}, rows, nil
}

// endpointDetail is one endpoint's Property/Value view.
func endpointDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	e, err := sc.Server.EndpointByNameContext(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	return propertyRows(
		"Name", e.Name,
		"Protocol", e.Protocol,
		"Type", e.Type,
		"State", endpointStateLabel(e.State),
		"Port", endpointPortText(e),
		"Owner", e.Owner,
		"System endpoint", yesNo(e.IsSystem),
	)
}

// endpointPortText renders the listener port. A non-TCP endpoint has none, and
// the built-in TCP ones report 0 rather than the instance's real port — "0"
// in either column reads as a port the caller could connect to.
func endpointPortText(e *gosmo.Endpoint) string {
	if e.Port == 0 {
		return ""
	}
	return strconv.Itoa(e.Port)
}

// enabledText renders an is-enabled flag the way the Details pane and the
// Properties pages both name it.
func enabledText(enabled bool) string {
	if enabled {
		return "Enabled"
	}
	return "Disabled"
}
