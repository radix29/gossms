package tui

import (
	"context"
	"strconv"

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
			mem, err := sc.Server.MemoryStatsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			var intRows []configRow
			cfgInt := newConfigEditor(configs, &intRows)

			f := propsheet.NewForm(
				propsheet.Section("Server memory options"),
				cfgInt("min server memory (MB)", "Minimum server memory", "MB"),
				cfgInt("max server memory (MB)", "Maximum server memory", "MB"),
				cfgInt("index create memory (KB)", "Index creation memory", "KB"),
				cfgInt("min memory per query (KB)", "Minimum memory per query", "KB"),
				propsheet.Section("Current values"),
				propsheet.Static("Physical memory (MB)", strconv.FormatInt(mem.PhysicalMemoryMB, 10)),
				propsheet.Static("Available memory (MB)", strconv.FormatInt(mem.AvailableMemoryMB, 10)),
				propsheet.Static("Target server memory (MB)", strconv.FormatInt(mem.TargetServerMemoryMB, 10)),
				propsheet.Static("Total server memory (MB)", strconv.FormatInt(mem.TotalServerMemoryMB, 10)),
				propsheet.Note("Max server memory should leave memory for the OS, agents, backups, linked components, and monitoring tools."),
			)
			return f, configApply(sc, intRows, nil), nil
		},
	}
}
