package tui

import (
	"time"

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
	NodeManagement
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
	NodeLinkedServers
	NodeLinkedServer
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
// instead of the generic folder icon its container status would otherwise
// give it — every other container node type has no specific identity of its
// own, but SQL Server Agent does, the same way SSMS gives it a distinct icon
// rather than a plain folder. d.IsPrimaryKey overrides the normal NodeColumn
// glyph with the primary-key glyph, since that's per-column data, not
// something the node's Type alone can express.
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
// normal NodeColumn icon — the 🗝/⚿ "Primary Key" glyph from todo/icons.md,
// shared with NodeKey (the same glyph a Keys-folder primary/unique key entry
// uses).
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
		NodeServerRoles, NodeManagement, NodeAgentJobs, NodeLinkedServers,
		NodeDatabaseSecurity, NodeUsers, NodeDatabaseRoles, NodeSchemas,
		NodeTriggers, NodeSequences, NodeSynonyms, NodeChecks,
		NodeAgentJobsFolder, NodeAgentUserJobs, NodeAgentSystemJobs,
		NodeAgentSchedules, NodeAgentAlerts, NodeAgentEventAlerts,
		NodeAgentOperators, NodeAgentAdmin:
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
	case NodeLinkedServer:
		return '🔗'
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
	case NodeLinkedServer:
		return '⇄'
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
// the plain '•' bullet, matching the portable glyph list in todo/icons.md,
// since '⁞' isn't guaranteed to render everywhere.
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
		NodeLoading, NodeError:
		return false
	}
	return true
}

// nodeData is the applicaton-specific payload attached to each
// controls.TreeNode via its Tag field. Name is the object's bare name
// (schema-free) — the one thing that must never be recovered by slicing
// Label, which is presentation-only and free to include the schema
// prefix, an icon, or anything else display wants. TableName is the owning
// table's bare name for a node scoped under a table (NodeIndex,
// NodeStatistic, NodeKey, NodeForeignKey) — Schema/Name on those already
// point at the index's, statistic's, or key's own schema/name, not the
// table's, so the table name would otherwise be lost once
// loadIndexesChildren/loadStatisticsChildren/loadKeysChildren flatten
// their parent folder node away. IsPrimaryKey is likewise dual-purpose: for
// NodeColumn it overrides
// the column's icon (see nodeIcon); for NodeKey it's set from the backing
// index's IsPrimaryKey so showKeyPropertiesFor can title the dialog
// "Primary Key Properties" vs. "Unique Key Properties" without a second
// round trip to the server.
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
	conn      *db.ServerConn
}
