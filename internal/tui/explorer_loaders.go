package tui

import (
	"context"
	"errors"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// errNotConnected is returned by fetchChildren when a node's connection
// has already been closed (or was never resolved) by the time its
// children are fetched.
var errNotConnected = errors.New("not connected")

// loaderCtx is what every childLoader needs to build child nodes: the
// cancellable/timeout-bound context for its gosmo calls, and the
// connection to make them on. Bundling the two avoids repeating both as
// separate parameters across every loader.
type loaderCtx struct {
	ctx context.Context
	sc  *db.ServerConn
}

// node builds a child explorerNode bound to the same connection as the
// loader itself. id/parent are assigned later, on the UI goroutine, by
// ObjectExplorer.SetChildren.
func (l loaderCtx) node(label string, t NodeType, schema, name, dbName string) *explorerNode {
	return &explorerNode{
		label: label,
		data:  nodeData{Type: t, Schema: schema, Name: name, DBName: dbName, conn: l.sc},
	}
}

// childLoader fetches the children of one explorer node. Implementations
// live in the explorer_*.go files, grouped by domain (databases, schema
// objects, security, management); this file only holds the registry that
// ties a NodeType to its loader and the shared machinery every loader uses.
type childLoader func(l loaderCtx, node *explorerNode) ([]*explorerNode, error)

// childLoaders maps every expandable NodeType to the function that fetches
// its children. A NodeType with no entry (a leaf, or NodeLoading/NodeError)
// simply yields no children.
var childLoaders = map[NodeType]childLoader{
	NodeServer:            loadServerChildren,
	NodeDatabases:         loadDatabasesChildren,
	NodeSystemDatabases:   loadSystemDatabasesChildren,
	NodeDatabaseSnapshots: loadDatabaseSnapshotsChildren,
	NodeDatabaseSnapshot:  loadDatabaseSnapshotChildren,
	NodeDatabase:          loadDatabaseChildren,
	NodeDatabaseSecurity:  loadDatabaseSecurityChildren,
	NodeUsers:             loadUsersChildren,
	NodeDatabaseRoles:     loadDatabaseRolesChildren,
	NodeSchemas:           loadSchemasChildren,

	NodeDatabaseAuditSpecifications: loadDatabaseAuditSpecificationsChildren,
	NodeDatabaseScopedCredentials:   loadDatabaseScopedCredentialsChildren,

	NodeStorage:              loadStorageChildren,
	NodePartitionFunctions:   loadPartitionFunctionsChildren,
	NodePartitionSchemes:     loadPartitionSchemesChildren,
	NodeSecurityPolicies:     loadSecurityPoliciesChildren,
	NodeQueryStore:           loadQueryStoreChildren,
	NodeAlwaysEncryptedKeys:  loadAlwaysEncryptedKeysChildren,
	NodeColumnMasterKeys:     loadColumnMasterKeysChildren,
	NodeColumnEncryptionKeys: loadColumnEncryptionKeysChildren,

	NodeTables:           loadTablesChildren,
	NodeSystemTables:     tablesOfKindLoader(gosmo.TableKindSystem, true),
	NodeFileTables:       tablesOfKindLoader(gosmo.TableKindFileTable, false),
	NodeExternalTables:   tablesOfKindLoader(gosmo.TableKindExternal, false),
	NodeGraphTables:      tablesOfKindLoader(gosmo.TableKindGraph, false),
	NodeTable:            loadTableChildren,
	NodeColumns:          loadColumnsChildren,
	NodeKeys:             loadKeysChildren,
	NodeChecks:           loadConstraintsChildren,
	NodeIndexes:          loadIndexesChildren,
	NodeStatistics:       loadStatisticsChildren,
	NodeViews:            loadViewsChildren,
	NodeView:             loadViewChildren,
	NodeSystemViews:      loadSystemViewsChildren,
	NodeStoredProcedures: loadStoredProceduresChildren,
	NodeSystemProcedures: loadSystemProceduresChildren,
	NodeFunctions:        loadFunctionsChildren,
	NodeSystemFunctions:  loadSystemFunctionsChildren,
	NodeProgrammability:  loadProgrammabilityChildren,
	NodeTriggers:         loadTriggersChildren,
	NodeDatabaseTriggers: loadDatabaseTriggersChildren,
	NodeSequences:        loadSequencesChildren,
	NodeSynonyms:         loadSynonymsChildren,

	NodeTypes:                 loadTypesChildren,
	NodeSystemDataTypes:       loadSystemDataTypesChildren,
	NodeUserDefinedDataTypes:  loadUserDefinedDataTypesChildren,
	NodeUserDefinedTableTypes: loadUserDefinedTableTypesChildren,
	NodeUserDefinedTypes:      loadUserDefinedTypesChildren,
	NodeXmlSchemaCollections:  loadXmlSchemaCollectionsChildren,
	NodeAssemblies:            loadAssembliesChildren,
	NodeRules:                 loadRulesChildren,
	NodeDefaults:              loadDefaultsChildren,
	NodePlanGuides:            loadPlanGuidesChildren,

	NodeExternalResources:   loadExternalResourcesChildren,
	NodeExternalDataSources: loadExternalDataSourcesChildren,
	NodeExternalFileFormats: loadExternalFileFormatsChildren,
	NodeExternalLibraries:   loadExternalLibrariesChildren,

	NodeSecurity:                  loadSecurityChildren,
	NodeLogins:                    loadLoginsChildren,
	NodeServerRoles:               loadServerRolesChildren,
	NodeCredentials:               loadCredentialsChildren,
	NodeCryptographicProviders:    loadCryptographicProvidersChildren,
	NodeAudits:                    loadAuditsChildren,
	NodeServerAuditSpecifications: loadServerAuditSpecificationsChildren,

	NodeServerObjects:  loadServerObjectsChildren,
	NodeBackupDevices:  loadBackupDevicesChildren,
	NodeServerTriggers: loadServerTriggersChildren,
	NodeEndpoints:      loadEndpointsChildren,
	NodeLinkedServers:  loadLinkedServersChildren,

	NodeManagement:    loadManagementChildren,
	NodeSQLServerLogs: loadSQLServerLogsChildren,

	NodeAlwaysOn:              loadAlwaysOnChildren,
	NodeAvailabilityGroups:    loadAvailabilityGroupsChildren,
	NodeAvailabilityGroup:     loadAvailabilityGroupChildren,
	NodeAvailabilityReplicas:  loadAvailabilityReplicasChildren,
	NodeAvailabilityDatabases: loadAvailabilityDatabasesChildren,
	NodeAGListeners:           loadAGListenersChildren,

	NodeAgentJobs:        loadAgentRootChildren,
	NodeAgentJobsFolder:  loadAgentJobsFolderChildren,
	NodeAgentUserJobs:    loadAgentUserJobsChildren,
	NodeAgentSystemJobs:  loadAgentSystemJobsChildren,
	NodeAgentSchedules:   loadAgentSchedulesChildren,
	NodeAgentAlerts:      loadAgentAlertsChildren,
	NodeAgentEventAlerts: loadAgentEventAlertsChildren,
	NodeAgentOperators:   loadAgentOperatorsChildren,
	NodeAgentAdmin:       loadAgentAdminChildren,
	NodeAgentErrorLogs:   loadAgentErrorLogsChildren,
}

// menuBuilder builds one node type's own context menu. newQuery and refresh
// are built once by nodeMenuItems, since nearly every menu carries both and
// Refresh is what insertBeforeRefresh anchors on. Implementations sit beside
// their family's loaders in the explorer_*.go files, and in alwayson_menu.go
// and agent_menu.go.
type menuBuilder func(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem

// nodeMenus maps a NodeType to its menu builder. A NodeType with no entry
// gets New Query and Refresh only — the folders with nothing to create
// (Server Triggers, Database Triggers, Endpoints) and Cryptographic
// Providers. That one is read-only on purpose: registering a provider takes a
// DLL path on the server's own filesystem, which SSMS answers with a file
// browser this build has no way to offer, and everything
// sys.cryptographic_providers records is already in the Detail Browser's grid.
var nodeMenus = map[NodeType]menuBuilder{
	NodeServer:            serverMenuItems,
	NodeDatabases:         databasesMenuItems,
	NodeDatabase:          databaseMenuItems,
	NodeDatabaseSnapshots: databaseSnapshotsMenuItems,
	NodeDatabaseSnapshot:  databaseSnapshotMenuItems,
	NodeQueryStore:        queryStoreMenuItems,
	NodeQueryStoreReport:  queryStoreReportMenuItems,
	NodeUser:              userMenuItems,
	NodeDatabaseRole:      databaseRoleMenuItems,
	NodeSchema:            schemaMenuItems,
	NodeDatabaseTrigger:   databaseTriggerMenuItems,
	NodeSecurityPolicy:    securityPolicyMenuItems,

	NodeDatabaseAuditSpecifications: databaseAuditSpecificationsMenuItems,
	NodeDatabaseAuditSpecification:  databaseAuditSpecificationMenuItems,
	NodeDatabaseScopedCredentials:   databaseScopedCredentialsMenuItems,
	NodeDatabaseScopedCredential:    databaseScopedCredentialMenuItems,
	NodeColumnMasterKeys:            columnMasterKeysMenuItems,
	NodeColumnMasterKey:             columnMasterKeyMenuItems,
	NodeColumnEncryptionKeys:        columnEncryptionKeysMenuItems,
	NodeColumnEncryptionKey:         columnEncryptionKeyMenuItems,

	NodePartitionFunction: partitionFunctionMenuItems,
	NodePartitionScheme:   partitionSchemeMenuItems,

	NodeTable:           tableMenuItems,
	NodeKey:             keyMenuItems,
	NodeForeignKey:      foreignKeyMenuItems,
	NodeIndexes:         indexesMenuItems,
	NodeIndex:           indexMenuItems,
	NodeStatistics:      statisticsMenuItems,
	NodeStatistic:       statisticMenuItems,
	NodeView:            viewMenuItems,
	NodeStoredProcedure: storedProcedureMenuItems,

	NodeUserDefinedDataType:  userDefinedDataTypeMenuItems,
	NodeUserDefinedTableType: userDefinedTableTypeMenuItems,
	NodeUserDefinedType:      userDefinedTypeMenuItems,
	NodeXmlSchemaCollection:  xmlSchemaCollectionMenuItems,
	NodeAssembly:             assemblyMenuItems,
	NodeRule:                 ruleMenuItems,
	NodeDefault:              defaultObjectMenuItems,
	NodePlanGuide:            planGuideMenuItems,

	NodeExternalDataSource: externalDataSourceMenuItems,
	NodeExternalFileFormat: externalFileFormatMenuItems,
	NodeExternalLibrary:    externalLibraryMenuItems,

	NodeLogins:                    loginsMenuItems,
	NodeLogin:                     loginMenuItems,
	NodeServerRole:                serverRoleMenuItems,
	NodeCredentials:               credentialsMenuItems,
	NodeCredential:                credentialMenuItems,
	NodeAudits:                    auditsMenuItems,
	NodeAudit:                     auditMenuItems,
	NodeServerAuditSpecifications: serverAuditSpecificationsMenuItems,
	NodeServerAuditSpecification:  serverAuditSpecificationMenuItems,

	NodeBackupDevices: backupDevicesMenuItems,
	NodeBackupDevice:  backupDeviceMenuItems,
	NodeServerTrigger: serverTriggerMenuItems,
	NodeEndpoint:      endpointMenuItems,

	NodeManagement:     managementMenuItems,
	NodeSQLServerLogs:  errorLogsMenuItems,
	NodeSQLServerLog:   errorLogMenuItems,
	NodeAgentErrorLogs: errorLogsMenuItems,
	NodeAgentErrorLog:  errorLogMenuItems,

	NodeAlwaysOn:              alwaysOnRootMenuItems,
	NodeAvailabilityGroups:    agGroupsFolderMenuItems,
	NodeAvailabilityGroup:     agGroupMenuItems,
	NodeAvailabilityReplicas:  agReplicasFolderMenuItems,
	NodeAvailabilityReplica:   agReplicaMenuItems,
	NodeAvailabilityDatabases: agDatabasesFolderMenuItems,
	NodeAvailabilityDatabase:  agDatabaseMenuItems,
	NodeAGListeners:           agListenersFolderMenuItems,
	NodeAGListener:            agListenerMenuItems,

	NodeAgentUserJobs:    agentUserJobsMenuItems,
	NodeAgentJob:         agentJobMenuItems,
	NodeAgentSchedules:   agentSchedulesMenuItems,
	NodeAgentSchedule:    agentScheduleMenuItems,
	NodeAgentEventAlerts: agentEventAlertsMenuItems,
	NodeAgentAlert:       agentAlertMenuItems,
	NodeAgentOperators:   agentOperatorsMenuItems,
	NodeAgentOperator:    agentOperatorMenuItems,
}

// propertiesOnlyMenu is the menu shape shared by every leaf whose only
// command is Properties — New Query, Refresh, Properties. Written once so a
// hand-copied one is not where the divider goes missing.
//
// Delete, Script as and Rename are not here: explorer_object_ops.go and
// scripting.go add them from their own tables, on the node types they list.
func propertiesOnlyMenu(newQuery, refresh controls.MenuItem, show func()) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		refresh,
		{Label: "Properties...", Action: show},
	}
}

// fetchChildren looks up and runs the loader for node.data.Type. Runs on a
// background goroutine (see App.loadChildren) — must not touch
// ObjectExplorer's id-allocation or map state; node.id is left zero and
// assigned later by SetChildren on the UI goroutine.
func (a *App) fetchChildren(ctx context.Context, node *explorerNode) []*explorerNode {
	sc := resolveConn(node)
	if sc == nil {
		return []*explorerNode{errExplorerNode(errNotConnected)}
	}
	loader, ok := childLoaders[node.data.Type]
	if !ok {
		return nil
	}
	children, err := loader(loaderCtx{ctx: ctx, sc: sc}, node)
	if err != nil {
		a.logStatus("fetchChildren [%v]: %v", node.data.Type, err)
		return []*explorerNode{errExplorerNode(err)}
	}
	a.restoreFilters(sc, children)
	return filterChildren(node.data.Filter, children)
}

// listChildren runs a gosmo collection fetch and maps each item to an
// explorerNode via toNode, the shape every simple loader shares: fetch a
// collection, fail together or map every element.
func listChildren[T any](fetch func() ([]T, error), toNode func(T) *explorerNode) ([]*explorerNode, error) {
	items, err := fetch()
	if err != nil {
		return nil, err
	}
	out := make([]*explorerNode, 0, len(items))
	for _, it := range items {
		out = append(out, toNode(it))
	}
	return out, nil
}

// errExplorerNode builds a placeholder error node. It carries no id — the
// id is assigned later by ObjectExplorer.SetChildren on the UI goroutine.
//
// A permission refusal is rendered as the server's own sentence about it,
// rather than the nested plumbing string the wrapping produces
// ("gosmo: list tables in \"x\": gosmo: USE x: mssql: The server principal…").
// Only a refusal: any other failure keeps its full text, because a truncated
// read reported as a permissions problem sends the user after the wrong thing.
func errExplorerNode(err error) *explorerNode {
	label := err.Error()
	if denied := accessDeniedText(err); denied != "" {
		label = denied
	}
	return &explorerNode{label: label, data: nodeData{Type: NodeError}}
}

// fqn brackets a schema-qualified SQL Server identifier for use in
// generated T-SQL ("SELECT TOP 1000 * FROM "+fqn(schema, name)). schema=""
// (e.g. a server-level object) omits the schema part entirely rather than
// emitting an empty "[]." prefix. Embedded "]" characters are doubled,
// same escaping rule SQL Server itself uses for bracketed identifiers.
func fqn(schema, name string) string {
	if schema == "" {
		return gosmo.QuoteName(name)
	}
	return gosmo.QuoteName(schema) + "." + gosmo.QuoteName(name)
}
