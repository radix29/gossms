package tui

import (
	"strings"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/layout"
)

// app_show_properties.go is one entry point per Properties dialog and per New
// dialog, each taking the connection and the names its page needs. They are
// gathered here because every caller — the tree's menus, the Details pane, the
// menu bar — reaches them the same way, and the dialogs themselves live one per
// file beside prop_dialog.go.

func (a *App) refreshSelected() { a.explorer.RefreshSelected() }

func (a *App) showServerProperties() {
	if sc := a.connOrFirst(); sc != nil {
		a.showServerPropertiesFor(sc)
	}
}

// showServerPropertiesFor opens Server Properties for a known connection —
// the shared entry point for the Tools menu and the Object Explorer context
// menu.
func (a *App) showServerPropertiesFor(sc *db.ServerConn) {
	a.propDialog.show(sc, "", "Server Properties", "Instance: "+sc.Opts.Server, "Connected: yes",
		func() []propPage { return serverPropPages(sc) })
}

// showNewDatabaseDialog opens New Database for a known connection — the
// Object Explorer context menu on both the server node and the "Databases"
// folder.
func (a *App) showNewDatabaseDialog(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	a.newDatabaseDialog.show(sc)
}

// showDatabaseProperties runs Tools > Database Properties on whichever
// database the selected Object Explorer node belongs to — nodeData.DBName is
// propagated to every node under a database.
func (a *App) showDatabaseProperties() {
	node := a.explorer.Selected()
	if node == nil || node.data.DBName == "" {
		a.setStatus("Select a database (or an object within one) in Object Explorer first")
		return
	}
	a.showDatabasePropertiesFor(resolveConn(node), node.data.DBName)
}

// showDatabasePropertiesFor opens Database Properties for a known connection
// and database — the shared entry point for the Tools menu and the Object
// Explorer context menu.
func (a *App) showDatabasePropertiesFor(sc *db.ServerConn, dbName string) {
	a.propDialog.show(sc, dbName, "Database Properties", "Database: "+dbName, "Server: "+sc.Opts.Server,
		func() []propPage { return databasePropPages(sc, dbName) })
}

// showAGPropertiesFor opens Availability Group Properties for a group on sc,
// from the Object Explorer context menu. sc need not be the group's primary:
// every page follows the primary itself and reports an error when it can't.
func (a *App) showAGPropertiesFor(sc *db.ServerConn, agName string) {
	a.propDialog.show(sc, "", "Availability Group Properties",
		"Availability group: "+agName, "Server: "+sc.Opts.Server,
		func() []propPage { return agPropPages(sc, agName) })
}

// showAGDashboardFor opens the Always On dashboard — the context menu's "Show
// Dashboard", on a group or, with an empty agName, on the Always On root for
// every group at once.
//
// One panel per (connection, group): reopening raises the existing one rather
// than starting a second poller against the same primary. The all-groups view
// is one more such key, so it coexists with any number of per-group panels.
func (a *App) showAGDashboardFor(sc *db.ServerConn, agName string) {
	if !a.requireConn(sc) {
		return
	}
	idx := a.panels.FindIndex(func(p layout.Panel) bool {
		dash, ok := p.(*AGDashboard)
		return ok && dash.conn == sc && strings.EqualFold(dash.agName, agName)
	})
	if idx < 0 {
		idx = a.panels.AddPanel(NewAGDashboard(a, sc, agName))
	}
	a.panels.SetActive(idx)
	a.focusPanels()
}

// showNewLoginDialog opens New Login for a known connection — the Object
// Explorer context menu on Security > Logins.
func (a *App) showNewLoginDialog(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	a.newLoginDialog.show(sc)
}

// showBackupDialog opens Back Up Database for a known connection — the Object
// Explorer context menu on a database node or the "Databases" folder
// (dbName "").
func (a *App) showBackupDialog(sc *db.ServerConn, dbName string) {
	if !a.requireConn(sc) {
		return
	}
	a.backupDialog.show(sc, dbName)
}

// showRestoreDialog opens Restore Database for a known connection — the Object
// Explorer context menu on a database node or the "Databases" folder
// (dbName "").
func (a *App) showRestoreDialog(sc *db.ServerConn, dbName string) {
	if !a.requireConn(sc) {
		return
	}
	a.restoreDialog.show(sc, dbName)
}

// showBackupHistoryFor opens a new query window scoped to msdb, where the backup
// catalog lives whichever database the menu was opened from, pre-filled with
// backupHistoryQuery(dbName) and run immediately — the context menu's "View
// Backup History...", database node only.
func (a *App) showBackupHistoryFor(sc *db.ServerConn, dbName string) {
	if !a.requireConn(sc) {
		return
	}
	a.openQueryWithTextAndExecute(sc, "msdb", backupHistoryQuery(dbName))
}

// showLoginProperties opens Login Properties for a login on sc.
func (a *App) showLoginProperties(sc *db.ServerConn, loginName string) {
	a.propDialog.show(sc, "", "Login Properties", "Login: "+loginName, "Server: "+sc.Opts.Server,
		func() []propPage { return loginPropPages(a.propDialog, sc, loginName) })
}

// showTablePropertiesFor opens Table Properties for a known connection,
// database, and schema-qualified table, from the Object Explorer context menu.
func (a *App) showTablePropertiesFor(sc *db.ServerConn, dbName, schema, name string) {
	a.propDialog.show(sc, dbName, "Table Properties", "Table: "+fqn(schema, name), "Database: "+dbName,
		func() []propPage { return tablePropPages(sc, dbName, schema, name) })
}

// showIndexPropertiesFor opens Index Properties for a known connection,
// database, and schema-qualified table/index, from the context menu.
func (a *App) showIndexPropertiesFor(sc *db.ServerConn, dbName, schema, table, index string) {
	a.propDialog.show(sc, dbName, "Index Properties", "Index: "+index, "Table: "+fqn(schema, table),
		func() []propPage { return indexPropPages(a.propDialog, sc, dbName, schema, table, index) })
}

// showStatisticPropertiesFor opens Statistics Properties for a known
// connection, database, and schema-qualified table/statistic.
func (a *App) showStatisticPropertiesFor(sc *db.ServerConn, dbName, schema, table, stat string) {
	a.propDialog.show(sc, dbName, "Statistics Properties", "Statistic: "+stat, "Table: "+fqn(schema, table),
		func() []propPage { return statisticPropPages(a.propDialog, sc, dbName, schema, table, stat) })
}

// showForeignKeyPropertiesFor opens the read-only Foreign Key Properties for a
// known connection, database, and schema-qualified table/foreign key.
func (a *App) showForeignKeyPropertiesFor(sc *db.ServerConn, dbName, schema, table, fk string) {
	a.propDialog.show(sc, dbName, "Foreign Key Properties", "Key: "+fk, "Table: "+fqn(schema, table),
		func() []propPage { return fkPropPages(sc, dbName, schema, table, fk) })
}

// showKeyPropertiesFor opens Primary/Unique Key Properties for a known
// connection, database, and schema-qualified table/key. isPrimaryKey picks the
// dialog title and comes off the tree node's nodeData, which loadKeysChildren
// already knows.
func (a *App) showKeyPropertiesFor(sc *db.ServerConn, dbName, schema, table, key string, isPrimaryKey bool) {
	title := keyTypeName(isPrimaryKey) + " Properties"
	a.propDialog.show(sc, dbName, title, "Key: "+key, "Table: "+fqn(schema, table),
		func() []propPage { return keyPropPages(a.propDialog, sc, dbName, schema, table, key) })
}

// showRolePropertiesFor opens Database Role Properties for a known connection,
// database, and role name.
func (a *App) showRolePropertiesFor(sc *db.ServerConn, dbName, roleName string) {
	a.propDialog.show(sc, dbName, "Database Role Properties", "Role: "+roleName, "Database: "+dbName,
		func() []propPage { return rolePropPages(a.propDialog, sc, dbName, roleName) })
}

// showServerRolePropertiesFor opens Server Role Properties for a known
// connection and role name — a server-level principal with no dbName, like
// showLoginProperties.
func (a *App) showServerRolePropertiesFor(sc *db.ServerConn, roleName string) {
	a.propDialog.show(sc, "", "Server Role Properties", "Role: "+roleName, "Server: "+sc.Opts.Server,
		func() []propPage { return serverRolePropPages(sc, roleName) })
}

// showCredentialPropertiesFor opens Credential Properties for a known
// connection and credential name.
func (a *App) showCredentialPropertiesFor(sc *db.ServerConn, credName string) {
	a.propDialog.show(sc, "", "Credential Properties", "Credential: "+credName, "Server: "+sc.Opts.Server,
		func() []propPage { return credentialPropPages(sc, credName) })
}

// showNewCredentialDialog opens New Credential for a known connection.
func (a *App) showNewCredentialDialog(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	a.newCredentialDialog.show(sc)
}

// showBackupDevicePropertiesFor opens Backup Device Properties for a known
// connection and device name.
func (a *App) showBackupDevicePropertiesFor(sc *db.ServerConn, devName string) {
	a.propDialog.show(sc, "", "Backup Device Properties", "Device: "+devName, "Server: "+sc.Opts.Server,
		func() []propPage { return backupDevicePropPages(sc, devName) })
}

// showServerTriggerPropertiesFor opens Server Trigger Properties for a known
// connection and trigger name.
func (a *App) showServerTriggerPropertiesFor(sc *db.ServerConn, trigName string) {
	a.propDialog.show(sc, "", "Server Trigger Properties", "Trigger: "+trigName, "Server: "+sc.Opts.Server,
		func() []propPage { return serverTriggerPropPages(sc, trigName) })
}

// showEndpointPropertiesFor opens Endpoint Properties for a known connection
// and endpoint name.
func (a *App) showEndpointPropertiesFor(sc *db.ServerConn, epName string) {
	a.propDialog.show(sc, "", "Endpoint Properties", "Endpoint: "+epName, "Server: "+sc.Opts.Server,
		func() []propPage { return endpointPropPages(sc, epName) })
}

// showAuditPropertiesFor opens Audit Properties for a known connection and
// audit name.
func (a *App) showAuditPropertiesFor(sc *db.ServerConn, auditName string) {
	a.propDialog.show(sc, "", "Audit Properties", "Audit: "+auditName, "Server: "+sc.Opts.Server,
		func() []propPage { return auditPropPages(sc, auditName) })
}

// showServerAuditSpecificationPropertiesFor opens Server Audit Specification
// Properties for a known connection and specification name.
func (a *App) showServerAuditSpecificationPropertiesFor(sc *db.ServerConn, specName string) {
	a.propDialog.show(sc, "", "Server Audit Specification Properties",
		"Specification: "+specName, "Server: "+sc.Opts.Server,
		func() []propPage { return serverAuditSpecificationPropPages(sc, specName) })
}

// showNewAuditDialog opens New Audit for a known connection.
func (a *App) showNewAuditDialog(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	a.newAuditDialog.show(sc)
}

// showNewServerAuditSpecificationDialog opens New Server Audit Specification
// for a known connection.
func (a *App) showNewServerAuditSpecificationDialog(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	a.newAuditSpecificationDialog.show(sc)
}

// showDatabaseAuditSpecificationPropertiesFor opens Database Audit
// Specification Properties for a known connection, database and specification
// name.
func (a *App) showDatabaseAuditSpecificationPropertiesFor(sc *db.ServerConn, dbName, specName string) {
	a.propDialog.show(sc, dbName, "Database Audit Specification Properties",
		"Specification: "+specName, "Database: "+dbName,
		func() []propPage { return databaseAuditSpecificationPropPages(sc, dbName, specName) })
}

// showDatabaseTriggerPropertiesFor opens Database Trigger Properties for a
// known connection, database and trigger name.
func (a *App) showDatabaseTriggerPropertiesFor(sc *db.ServerConn, dbName, trigName string) {
	a.propDialog.show(sc, dbName, "Database Trigger Properties",
		"Trigger: "+trigName, "Database: "+dbName,
		func() []propPage { return databaseTriggerPropPages(sc, dbName, trigName) })
}

// showDatabaseScopedCredentialPropertiesFor opens Database Scoped Credential
// Properties for a known connection, database and credential name.
func (a *App) showDatabaseScopedCredentialPropertiesFor(sc *db.ServerConn, dbName, credName string) {
	a.propDialog.show(sc, dbName, "Database Scoped Credential Properties",
		"Credential: "+credName, "Database: "+dbName,
		func() []propPage { return databaseScopedCredentialPropPages(sc, dbName, credName) })
}

// showNewDatabaseScopedCredentialDialog opens New Database Scoped Credential
// for one database's folder node.
func (a *App) showNewDatabaseScopedCredentialDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newDBScopedCredDialog.show(sc, node)
}

// showNewDatabaseAuditSpecificationDialog opens New Database Audit
// Specification for one database's folder node.
func (a *App) showNewDatabaseAuditSpecificationDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newDBAuditSpecDialog.show(sc, node)
}

// showNewBackupDeviceDialog opens New Backup Device for a known connection.
func (a *App) showNewBackupDeviceDialog(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	a.newBackupDeviceDialog.show(sc)
}

// showUserPropertiesFor opens Database User Properties for a known connection,
// database, and user name.
func (a *App) showUserPropertiesFor(sc *db.ServerConn, dbName, userName string) {
	a.propDialog.show(sc, dbName, "Database User Properties", "User: "+userName, "Database: "+dbName,
		func() []propPage { return userPropPages(a.propDialog, sc, dbName, userName) })
}

// showSchemaPropertiesFor opens Schema Properties for a known connection,
// database, and schema name.
func (a *App) showSchemaPropertiesFor(sc *db.ServerConn, dbName, schemaName string) {
	a.propDialog.show(sc, dbName, "Schema Properties", "Schema: "+schemaName, "Database: "+dbName,
		func() []propPage { return schemaPropPages(sc, dbName, schemaName) })
}

// showPartitionFunctionPropertiesFor opens the read-only Partition Function
// Properties for a known connection and database.
func (a *App) showPartitionFunctionPropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "Partition Function Properties", "Function: "+name, "Database: "+dbName,
		func() []propPage { return partitionFunctionPropPages(sc, dbName, name) })
}

// showPartitionSchemePropertiesFor opens the read-only Partition Scheme
// Properties.
func (a *App) showPartitionSchemePropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "Partition Scheme Properties", "Scheme: "+name, "Database: "+dbName,
		func() []propPage { return partitionSchemePropPages(sc, dbName, name) })
}

// showSecurityPolicyPropertiesFor opens the read-only Security Policy
// Properties.
func (a *App) showSecurityPolicyPropertiesFor(sc *db.ServerConn, dbName, schema, name string) {
	a.propDialog.show(sc, dbName, "Security Policy Properties", "Policy: "+fqn(schema, name), "Database: "+dbName,
		func() []propPage { return securityPolicyPropPages(sc, dbName, schema, name) })
}

// showColumnMasterKeyPropertiesFor opens the read-only Column Master Key
// Properties.
func (a *App) showColumnMasterKeyPropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "Column Master Key Properties", "Key: "+name, "Database: "+dbName,
		func() []propPage { return columnMasterKeyPropPages(sc, dbName, name) })
}

// showColumnEncryptionKeyPropertiesFor opens the read-only Column Encryption
// Key Properties.
func (a *App) showColumnEncryptionKeyPropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "Column Encryption Key Properties", "Key: "+name, "Database: "+dbName,
		func() []propPage { return columnEncryptionKeyPropPages(sc, dbName, name) })
}

// The Phase 3 tree families' Properties, all read-only bar the plan guide's
// General page — see type_props.go, assembly_props.go, rule_default_props.go,
// plan_guide_props.go and external_resource_props.go for why each is.
//
// System Data Types has no entry here on purpose: SSMS offers no Properties on
// a built-in type either, and there is nothing to say about `int` that its
// name does not.

// showUserDefinedDataTypePropertiesFor opens the read-only Properties for an
// alias type.
func (a *App) showUserDefinedDataTypePropertiesFor(sc *db.ServerConn, dbName, schema, name string) {
	a.propDialog.show(sc, dbName, "User-Defined Data Type Properties",
		"Type: "+fqn(schema, name), "Database: "+dbName,
		func() []propPage { return userDefinedDataTypePropPages(sc, dbName, schema, name) })
}

// showUserDefinedTableTypePropertiesFor opens the read-only Properties for a
// table type.
func (a *App) showUserDefinedTableTypePropertiesFor(sc *db.ServerConn, dbName, schema, name string) {
	a.propDialog.show(sc, dbName, "User-Defined Table Type Properties",
		"Type: "+fqn(schema, name), "Database: "+dbName,
		func() []propPage { return userDefinedTableTypePropPages(sc, dbName, schema, name) })
}

// showClrTypePropertiesFor opens the read-only Properties for a CLR type.
func (a *App) showClrTypePropertiesFor(sc *db.ServerConn, dbName, schema, name string) {
	a.propDialog.show(sc, dbName, "User-Defined Type Properties",
		"Type: "+fqn(schema, name), "Database: "+dbName,
		func() []propPage { return clrTypePropPages(sc, dbName, schema, name) })
}

// showXMLSchemaCollectionPropertiesFor opens the read-only Properties for an
// XML schema collection.
func (a *App) showXMLSchemaCollectionPropertiesFor(sc *db.ServerConn, dbName, schema, name string) {
	a.propDialog.show(sc, dbName, "XML Schema Collection Properties",
		"Collection: "+fqn(schema, name), "Database: "+dbName,
		func() []propPage { return xmlSchemaCollectionPropPages(sc, dbName, schema, name) })
}

// showAssemblyPropertiesFor opens the read-only Properties for a CLR assembly.
func (a *App) showAssemblyPropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "Assembly Properties", "Assembly: "+name, "Database: "+dbName,
		func() []propPage { return assemblyPropPages(sc, dbName, name) })
}

// showRulePropertiesFor opens the read-only Properties for a standalone rule.
func (a *App) showRulePropertiesFor(sc *db.ServerConn, dbName, schema, name string) {
	a.propDialog.show(sc, dbName, "Rule Properties", "Rule: "+fqn(schema, name), "Database: "+dbName,
		func() []propPage { return rulePropPages(sc, dbName, schema, name) })
}

// showDefaultPropertiesFor opens the read-only Properties for a standalone
// default — never a table's DF_… constraint, which is a different family
// under the same sys.objects type code.
func (a *App) showDefaultPropertiesFor(sc *db.ServerConn, dbName, schema, name string) {
	a.propDialog.show(sc, dbName, "Default Properties", "Default: "+fqn(schema, name), "Database: "+dbName,
		func() []propPage { return defaultPropPages(sc, dbName, schema, name) })
}

// showDatabaseSnapshotPropertiesFor opens the read-only Properties for a
// database snapshot. Not the Database Properties dialog: a snapshot is
// read-only by construction, with no recovery model, no files to add and no
// options to set — see database_snapshot_props.go.
func (a *App) showDatabaseSnapshotPropertiesFor(sc *db.ServerConn, name string) {
	a.propDialog.show(sc, name, "Database Snapshot Properties", "Snapshot: "+name, "",
		func() []propPage { return databaseSnapshotPropPages(sc, name) })
}

// showPlanGuidePropertiesFor opens Plan Guide Properties, whose General page
// can enable and disable the guide.
func (a *App) showPlanGuidePropertiesFor(sc *db.ServerConn, dbName, name, scopeSchema, scopeName string) {
	a.propDialog.show(sc, dbName, "Plan Guide Properties", "Plan guide: "+name, "Database: "+dbName,
		func() []propPage { return planGuidePropPages(sc, dbName, name, scopeSchema, scopeName) })
}

// The Service Broker families' Properties. Five are read-only for the reason
// each props file argues; Queue and Route can write — see queue_props.go and
// route_props.go.

// showMessageTypePropertiesFor opens the read-only Properties for a message
// type.
func (a *App) showMessageTypePropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "Message Type Properties", "Message type: "+name, "Database: "+dbName,
		func() []propPage { return messageTypePropPages(sc, dbName, name) })
}

// showContractPropertiesFor opens the read-only Properties for a service
// contract.
func (a *App) showContractPropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "Contract Properties", "Contract: "+name, "Database: "+dbName,
		func() []propPage { return contractPropPages(sc, dbName, name) })
}

// showBrokerQueuePropertiesFor opens Queue Properties, whose General page
// writes the queue's status, retention, poison-message handling and
// activation.
func (a *App) showBrokerQueuePropertiesFor(sc *db.ServerConn, dbName, schema, name string) {
	a.propDialog.show(sc, dbName, "Queue Properties", "Queue: "+fqn(schema, name), "Database: "+dbName,
		func() []propPage { return brokerQueuePropPages(sc, dbName, schema, name) })
}

// showBrokerServicePropertiesFor opens the read-only Properties for a Service
// Broker service.
func (a *App) showBrokerServicePropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "Service Properties", "Service: "+name, "Database: "+dbName,
		func() []propPage { return brokerServicePropPages(sc, dbName, name) })
}

// showRoutePropertiesFor opens Route Properties, whose General page writes the
// route's destination and lifetime.
func (a *App) showRoutePropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "Route Properties", "Route: "+name, "Database: "+dbName,
		func() []propPage { return routePropPages(sc, dbName, name) })
}

// showRemoteServiceBindingPropertiesFor opens the read-only Properties for a
// remote service binding.
func (a *App) showRemoteServiceBindingPropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "Remote Service Binding Properties",
		"Binding: "+name, "Database: "+dbName,
		func() []propPage { return remoteServiceBindingPropPages(sc, dbName, name) })
}

// showBrokerPriorityPropertiesFor opens the read-only Properties for a
// conversation priority.
func (a *App) showBrokerPriorityPropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "Broker Priority Properties", "Priority: "+name, "Database: "+dbName,
		func() []propPage { return brokerPriorityPropPages(sc, dbName, name) })
}

// showExternalDataSourcePropertiesFor opens the read-only Properties for an
// external data source.
func (a *App) showExternalDataSourcePropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "External Data Source Properties",
		"Data source: "+name, "Database: "+dbName,
		func() []propPage { return externalDataSourcePropPages(sc, dbName, name) })
}

// showExternalFileFormatPropertiesFor opens the read-only Properties for an
// external file format.
func (a *App) showExternalFileFormatPropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "External File Format Properties",
		"File format: "+name, "Database: "+dbName,
		func() []propPage { return externalFileFormatPropPages(sc, dbName, name) })
}

// showExternalLibraryPropertiesFor opens the read-only Properties for an
// external library.
func (a *App) showExternalLibraryPropertiesFor(sc *db.ServerConn, dbName, name string) {
	a.propDialog.show(sc, dbName, "External Library Properties",
		"Library: "+name, "Database: "+dbName,
		func() []propPage { return externalLibraryPropPages(sc, dbName, name) })
}

// canSaveActivePanel gates File > Save / Save As...: a query panel saves its
// script, a plan panel its .sqlplan, and nothing else has anything to write.
func (a *App) canSaveActivePanel() bool {
	if a.activeQueryPanel() != nil {
		return true
	}
	_, ok := a.panels.ActivePanel().(*PlanPanel)
	return ok
}
