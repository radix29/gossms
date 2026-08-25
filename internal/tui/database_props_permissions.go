package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

func pageDatabasePermissions(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Permissions",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			perms, err := d.DatabasePermissionsContext(ctx)
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
					Grantor: p.Grantor, Permission: p.Permission, State: p.State,
				}
			}

			f, apply := buildPermissionsMatrix(databasePermPrincipals(users, roles), gosmo.DatabasePermissionNames(), entries, 8, 12,
				databasePermApply(d))
			// Same visibility filtering as the server-scope page, one scope
			// down — see server_props_permissions.go.
			f.Prepend(visibilityNote(sc.DatabaseCapabilities(ctx, dbName), "VIEW DEFINITION",
				"users, database roles and permissions")...)
			return f, apply, nil
		},
	}
}

func pageDatabaseExtendedProperties(sc *db.ServerConn, dbName string) propPage {
	return propPage{
		title: "Extended Properties",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			props, err := d.DatabaseExtendedPropertiesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			f, apply := buildExtendedPropertiesForm(sc, dbName, gosmo.ExtendedPropertyLevel{}, props)
			return f, apply, nil
		},
	}
}
