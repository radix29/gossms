package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

func pageServerPermissions(sc *db.ServerConn) propPage {
	return propPage{
		title: "Permissions",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			perms, err := sc.Server.ServerPermissionsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			logins, err := sc.Server.LoginsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			roles, err := sc.Server.ServerRolesContext(ctx)
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
			principals := make([]permPrincipal, 0, len(logins)+len(roles))
			for _, l := range logins {
				principals = append(principals, permPrincipal{Name: l.Name, Type: l.LoginType})
			}
			for _, r := range roles {
				principals = append(principals, permPrincipal{Name: r.Name, Type: "SERVER_ROLE"})
			}

			f, apply := buildPermissionsMatrix(principals, gosmo.ServerPermissionNames(), entries, 8, 12,
				serverPermApply(sc.Server))
			// sys.server_permissions and sys.server_principals are both
			// metadata-visibility filtered, so without VIEW ANY DEFINITION a
			// grant that exists simply is not in the grid — and the cell for it
			// reads "(none)", which is a positive claim that it is not granted.
			// There is no cell to blank, so the caveat goes on the page.
			f.Prepend(visibilityNote(sc.Capabilities(), "VIEW ANY DEFINITION",
				"logins, server roles and permissions")...)
			return f, apply, nil
		},
	}
}
