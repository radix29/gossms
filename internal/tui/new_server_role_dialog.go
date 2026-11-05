package tui

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_server_role_dialog.go is the New Server Role dialog (Object Explorer's
// Security > Server Roles folder), built on newObjectDialog. Three pages,
// applied in order: General (the name and the AUTHORIZATION owner) creates the
// role, then Members and Securables target it by the typed name — Server Role
// Properties' pages, less General's read-only detail and Owned Roles, which
// only describe an existing role.

// saSID is the sa login's SID, which it keeps when renamed — the server refuses
// it as a server role member by identity, not by name.
var saSID = []byte{0x01}

// serverRoleMemberEligible reports whether ALTER SERVER ROLE ... ADD MEMBER
// accepts a login: everything but sa (Msg 15405 "Cannot use the special
// principal", major 17, 2026-11-05). Windows logins and groups, certificate-
// mapped and ## logins are all accepted.
func serverRoleMemberEligible(l *gosmo.Login) bool {
	return !bytes.Equal(l.SID, saSID)
}

// serverRoleMemberRoleEligible is serverRoleMemberEligible for a server role:
// only user-defined ones. public and every fixed role, the ##MS_*## ones
// included, are refused Msg 15405 (same probe).
func serverRoleMemberRoleEligible(r *gosmo.ServerRole) bool {
	return !isSystemServerRole(r)
}

// nsroleDefaultOwner is the Owner picker's "leave AUTHORIZATION off" entry; the
// server then makes the creating login the owner.
const nsroleDefaultOwner = "(Default)"

// nsrolePrefetch is everything the dialog reads before it opens.
type nsrolePrefetch struct {
	// existingNames holds every server principal's name — logins and server
	// roles share one namespace (Msg 15025).
	existingNames *nameSet
	// owners are the principals AUTHORIZATION may name: every login and server
	// role but public (Msg 15405). A fixed role, sysadmin or ##MS_*##, is
	// accepted (major 17, 2026-11-05).
	owners []string
	// members are the principals ADD MEMBER accepts; see
	// serverRoleMemberEligible.
	members       []string
	principalType map[string]gosmo.PrincipalType
	collation     string
}

func fetchNewServerRolePrefetch(ctx context.Context, sc *db.ServerConn) (*nsrolePrefetch, error) {
	logins, err := sc.Server.Logins(ctx)
	if err != nil {
		return nil, err
	}
	roles, err := sc.Server.ServerRoles(ctx)
	if err != nil {
		return nil, err
	}
	return newServerRolePrefetchFrom(serverCollation(sc), logins, roles), nil
}

// newServerRolePrefetchFrom sorts the server's principals into the pickers.
func newServerRolePrefetchFrom(collation string, logins []*gosmo.Login, roles []*gosmo.ServerRole) *nsrolePrefetch {
	pf := &nsrolePrefetch{existingNames: newNameSet(collation), collation: collation,
		principalType: map[string]gosmo.PrincipalType{}}
	for _, n := range serverPrincipalNames(logins, roles) {
		pf.existingNames.Add(n)
		if n != publicRoleName {
			pf.owners = append(pf.owners, n)
		}
	}
	for _, l := range logins {
		if serverRoleMemberEligible(l) {
			pf.members = append(pf.members, l.Name)
			pf.principalType[l.Name] = l.LoginType
		}
	}
	for _, r := range roles {
		if serverRoleMemberRoleEligible(r) {
			pf.members = append(pf.members, r.Name)
			pf.principalType[r.Name] = gosmo.PrincipalServerRole
		}
	}
	return pf
}

// NewServerRoleDialog is the New Server Role dialog.
type NewServerRoleDialog struct {
	newObjectDialog[nsrolePrefetch]
}

// NewNewServerRoleDialog creates the dialog and wires its callbacks.
func NewNewServerRoleDialog(app *App) *NewServerRoleDialog {
	d := &NewServerRoleDialog{}
	d.init(app, newObjectConfig[nsrolePrefetch]{
		title:   "New Server Role",
		noun:    "Server role",
		pages:   []string{"General", "Members", "Securables"},
		fetch:   fetchNewServerRolePrefetch,
		build:   d.buildPages,
		refresh: func(sc *db.ServerConn) { d.app.explorer.ReloadFolders(sc, folderOf("", NodeServerRoles)) },
	})
	return d
}

func (d *NewServerRoleDialog) buildPages(pf *nsrolePrefetch) {
	sc := d.sc

	nameField := propsheet.Text("Role name", "", 30)
	ownerField := propsheet.Select("Owner", append([]string{nsroleDefaultOwner}, pf.owners...), 0)
	// Principal names run long (##MS_PerformanceDefinitionReader##, DOMAIN\group):
	// the control widens to the widest rather than cutting them alike.
	ownerField.SetFitItems(true)

	d.forms[0] = propsheet.NewForm(
		propsheet.Section("Server role"),
		nameField,
		ownerField,
		propsheet.Note("Left at "+nsroleDefaultOwner+", the role is owned by the login creating it. "+
			"Naming another principal as owner also takes IMPERSONATE on that login, "+
			"or CONTROL on that role."),
	)

	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("role name is required")
		}
		if pf.existingNames.Has(name) {
			return fmt.Errorf("a login or server role named %q already exists", name)
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		req := gosmo.CreateServerRoleRequest{Name: d.objectName()}
		if o := ownerField.Value(); o != nsroleDefaultOwner {
			req.Owner = o
		}
		_, err := sc.Server.CreateServerRole(ctx, req)
		return err
	}

	// The role is addressed by name when apply runs: the name is only known once
	// typed. ServerRoleRef keeps it scriptable, as the CREATE is.
	d.forms[1], d.applyFns[1] = buildMembershipForm(membershipConfig{
		collation:     pf.collation,
		candidates:    pf.members,
		principalType: pf.principalType,
		note: "Add a login or a user-defined server role from the dropdown. " +
			"sa and the fixed roles cannot be members of a role.",
		add: func(ctx context.Context, name string) error {
			return sc.Server.ServerRoleRef(d.objectName()).AddMember(ctx, name)
		},
		remove: func(ctx context.Context, name string) error {
			// Nothing is a member before the CREATE, so only a pending add can be removed,
			// and that never reaches apply.
			return nil
		},
	})

	d.forms[2], d.applyFns[2] = buildNewServerPrincipalSecurablesPage(sc, d.objectName)
}

// showNewServerRoleDialog is the Server Roles folder's entry point.
func (a *App) showNewServerRoleDialog(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	a.newServerRoleDialog.show(sc)
}
