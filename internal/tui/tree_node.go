package tui

import (
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// formatSQLDate formats a time.Time the way SSMS displays dates in object
// properties. gosmo returns time.Time (not string) for every CreateDate/
// ModifyDate, so every caller putting a date into a []string grid row needs it.
func formatSQLDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

// NodeType identifies what kind of SQL Server object an explorer node
// represents. Application-specific domain data; the generic tree
// rendering/navigation lives in tuikit/controls.TreeView.
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
	NodeReplication
	NodeLocalPublications
	NodePublication
	NodeLocalSubscriptions
	NodeLocalSubscription
	NodeFullTextCatalogs
	NodeFullTextCatalog
	NodeFullTextStoplists
	NodeFullTextStoplist
	NodeSearchPropertyLists
	NodeSearchPropertyList
	// NodeViewColumns and NodeViewColumn are a view's Columns folder and its
	// columns: types of their own, not NodeColumns/NodeColumn, because every
	// table-column action (Delete, Rename) is ALTER TABLE, which a view refuses.
	NodeViewColumns
	NodeViewColumn
	NodeLoading
	NodeError

	// nodeTypeCount is the number of NodeTypes, for tests that must cover every
	// one (a hand-written list silently misses a new type; 0..nodeTypeCount can't).
	// Keep it last.
	nodeTypeCount
)

// nodeData is the application-specific payload attached to each
// controls.TreeNode via its Tag field. Name is the object's bare, schema-free
// name; never recover it by slicing Label, which is presentation-only.
// TableName is the owning table's bare name for a node scoped under a table
// (NodeIndex, NodeStatistic, NodeKey, NodeForeignKey): Schema/Name on those
// point at the index's, statistic's or key's own schema/name, so the table name
// would be lost once loadIndexesChildren/loadStatisticsChildren/
// loadKeysChildren flatten their parent folder away. IsPrimaryKey is
// dual-purpose: for NodeColumn it overrides the column's icon (see nodeIcon);
// for NodeKey it's set from the backing index so showKeyPropertiesFor can title
// the dialog "Primary Key Properties" vs. "Unique Key Properties" without
// another round trip.
type nodeData struct {
	Type      NodeType
	Schema    string
	Name      string
	TableName string
	DBName    string
	// OnView marks a view's Indexes and Statistics folders and their leaves,
	// where TableName (or the folder's Name) is the view's: what an index or
	// statistic is on decides the extended-property level (VIEW, not TABLE)
	// and which New Index kinds the folder offers, before anything is read.
	OnView       bool
	Loaded       bool
	IsPrimaryKey bool
	IsOffline    bool
	// DBState is a NodeDatabase's sys.databases state_desc (ONLINE, OFFLINE,
	// RESTORING, …), read at load time; IsOffline is DBState != gosmo.DatabaseOnline. The
	// delete confirmation names what a drop in that state leaves on disk.
	DBState gosmo.DatabaseState
	// DBReadOnly is a NodeDatabase's sys.databases is_read_only, read with
	// DBState: Shrink is not offered on one, as every DBCC SHRINK* fails there.
	DBReadOnly bool
	// IsSystem marks an object SQL Server owns: a system database, a sys-schema
	// view/procedure/function, a SQL-Server-created Agent job. The System * folder
	// loaders emit the same node types as the user ones, so without this flag
	// nothing downstream can tell master from a user database. Delete and Rename
	// gate on it (see objectOpsMenuItems): renaming a system database runs SET
	// SINGLE_USER WITH ROLLBACK IMMEDIATE before the server refuses, and renaming a
	// system Agent job succeeds outright.
	IsSystem bool
	// IsDistributor marks a NodeDatabase that is a replication Distributor's
	// distribution database (gosmo Database.IsDistributor). It is also
	// IsSystem, so Delete, Rename and Detach are hidden as on master; this flag
	// additionally hides Take Offline/Bring Online — SSMS 21 offers neither on
	// it (checked 2026-10-16). It is removed with sp_dropdistributiondb.
	IsDistributor bool
	// IsEnabled mirrors the object's own enabled flag: an Agent job, schedule,
	// alert or operator, a server trigger, a server audit or audit specification,
	// a security policy, an endpoint (STARTED), and an event session (running;
	// nodeIcon draws a stopped one hollow). Set at load time so the context menu
	// can offer a single "Enable"/"Disable" toggle (see nodeIcon's IsOffline for
	// the same one-flag idiom).
	IsEnabled bool

	// HasPrivateKey is whether a NodeCertificate or NodeAsymmetricKey holds its
	// private key, read at load time: Remove Private Key is withheld without one,
	// and the label does not say.
	HasPrivateKey bool

	// SourceDatabase is the database a NodeDatabaseSnapshot was taken of.
	// Restore-from-snapshot acts on it and the folder's detail pane shows it; the
	// label is the snapshot's own name, so nothing downstream can recover it. Empty
	// when the source has since been dropped, leaving the snapshot unusable.
	SourceDatabase string

	// ScopeSchema and ScopeName are the routine an OBJECT-scoped NodePlanGuide is
	// bound to, empty for SQL and TEMPLATE scopes. The guide's Delete is permitted
	// by ALTER on that routine (objectDataRights); the label doesn't name it.
	ScopeSchema string
	ScopeName   string

	// AGName is the owning availability group's name for any node under it (the
	// Replicas/Databases/Listeners folders and their leaves). The role TableName
	// plays for table-scoped nodes: Name on a leaf is the replica or listener, so
	// the group would be lost once the folder above is flattened away.
	AGName string

	// UnregisteredPeer is, on a NodeError note, the instance a read could not
	// follow to because it has no registered connection (agView.unregistered).
	// Activating the note offers the Connect prompt for it.
	UnregisteredPeer string

	// AGSuspended and AGIsPrimary carry the two pieces of availability state the
	// Always On context menus gate on: whether this database's data movement is
	// suspended, and whether this replica is currently the primary. Both are known
	// when the node is built and not recoverable from the rendered label.
	AGSuspended bool
	AGIsPrimary bool

	// AGLocalSecondary and AGLocalJoined describe the copy of this availability
	// database held by the instance the tree is connected to: whether it is a
	// secondary for the group, and whether its own copy has joined. Joining and
	// unjoining are ALTER DATABASE statements acting on that one copy, so both
	// facts are about the local instance even though the folder above is read from
	// the primary.
	AGLocalSecondary bool
	AGLocalJoined    bool

	// FuncType is a NodeFunction's sys.objects type (T-SQL or CLR, scalar or
	// table-valued). Read at load time because the "Script Function as SELECT"
	// template differs between them (a scalar function is selected, a
	// table-valued one selected *from*) and the label doesn't carry it.
	FuncType gosmo.FunctionType

	// CreateDate and IsMemoryOptimized back the Object Explorer folder filter's
	// "Creation Date" and "Is Memory Optimized" criteria (see explorer_filter.go).
	// Only loaders whose folder offers the property populate them; filterProps and
	// the loaders must stay in step, since a criterion matched against a zero
	// CreateDate rejects every row.
	CreateDate        time.Time
	IsMemoryOptimized bool

	// Filter is this folder node's Object Explorer filter, nil when none. Applied
	// by fetchChildren to whatever the folder's loader returned, so it survives a
	// Refresh and a collapse/expand alike.
	Filter *nodeFilter

	// LogType and LogNumber address the error-log file a NodeSQLServerLog or
	// NodeAgentErrorLog leaf stands for: which family, and which archive number.
	// Both are needed to open the viewer and neither is recoverable from the label,
	// which is a rendered date.
	LogType   gosmo.ErrorLogType
	LogNumber int

	// AgentScheduleID is a NodeAgentSchedule's msdb schedule_id. Schedule names are
	// not unique (SSMS's New Job > Schedules makes one schedule per job, so several
	// jobs scheduled "Daily" leave several schedules named Daily), and such nodes
	// carry the same label, as SSMS shows them. Every action reads and writes by
	// this id: by name, msdb refuses (Msg 14371) and ScheduleByName answers
	// ErrAmbiguous.
	AgentScheduleID int
	// AgentScheduleDesc is the schedule's frequency (gosmo Schedule.Description) as
	// of the folder's load: what tells namesakes apart in the Delete confirmation.
	AgentScheduleDesc string

	// XESession is the event session a NodeEventTarget belongs to. Name on the leaf
	// is the target's own name (event_file), unique only within its session (the
	// TableName idiom, one level up).
	XESession string

	// RGPool is the resource pool a NodeWorkloadGroups folder lists the groups of.
	// The folder's label is the fixed "Workload Groups", and its Name is empty as
	// every folder's is.
	RGPool string
	// RGPending is the Resource Governor node's "(Reconfiguration pending)": stored
	// changes waiting for RECONFIGURE on an enabled governor. Its Reconfigure item
	// is offered only then.
	RGPending bool
	// MailState is the Database Mail node's state, as its label shows it, and
	// MailStateKnown whether it could be read. Start or Stop is offered by it, and
	// Start, Stop and Send Test E-Mail are withheld while 'Database Mail XPs' is
	// off, where the server refuses all three (Msg 15281). MailDisabled is the zero
	// value, so an unread state must not be taken for it: unknown withholds
	// nothing.
	MailState      gosmo.MailState
	MailStateKnown bool

	// ReplPubType is the kind of publication a NodePublication is, or a
	// NodeLocalSubscription subscribes to; a publication's icon shows it, as
	// SSMS's does. ReplPublisher and ReplPublisherDB are where a
	// NodeLocalSubscription's publication lives: Name is the publication's
	// name, which one database can subscribe to from two publishers, so both
	// are in its nodeKey.
	ReplPubType     gosmo.PublicationType
	ReplPublisher   string
	ReplPublisherDB string

	conn *db.ServerConn
}
