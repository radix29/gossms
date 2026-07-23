package tui

import (
	"context"
	"fmt"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// schemaPropPages builds the page set for Schema Properties: General
// (read-only info plus an editable Owner for non-system schemas — SQL
// Server has no RENAME SCHEMA facility, so unlike Table/Role/User
// Properties, the schema name itself is always read-only, not just for
// built-ins), Permissions (schema-scoped GRANT/DENY, the schema analog of
// Table Properties' Permissions page), and Extended Properties. The
// mockup's owner/add-principal picker modals, permission-detail/
// effective-permissions/object-inventory modals, high-impact filter, and
// Drop Schema safety modal are folded into inline fields and the existing
// Script Changes/SetMessage mechanisms, the same simplification made on
// every prior Properties dialog in this project. sys.schemas has no
// create_date/modify_date columns (unlike sys.tables/sys.procedures/...),
// so the mockup's "Created"/"Modified" General rows are dropped rather
// than faked.
func schemaPropPages(sc *db.ServerConn, dbName, schemaName string) []propPage {
	return []propPage{
		pageSchemaGeneral(sc, dbName, schemaName),
		pageSchemaPermissions(sc, dbName, schemaName),
		pageSchemaExtendedProperties(sc, dbName, schemaName),
	}
}

// findSchema resolves dbName/schemaName to a *gosmo.Schema — there's no
// SchemaByNameContext (gosmo only exposes the bulk SchemasContext
// listing), so this finds it by name the same way pageRoleOwnedSchemas/
// pageUserOwnedSchemas already do for their own owner-change apply.
func findSchema(ctx context.Context, sc *db.ServerConn, dbName, schemaName string) (*gosmo.Schema, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	schemas, err := d.SchemasContext(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range schemas {
		if s.Name == schemaName {
			return s, nil
		}
	}
	return nil, fmt.Errorf("schema %q not found in %q", schemaName, dbName)
}

// isSystemSchema reports whether a schema's owner can't be changed —
// dbo/guest/sys/INFORMATION_SCHEMA, the same fixed set isSystemUser
// already treats as non-interactive for the analogous reason.
func isSystemSchema(name string) bool {
	switch name {
	case "dbo", "guest", "sys", "INFORMATION_SCHEMA":
		return true
	default:
		return false
	}
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
			tables, err := d.TablesBySchemaContext(ctx, schemaName)
			if err != nil {
				return nil, nil, err
			}
			views, err := d.ViewsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			procs, err := d.StoredProceduresContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			funcs, err := d.UserDefinedFunctionsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			synonyms, err := d.SynonymsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			sequences, err := d.SequencesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			perms, err := d.SchemaPermissionsContext(ctx, schemaName)
			if err != nil {
				return nil, nil, err
			}

			viewCount, procCount, funcCount, synCount, seqCount := 0, 0, 0, 0, 0
			for _, v := range views {
				if v.Schema == schemaName {
					viewCount++
				}
			}
			for _, p := range procs {
				if p.Schema == schemaName {
					procCount++
				}
			}
			for _, fn := range funcs {
				if fn.Schema == schemaName {
					funcCount++
				}
			}
			for _, s := range synonyms {
				if s.Schema == schemaName {
					synCount++
				}
			}
			for _, s := range sequences {
				if s.Schema == schemaName {
					seqCount++
				}
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
				ownerRow = propsheet.Select("Owner", ownerNames, indexOf(ownerNames, schema.Owner))
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
				propsheet.Static("Tables", strconv.Itoa(len(tables))),
				propsheet.Static("Views", strconv.Itoa(viewCount)),
				propsheet.Static("Stored procedures", strconv.Itoa(procCount)),
				propsheet.Static("Functions", strconv.Itoa(funcCount)),
				propsheet.Static("Synonyms", strconv.Itoa(synCount)),
				propsheet.Static("Sequences", strconv.Itoa(seqCount)),
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
					if !ownerRow.Dirty() {
						return nil
					}
					s, err := findSchema(ctx, sc, dbName, schemaName)
					if err != nil {
						return err
					}
					return s.ChangeOwnerContext(ctx, ownerRow.Value())
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

			f, apply := buildPermissionsMatrix(principals, gosmo.SchemaPermissionNames(), entries, 8, 12,
				func(ctx context.Context, permission, principal string) error {
					return d.GrantSchemaPermissionContext(ctx, schemaName, gosmo.ObjectPermission(permission), principal)
				},
				func(ctx context.Context, permission, principal string) error {
					return d.DenySchemaPermissionContext(ctx, schemaName, gosmo.ObjectPermission(permission), principal)
				},
				func(ctx context.Context, permission, principal string) error {
					return d.RevokeSchemaPermissionContext(ctx, schemaName, gosmo.ObjectPermission(permission), principal)
				},
			)
			return f, apply, nil
		},
	}
}

func pageSchemaExtendedProperties(sc *db.ServerConn, dbName, schemaName string) propPage {
	return propPage{
		title: "Extended Properties",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			level := gosmo.ExtendedPropertyLevel{Level0Type: "SCHEMA", Level0Name: schemaName}
			props, err := d.ExtendedPropertiesContext(ctx, level)
			if err != nil {
				return nil, nil, err
			}
			f, apply := buildExtendedPropertiesForm(sc, dbName, level, props)
			return f, apply, nil
		},
	}
}
