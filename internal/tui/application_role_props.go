package tui

import (
	"context"
	"fmt"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// application_role_props.go is Application Role Properties (a database's
// Security > Roles > Application Roles folder): General (name, default schema,
// password), Owned Schemas, Securables and Extended Properties — SSMS's pages.
// Like Database Role Properties it has no Members page (an application role has
// none: a session takes it on with sp_setapprole) and no Effective Permissions
// page (a role cannot be impersonated, Msg 15517).
//
// Every page is gated on ALTER ANY APPLICATION ROLE or what the shared page
// already asks; gate.AlterAnyApplicationRole records the probe.
//
// roleName is boxed and shared by every page, as on Database Role Properties:
// the rename is General's last write (propPage.renames) and commitRename moves
// the box, so the reload re-reads under the new name.
func applicationRolePropPages(d *PropDialog, sc *db.ServerConn, dbName, roleName string) []propPage {
	namePtr := &roleName
	return []propPage{
		withRequires(pageApplicationRoleGeneral(sc, dbName, namePtr), dbName, gate.AlterAnyApplicationRole),
		withRequires(pagePrincipalOwnedSchemas(sc, dbName, namePtr, "application role"), dbName, gate.AlterAnySchema, gate.ControlDB),
		withRequires(pageDatabasePrincipalSecurables(d, sc, dbName, namePtr), dbName, gate.ControlDB),
		// An application role is classed as USER in sp_addextendedproperty's level
		// names, as a database role is: a database principal, not a level of its own.
		withRequires(pageExtendedProperties(sc, dbName, func() gosmo.ExtendedPropertyLevel {
			return gosmo.ExtendedPropertyLevel{Level0Type: "USER", Level0Name: *namePtr}
		}), dbName, gate.AlterAnyApplicationRole),
	}
}

func pageApplicationRoleGeneral(sc *db.ServerConn, dbName string, roleName *string) propPage {
	return propPage{
		title:   "General",
		renames: roleName,
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByName(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			r, err := d.ApplicationRoleByName(ctx, *roleName)
			if err != nil {
				return nil, nil, err
			}
			schemas, err := d.Schemas(ctx)
			if err != nil {
				return nil, nil, err
			}
			securables, err := d.PermissionsForPrincipal(ctx, *roleName)
			if err != nil {
				return nil, nil, err
			}

			schemaNames := make([]string, len(schemas))
			ownedSchemas := 0
			for i, s := range schemas {
				schemaNames[i] = s.Name
				if s.Owner == *roleName {
					ownedSchemas++
				}
			}
			distinctSecurables := make(map[string]bool)
			for _, e := range securables {
				distinctSecurables[securable{e.SecurableType, e.Schema, e.Name}.key()] = true
			}

			nameRow := propsheet.Text("Role name", r.Name, 24)
			schemaRow := selectPreserving("Default schema", schemaNames, r.DefaultSchema, unsetItem)
			passwordRow := propsheet.Password("Password", 20)
			confirmRow := propsheet.Password("Confirm password", 20)
			// On passwordRow, not confirmRow, for Login Properties' reason: Form.Validate
			// runs only a dirty row's validator, and passwordRow is dirty exactly when
			// something is typed in it.
			passwordRow.SetValidate(func(v string) error {
				if v != confirmRow.Value() {
					return fmt.Errorf("passwords do not match")
				}
				return nil
			})

			f := propsheet.NewForm(
				propsheet.Section("Application role"),
				nameRow,
				schemaRow,
				propsheet.Section("Password"),
				passwordRow, confirmRow,
				propsheet.Note("Leave both password fields blank to keep the current password. "+
					"The password is what sp_setapprole must present to take the role on."),
				propsheet.Section("Identity"),
				propsheet.Static("Principal ID", strconv.Itoa(r.PrincipalID)),
				propsheet.Static("Created", formatSQLDate(r.CreateDate)),
				propsheet.Static("Modified", formatSQLDate(r.ModifyDate)),
				propsheet.Section("Summary"),
				propsheet.Static("Owned schemas", strconv.Itoa(ownedSchemas)),
				propsheet.Static("Explicit securables", strconv.Itoa(len(distinctSecurables))),
			)

			apply := func(ctx context.Context) error {
				// Every write addresses the role by name, so the handle is enough.
				role := sc.Server.DatabaseRef(dbName).ApplicationRoleRef(*roleName)
				if schema, ok := changedTo(schemaRow, unsetItem); ok {
					if err := role.SetDefaultSchema(ctx, schema); err != nil {
						return err
					}
				}
				if pw := passwordRow.Value(); pw != "" {
					if err := role.ChangePassword(ctx, pw); err != nil {
						return err
					}
				}
				// Last, and only then committed to the box: every other page is addressed by
				// this name.
				if nameRow.Dirty() {
					if err := role.Rename(ctx, nameRow.Value()); err != nil {
						return err
					}
					commitRename(ctx, roleName, nameRow.Value())
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

// showApplicationRolePropertiesFor opens Application Role Properties. It can
// rename the role, so Apply reloads the folder listing it.
func (a *App) showApplicationRolePropertiesFor(sc *db.ServerConn, dbName, roleName string) {
	a.propDialog.showReloading(sc, dbName, "Application Role Properties", "Role: "+roleName, "Database: "+dbName,
		func() []propPage { return applicationRolePropPages(a.propDialog, sc, dbName, roleName) },
		folderOf(dbName, NodeApplicationRoles))
}
