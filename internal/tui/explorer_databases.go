package tui

import gosmo "github.com/radix29/gosmo"

// loadServerChildren returns a connected server's top-level folders:
// Databases, Security, and Server Objects (Agent jobs, linked servers).
func loadServerChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Databases", NodeDatabases, "", "", ""),
		l.node("Security", NodeSecurity, "", "", ""),
		l.node("Server Objects", NodeManagement, "", "", ""),
	}, nil
}

// loadDatabasesChildren lists user databases, with a "System Databases"
// folder listed first if the server has any — matching SSMS.
func loadDatabasesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbs, err := l.sc.Server.DatabasesContext(l.ctx)
	if err != nil {
		return nil, err
	}
	var userDBs []*explorerNode
	hasSystem := false
	for _, d := range dbs {
		if d.IsSystem() {
			hasSystem = true
			continue
		}
		n := l.node(d.Name(), NodeDatabase, "", d.Name(), d.Name())
		n.data.IsOffline = d.State() != "ONLINE"
		userDBs = append(userDBs, n)
	}
	if !hasSystem {
		return userDBs, nil
	}
	return append([]*explorerNode{l.node("System Databases", NodeSystemDatabases, "", "", "")}, userDBs...), nil
}

// loadSystemDatabasesChildren lists master/tempdb/model/msdb.
func loadSystemDatabasesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbs, err := l.sc.Server.DatabasesContext(l.ctx)
	if err != nil {
		return nil, err
	}
	var out []*explorerNode
	for _, d := range dbs {
		if d.IsSystem() {
			n := l.node(d.Name(), NodeDatabase, "", d.Name(), d.Name())
			n.data.IsOffline = d.State() != "ONLINE"
			out = append(out, n)
		}
	}
	return out, nil
}

// loadDatabaseChildren returns one database's object-family folders, or a
// single explanatory leaf if the database is offline. SQL Server can't run
// any metadata query against an offline database (USE fails outright), so
// showing the normal folder list would just let the user expand each of
// Tables/Views/.../Security in turn only to hit the same "cannot open
// database" error eight separate times — this short-circuits straight to
// one clear leaf instead.
func loadDatabaseChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	if node.data.IsOffline {
		return []*explorerNode{l.node("(Database is offline)", NodeError, "", "", node.data.DBName)}, nil
	}
	dbName := node.data.DBName
	return []*explorerNode{
		l.node("Tables", NodeTables, "", "", dbName),
		l.node("Views", NodeViews, "", "", dbName),
		l.node("Stored Procedures", NodeStoredProcedures, "", "", dbName),
		l.node("Functions", NodeFunctions, "", "", dbName),
		l.node("Triggers", NodeTriggers, "", "", dbName),
		l.node("Sequences", NodeSequences, "", "", dbName),
		l.node("Synonyms", NodeSynonyms, "", "", dbName),
		l.node("Security", NodeDatabaseSecurity, "", "", dbName),
	}, nil
}

// loadDatabaseSecurityChildren returns a database's Users/Roles/Schemas folders.
func loadDatabaseSecurityChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Users", NodeUsers, "", "", node.data.DBName),
		l.node("Roles", NodeDatabaseRoles, "", "", node.data.DBName),
		l.node("Schemas", NodeSchemas, "", "", node.data.DBName),
	}, nil
}

func loadUsersChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.User, error) { return dbObj.UsersContext(l.ctx) },
		func(u *gosmo.User) *explorerNode {
			return l.node(u.Name, NodeUser, "", u.Name, node.data.DBName)
		})
}

func loadDatabaseRolesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.DatabaseRole, error) { return dbObj.DatabaseRolesContext(l.ctx) },
		func(r *gosmo.DatabaseRole) *explorerNode {
			return l.node(r.Name, NodeDatabaseRole, "", r.Name, node.data.DBName)
		})
}

func loadSchemasChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.Schema, error) { return dbObj.SchemasContext(l.ctx) },
		func(s *gosmo.Schema) *explorerNode {
			return l.node(s.Name, NodeSchema, s.Name, s.Name, node.data.DBName)
		})
}
