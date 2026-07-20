package tui

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// databasePropPages builds the page set for Database Properties. General
// is mostly a read-only info page, aside from Owner/Recovery model; every
// other page is fully or partially editable — Files/Filegroups support
// rename/resize/growth/max size and Add/Remove, Database Scoped
// Configurations covers the well-known options with a read-only dump of
// the rest, and Query Store exposes its full configuration plus
// Flush/Clear actions.
func databasePropPages(sc *db.ServerConn, dbName string) []propPage {
	return []propPage{
		pageDatabaseGeneral(sc, dbName),
		pageDatabaseFiles(sc, dbName),
		pageDatabaseFilegroups(sc, dbName),
		pageDatabaseOptions(sc, dbName),
		pageDatabaseChangeTracking(sc, dbName),
		pageDatabaseQueryStore(sc, dbName),
		pageDatabasePermissions(sc, dbName),
		pageDatabaseExtendedProperties(sc, dbName),
		pageDatabaseScopedConfig(sc, dbName),
	}
}

func pageDatabaseGeneral(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			space, err := d.SpaceUsedContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			opts, err := d.OptionsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			users, err := d.UsersContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			history, err := sc.Server.BackupHistoryContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			logins, err := sc.Server.LoginsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			loginNames := make([]string, len(logins))
			for i, l := range logins {
				loginNames[i] = l.Name
			}
			sort.Strings(loginNames)
			lastFull, lastDiff, lastLog := "Never", "Never", "Never"
			for _, b := range history {
				switch b.BackupType {
				case gosmo.BackupActionDatabase:
					if lastFull == "Never" {
						lastFull = formatSQLDate(b.BackupFinish)
					}
				case gosmo.BackupActionDifferential:
					if lastDiff == "Never" {
						lastDiff = formatSQLDate(b.BackupFinish)
					}
				case gosmo.BackupActionLog:
					if lastLog == "Never" {
						lastLog = formatSQLDate(b.BackupFinish)
					}
				}
			}

			ownerRow := propsheet.Select("Owner", loginNames, indexOf(loginNames, opts.Owner))
			recoveryItems := []string{"SIMPLE", "FULL", "BULK_LOGGED"}
			recoveryRow := propsheet.Select("Recovery model", recoveryItems, indexOf(recoveryItems, string(d.RecoveryModel())))

			f := propsheet.NewForm(
				propsheet.Section("Database information"),
				propsheet.Static("Name", d.Name()),
				propsheet.Static("Status", d.State()),
				ownerRow,
				propsheet.Static("Date created", formatSQLDate(d.CreateDate())),
				propsheet.Static("Size (MB)", fmt.Sprintf("%.2f", space.TotalMB)),
				propsheet.Static("Space available (MB)", fmt.Sprintf("%.2f", space.UnallocatedMB)),
				propsheet.Static("Number of users", strconv.Itoa(len(users))),
				propsheet.Section("Maintenance"),
				propsheet.Static("Collation", d.Collation()),
				propsheet.Static("Compatibility level", strconv.Itoa(int(d.CompatibilityLevel()))),
				recoveryRow,
				propsheet.Static("Page verify", opts.PageVerify),
				propsheet.Static("Auto close", boolStr(opts.AutoClose)),
				propsheet.Static("Auto shrink", boolStr(opts.AutoShrink)),
				propsheet.Static("Last database backup", lastFull),
				propsheet.Static("Last differential backup", lastDiff),
				propsheet.Static("Last log backup", lastLog),
				propsheet.Section("Containment"),
				propsheet.Static("Containment type", opts.Containment),
				propsheet.Static("Encrypted", boolStr(opts.IsEncrypted)),
				propsheet.Static("Trustworthy", boolStr(opts.IsTrustworthy)),
				propsheet.Static("Read only", boolStr(d.IsReadOnly())),
			)

			apply := func(ctx context.Context) error {
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				if ownerRow.Dirty() {
					if err := d.SetOwnerContext(ctx, ownerRow.Value()); err != nil {
						return err
					}
				}
				if recoveryRow.Dirty() {
					model := gosmo.RecoveryModel(recoveryItems[recoveryRow.Selected()])
					if err := d.SetRecoveryModelContext(ctx, model); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}
