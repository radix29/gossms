package tui

import (
	"context"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

func pageServerConnections(sc *db.ServerConn) propPage {
	return propPage{
		title: "Connections",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			configs, err := sc.Server.ConfigurationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			var intRows []configRow
			var boolRows []configBoolRow
			cfgInt := newConfigEditor(configs, &intRows)
			cfgBool := newConfigBoolEditor(configs, &boolRows)

			f := propsheet.NewForm(
				propsheet.Section("Connections"),
				cfgInt("user connections", "Maximum concurrent connections", ""),
				propsheet.Section("Remote server connections"),
				cfgBool("remote access", "Allow remote connections to this server"),
				cfgBool("remote proc trans", "Require distributed transactions"),
				cfgInt("remote query timeout (s)", "Remote query timeout", "sec"),
				cfgInt("remote login timeout (s)", "Remote login timeout", "sec"),
				propsheet.Section("Query governor"),
				cfgInt("query governor cost limit", "Estimated query cost limit", ""),
				propsheet.Section("Default connection options"),
				cfgInt("network packet size (B)", "Network packet size", "bytes"),
				propsheet.Section("User defaults"),
				cfgInt("default language", "Default language", ""),
				cfgInt("default full-text language", "Default full-text language", ""),
				cfgInt("two digit year cutoff", "Two digit year cutoff", ""),
				propsheet.Note("A value of 0 for maximum connections means SQL Server automatically manages the limit."),
			)
			return f, configApply(sc, intRows, boolRows), nil
		},
	}
}
