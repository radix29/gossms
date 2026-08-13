package tui

import (
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
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
	NodeDatabase
	NodeTables
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
	NodeServerObjects
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
	NodeSequences
	NodeSequence
	NodeSynonyms
	NodeSynonym
	NodeForeignKey
	NodeChecks
	NodeCheck
	NodeLoading
	NodeError
)

// nodeIcon returns the icon glyph for a node, in the given icon style.
// expanded only affects container ("folder") node types, which show a
// different glyph open vs. closed. style == config.IconStyleNone always
// returns 0 (no icon), which TreeView's Draw treats as "don't draw one".
// NodeAgentJobs (the "SQL Server Agent" node) gets a fixed stopwatch glyph
// rather than the generic folder icon its container status would give it.
// d.IsPrimaryKey overrides the normal NodeColumn glyph with the primary-key
// glyph, since that's per-column data, not something Type alone expresses.
func nodeIcon(d nodeData, style config.IconStyle, expanded bool) rune {
	if style == config.IconStyleNone {
		return 0
	}
	if d.Type == NodeAgentJobs {
		return '⏱'
	}
	if isContainerNode(d.Type) {
		return folderIcon(style, expanded)
	}
	if d.Type == NodeColumn && d.IsPrimaryKey {
		return primaryKeyIcon(style)
	}
	if d.Type == NodeDatabase && d.IsOffline {
		return offlineDatabaseIcon(style)
	}
	return objectIcon(d.Type, style)
}

// offlineDatabaseIcon returns the glyph substituted for a NodeDatabase
// that's currently offline — a hollow hexagon in the geometric styles
// (vs. the filled '⬢' an online database uses), a "powered off" glyph
// for Emoji.
func offlineDatabaseIcon(style config.IconStyle) rune {
	if style == config.IconStyleEmoji {
		return '📴'
	}
	return '⬡'
}

// primaryKeyIcon returns the glyph substituted for a primary-key column's
// normal NodeColumn icon — the 🗝/⚿ "Primary Key" glyph, shared with NodeKey
// (the same glyph a Keys-folder primary/unique key entry uses).
func primaryKeyIcon(style config.IconStyle) rune {
	if style == config.IconStyleEmoji {
		return '🗝'
	}
	return '⚿'
}

// isContainerNode reports whether t is a grouping ("folder") node — e.g.
// "Tables", "Views" — rather than a concrete SQL Server object.
func isContainerNode(t NodeType) bool {
	switch t {
	case NodeDatabases, NodeSystemDatabases, NodeTables, NodeColumns, NodeKeys, NodeIndexes,
		NodeStatistics, NodeViews, NodeSystemViews,
		NodeStoredProcedures, NodeSystemProcedures, NodeFunctions, NodeSystemFunctions,
		NodeSecurity, NodeLogins,
		NodeServerRoles, NodeServerObjects, NodeManagement, NodeSQLServerLogs,
		NodeAgentErrorLogs, NodeAgentJobs, NodeLinkedServers,
		NodeDatabaseSecurity, NodeUsers, NodeDatabaseRoles, NodeSchemas,
		NodeTriggers, NodeSequences, NodeSynonyms, NodeChecks,
		NodeAgentJobsFolder, NodeAgentUserJobs, NodeAgentSystemJobs,
		NodeAgentSchedules, NodeAgentAlerts, NodeAgentEventAlerts,
		NodeAgentOperators, NodeAgentAdmin,
		NodeAlwaysOn, NodeAvailabilityGroups, NodeAvailabilityReplicas,
		NodeAvailabilityDatabases, NodeAGListeners:
		return true
	}
	return false
}

// folderIcon returns the container glyph for a style, open vs. closed.
func folderIcon(style config.IconStyle, expanded bool) rune {
	if style == config.IconStyleEmoji {
		if expanded {
			return '📂'
		}
		return '📁'
	}
	// Symbols and Portable share the same geometric folder glyphs.
	if expanded {
		return '▾'
	}
	return '▸'
}

// objectIcon returns the glyph for a concrete (non-container) node type.
func objectIcon(t NodeType, style config.IconStyle) rune {
	switch style {
	case config.IconStyleEmoji:
		return objectIconEmoji(t)
	case config.IconStylePortable:
		return objectIconPortable(t)
	default: // Symbols
		return objectIconSymbols(t)
	}
}

func objectIconEmoji(t NodeType) rune {
	switch t {
	case NodeServer:
		return '🖥'
	case NodeDatabase:
		return '🛢'
	case NodeTable:
		return '▤'
	case NodeColumn:
		return '🏷'
	case NodeIndex:
		return '📇'
	case NodeKey:
		return '🗝'
	case NodeStatistic:
		return '📊'
	case NodeView:
		return '👁'
	case NodeStoredProcedure:
		return '⚙'
	case NodeFunction:
		return 'ƒ'
	case NodeLogin:
		return '🔐'
	case NodeUser:
		return '👤'
	case NodeServerRole, NodeDatabaseRole:
		return '🎭'
	case NodeAgentJob:
		return '⏱'
	case NodeAgentSchedule:
		return '📅'
	case NodeAgentAlert:
		return '🔔'
	case NodeAgentOperator:
		return '📞'
	case NodeAgentJobActivity:
		return '📈'
	case NodeAgentJobHistory:
		return '🕒'
	case NodeAgentJobCategories, NodeAgentAlertCategories:
		return '🗂'
	case NodeAgentReport:
		return '📋'
	case NodeSQLServerLog, NodeAgentErrorLog:
		return '📄'
	case NodeLinkedServer:
		return '🔗'
	case NodeAvailabilityGroup:
		return '🔄'
	case NodeAvailabilityReplica:
		return '🖧'
	case NodeAvailabilityDatabase:
		return '🛢'
	case NodeAGListener:
		return '📡'
	case NodeTrigger:
		return '⚡'
	case NodeSequence:
		return '🔢'
	case NodeSynonym:
		return '🔖'
	case NodeForeignKey:
		return '🔗'
	case NodeCheck:
		return '✔'
	case NodeSchema:
		return '🧩'
	case NodeLoading:
		return '⏳'
	case NodeError:
		return '⚠'
	default:
		return '•'
	}
}

func objectIconSymbols(t NodeType) rune {
	switch t {
	case NodeServer:
		return '◉'
	case NodeDatabase:
		return '⬢'
	case NodeTable:
		return '▦'
	case NodeColumn:
		return '⁞'
	case NodeIndex:
		return '⌗'
	case NodeKey:
		return '⚿'
	case NodeStatistic:
		return '▥'
	case NodeView:
		return '◫'
	case NodeStoredProcedure:
		return '⚙'
	case NodeFunction:
		return 'λ'
	case NodeLogin:
		return '⚿'
	case NodeUser:
		return '◇'
	case NodeServerRole, NodeDatabaseRole:
		return '▣'
	case NodeAgentJob:
		return '▶'
	case NodeAgentSchedule:
		return '◷'
	case NodeAgentAlert:
		return '◈'
	case NodeAgentOperator:
		return '☏'
	case NodeAgentJobActivity:
		return '▲'
	case NodeAgentJobHistory:
		return '↺'
	case NodeAgentJobCategories, NodeAgentAlertCategories:
		return '▨'
	case NodeAgentReport:
		return '≡'
	case NodeSQLServerLog, NodeAgentErrorLog:
		return '▤'
	case NodeLinkedServer:
		return '⇄'
	case NodeAvailabilityGroup:
		return '↻'
	case NodeAvailabilityReplica:
		return '⧉'
	case NodeAvailabilityDatabase:
		return '⬢'
	case NodeAGListener:
		return '◎'
	case NodeTrigger:
		return '⚡'
	case NodeSequence:
		return '↑'
	case NodeSynonym:
		return '≈'
	case NodeForeignKey:
		return '⛓'
	case NodeCheck:
		return '✓'
	case NodeSchema:
		return '▧'
	case NodeLoading:
		return '…'
	case NodeError:
		return '✗'
	default:
		return '•'
	}
}

// objectIconPortable is the Symbols set with one substitution: Column uses
// the plain '•' bullet, since '⁞' isn't guaranteed to render everywhere.
func objectIconPortable(t NodeType) rune {
	if t == NodeColumn {
		return '•'
	}
	return objectIconSymbols(t)
}

// nodeTypeName returns a human-readable name for the node type.
func nodeTypeName(t NodeType) string {
	switch t {
	case NodeServer:
		return "Server"
	case NodeDatabase:
		return "Database"
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
	default:
		return "Object"
	}
}

// hasChildren reports whether this node type can ever have children.
func hasChildren(t NodeType) bool {
	switch t {
	case NodeColumn, NodeLogin, NodeUser, NodeServerRole, NodeDatabaseRole,
		NodeSchema, NodeForeignKey, NodeCheck, NodeSequence, NodeSynonym,
		NodeIndex, NodeTrigger, NodeKey, NodeStatistic,
		NodeView, NodeStoredProcedure, NodeFunction, NodeAgentJob, NodeLinkedServer,
		NodeAgentJobActivity, NodeAgentJobHistory, NodeAgentJobCategories,
		NodeAgentSchedule, NodeAgentAlert, NodeAgentAlertCategories,
		NodeAgentOperator, NodeAgentReport,
		NodeSQLServerLog, NodeAgentErrorLog,
		NodeAvailabilityReplica, NodeAvailabilityDatabase, NodeAGListener,
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
	// IsEnabled mirrors a SQL Server Agent job/schedule/alert/operator's
	// own Enabled flag — set at load time so the context menu can offer a
	// single "Enable"/"Disable" toggle (see nodeIcon's IsOffline for the
	// same single-flag-drives-one-label idiom).
	IsEnabled bool

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

	conn *db.ServerConn
}
