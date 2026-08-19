package tui

import gosmo "github.com/radix29/gosmo"

// loadServerChildren returns a connected server's top-level folders:
// Databases, Security, Server Objects (linked servers), Management (the SQL
// Server logs), Always On High Availability, and SQL Server Agent — the last
// three siblings of Databases
// here rather than nested under Server Objects, matching SSMS's own top-level
// placement. Kept a static, no-query loader (unlike loadDatabasesChildren
// etc.) so it stays safe to call directly in tests; the Agent node's
// " (Stopped)" label suffix is instead filled in by a follow-up async check —
// see refreshAgentRootLabel in app_explorer_data.go. The Always On folder is
// listed unconditionally for the same reason SSMS does: whether the instance
// has Always On enabled is a query, and the answer belongs in the folder's
// own expansion (loadAlwaysOnChildren), not in whether it appears.
func loadServerChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Databases", NodeDatabases, "", "", ""),
		l.node("Security", NodeSecurity, "", "", ""),
		l.node("Server Objects", NodeServerObjects, "", "", ""),
		l.node("Management", NodeManagement, "", "", ""),
		l.node(alwaysOnRootLabel, NodeAlwaysOn, "", "", ""),
		l.node(agentRootLabel, NodeAgentJobs, "", "", ""),
	}, nil
}

// agentRootLabel is the "SQL Server Agent" node's base label — the literal
// string refreshAgentRootLabel matches against when appending " (Stopped)"
// so the two stay in sync.
const agentRootLabel = "SQL Server Agent"

// loadDatabasesChildren lists user databases, with a "System Databases"
// folder listed first if the server has any — matching SSMS. A database that
// belongs to an availability group carries its synchronization state in the
// label, the same way the Availability Databases folder writes it; see
// agLocalDatabaseStates for why the state shown here is the local replica's
// alone.
func loadDatabasesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbs, err := l.sc.Server.DatabasesContext(l.ctx)
	if err != nil {
		return nil, err
	}
	agStates := agLocalDatabaseStates(l)
	var userDBs []*explorerNode
	hasSystem := false
	for _, d := range dbs {
		if d.IsSystem() {
			hasSystem = true
			continue
		}
		n := l.node(agLabelForDatabase(d.Name(), agStates), NodeDatabase, "", d.Name(), d.Name())
		n.data.IsOffline = d.State() != "ONLINE"
		n.data.CreateDate = d.CreateDate()
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
			n.data.CreateDate = d.CreateDate()
			n.data.IsSystem = true
			out = append(out, n)
		}
	}
	return out, nil
}

// loadDatabaseChildren returns one database's object-family folders, or a
// single explanatory leaf if the database is offline. SQL Server can't run
// any metadata query against an offline database (USE fails outright), so
// the normal folder list would just let each of Tables/Views/.../Security
// expand into the same "cannot open database" error; this short-circuits
// to one clear leaf.
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
		l.node("Storage", NodeStorage, "", "", dbName),
	}, nil
}

// loadDatabaseSecurityChildren returns a database's security folders, in
// SSMS's order.
func loadDatabaseSecurityChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Users", NodeUsers, "", "", node.data.DBName),
		l.node("Roles", NodeDatabaseRoles, "", "", node.data.DBName),
		l.node("Schemas", NodeSchemas, "", "", node.data.DBName),
		l.node("Security Policies", NodeSecurityPolicies, "", "", node.data.DBName),
		l.node("Always Encrypted Keys", NodeAlwaysEncryptedKeys, "", "", node.data.DBName),
	}, nil
}

// loadSecurityPoliciesChildren lists a database's row-level security
// policies, each labelled with its state — a disabled policy filters
// nothing, which is the first thing to know about one.
func loadSecurityPoliciesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.SecurityPolicy, error) { return dbObj.SecurityPoliciesContext(l.ctx) },
		func(p *gosmo.SecurityPolicy) *explorerNode {
			label := p.Schema + "." + p.Name
			if !p.IsEnabled {
				label += " (Disabled)"
			}
			n := l.node(label, NodeSecurityPolicy, p.Schema, p.Name, node.data.DBName)
			n.data.IsEnabled = p.IsEnabled
			return n
		})
}

// loadAlwaysEncryptedKeysChildren returns the two Always Encrypted key
// folders.
func loadAlwaysEncryptedKeysChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Column Master Keys", NodeColumnMasterKeys, "", "", node.data.DBName),
		l.node("Column Encryption Keys", NodeColumnEncryptionKeys, "", "", node.data.DBName),
	}, nil
}

func loadColumnMasterKeysChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.ColumnMasterKey, error) { return dbObj.ColumnMasterKeysContext(l.ctx) },
		func(k *gosmo.ColumnMasterKey) *explorerNode {
			return l.node(k.Name, NodeColumnMasterKey, "", k.Name, node.data.DBName)
		})
}

func loadColumnEncryptionKeysChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.ColumnEncryptionKey, error) { return dbObj.ColumnEncryptionKeysContext(l.ctx) },
		func(k *gosmo.ColumnEncryptionKey) *explorerNode {
			return l.node(k.Name, NodeColumnEncryptionKey, "", k.Name, node.data.DBName)
		})
}

func loadUsersChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.User, error) { return dbObj.UsersContext(l.ctx) },
		func(u *gosmo.User) *explorerNode {
			n := l.node(u.Name, NodeUser, "", u.Name, node.data.DBName)
			n.data.IsSystem = isSystemUser(u.Name)
			return n
		})
}

func loadDatabaseRolesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.DatabaseRole, error) { return dbObj.DatabaseRolesContext(l.ctx) },
		func(r *gosmo.DatabaseRole) *explorerNode {
			n := l.node(r.Name, NodeDatabaseRole, "", r.Name, node.data.DBName)
			n.data.IsSystem = isSystemDatabaseRole(r)
			return n
		})
}

func loadSchemasChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.Schema, error) { return dbObj.SchemasContext(l.ctx) },
		func(s *gosmo.Schema) *explorerNode {
			n := l.node(s.Name, NodeSchema, s.Name, s.Name, node.data.DBName)
			n.data.IsSystem = isSystemSchema(s.Name)
			return n
		})
}
