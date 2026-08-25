package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// unknownOwnerItem is the stand-in every Owner dropdown shows when the
// owning principal doesn't resolve — SUSER_SNAME/DPRINCIPAL name comes back
// NULL (gosmo reports "") for a database restored from elsewhere, a job or
// schedule whose owner_sid no longer exists, or a role or schema owned by a
// dropped principal. Without it, indexOf's 0-fallback silently displays an
// unrelated real login as though it were the actual owner.
//
// Shared by database, job, schedule, role, server-role and schema properties
// via selectPreserving, so the six pages say the same thing about the same
// condition.
const unknownOwnerItem = "(unresolved owner)"

// unsetItem is the stand-in for a non-owner setting the server reports blank
// — a login with no default database or language, a user with no default
// schema (normal for one mapped to a Windows group). Distinct from
// unknownOwnerItem because "nothing is configured" and "the configured
// principal has vanished" are different facts, and the second is the one
// worth chasing.
const unsetItem = "(not set)"

// databasePropPages builds the page set for Database Properties. General
// is mostly a read-only info page, aside from Owner/Recovery model; every
// other page is fully or partially editable — Files/Filegroups support
// rename/resize/growth/max size and Add/Remove, Database Scoped
// Configurations covers the well-known options with a read-only dump of
// the rest, and Query Store exposes its full configuration plus
// Flush/Clear actions.
func databasePropPages(sc *db.ServerConn, dbName string) []propPage {
	w := databaseWriteRights()
	return []propPage{
		withRequires(pageDatabaseGeneral(sc, dbName), dbName, w...),
		withRequires(pageDatabaseFiles(sc, dbName), dbName, w...),
		withRequires(pageDatabaseFilegroups(sc, dbName), dbName, w...),
		withRequires(pageDatabaseOptions(sc, dbName), dbName, w...),
		withRequires(pageDatabaseChangeTracking(sc, dbName), dbName, w...),
		withRequires(pageDatabaseQueryStore(sc, dbName), dbName, w...),
		withRequires(pageDatabasePermissions(sc, dbName), dbName, rightControlDB, rightAlterAnyDatabase),
		withRequires(pageDatabaseExtendedProperties(sc, dbName), dbName, w...),
		withRequires(pageDatabaseScopedConfig(sc, dbName), dbName, w...),
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
			slices.Sort(loginNames)
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

			// sys.database_principals is metadata-visibility filtered, so a
			// login without VIEW DEFINITION counts only the users it can see
			// and the page would state that smaller number as fact — 5 of
			// HealthClinic's 8, measured on both test servers.
			dbCaps := sc.DatabaseCapabilities(ctx, dbName)
			userCount := valueOrUnreadable(dbCaps, "VIEW DEFINITION", strconv.Itoa(len(users)))

			ownerRow := selectPreserving("Owner", loginNames, opts.Owner, unknownOwnerItem)
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
				propsheet.Static("Number of users", userCount),
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
				if owner, ok := changedTo(ownerRow, unknownOwnerItem); ok {
					if err := d.SetOwnerContext(ctx, owner); err != nil {
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
