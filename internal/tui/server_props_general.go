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
			mem, err := sc.Server.MemoryStatsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			configs, err := sc.Server.ConfigurationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			f := propsheet.NewForm(
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
				propsheet.Static("CPU count", strconv.Itoa(info.LogicalCPUCount)),
				propsheet.Static("Memory", strconv.FormatInt(mem.PhysicalMemoryMB, 10)+" MB"),
				propsheet.Static("Max worker threads", configValue(configs, "max worker threads")),
			)
			return f, nil, nil
		},
	}
}
