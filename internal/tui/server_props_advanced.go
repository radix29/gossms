package tui

import (
	"context"
	"strconv"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// pageServerAdvanced groups the sp_configure options that have no more
// specific home elsewhere (Memory/Processors/Connections/Database
// Settings/Security already cover the rest) into editable rows, then
// lists every remaining option — including ones this build doesn't expose
// individually — in a read-only grid underneath for full visibility.
func pageServerAdvanced(sc *db.ServerConn) propPage {
	return propPage{
		title: "Advanced",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			configs, err := sc.Server.ConfigurationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			var intRows []configRow
			var boolRows []configBoolRow
			cfgInt := newConfigEditor(configs, &intRows)
			cfgBool := newConfigBoolEditor(configs, &boolRows)

			rows := make([][]string, len(configs))
			for i, c := range configs {
				rows[i] = []string{c.Name, strconv.FormatInt(c.ValueInUse, 10), c.Description}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Option", "Value", "Description"}, rows)
			grid.SetCellCursor(true)

			f := propsheet.NewForm(
				propsheet.Section("Miscellaneous"),
				cfgBool("optimize for ad hoc workloads", "Optimize for ad hoc workloads"),
				cfgInt("blocked process threshold (s)", "Blocked process threshold", "sec"),
				cfgInt("cursor threshold", "Cursor threshold", ""),
				cfgInt("in-doubt xact resolution", "In-doubt xact resolution", ""),
				cfgBool("scan for startup procs", "Scan for startup procs"),
				propsheet.Section("Security"),
				cfgBool("common criteria compliance enabled", "Common criteria compliance enabled"),
				cfgBool("default trace enabled", "Default trace enabled"),
				propsheet.Section("Server Configuration"),
				cfgBool("Ad Hoc Distributed Queries", "Ad Hoc Distributed Queries"),
				cfgBool("Agent XPs", "Agent XPs"),
				cfgBool("clr enabled", "CLR enabled"),
				cfgBool("clr strict security", "CLR strict security"),
				cfgBool("Database Mail XPs", "Database Mail XPs"),
				cfgBool("external scripts enabled", "External scripts enabled"),
				cfgBool("Ole Automation Procedures", "Ole Automation Procedures"),
				cfgBool("remote admin connections", "Remote admin connections"),
				cfgBool("show advanced options", "Show advanced options"),
				cfgBool("xp_cmdshell", "xp_cmdshell"),
				propsheet.Section("All server configuration options (sys.configurations)"),
				propsheet.NewGridRow(grid, 10),
				propsheet.Note("The grid above is read-only — edit an option from its group above, or from its own page (Memory, Processors, Connections, Database Settings, Security), if it has one."),
			)
			return f, configApply(sc, intRows, boolRows), nil
		},
	}
}
