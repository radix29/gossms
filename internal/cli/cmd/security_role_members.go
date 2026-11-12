package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// security role add-member / remove-member: the command and its handler,
// listed with the other security writes in securityWriteCommands
// (security_permissions.go) and following their conventions (admin, no
// --confirm, --dry-run the safeguard). --scope is security.go's.

func roleMemberCommand(d Deps, add bool) *spec.Command {
	verb, stmt, word := "add-member", "ADD MEMBER", "Add"
	if !add {
		verb, stmt, word = "remove-member", "DROP MEMBER", "Remove"
	}
	return &spec.Command{
		Path:    []string{"security", "role", verb},
		Summary: word + " a member of a database role, or of a server role",
		Description: "ALTER ROLE … " + stmt + " (with --scope server, ALTER SERVER ROLE … " + stmt + ") through gosmo: " +
			"<role> a role of the connection's database, <member> a user or another role of it; with --scope server " +
			"a server role and a login or another server role. Both are read first: either missing is exit 8, the " +
			"hint naming the other scope when it has the name (or, for a login named in database scope, its user " +
			"here). Record: role, scope, member, memberType, wasMember — a member already in (or, removing, not in) " +
			"the role is a warning, the statement then changing nothing — and on a real run isMember, read back. " +
			"Needs --allow admin, and ALTER on the role, ALTER ANY ROLE or db_securityadmin (server: ALTER ANY SERVER " +
			"ROLE or securityadmin; a fixed server role takes members only from one of its own members or a " +
			"sysadmin); without: exit 9.",
		Args: []spec.Arg{
			{Name: "role", Help: "Role name", Required: true},
			{Name: "member", Help: "User or role; with --scope server, login or server role", Required: true},
		},
		Options: slices.Concat(ConnectOptions(), writeOptions(),
			[]spec.Option{scopeOption("database: a role of the connection database; server: a server role")}),
		Class: spec.ClassAdmin,
		Run: func(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
			return d.securityRoleMember(ctx, inv, add)
		},
		Examples: []string{
			"gossms security role " + verb + " db_datareader app_reader -p dev --allow admin --dry-run",
			"gossms security role " + verb + " sysadmin etl_login --scope server -p dev --allow admin",
		},
	}
}

// --- role add-member / remove-member ------------------------------------------

func (d Deps) securityRoleMember(ctx context.Context, inv *spec.Invocation, add bool) (*output.Result, error) {
	scope := inv.Values[optScope].(string)
	roleName, memberName := inv.Args[0], inv.Args[1]
	res := &output.Result{}
	sc, err := d.connectGated(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	var (
		dbase   *gosmo.Database
		role    string
		members []string
		// alter adds or removes a member through the role just read.
		alter func(ctx context.Context, member string) error
	)
	if scope == scopeServer {
		r, err := sc.Server.ServerRoleByName(ctx, roleName)
		if errors.Is(err, gosmo.ErrNotFound) {
			return res, roleNotFound(ctx, sc.Server, res, roleName, scope)
		}
		if err != nil {
			return res, readError(err)
		}
		role, members, alter = r.Name, r.Members, r.RemoveMember
		if add {
			alter = r.AddMember
		}
	} else {
		if dbase, err = currentDatabase(ctx, sc.Server, res); err != nil {
			return res, err
		}
		r, err := dbase.RoleByName(ctx, roleName)
		if errors.Is(err, gosmo.ErrNotFound) {
			return res, roleNotFound(ctx, sc.Server, res, roleName, scope)
		}
		if err != nil {
			return res, readError(err)
		}
		role, members, alter = r.Name, r.Members, r.RemoveMember
		if add {
			alter = r.AddMember
		}
	}
	p, err := resolvePrincipal(ctx, sc.Server, res, dbase, memberName, roleScope)
	if err != nil {
		return res, err
	}

	was := slices.Contains(members, p.name)
	res.Fields = []output.Field{
		{Key: "role", Value: role}, {Key: "scope", Value: scope},
		{Key: "member", Value: p.name}, {Key: "memberType", Value: string(p.typ)},
		{Key: "wasMember", Value: boolText(was)},
	}
	switch {
	case add && was:
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s is already a member of %s: the statement changes nothing.",
			gosmo.QuoteName(p.name), gosmo.QuoteName(role)))
	case !add && !was:
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s is not a direct member of %s: the statement changes nothing.",
			gosmo.QuoteName(p.name), gosmo.QuoteName(role)))
	}
	write := func(ctx context.Context) error {
		err := alter(ctx, p.name)
		if gosmo.IsMissingOrDenied(err) || gosmo.IsPermissionDenied(err) {
			hint := "Changing a database role's members needs ALTER on the role, ALTER ANY ROLE, or db_securityadmin / db_owner. " +
				"Use a login that has it, or have it granted."
			if dbase == nil {
				hint = "Changing a server role's members needs ALTER ANY SERVER ROLE or securityadmin; a fixed server role " +
					"takes members only from one of its own members or a sysadmin. Use a login that has it, or have it granted."
			}
			e := output.NewError(output.CodePermissionDenied, err.Error(), hint)
			if se, ok := gosmo.AsSQLError(err); ok {
				e.SQL = sqlError(se).SQL
			}
			return e
		}
		return err
	}
	if err := applyWrite(ctx, inv, res, write); err != nil {
		return res, err
	}
	if dryRun(inv) {
		return res, nil
	}
	var now []*gosmo.RoleMember
	if dbase == nil {
		now, err = sc.Server.ServerRoleMembers(ctx, role)
	} else {
		now, err = dbase.RoleMembers(ctx, role)
	}
	if err != nil {
		res.Warnings = append(res.Warnings, "The role's members were not read back: "+readError(err).Message)
		res.Fields = append(res.Fields, output.Field{Key: "isMember", Null: true})
		return res, nil
	}
	is := slices.ContainsFunc(now, func(m *gosmo.RoleMember) bool { return m.Name == p.name })
	res.Fields = append(res.Fields, output.Field{Key: "isMember", Value: boolText(is)})
	switch {
	case add && !is:
		res.Warnings = append(res.Warnings, fmt.Sprintf("Read back, %s is not a member of %s.", gosmo.QuoteName(p.name), gosmo.QuoteName(role)))
	case !add && is:
		res.Warnings = append(res.Warnings, fmt.Sprintf("Read back, %s is still a member of %s.", gosmo.QuoteName(p.name), gosmo.QuoteName(role)))
	}
	return res, nil
}
