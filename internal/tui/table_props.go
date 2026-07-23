package tui

import (
	"context"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// tablePropPages builds the page set for Table Properties. General,
// Columns, and Storage are read-only info pages; Change Tracking,
// Permissions, and Extended Properties are editable. Temporal, Ledger,
// Memory Optimization, FileTable, External Table, and Stretch — the
// mockup's remaining, version-sensitive pages — aren't built: nothing on
// this build's test databases uses any of them, and each needs its own
// new gosmo DDL/query support with no way to verify it live.
func tablePropPages(sc *db.ServerConn, dbName, schema, name string) []propPage {
	return []propPage{
		pageTableGeneral(sc, dbName, schema, name),
		pageTableColumns(sc, dbName, schema, name),
		pageTableStorage(sc, dbName, schema, name),
		pageTableChangeTracking(sc, dbName, schema, name),
		pageTablePermissions(sc, dbName, schema, name),
		pageTableExtendedProperties(sc, dbName, schema, name),
	}
}

// findTable resolves dbName/schema/name to a *gosmo.Table, the one lookup
// every page on this dialog needs first.
func findTable(ctx context.Context, sc *db.ServerConn, dbName, schema, name string) (*gosmo.Table, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.TableByNameContext(ctx, schema, name)
}

func pageTableGeneral(sc *db.ServerConn, dbName, schema, name string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, err := findTable(ctx, sc, dbName, schema, name)
			if err != nil {
				return nil, nil, err
			}
			detail, err := t.DetailContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			rowCount, err := t.RowCountContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			fks, err := t.ForeignKeysContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			idxs, err := t.IndexesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			triggers, err := t.TriggersContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			f := propsheet.NewForm(
				propsheet.Section("Table information"),
				propsheet.Static("Name", t.Name),
				propsheet.Static("Schema", t.Schema),
				propsheet.Static("Owner", detail.SchemaOwner),
				propsheet.Static("Created", formatSQLDate(t.CreateDate)),
				propsheet.Static("Last modified", formatSQLDate(t.ModifyDate)),
				propsheet.Static("Table type", "User table"),
				propsheet.Static("Row count", strconv.FormatInt(rowCount, 10)),
				propsheet.Static("Data space", detail.DataSpace),
				propsheet.Static("Lock escalation", detail.LockEscalation),
				propsheet.Section("Object details"),
				propsheet.Static("Object ID", strconv.Itoa(t.ObjectID)),
				propsheet.Static("ANSI NULLs", boolStr(detail.UsesAnsiNulls)),
				propsheet.Static("Replicated", boolStr(detail.IsReplicated)),
				propsheet.Static("Tracked by CDC", boolStr(detail.IsTrackedByCDC)),
				propsheet.Static("Temporal type", detail.TemporalType),
				propsheet.Static("Ledger type", detail.LedgerType),
				propsheet.Static("Memory optimized", boolStr(t.IsMemoryOptimized)),
				propsheet.Section("Dependencies"),
				propsheet.Static("Primary key", orDefault(detail.PrimaryKeyName, "None")),
				propsheet.Static("Foreign keys", strconv.Itoa(len(fks))),
				propsheet.Static("Indexes", strconv.Itoa(len(idxs))),
				propsheet.Static("Triggers", strconv.Itoa(len(triggers))),
			)
			return f, nil, nil
		},
	}
}

// pageTableColumns is a read-only grid (matching the mockup's own framing
// of Columns as "effectively a read-only summary") with a "selected
// column" detail section below it, rather than a separate popup modal —
// the same inline-detail convention Files/Extended Properties already use
// instead of introducing new modal infrastructure.
func pageTableColumns(sc *db.ServerConn, dbName, schema, name string) propPage {
	return propPage{
		title: "Columns",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, err := findTable(ctx, sc, dbName, schema, name)
			if err != nil {
				return nil, nil, err
			}
			cols, err := t.ColumnsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			rows := make([][]string, len(cols))
			for i, c := range cols {
				nullable := "Yes"
				if !c.IsNullable {
					nullable = "No"
				}
				key := ""
				if c.IsPrimaryKey {
					key = "PK"
				}
				rows[i] = []string{c.Name, gosmo.ColumnTypeString(c), nullable, key}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Column", "Type", "Null", "Key"}, rows)
			grid.SetCellCursor(true)

			nameStatic := propsheet.Static("Name", "")
			typeStatic := propsheet.Static("Data type", "")
			nullableStatic := propsheet.Static("Nullable", "")
			identityStatic := propsheet.Static("Identity", "")
			seedStatic := propsheet.Static("Identity seed", "")
			incrStatic := propsheet.Static("Identity increment", "")
			defaultStatic := propsheet.Static("Default", "")
			collationStatic := propsheet.Static("Collation", "")
			computedStatic := propsheet.Static("Computed", "")
			rowGUIDStatic := propsheet.Static("RowGuidCol", "")

			syncFromSelection := func() {
				i := grid.SelectedRow()
				if i < 0 || i >= len(cols) {
					return
				}
				c := cols[i]
				nameStatic.SetValue(c.Name)
				typeStatic.SetValue(gosmo.ColumnTypeString(c))
				nullableStatic.SetValue(boolStr(c.IsNullable))
				identityStatic.SetValue(boolStr(c.IsIdentity))
				if c.IsIdentity {
					seedStatic.SetValue(strconv.FormatInt(c.IdentitySeed, 10))
					incrStatic.SetValue(strconv.FormatInt(c.IdentityIncrement, 10))
				} else {
					seedStatic.SetValue("n/a")
					incrStatic.SetValue("n/a")
				}
				def := "n/a"
				if c.DefaultValue != nil {
					def = c.DefaultValue.Definition
				}
				defaultStatic.SetValue(def)
				collationStatic.SetValue(orDefault(c.Collation, "n/a"))
				computedStatic.SetValue(boolStr(c.IsComputed))
				rowGUIDStatic.SetValue(boolStr(c.IsRowGUID))
			}
			grid.OnSelectRow = func(row int) { syncFromSelection() }
			if len(cols) > 0 {
				syncFromSelection()
			}

			f := propsheet.NewForm(
				propsheet.Section("Columns"),
				propsheet.NewGridRow(grid, 10),
				propsheet.Section("Selected column"),
				nameStatic, typeStatic, nullableStatic, identityStatic, seedStatic, incrStatic,
				defaultStatic, collationStatic, computedStatic, rowGUIDStatic,
			)
			return f, nil, nil
		},
	}
}

func pageTableStorage(sc *db.ServerConn, dbName, schema, name string) propPage {
	return propPage{
		title: "Storage",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			t, err := findTable(ctx, sc, dbName, schema, name)
			if err != nil {
				return nil, nil, err
			}
			space, err := t.SpaceUsedContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			rowCount, err := t.RowCountContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			parts, err := t.PartitionsContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			rows := make([][]string, len(parts))
			for i, p := range parts {
				rows[i] = []string{strconv.Itoa(p.PartitionNumber), strconv.FormatInt(p.Rows, 10), p.DataCompression}
			}
			grid := controls.NewDataGrid()
			grid.SetData([]string{"Partition", "Rows", "Compression"}, rows)
			grid.SetCellCursor(true)

			f := propsheet.NewForm(
				propsheet.Section("Space"),
				propsheet.Static("Row count", strconv.FormatInt(rowCount, 10)),
				propsheet.Static("Reserved (KB)", strconv.FormatInt(space.ReservedKB, 10)),
				propsheet.Static("Data (KB)", strconv.FormatInt(space.DataKB, 10)),
				propsheet.Static("Indexes (KB)", strconv.FormatInt(space.IndexKB, 10)),
				propsheet.Static("LOB data (KB)", strconv.FormatInt(space.LOBKB, 10)),
				propsheet.Static("Unused (KB)", strconv.FormatInt(space.UnusedKB, 10)),
				propsheet.Section("Data location"),
				propsheet.Static("Filegroup", space.FileGroup),
				propsheet.Section("Partitions"),
				propsheet.NewGridRow(grid, 8),
			)
			return f, nil, nil
		},
	}
}

func pageTableChangeTracking(sc *db.ServerConn, dbName, schema, name string) propPage {
	return propPage{
		title: "Change Tracking",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			dbCT, err := d.ChangeTrackingContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			tables, err := d.TableChangeTrackingContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			current := &gosmo.TableChangeTracking{Schema: schema, Name: name}
			for _, tct := range tables {
				if tct.Schema == schema && tct.Name == name {
					current = tct
					break
				}
			}

			enabledRow := propsheet.Select("Table change tracking", onOff, boolIdx(current.Enabled))
			trackColsRow := propsheet.Select("Track columns updated", onOff, boolIdx(current.TrackColumnsUpdated))

			f := propsheet.NewForm(
				propsheet.Section("Database change tracking"),
				propsheet.Static("Database change tracking", boolStr(dbCT.Enabled)),
				propsheet.Static("Retention period", strconv.Itoa(dbCT.RetentionPeriod)+" "+strings.ToLower(orDefault(dbCT.RetentionUnit, "DAYS"))),
				propsheet.Static("Auto cleanup", boolStr(dbCT.AutoCleanup)),
				propsheet.Section("Table change tracking"),
				enabledRow,
				trackColsRow,
				propsheet.Note("Table-level change tracking requires database-level change tracking to be enabled first (Database Properties > Change Tracking)."),
			)

			apply := func(ctx context.Context) error {
				if !enabledRow.Dirty() && !trackColsRow.Dirty() {
					return nil
				}
				d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
				if err != nil {
					return err
				}
				return d.SetTableChangeTrackingContext(ctx, schema, name, enabledRow.Selected() == 1, trackColsRow.Selected() == 1)
			}
			return f, apply, nil
		},
	}
}

func pageTablePermissions(sc *db.ServerConn, dbName, schema, name string) propPage {
	return propPage{
		title: "Permissions",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			perms, err := d.PermissionsContext(ctx, schema, name)
			if err != nil {
				return nil, nil, err
			}
			users, err := d.UsersContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			roles, err := d.DatabaseRolesContext(ctx)
			if err != nil {
				return nil, nil, err
			}

			entries := make([]permEntry, len(perms))
			for i, p := range perms {
				entries[i] = permEntry{
					Principal: p.Principal, PrincipalType: p.PrincipalType,
					Grantor: p.Grantor, Permission: string(p.Permission), State: string(p.State),
				}
			}
			principals := make([]permPrincipal, 0, len(users)+len(roles))
			for _, u := range users {
				principals = append(principals, permPrincipal{Name: u.Name, Type: u.UserType})
			}
			for _, r := range roles {
				principals = append(principals, permPrincipal{Name: r.Name, Type: "DATABASE_ROLE"})
			}

			f, apply := buildPermissionsMatrix(principals, gosmo.ObjectPermissionNames(), entries, 8, 12,
				func(ctx context.Context, permission, principal string) error {
					return d.GrantPermissionContext(ctx, schema, name, gosmo.ObjectPermission(permission), principal)
				},
				func(ctx context.Context, permission, principal string) error {
					return d.DenyPermissionContext(ctx, schema, name, gosmo.ObjectPermission(permission), principal)
				},
				func(ctx context.Context, permission, principal string) error {
					return d.RevokePermissionContext(ctx, schema, name, gosmo.ObjectPermission(permission), principal)
				},
			)
			return f, apply, nil
		},
	}
}

func pageTableExtendedProperties(sc *db.ServerConn, dbName, schema, name string) propPage {
	return propPage{
		title: "Extended Properties",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			level := gosmo.ExtendedPropertyLevel{Level0Type: "SCHEMA", Level0Name: schema, Level1Type: "TABLE", Level1Name: name}
			props, err := d.ExtendedPropertiesContext(ctx, level)
			if err != nil {
				return nil, nil, err
			}
			f, apply := buildExtendedPropertiesForm(sc, dbName, level, props)
			return f, apply, nil
		},
	}
}
