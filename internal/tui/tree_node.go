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
	NodeStoredProcedures
	NodeStoredProcedure
	NodeFunctions
	NodeFunction
	NodeSecurity
	NodeLogins
	NodeLogin
	NodeServerRoles
	NodeServerRole
	NodeManagement
	NodeAgentJobs
	NodeAgentJob
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
// d.IsPrimaryKey overrides the normal NodeColumn glyph with the primary-key
// glyph, since that's per-column data, not something the node's Type alone
// can express.
func nodeIcon(d nodeData, style config.IconStyle, expanded bool) rune {
	if style == config.IconStyleNone {
		return 0
	}
	if isContainerNode(d.Type) {
		return folderIcon(style, expanded)
	}
	if d.Type == NodeColumn && d.IsPrimaryKey {
		return primaryKeyIcon(style)
	}
	return objectIcon(d.Type, style)
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
		NodeStatistics, NodeViews,
		NodeStoredProcedures, NodeFunctions, NodeSecurity, NodeLogins,
		NodeServerRoles, NodeManagement, NodeAgentJobs, NodeLinkedServers,
		NodeDatabaseSecurity, NodeUsers, NodeDatabaseRoles, NodeSchemas,
		NodeTriggers, NodeSequences, NodeSynonyms, NodeChecks:
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
		NodeLoading, NodeError:
		return false
	}
	return true
}

// nodeData is the applicaton-specific payload attached to each
// controls.TreeNode via its Tag field. Name is the object's bare name
// (schema-free) — the one thing that must never be recovered by slicing
// Label, which is presentation-only and free to include the schema
// prefix, an icon, or anything else display wants.
type nodeData struct {
	Type         NodeType
	Schema       string
	Name         string
	DBName       string
	Loaded       bool
	IsPrimaryKey bool
	conn         *db.ServerConn
}
