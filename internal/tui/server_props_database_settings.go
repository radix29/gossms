package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

var filestreamLevels = []string{"Disabled", "Transact-SQL access enabled", "Full access enabled"}

func pageServerDatabaseSettings(sc *db.ServerConn) propPage {
	return propPage{
		title: "Database Settings",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			info := sc.Server.Info()
			configs, err := sc.Server.ConfigurationsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			vols, err := sc.Server.DiskVolumesContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			var intRows []configRow
			var boolRows []configBoolRow
			cfgInt := newConfigEditor(configs, &intRows)
			cfgBool := newConfigBoolEditor(configs, &boolRows)

			filestream := propsheet.Select("FILESTREAM access level", filestreamLevels, 0)
			var filestreamCfg *gosmo.ConfigurationOption
			if c := findConfig(configs, "filestream access level"); c != nil {
				filestreamCfg = c
				filestream.SetSelected(int(c.ValueInUse))
			} else {
				filestream = propsheet.Select("FILESTREAM access level", []string{"N/A"}, 0)
			}

			rows := []propsheet.Row{
				propsheet.Section("Default database settings"),
				cfgInt("fill factor (%)", "Default index fill factor", "%"),
				propsheet.Section("Backup and restore"),
				cfgInt("media retention", "Backup media retention", "days"),
				cfgBool("backup compression default", "Backup compression default"),
				cfgBool("backup checksum default", "Backup checksum default"),
				propsheet.Section("Default locations"),
				propsheet.Static("Data", info.DefaultDataPath),
				propsheet.Static("Log", info.DefaultLogPath),
				propsheet.Static("Backup", info.DefaultBackupPath),
			}
			if len(vols) > 0 {
				rows = append(rows, propsheet.Section("Disk space"))
				for i, v := range vols {
					rows = append(rows, propsheet.Static(diskVolumeLabel(i, v), diskVolumeValue(v)))
				}
			}
			rows = append(rows,
				propsheet.Section("Recovery"),
				cfgInt("recovery interval (min)", "Recovery interval", "min"),
				propsheet.Section("FILESTREAM"),
				filestream,
				propsheet.Note("A fill factor of 0 uses the server default."),
			)
			f := propsheet.NewForm(rows...)

			apply := func(ctx context.Context) error {
				changed, err := applyConfigRows(ctx, sc, intRows, boolRows)
				if err != nil {
					return err
				}
				if filestreamCfg != nil && filestream.Dirty() {
					opt, err := sc.Server.ConfigurationByNameContext(ctx, "filestream access level")
					if err != nil {
						return err
					}
					if err := opt.SetValueContext(ctx, int64(filestream.Selected())); err != nil {
						return err
					}
					changed = true
				}
				if changed {
					return sc.Server.ReconfigureContext(ctx, false)
				}
				return nil
			}
			return f, apply, nil
		},
	}
}
