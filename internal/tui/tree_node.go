package tui

import (
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// formatSQLDate formats a time.Time the way SSMS conventionally displays
// dates in object properties. gosmo returns time.Time (not string) for
// every CreateDate/ModifyDate field and method, so every caller that puts
// a date into a []string grid row needs this.
func formatSQLDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

// NodeType identifies what kind of SQL Server object an explorer node
// represents. This is application-specific domain data — the generic
// tree rendering/navigation lives in tuikit/controls.TreeView.
type NodeType int

const (
	NodeServer NodeType = iota
	NodeDatabases
	NodeSystemDatabases
	NodeDatabaseSnapshots
	NodeDatabaseSnapshot
	NodeDatabase
	NodeTables
	NodeSystemTables
	NodeFileTables
	NodeExternalTables
	NodeGraphTables
	NodeTable
	NodeColumns
	NodeColumn
	NodeKeys
	NodeKey
	NodeIndexes
	NodeIndex
	NodeStatistics
	NodeStatistic
	NodeViews
	NodeView
	NodeSystemViews
	NodeStoredProcedures
	NodeStoredProcedure
	NodeSystemProcedures
	NodeFunctions
	NodeFunction
	NodeSystemFunctions
	NodeSecurity
	NodeLogins
	NodeLogin
	NodeServerRoles
	NodeServerRole
	NodeCredentials
	NodeCredential
	NodeCryptographicProviders
	NodeCryptographicProvider
	NodeAudits
	NodeAudit
	NodeServerAuditSpecifications
	NodeServerAuditSpecification
	NodeServerObjects
	NodeBackupDevices
	NodeBackupDevice
	NodeServerTriggers
	NodeServerTrigger
	NodeEndpoints
	NodeEndpoint
	NodeManagement
	NodeSQLServerLogs
	NodeSQLServerLog
	NodeAgentJobs
	NodeAgentJobsFolder
	NodeAgentUserJobs
	NodeAgentSystemJobs
	NodeAgentJob
	NodeAgentJobActivity
	NodeAgentJobHistory
	NodeAgentJobCategories
	NodeAgentSchedules
	NodeAgentSchedule
	NodeAgentAlerts
	NodeAgentEventAlerts
	NodeAgentAlert
	NodeAgentAlertCategories
	NodeAgentOperators
	NodeAgentOperator
	NodeAgentAdmin
	NodeAgentReport
	NodeAgentErrorLogs
	NodeAgentErrorLog
	NodeLinkedServers
	NodeLinkedServer
	NodeAlwaysOn
	NodeAvailabilityGroups
	NodeAvailabilityGroup
	NodeAvailabilityReplicas
	NodeAvailabilityReplica
	NodeAvailabilityDatabases
	NodeAvailabilityDatabase
	NodeAGListeners
	NodeAGListener
	NodeDatabaseSecurity
	NodeUsers
	NodeUser
	NodeDatabaseRoles
	NodeDatabaseRole
	NodeSchemas
	NodeSchema
	NodeTriggers
	NodeTrigger
	NodeProgrammability
	NodeDatabaseTriggers
	NodeDatabaseTrigger
	NodeSequences
	NodeSequence
	NodeSynonyms
	NodeSynonym
	NodeTypes
	NodeSystemDataTypes
	NodeSystemDataType
	NodeUserDefinedDataTypes
	NodeUserDefinedDataType
	NodeUserDefinedTableTypes
	NodeUserDefinedTableType
	NodeUserDefinedTypes
	NodeUserDefinedType
	NodeXMLSchemaCollections
	NodeXMLSchemaCollection
	NodeAssemblies
	NodeAssembly
	NodeRules
	NodeRule
	NodeDefaults
	NodeDefault
	NodePlanGuides
	NodePlanGuide
	NodeServiceBroker
	NodeMessageTypes
	NodeMessageType
	NodeContracts
	NodeContract
	NodeBrokerQueues
	NodeBrokerQueue
	NodeBrokerServices
	NodeBrokerService
	NodeRoutes
	NodeRoute
	NodeRemoteServiceBindings
	NodeRemoteServiceBinding
	NodeBrokerPriorities
	NodeBrokerPriority
	NodeExternalResources
	NodeExternalDataSources
	NodeExternalDataSource
	NodeExternalFileFormats
	NodeExternalFileFormat
	NodeExternalLibraries
	NodeExternalLibrary
	NodeForeignKey
	NodeChecks
	NodeCheck
	NodeStorage
	NodePartitionFunctions
	NodePartitionFunction
	NodePartitionSchemes
	NodePartitionScheme
	NodeDatabaseAuditSpecifications
	NodeDatabaseAuditSpecification
	NodeDatabaseScopedCredentials
	NodeDatabaseScopedCredential
	NodeAsymmetricKeys
	NodeAsymmetricKey
	NodeCertificates
	NodeCertificate
	NodeSymmetricKeys
	NodeSymmetricKey
	NodeMasterKey
	NodeSecurityPolicies
	NodeSecurityPolicy
	NodeAlwaysEncryptedKeys
	NodeQueryStore
	NodeQueryStoreReport
	NodeColumnMasterKeys
	NodeColumnMasterKey
	NodeColumnEncryptionKeys
	NodeColumnEncryptionKey
	NodeExtendedEvents
	NodeEventSessions
	NodeEventSession
	NodeEventTarget
	NodeXEventProfiler
	NodeXEventProfilerSession
	NodeResourceGovernor
	NodeResourcePools
	NodeResourcePool
	NodeWorkloadGroups
	NodeWorkloadGroup
	NodeExternalResourcePools
	NodeExternalResourcePool
	NodeDatabaseMail
	NodeLoading
	NodeError

	// nodeTypeCount is the number of NodeTypes, for tests that must cover
	// every one — a new type added above is silently missing from a
	// hand-written list, and cannot be missing from 0..nodeTypeCount.
	// Keep it last.
	nodeTypeCount
)

// isContainerNode reports whether t is a grouping ("folder") node — e.g.
// "Tables", "Views" — rather than a concrete SQL Server object.
func isContainerNode(t NodeType) bool {
	switch t {
	case NodeDatabases, NodeSystemDatabases, NodeDatabaseSnapshots, NodeTables,
		NodeSystemTables, NodeFileTables, NodeExternalTables, NodeGraphTables,
		NodeColumns, NodeKeys, NodeIndexes,
		NodeStatistics, NodeViews, NodeSystemViews,
		NodeStoredProcedures, NodeSystemProcedures, NodeFunctions, NodeSystemFunctions,
		NodeSecurity, NodeLogins,
		NodeServerRoles, NodeCredentials, NodeCryptographicProviders,
		NodeAudits, NodeServerAuditSpecifications,
		NodeServerObjects, NodeBackupDevices, NodeServerTriggers, NodeEndpoints,
		NodeManagement, NodeSQLServerLogs,
		NodeAgentErrorLogs, NodeAgentJobs, NodeLinkedServers,
		NodeDatabaseSecurity, NodeUsers, NodeDatabaseRoles, NodeSchemas,
		NodeTriggers, NodeProgrammability, NodeDatabaseTriggers,
		NodeSequences, NodeSynonyms, NodeChecks,
		NodeTypes, NodeSystemDataTypes, NodeUserDefinedDataTypes,
		NodeUserDefinedTableTypes, NodeUserDefinedTypes, NodeXMLSchemaCollections,
		NodeAssemblies, NodeRules, NodeDefaults, NodePlanGuides,
		NodeServiceBroker, NodeMessageTypes, NodeContracts, NodeBrokerQueues,
		NodeBrokerServices, NodeRoutes, NodeRemoteServiceBindings,
		NodeBrokerPriorities,
		NodeExternalResources, NodeExternalDataSources, NodeExternalFileFormats,
		NodeExternalLibraries,
		NodeStorage, NodePartitionFunctions, NodePartitionSchemes,
		NodeDatabaseAuditSpecifications, NodeDatabaseScopedCredentials,
		NodeAsymmetricKeys, NodeCertificates, NodeSymmetricKeys,
		NodeSecurityPolicies, NodeAlwaysEncryptedKeys, NodeQueryStore,
		NodeColumnMasterKeys, NodeColumnEncryptionKeys,
		NodeAgentJobsFolder, NodeAgentUserJobs, NodeAgentSystemJobs,
		NodeAgentSchedules, NodeAgentAlerts, NodeAgentEventAlerts,
		NodeAgentOperators, NodeAgentAdmin,
		NodeAlwaysOn, NodeAvailabilityGroups, NodeAvailabilityReplicas,
		NodeAvailabilityDatabases, NodeAGListeners,
		NodeExtendedEvents, NodeEventSessions, NodeXEventProfiler,
		NodeResourcePools, NodeWorkloadGroups, NodeExternalResourcePools:
		return true
	}
	return false
}

// nodeTypeName returns a human-readable name for the node type.
func nodeTypeName(t NodeType) string {
	switch t {
	case NodeServer:
		return "Server"
	case NodeDatabase:
		return "Database"
	case NodeDatabaseSnapshot:
		return "Database Snapshot"
	case NodeTable:
		return "Table"
	case NodeView:
		return "View"
	case NodeStoredProcedure:
		return "Stored Procedure"
	case NodeFunction:
		return "Function"
	case NodeLogin:
		return "Login"
	case NodeUser:
		return "User"
	case NodeCredential:
		return "Credential"
	case NodeDatabaseScopedCredential:
		return "Database Scoped Credential"
	case NodeAsymmetricKey:
		return "Asymmetric Key"
	case NodeCertificate:
		return "Certificate"
	case NodeSymmetricKey:
		return "Symmetric Key"
	case NodeMasterKey:
		return "Database Master Key"
	case NodeCryptographicProvider:
		return "Cryptographic Provider"
	case NodeAudit:
		return "Audit"
	case NodeServerAuditSpecification:
		return "Server Audit Specification"
	case NodeDatabaseAuditSpecification:
		return "Database Audit Specification"
	case NodeBackupDevice:
		return "Backup Device"
	case NodeServerTrigger:
		return "Server Trigger"
	case NodeDatabaseTrigger:
		return "Database Trigger"
	case NodeEndpoint:
		return "Endpoint"
	case NodeAvailabilityGroup:
		return "Availability Group"
	case NodeAvailabilityReplica:
		return "Availability Replica"
	case NodeAvailabilityDatabase:
		return "Availability Database"
	case NodeAGListener:
		return "Availability Group Listener"
	case NodeSQLServerLog:
		return "SQL Server Log"
	case NodeAgentErrorLog:
		return "SQL Server Agent Error Log"
	case NodePartitionFunction:
		return "Partition Function"
	case NodePartitionScheme:
		return "Partition Scheme"
	case NodeSecurityPolicy:
		return "Security Policy"
	case NodeQueryStoreReport:
		return "Query Store Report"
	case NodeColumnMasterKey:
		return "Column Master Key"
	case NodeColumnEncryptionKey:
		return "Column Encryption Key"
	case NodeSystemDataType:
		return "System Data Type"
	case NodeUserDefinedDataType:
		return "User-Defined Data Type"
	case NodeUserDefinedTableType:
		return "User-Defined Table Type"
	case NodeUserDefinedType:
		return "User-Defined Type"
	case NodeXMLSchemaCollection:
		return "XML Schema Collection"
	case NodeAssembly:
		return "Assembly"
	case NodeRule:
		return "Rule"
	case NodeDefault:
		return "Default"
	case NodePlanGuide:
		return "Plan Guide"
	case NodeMessageType:
		return "Message Type"
	case NodeContract:
		return "Contract"
	case NodeBrokerQueue:
		return "Queue"
	case NodeBrokerService:
		return "Service"
	case NodeRoute:
		return "Route"
	case NodeRemoteServiceBinding:
		return "Remote Service Binding"
	case NodeBrokerPriority:
		return "Broker Priority"
	case NodeExternalDataSource:
		return "External Data Source"
	case NodeExternalFileFormat:
		return "External File Format"
	case NodeExternalLibrary:
		return "External Library"
	case NodeEventSession:
		return "Event Session"
	case NodeEventTarget:
		return "Event Session Target"
	case NodeXEventProfilerSession:
		return "XEvent Profiler Session"
	case NodeResourceGovernor:
		return "Resource Governor"
	case NodeResourcePool:
		return "Resource Pool"
	case NodeWorkloadGroup:
		return "Workload Group"
	case NodeExternalResourcePool:
		return "External Resource Pool"
	case NodeDatabaseMail:
		return "Database Mail"
	default:
		return "Object"
	}
}

// hasChildren reports whether this node type can ever have children.
func hasChildren(t NodeType) bool {
	switch t {
	case NodeColumn, NodeLogin, NodeUser, NodeServerRole, NodeDatabaseRole,
		NodeCredential, NodeDatabaseScopedCredential, NodeCertificate,
		NodeAsymmetricKey, NodeSymmetricKey, NodeMasterKey, NodeCryptographicProvider,
		NodeAudit, NodeServerAuditSpecification,
		NodeDatabaseAuditSpecification,
		NodeBackupDevice, NodeServerTrigger, NodeDatabaseTrigger, NodeEndpoint,
		NodeSchema, NodeForeignKey, NodeCheck, NodeSequence, NodeSynonym,
		NodeSystemDataType, NodeUserDefinedDataType, NodeUserDefinedTableType,
		NodeUserDefinedType, NodeXMLSchemaCollection,
		NodeAssembly, NodeRule, NodeDefault, NodePlanGuide,
		NodeMessageType, NodeContract, NodeBrokerQueue, NodeBrokerService,
		NodeRoute, NodeRemoteServiceBinding, NodeBrokerPriority,
		NodeExternalDataSource, NodeExternalFileFormat, NodeExternalLibrary,
		NodeIndex, NodeTrigger, NodeKey, NodeStatistic,
		NodeStoredProcedure, NodeFunction, NodeAgentJob, NodeLinkedServer,
		NodeAgentJobActivity, NodeAgentJobHistory, NodeAgentJobCategories,
		NodeAgentSchedule, NodeAgentAlert, NodeAgentAlertCategories,
		NodeAgentOperator, NodeAgentReport, NodeQueryStoreReport,
		NodeSQLServerLog, NodeAgentErrorLog,
		NodeAvailabilityReplica, NodeAvailabilityDatabase, NodeAGListener,
		NodePartitionFunction, NodePartitionScheme, NodeSecurityPolicy,
		NodeColumnMasterKey, NodeColumnEncryptionKey,
		NodeEventTarget, NodeXEventProfilerSession,
		NodeWorkloadGroup, NodeExternalResourcePool, NodeDatabaseMail,
		NodeLoading, NodeError:
		return false
	}
	return true
}

// nodeData is the application-specific payload attached to each
// controls.TreeNode via its Tag field. Name is the object's bare,
// schema-free name — never recover it by slicing Label, which is
// presentation-only and free to carry a schema prefix, an icon, or anything
// else display wants. TableName is the owning table's bare name for a node
// scoped under a table (NodeIndex, NodeStatistic, NodeKey, NodeForeignKey):
// Schema/Name on those point at the index's, statistic's or key's own
// schema/name, so the table name would be lost once
// loadIndexesChildren/loadStatisticsChildren/loadKeysChildren flatten their
// parent folder away. IsPrimaryKey is dual-purpose: for NodeColumn it
// overrides the column's icon (see nodeIcon); for NodeKey it's set from the
// backing index so showKeyPropertiesFor can title the dialog "Primary Key
// Properties" vs. "Unique Key Properties" without another round trip.
type nodeData struct {
	Type         NodeType
	Schema       string
	Name         string
	TableName    string
	DBName       string
	Loaded       bool
	IsPrimaryKey bool
	IsOffline    bool
	// IsSystem marks an object SQL Server owns — a system database, a sys-schema
	// view/procedure/function, a SQL-Server-created Agent job. The loaders of the
	// System * folders emit the same node types as the user ones, so without this
	// flag nothing downstream can tell master from a user database. Delete and
	// Rename gate on it (see objectOpsMenuItems): renaming a system database runs
	// SET SINGLE_USER WITH ROLLBACK IMMEDIATE before the server refuses the
	// rename, and renaming a system Agent job succeeds outright.
	IsSystem bool
	// IsEnabled mirrors the object's own enabled flag — a SQL Server Agent
	// job, schedule, alert or operator, a server trigger, a server audit or
	// audit specification, a security policy, an endpoint, where it means
	// STARTED, and an event session, where it means running (nodeIcon draws
	// a stopped one hollow). Set at load time so the context menu can offer a single
	// "Enable"/"Disable" toggle (see nodeIcon's IsOffline for the same
	// one-flag-drives-the-presentation idiom).
	IsEnabled bool

	// HasPrivateKey is whether a NodeCertificate or NodeAsymmetricKey holds its
	// private key, read at load time: Remove Private Key is withheld without
	// one, and the label does not say.
	HasPrivateKey bool

	// SourceDatabase is the database a NodeDatabaseSnapshot was taken of.
	// Restore-from-snapshot acts on it, and the folder's detail pane shows
	// it; the label is the snapshot's own name alone, so nothing downstream
	// can recover it. Empty when the source has since been dropped, which
	// leaves the snapshot in the catalog and unusable.
	SourceDatabase string

	// ScopeSchema and ScopeName are the routine an OBJECT-scoped
	// NodePlanGuide is bound to, empty for the SQL and TEMPLATE scopes. The
	// guide's Delete is permitted by ALTER on that routine (objectDataRights),
	// and nothing in the label names it.
	ScopeSchema string
	ScopeName   string

	// AGName is the owning availability group's name for any node under it
	// (the Replicas/Databases/Listeners folders and their leaves). Same role
	// TableName plays for table-scoped nodes: Name on a leaf points at the
	// replica or listener itself, so the group would otherwise be lost once
	// the folder above is flattened away.
	AGName string

	// AGSuspended and AGIsPrimary carry the two pieces of availability state
	// the Always On context menus gate on — whether this database's data
	// movement is suspended, and whether this replica is currently the
	// primary. Both are already known when the node is built and neither can
	// be recovered from the label, which is a rendered string.
	AGSuspended bool
	AGIsPrimary bool

	// AGLocalSecondary and AGLocalJoined describe the copy of this availability
	// database held by the instance the tree is connected to: whether that
	// instance is a secondary for the group, and whether its own copy has
	// joined. Joining and unjoining are ALTER DATABASE statements that act on
	// that one copy, so both facts are about the local instance even though the
	// folder above is read from the primary.
	AGLocalSecondary bool
	AGLocalJoined    bool

	// FuncType is a NodeFunction's sys.objects type — T-SQL or CLR, scalar
	// or table-valued. Read at load time because the "Script Function as
	// SELECT" template differs between them (a scalar function is selected,
	// a table-valued one selected *from*) and nothing downstream can recover
	// it from the label.
	FuncType gosmo.FunctionType

	// CreateDate and IsMemoryOptimized back the Object Explorer folder
	// filter's "Creation Date" and "Is Memory Optimized" criteria (see
	// explorer_filter.go). Only the loaders whose folder offers the property
	// populate them — filterProps and the loaders must stay in step, since a
	// criterion matched against a zero CreateDate rejects every row.
	CreateDate        time.Time
	IsMemoryOptimized bool

	// Filter is this folder node's Object Explorer filter, nil when it has
	// none. Applied by fetchChildren to whatever the folder's loader
	// returned, so it survives a Refresh and a collapse/expand alike.
	Filter *nodeFilter

	// LogType and LogNumber address the error-log file a NodeSQLServerLog or
	// NodeAgentErrorLog leaf stands for: which family, and which archive
	// number within it. Both are needed to open the viewer on that file, and
	// neither can be recovered from the label, which is a rendered date.
	LogType   gosmo.ErrorLogType
	LogNumber int

	// XESession is the event session a NodeEventTarget belongs to. Name on
	// the leaf is the target's own name (event_file), which is unique only
	// within its session — the TableName idiom, one level up.
	XESession string

	// RGPool is the resource pool a NodeWorkloadGroups folder lists the
	// groups of. The folder's label is the fixed "Workload Groups", and its
	// Name is left empty as every folder's is.
	RGPool string
	// RGPending is the Resource Governor node's "(Reconfiguration pending)":
	// stored changes waiting for RECONFIGURE on an enabled governor. Its
	// Reconfigure item is offered only then.
	RGPending bool
	// MailState is the Database Mail node's state, as its label shows it, and
	// MailStateKnown whether it could be read. Start or Stop is offered by
	// it, and Start, Stop and Send Test E-Mail are withheld while 'Database
	// Mail XPs' is off, where the server refuses all three (Msg 15281).
	// MailDisabled is the zero value, so an unread state must not be taken
	// for it: unknown withholds nothing.
	MailState      gosmo.MailState
	MailStateKnown bool

	conn *db.ServerConn
}
