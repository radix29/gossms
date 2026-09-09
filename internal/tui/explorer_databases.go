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
// folder listed first if the server has any, then "Database Snapshots" —
// matching SSMS. A database that belongs to an availability group carries its
// synchronization state in the label, the same way the Availability Databases
// folder writes it; see agLocalDatabaseStates for why the state shown here is
// the local replica's alone.
//
// A snapshot is an ordinary row in sys.databases, so it comes back from
// DatabasesContext with the user databases and has to be excluded here or it
// appears twice — once as a user database and once under its own folder.
// Database.IsSnapshot answers from the source_database_id the listing already
// read, so the exclusion costs no second query. The Detail Browser's own
// Databases list makes the same exclusion (see loadDatabasesFolderDetails):
// the two panes describe the same folder.
//
// The Database Snapshots folder is listed whether or not the server has any,
// unlike System Databases: it is where New Snapshot lives, so a server with
// no snapshots is exactly the server that needs to reach it. An Azure engine
// edition is the exception — see below.
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
		if d.IsSnapshot() {
			continue
		}
		n := l.node(agLabelForDatabase(d.Name(), agStates), NodeDatabase, "", d.Name(), d.Name())
		n.data.IsOffline = d.State() != "ONLINE"
		n.data.CreateDate = d.CreateDate()
		userDBs = append(userDBs, n)
	}
	folders := []*explorerNode{}
	if hasSystem {
		folders = append(folders, l.node("System Databases", NodeSystemDatabases, "", "", ""))
	}
	// Not on an Azure engine edition: CREATE DATABASE ... AS SNAPSHOT OF is
	// not implemented there at all, so the folder could only ever be empty
	// and the New Snapshot item inside it is already edition-gated.
	if !serverIsAzure(l.sc) {
		folders = append(folders, l.node("Database Snapshots", NodeDatabaseSnapshots, "", "", ""))
	}
	return append(folders, userDBs...), nil
}

// loadDatabaseSnapshotsChildren lists the server's database snapshots. The
// label is the snapshot's own name, as SSMS writes it; which database it was
// taken of is in the detail pane and on its Properties dialog, since two
// snapshots of the same source differ only by name.
//
// A snapshot node is a database node in every other respect — DBName is the
// snapshot's own name — so its contents are browsable the way any other
// database's are; see loadDatabaseSnapshotChildren for the folders it gets.
func loadDatabaseSnapshotsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(
		func() ([]*gosmo.DatabaseSnapshot, error) { return l.sc.Server.DatabaseSnapshotsContext(l.ctx) },
		func(s *gosmo.DatabaseSnapshot) *explorerNode {
			n := l.node(s.Name, NodeDatabaseSnapshot, "", s.Name, s.Name)
			n.data.CreateDate = s.CreateDate
			n.data.IsOffline = s.State != "ONLINE"
			n.data.SourceDatabase = s.SourceDatabase
			return n
		})
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

// loadDatabaseSnapshotChildren returns a snapshot's folders: the object
// families, and only those.
//
// Query Store, Storage and Security are deliberately absent. A snapshot is
// read-only by construction — it has no transaction log, no file to add and
// no recovery model — and each of those three folders' menus leads to
// Database Properties pages that write: the files page, the recovery model,
// New User. Offering them on a database that refuses every one of them is
// worse than not offering them, and the snapshot's own read-only Properties
// dialog is on its context menu instead (see database_snapshot_props.go).
//
// What is left is what a snapshot is *for*: reading the data as it was.
func loadDatabaseSnapshotChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	if node.data.IsOffline {
		return []*explorerNode{l.node("(Snapshot is not online)", NodeError, "", "", node.data.DBName)}, nil
	}
	dbName := node.data.DBName
	if !l.sc.DatabaseCapabilities(l.ctx, dbName).Accessible {
		return []*explorerNode{l.node(accessDeniedLabel+"CONNECT permission on this snapshot is required.",
			NodeError, "", "", dbName)}, nil
	}
	return []*explorerNode{
		l.node("Tables", NodeTables, "", "", dbName),
		l.node("Views", NodeViews, "", "", dbName),
		l.node("Programmability", NodeProgrammability, "", "", dbName),
	}, nil
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
	// Same reasoning as the offline case one line up, for the same reason: SQL
	// Server lists a database the login cannot open, every folder below it
	// opens with a USE that fails, and the user gets one copy of Msg 916 per
	// folder instead of one answer. Accessible is only false when the server
	// itself said so — a probe that could not run leaves the folders in place.
	if !l.sc.DatabaseCapabilities(l.ctx, dbName).Accessible {
		return []*explorerNode{l.node(accessDeniedLabel+"CONNECT permission on this database is required.",
			NodeError, "", "", dbName)}, nil
	}
	return []*explorerNode{
		l.node("Tables", NodeTables, "", "", dbName),
		l.node("Views", NodeViews, "", "", dbName),
		l.node("External Resources", NodeExternalResources, "", "", dbName),
		l.node("Programmability", NodeProgrammability, "", "", dbName),
		l.node("Query Store", NodeQueryStore, "", "", dbName),
		l.node("Security", NodeDatabaseSecurity, "", "", dbName),
		l.node("Storage", NodeStorage, "", "", dbName),
	}, nil
}

// loadProgrammabilityChildren returns the module families SSMS files under
// Programmability, in SSMS's order.
//
// The folder exists because Database Triggers needed a home. Database-scope
// DDL triggers are a third trigger family (gosmo's database_trigger.go) and
// the label "Database Triggers" beside a flat "Triggers" folder listing DML
// triggers reads as a distinction without a difference — so the DML roll-up
// that used to sit here is gone instead: a DML trigger belongs to a table or
// a view, and is now reachable only under that object's own Triggers folder,
// which is where SSMS puts it and where it was already listed.
func loadProgrammabilityChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbName := node.data.DBName
	return []*explorerNode{
		l.node("Stored Procedures", NodeStoredProcedures, "", "", dbName),
		l.node("Functions", NodeFunctions, "", "", dbName),
		l.node("Database Triggers", NodeDatabaseTriggers, "", "", dbName),
		l.node("Assemblies", NodeAssemblies, "", "", dbName),
		l.node("Types", NodeTypes, "", "", dbName),
		l.node("Rules", NodeRules, "", "", dbName),
		l.node("Defaults", NodeDefaults, "", "", dbName),
		l.node("Plan Guides", NodePlanGuides, "", "", dbName),
		l.node("Sequences", NodeSequences, "", "", dbName),
		l.node("Synonyms", NodeSynonyms, "", "", dbName),
	}, nil
}

// loadDatabaseTriggersChildren lists a database's DDL triggers, each labelled
// with its state — the same "(Disabled)" suffix the server-scope folder uses,
// and for the same reason: a disabled trigger enforces nothing and nothing
// else in the row says so.
func loadDatabaseTriggersChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(
		func() ([]*gosmo.DatabaseTrigger, error) { return dbObj.DatabaseTriggersContext(l.ctx) },
		func(t *gosmo.DatabaseTrigger) *explorerNode {
			label := t.Name
			if !t.IsEnabled {
				label += " (Disabled)"
			}
			n := l.node(label, NodeDatabaseTrigger, "", t.Name, node.data.DBName)
			n.data.CreateDate = t.CreateDate
			n.data.IsEnabled = t.IsEnabled
			return n
		})
}

// loadQueryStoreChildren returns the Query Store folder's seven report
// leaves — the views SSMS shows under the same folder, in the same order.
//
// The folder is listed whether or not Query Store is turned on: deciding here
// would cost a sys.database_query_store_options read on every database
// expansion, for a folder the user may never open. Each report answers for
// itself instead — see queryStoreReportDetail.
func loadQueryStoreChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	out := make([]*explorerNode, 0, len(queryStoreReportTitles))
	for _, title := range queryStoreReportTitles {
		out = append(out, l.node(title, NodeQueryStoreReport, "", title, node.data.DBName))
	}
	return out, nil
}

// loadDatabaseSecurityChildren returns a database's security folders, in
// SSMS's order.
func loadDatabaseSecurityChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Users", NodeUsers, "", "", node.data.DBName),
		l.node("Roles", NodeDatabaseRoles, "", "", node.data.DBName),
		l.node("Schemas", NodeSchemas, "", "", node.data.DBName),
		l.node("Database Audit Specifications", NodeDatabaseAuditSpecifications, "", "", node.data.DBName),
		l.node("Database Scoped Credentials", NodeDatabaseScopedCredentials, "", "", node.data.DBName),
		l.node("Security Policies", NodeSecurityPolicies, "", "", node.data.DBName),
		l.node("Always Encrypted Keys", NodeAlwaysEncryptedKeys, "", "", node.data.DBName),
	}, nil
}

// loadDatabaseAuditSpecificationsChildren lists a database's audit
// specifications, each labelled with its state — the same "(Disabled)" suffix
// the server-scope folder uses, and for the same reason: a disabled
// specification records nothing and nothing else in the row says so.
func loadDatabaseAuditSpecificationsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(
		func() ([]*gosmo.DatabaseAuditSpecification, error) {
			return dbObj.DatabaseAuditSpecificationsContext(l.ctx)
		},
		func(spec *gosmo.DatabaseAuditSpecification) *explorerNode {
			label := spec.Name
			if !spec.IsEnabled {
				label += " (Disabled)"
			}
			n := l.node(label, NodeDatabaseAuditSpecification, "", spec.Name, node.data.DBName)
			n.data.CreateDate = spec.CreateDate
			n.data.IsEnabled = spec.IsEnabled
			return n
		})
}

// loadDatabaseScopedCredentialsChildren lists a database's own credentials —
// sys.database_scoped_credentials, a separate securable from the server-level
// family under Security > Credentials.
func loadDatabaseScopedCredentialsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(
		func() ([]*gosmo.DatabaseScopedCredential, error) {
			return dbObj.DatabaseScopedCredentialsContext(l.ctx)
		},
		func(c *gosmo.DatabaseScopedCredential) *explorerNode {
			n := l.node(c.Name, NodeDatabaseScopedCredential, "", c.Name, node.data.DBName)
			n.data.CreateDate = c.CreateDate
			return n
		})
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
