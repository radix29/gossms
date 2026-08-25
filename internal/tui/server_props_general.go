package tui

import (
	"context"
	"strconv"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

func pageServerGeneral(sc *db.ServerConn) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			info := sc.Server.Info()
			sec, err := sc.Server.SecurityInfoContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			// Not fatal: sys.dm_os_process_memory needs VIEW SERVER
			// PERFORMANCE STATE, which a db_owner does not have, and failing
			// the page for it would put every value above out of that login's
			// reach for the sake of one row.
			mem, memErr := sc.Server.MemoryStatsContext(ctx)
			memText := unreadableValue
			if memErr == nil {
				memText = strconv.FormatInt(mem.PhysicalMemoryMB, 10) + " MB"
			}
			configs, err := sc.Server.ConfigurationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			rows := []propsheet.Row{
				propsheet.Section("Server information"),
				propsheet.Static("Name", sc.Opts.Server),
				propsheet.Static("Product", "Microsoft SQL Server"),
				propsheet.Static("Version", info.ProductVersion),
				propsheet.Static("Edition", info.Edition),
				propsheet.Static("Engine edition", engineEditionName(info.EngineEdition)),
				propsheet.Static("Collation", info.Collation),
				propsheet.Static("Language", "English"),
				propsheet.Static("Platform", info.OSVersion),
				propsheet.Section("Availability"),
				propsheet.Static("Is clustered", boolStr(info.IsClustered)),
				propsheet.Static("HADR enabled", boolStr(info.IsHADREnabled)),
				propsheet.Static("Single-user mode", boolStr(info.IsSingleUser)),
				propsheet.Section("Security"),
				propsheet.Static("Authentication", sec.AuthenticationMode),
				propsheet.Section("Resources"),
				propsheet.Static("CPU count", sysInfoInt(info, int64(info.LogicalCPUCount))),
				propsheet.Static("Memory", memText),
				propsheet.Static("Max worker threads", configValue(configs, "max worker threads")),
			}
			if memErr != nil || info.SysInfoUnavailable {
				rows = append(rows, deniedReadNote("VIEW SERVER STATE"))
			}
			return propsheet.NewForm(rows...), nil, nil
		},
	}
}
