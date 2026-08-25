package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

func pageServerMemory(sc *db.ServerConn) propPage {
	return propPage{
		title: "Memory",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			configs, err := sc.Server.ConfigurationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			// Not fatal — see server_props_general.go. The four Current
			// values rows below are the only thing that needs it, and the
			// configuration rows above them are editable without it.
			mem, memErr := sc.Server.MemoryStatsContext(ctx)
			if memErr != nil {
				// A refused read returns nil, and the rows below read fields
				// off it before memMB can decide anything.
				mem = &gosmo.ServerMemoryStats{}
			}
			memMB := func(v int64) string {
				if memErr != nil {
					return unreadableValue
				}
				return strconv.FormatInt(v, 10)
			}

			var intRows []configRow
			cfgInt := newConfigEditor(configs, &intRows)

			rows := []propsheet.Row{
				propsheet.Section("Server memory options"),
				cfgInt("min server memory (MB)", "Minimum server memory", "MB"),
				cfgInt("max server memory (MB)", "Maximum server memory", "MB"),
				cfgInt("index create memory (KB)", "Index creation memory", "KB"),
				cfgInt("min memory per query (KB)", "Minimum memory per query", "KB"),
				propsheet.Section("Current values"),
				propsheet.Static("Physical memory (MB)", memMB(mem.PhysicalMemoryMB)),
				propsheet.Static("Available memory (MB)", memMB(mem.AvailableMemoryMB)),
				propsheet.Static("Target server memory (MB)", memMB(mem.TargetServerMemoryMB)),
				propsheet.Static("Total server memory (MB)", memMB(mem.TotalServerMemoryMB)),
				propsheet.Note("Max server memory should leave memory for the OS, agents, backups, linked components, and monitoring tools."),
			}
			if memErr != nil {
				rows = append(rows, deniedReadNote(viewServerStateAdvice))
			}
			return propsheet.NewForm(rows...), configApply(sc, intRows, nil), nil
		},
	}
}
