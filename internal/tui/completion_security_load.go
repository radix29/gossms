package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/sqltext"
	"github.com/radix29/tuikit/controls/editor"
)

type securityName struct {
	class, schema, name string
	objectClass         gosmo.SecurableClass
}

type securityInventory struct {
	loading                   bool
	err                       error
	serverKey, database, kind string
	names                     []securityName
	permissions               []gosmo.BuiltinPermission
	load                      latest
}

func securityInventoryKey(sc *db.ServerConn, database, kind string) string {
	return completionInventoryKey(sc.Opts, database) + "\x00" + kind
}

func (p *QueryPanel) securityCollation(server bool) string {
	if !server {
		if inv := p.app.completion.inventories[completionInventoryKey(p.conn.Opts, p.database)]; inv != nil {
			return inv.collation
		}
	}
	return serverCollation(p.conn)
}

func (a *App) ensureSecurityInventory(sc *db.ServerConn, database, kind string) *securityInventory {
	key := securityInventoryKey(sc, database, kind)
	if inv := a.completion.security[key]; inv != nil {
		return inv
	}
	inv := &securityInventory{loading: true, serverKey: sysCompletionInventoryKey(sc.Opts), database: database, kind: kind}
	if a.completion.security == nil {
		a.completion.security = make(map[string]*securityInventory)
	}
	a.completion.security[key] = inv
	a.loadSecurityInventory(sc, key, inv)
	return inv
}

func (a *App) loadSecurityInventory(sc *db.ServerConn, key string, inv *securityInventory) {
	type result struct {
		names       []securityName
		permissions []gosmo.BuiltinPermission
	}
	startCompletionLoad(a, sc, completionLoad[result]{
		what: "loading security autocomplete metadata", timeout: completionInventoryTimeout, load: &inv.load,
		owned: func() bool { return a.completion.security[key] == inv },
		evict: func() { delete(a.completion.security, key) },
		fetch: func(ctx context.Context) (r result, err error) {
			if inv.kind == "permissions" {
				r.permissions, err = sc.Server.BuiltinPermissions(ctx)
			} else {
				r.names, err = loadSecurityNames(ctx, sc, inv.database, inv.kind)
			}
			return r, err
		},
		apply: func(r result, err error) {
			inv.loading, inv.err = false, err
			if err == nil {
				inv.names, inv.permissions = r.names, r.permissions
			} else if len(r.names) != 0 {
				collation := serverCollation(sc)
				if catalog := a.completion.inventories[completionInventoryKey(sc.Opts, inv.database)]; catalog != nil {
					collation = catalog.collation
				}
				seen := newNameSet(collation)
				names := make([]securityName, 0, len(r.names)+len(inv.names))
				for _, name := range append(slices.Clone(r.names), inv.names...) {
					key := name.class + "\x00" + name.schema + "\x00" + name.name
					if !seen.Has(key) {
						seen.Add(key)
						names = append(names, name)
					}
				}
				inv.names = names
			}
			if err != nil {
				a.setStatus(fmt.Sprintf("Security autocomplete metadata unavailable: %v (Ctrl+R retries)", err))
			}
		},
	})
}

func (a *App) purgeSecurityCompletion(serverKey string) {
	for key, inv := range a.completion.security {
		if inv.serverKey == serverKey {
			inv.load.Abandon()
			delete(a.completion.security, key)
		}
	}
}

func (a *App) refreshSecurityCompletion(sc *db.ServerConn) {
	for key, inv := range a.completion.security {
		if inv.serverKey == sysCompletionInventoryKey(sc.Opts) {
			inv.loading = true
			a.loadSecurityInventory(sc, key, inv)
		}
	}
}

func (p *QueryPanel) securityPermissionDefinitions() []gosmo.BuiltinPermission {
	if !p.conn.IsFabricDW() && p.conn.Server != nil {
		inv := p.app.ensureSecurityInventory(p.conn, "", "permissions")
		if len(inv.permissions) > 0 {
			return inv.permissions
		}
	}
	var defs []gosmo.BuiltinPermission
	add := func(class string, names []string) {
		for _, name := range names {
			defs = append(defs, gosmo.BuiltinPermission{Class: class, Name: name})
		}
	}
	if p.conn.IsFabricDW() {
		common := []string{"CONTROL", "ALTER", "VIEW DEFINITION"}
		for _, class := range []string{"DATABASE", "OBJECT", "ROLE", "SCHEMA", "USER"} {
			add(class, common)
			if class != "USER" {
				add(class, []string{"TAKE OWNERSHIP"})
			}
		}
		for _, class := range []string{"DATABASE", "SCHEMA", "OBJECT"} {
			add(class, []string{"DELETE", "EXECUTE", "INSERT", "SELECT", "UPDATE", "REFERENCES"})
		}
		add("DATABASE", []string{"ALTER ANY DATASPACE", "ALTER ANY ROLE", "ALTER ANY SCHEMA", "ALTER ANY USER",
			"BACKUP DATABASE", "CONNECT", "CREATE PROCEDURE", "CREATE ROLE", "CREATE SCHEMA", "CREATE TABLE", "CREATE VIEW", "SHOWPLAN"})
		add("USER", []string{"IMPERSONATE"})
		return defs
	}
	add("SERVER", gosmo.ServerPermissionNames())
	add("DATABASE", gosmo.DatabasePermissionNames())
	add("SCHEMA", gosmo.SchemaPermissionNames())
	for _, class := range securityObjectKinds {
		add("OBJECT", class.PermissionNames())
	}
	add("OBJECT", []string{"RECEIVE"})
	for _, class := range securityClasses {
		if slices.Contains([]string{"SERVER", "DATABASE", "SCHEMA", "OBJECT"}, class) {
			continue
		}
		add(class, []string{"CONTROL", "VIEW DEFINITION"})
		if class != "USER" && class != "LOGIN" {
			add(class, []string{"TAKE OWNERSHIP"})
		}
		if class != "CERTIFICATE" && class != "ASYMMETRIC KEY" && class != "TYPE" {
			add(class, []string{"ALTER"})
		}
		switch class {
		case "USER", "LOGIN":
			add(class, []string{"IMPERSONATE"})
		case "TYPE", "XML SCHEMA COLLECTION":
			add(class, []string{"EXECUTE", "REFERENCES"})
		case "ASSEMBLY", "ASYMMETRIC KEY", "CERTIFICATE", "SYMMETRIC KEY", "DATABASE SCOPED CREDENTIAL",
			"FULLTEXT CATALOG", "FULLTEXT STOPLIST", "SEARCH PROPERTY LIST", "CONTRACT", "MESSAGE TYPE":
			add(class, []string{"REFERENCES"})
		case "ENDPOINT":
			add(class, []string{"CONNECT"})
		case "SERVICE":
			add(class, []string{"SEND"})
		}
	}
	if p.conn.Server == nil || p.conn.Server.Info().AtLeast(gosmo.SQLServer2022) {
		add("OBJECT", []string{"UNMASK"})
	}
	return defs
}

func (p *QueryPanel) securityServerPermissions(names []string) bool {
	defs := p.securityPermissionDefinitions()
	for _, name := range names {
		server, database := false, false
		for _, def := range defs {
			if def.Name == name {
				server = server || def.Class == "SERVER"
				database = database || def.Class == "DATABASE"
			}
		}
		if server && !database {
			return true
		}
	}
	return false
}

func (p *QueryPanel) securityNames(class string) ([]securityName, bool) {
	return p.securityNamesInDatabase(p.database, class)
}

func (p *QueryPanel) securityNamesInDatabase(database, class string) ([]securityName, bool) {
	if class == "" {
		class = "OBJECT"
	}
	if class == "DATABASE" {
		if database == "" {
			return nil, false
		}
		return []securityName{{class: class, name: database}}, false
	}
	if class == "SCHEMA" {
		inv := p.app.ensureCompletionInventory(p.conn, database)
		var names []securityName
		if inv.catalog != nil {
			for _, schema := range inv.catalog.Schemas {
				names = append(names, securityName{class: class, name: schema})
			}
		}
		return names, inv.loading
	}
	if !slices.Contains(securityClasses, class) &&
		!slices.Contains([]string{"database-principals", "server-principals", "server-logins"}, class) {
		return nil, false
	}
	if slices.Contains([]string{"LOGIN", "SERVER ROLE", "ENDPOINT", "AVAILABILITY GROUP", "server-principals", "server-logins"}, class) {
		database = ""
	}
	inv := p.app.ensureSecurityInventory(p.conn, database, class)
	names := inv.names
	if class == "OBJECT" && (inv.loading || inv.err != nil) {
		catalog := p.app.ensureCompletionInventory(p.conn, database)
		if catalog.catalog != nil {
			names = slices.Clone(names)
			for _, obj := range catalog.catalog.Objects {
				kind := gosmo.SecurableTable
				if obj.Type == gosmo.CatalogView {
					kind = gosmo.SecurableView
				}
				names = append(names, securityName{class: class, schema: obj.Schema, name: obj.Name, objectClass: kind})
			}
			for _, proc := range catalog.procedures {
				names = append(names, securityName{class: class, schema: proc.Schema, name: proc.Name, objectClass: gosmo.SecurableProcedure})
			}
			for _, fn := range catalog.catalog.Functions {
				names = append(names, securityName{class: class, schema: fn.Schema, name: fn.Name, objectClass: securityTVFClass})
			}
		}
	}
	return names, inv.loading
}

func (p *QueryPanel) securityNameCandidates(pos *completionPosition, names []securityName, parts []string, excluded []sqltext.Token, loading bool, collation string) []editor.CompletionItem {
	seen := newNameSet(collation)
	for _, tok := range excluded {
		if tok.Kind == sqltext.KindWord || tok.Kind == sqltext.KindQuotedIdent {
			seen.Add(gosmo.UnquoteName(string(pos.stmt[tok.Start:tok.End])))
		}
	}
	var items []editor.CompletionItem
	for _, name := range names {
		text := gosmo.QuoteNameIfNeeded(name.name)
		label := name.name
		if name.schema != "" {
			label = name.schema + "." + name.name
			if len(parts) == 0 {
				text = gosmo.QuoteNameIfNeeded(name.schema) + "." + text
			} else if len(parts) != 1 || !gosmo.SameName(collation, parts[0], name.schema) {
				continue
			} else {
				label = name.name
			}
		} else if len(parts) != 0 {
			continue
		}
		if seen.Has(label) {
			continue
		}
		match := name.name
		if name.schema != "" && len(parts) == 0 && strings.Contains(pos.prefix, ".") {
			match = label
		}
		if ok, partial := nameMatch(match, strings.ToLower(pos.prefix)); ok {
			seen.Add(label)
			items = append(items, editor.CompletionItem{Text: text, Label: label, Detail: strings.ToLower(name.class),
				Partial: partial, AppendSpace: true})
		}
	}
	sortCompletionItems(items)
	if loading {
		items = append(items, loadingCompletionItem)
	}
	return items
}

func readSecurityNames[T any](ctx context.Context, class string, read func(context.Context) ([]*T, error), name func(*T) string) ([]securityName, error) {
	rows, err := read(ctx)
	var names []securityName
	for _, row := range rows {
		names = append(names, securityName{class: class, name: name(row)})
	}
	return names, err
}

func loadSecurityNames(ctx context.Context, sc *db.ServerConn, database, class string) ([]securityName, error) {
	s := sc.Server
	d := s.DatabaseRef(database)
	switch class {
	case "database-principals", "USER", "ROLE", "APPLICATION ROLE":
		principals, err := d.Principals(ctx)
		if err != nil {
			return nil, err
		}
		var names []securityName
		if class == "USER" || class == "database-principals" {
			for _, user := range principals.Users {
				names = append(names, securityName{class: "USER", name: user.Name})
			}
		}
		if class == "ROLE" || class == "database-principals" {
			for _, role := range principals.Roles {
				names = append(names, securityName{class: "ROLE", name: role.Name})
			}
		}
		if class == "APPLICATION ROLE" || class == "database-principals" {
			for _, role := range principals.ApplicationRoles {
				names = append(names, securityName{class: "APPLICATION ROLE", name: role.Name})
			}
		}
		return names, nil
	case "LOGIN", "server-logins", "server-principals":
		names, err := readSecurityNames(ctx, "LOGIN", s.Logins, func(x *gosmo.Login) string { return x.Name })
		if err != nil || class != "server-principals" {
			return names, err
		}
		roles, err := readSecurityNames(ctx, "SERVER ROLE", s.ServerRoles, func(x *gosmo.ServerRole) string { return x.Name })
		return append(names, roles...), err
	case "SERVER ROLE":
		return readSecurityNames(ctx, class, s.ServerRoles, func(x *gosmo.ServerRole) string { return x.Name })
	case "ENDPOINT":
		return readSecurityNames(ctx, class, s.Endpoints, func(x *gosmo.Endpoint) string { return x.Name })
	case "AVAILABILITY GROUP":
		return readSecurityNames(ctx, class, s.AvailabilityGroups, func(x *gosmo.AvailabilityGroup) string { return x.Name })
	case "OBJECT", "TYPE", "CERTIFICATE":
		rows, err := d.FindSecurables(ctx, gosmo.SecurableSearch{})
		var names []securityName
		for _, x := range rows {
			kind := "OBJECT"
			switch x.Class {
			case gosmo.SecurableSchema:
				continue
			case gosmo.SecurableUserType:
				kind = "TYPE"
			case gosmo.SecurableCertificate:
				kind = "CERTIFICATE"
			}
			if class == kind {
				names = append(names, securityName{class: kind, schema: x.Schema, name: x.Name, objectClass: x.Class})
			}
		}
		if err != nil || class != "OBJECT" || sc.IsFabricDW() || s.Info().IsDatabaseScoped() {
			return names, err
		}
		queues, err := d.ServiceBroker().Queues(ctx)
		for _, x := range queues {
			names = append(names, securityName{class: class, schema: x.Schema, name: x.Name, objectClass: securityQueueClass})
		}
		return names, err
	case "ASSEMBLY":
		return readSecurityNames(ctx, class, d.Assemblies, func(x *gosmo.Assembly) string { return x.Name })
	case "ASYMMETRIC KEY":
		return readSecurityNames(ctx, class, d.AsymmetricKeys, func(x *gosmo.AsymmetricKey) string { return x.Name })
	case "SYMMETRIC KEY":
		return readSecurityNames(ctx, class, d.SymmetricKeys, func(x *gosmo.SymmetricKey) string { return x.Name })
	case "DATABASE SCOPED CREDENTIAL":
		return readSecurityNames(ctx, class, d.DatabaseScopedCredentials, func(x *gosmo.DatabaseScopedCredential) string { return x.Name })
	case "XML SCHEMA COLLECTION":
		rows, err := d.XMLSchemaCollections(ctx)
		var names []securityName
		for _, x := range rows {
			names = append(names, securityName{class: class, schema: x.Schema, name: x.Name})
		}
		return names, err
	case "FULLTEXT CATALOG":
		return readSecurityNames(ctx, class, d.FullText().Catalogs, func(x *gosmo.FullTextCatalog) string { return x.Name })
	case "FULLTEXT STOPLIST":
		return readSecurityNames(ctx, class, d.FullText().Stoplists, func(x *gosmo.FullTextStoplist) string { return x.Name })
	case "SEARCH PROPERTY LIST":
		return readSecurityNames(ctx, class, d.FullText().SearchPropertyLists, func(x *gosmo.SearchPropertyList) string { return x.Name })
	case "CONTRACT":
		return readSecurityNames(ctx, class, d.ServiceBroker().Contracts, func(x *gosmo.ServiceContract) string { return x.Name })
	case "MESSAGE TYPE":
		return readSecurityNames(ctx, class, d.ServiceBroker().MessageTypes, func(x *gosmo.MessageType) string { return x.Name })
	case "REMOTE SERVICE BINDING":
		return readSecurityNames(ctx, class, d.ServiceBroker().RemoteServiceBindings, func(x *gosmo.RemoteServiceBinding) string { return x.Name })
	case "ROUTE":
		return readSecurityNames(ctx, class, d.ServiceBroker().Routes, func(x *gosmo.Route) string { return x.Name })
	case "SERVICE":
		return readSecurityNames(ctx, class, d.ServiceBroker().Services, func(x *gosmo.BrokerService) string { return x.Name })
	}
	return nil, nil
}
