package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// schemaPropPages builds the page set for Schema Properties: General
// (read-only info plus an editable Owner for non-system schemas — SQL
// Server has no RENAME SCHEMA facility, so unlike Table/Role/User
// Properties the schema name itself is always read-only, not just for
// built-ins), Permissions (schema-scoped GRANT/DENY, the schema analog of
// Table Properties' Permissions page), and Extended Properties. Principal
// pickers, permission-detail/effective-permissions modals, and the Drop
// Schema prompt are folded into inline fields and the existing Script
// Changes/SetMessage mechanisms. There are no Created/Modified rows:
// sys.schemas has no create_date/modify_date columns.
func schemaPropPages(sc *db.ServerConn, dbName, schemaName string) []propPage {
	return []propPage{
		pageSchemaGeneral(sc, dbName, schemaName),
		pageSchemaPermissions(sc, dbName, schemaName),
		pageExtendedProperties(sc, dbName, func() gosmo.ExtendedPropertyLevel {
			return gosmo.ExtendedPropertyLevel{Level0Type: "SCHEMA", Level0Name: schemaName}
		}),
	}
}

// findSchema resolves dbName/schemaName to a *gosmo.Schema.
func findSchema(ctx context.Context, sc *db.ServerConn, dbName, schemaName string) (*gosmo.Schema, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.SchemaByNameContext(ctx, schemaName)
}

func pageSchemaGeneral(sc *db.ServerConn, dbName, schemaName string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			schema, err := findSchema(ctx, sc, dbName, schemaName)
			if err != nil {
				return nil, nil, err
			}
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
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
			counts, err := schema.ObjectCountsByTypeContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			perms, err := d.SchemaPermissionsContext(ctx, schemaName)
			if err != nil {
				return nil, nil, err
			}

			principalsSet := make(map[string]bool)
			grants, denies, withGrant := 0, 0, 0
			for _, p := range perms {
				principalsSet[p.Principal] = true
				switch string(p.State) {
				case "DENY":
					denies++
				case "GRANT_WITH_GRANT_OPTION":
					grants++
					withGrant++
				default:
					grants++
				}
			}

			ownerType := "Database role"
			for _, u := range users {
				if u.Name == schema.Owner {
					ownerType = u.UserType
					break
				}
			}

			builtin := isSystemSchema(schema.Name)

			rows := []propsheet.Row{
				propsheet.Section("Schema information"),
				propsheet.Static("Schema name", schema.Name),
			}
			var ownerRow *propsheet.SelectRow
			if builtin {
				rows = append(rows, propsheet.Static("Owner", schema.Owner))
			} else {
				ownerNames := principalNames(users, roles)
				ownerRow = selectPreserving("Owner", ownerNames, schema.Owner, unknownOwnerItem)
				rows = append(rows, ownerRow)
			}
			rows = append(rows,
				propsheet.Static("Principal type", ownerType),
				propsheet.Static("Schema ID", strconv.Itoa(schema.ID)),
			)
			if builtin {
				rows = append(rows, propsheet.Static("Is system schema", "True"))
			}
			rows = append(rows,
				propsheet.Section("Object summary"),
				propsheet.Static("Tables", strconv.Itoa(counts.Tables)),
				propsheet.Static("Views", strconv.Itoa(counts.Views)),
				propsheet.Static("Stored procedures", strconv.Itoa(counts.StoredProcedures)),
				propsheet.Static("Functions", strconv.Itoa(counts.Functions)),
				propsheet.Static("Synonyms", strconv.Itoa(counts.Synonyms)),
				propsheet.Static("Sequences", strconv.Itoa(counts.Sequences)),
				propsheet.Section("Permission summary"),
				propsheet.Static("Explicit principals", strconv.Itoa(len(principalsSet))),
				propsheet.Static("Explicit grants", strconv.Itoa(grants)),
				propsheet.Static("Explicit denies", strconv.Itoa(denies)),
				propsheet.Static("With grant option", strconv.Itoa(withGrant)),
			)
			if builtin {
				rows = append(rows,
					propsheet.Section("Warning"),
					propsheet.Note("Changing ownership or broad permissions on common schemas such as "+schema.Name+" can affect many objects and applications."),
				)
			} else {
				rows = append(rows,
					propsheet.Section("Notes"),
					propsheet.Note("Schema-level permissions apply to current and future objects in this schema where the permission is valid for the object type."),
				)
			}

			f := propsheet.NewForm(rows...)

			var apply propApply
			if !builtin {
				apply = func(ctx context.Context) error {
					owner, ok := changedTo(ownerRow, unknownOwnerItem)
					if !ok {
						return nil
					}
					s, err := findSchema(ctx, sc, dbName, schemaName)
					if err != nil {
						return err
					}
					return s.ChangeOwnerContext(ctx, owner)
				}
			}
			return f, apply, nil
		},
	}
}

func pageSchemaPermissions(sc *db.ServerConn, dbName, schemaName string) propPage {
	return propPage{
		title: "Permissions",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			perms, err := d.SchemaPermissionsContext(ctx, schemaName)
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

			f, apply := buildPermissionsMatrix(databasePermPrincipals(users, roles), gosmo.SchemaPermissionNames(),
				objectPermEntries(perms), 8, 12,
				schemaPermApply(d, schemaName))
			return f, apply, nil
		},
	}
}
