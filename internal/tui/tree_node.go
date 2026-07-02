package tui

import "time"

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
	NodeDatabase
	NodeTables
	NodeTable
	NodeColumns
	NodeColumn
	NodeIndexes
	NodeIndex
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
	NodeForeignKeys
	NodeForeignKey
	NodeChecks
	NodeCheck
	NodeLoading
	NodeError
)

// nodeIcon returns the ASCII icon for a node type.
func nodeIcon(t NodeType) rune {
	switch t {
	case NodeServer:
		return 'S'
	case NodeDatabases, NodeTables, NodeViews, NodeStoredProcedures, NodeFunctions,
		NodeSecurity, NodeLogins, NodeServerRoles, NodeManagement,
		NodeAgentJobs, NodeLinkedServers, NodeDatabaseSecurity,
		NodeUsers, NodeDatabaseRoles, NodeSchemas, NodeTriggers,
		NodeSequences, NodeSynonyms, NodeColumns, NodeIndexes,
		NodeForeignKeys, NodeChecks:
		return '+'
	case NodeDatabase:
		return 'D'
	case NodeTable:
		return 'T'
	case NodeColumn:
		return 'c'
	case NodeIndex:
		return 'i'
	case NodeView:
		return 'V'
	case NodeStoredProcedure:
		return 'P'
	case NodeFunction:
		return 'F'
	case NodeLogin, NodeUser:
		return 'U'
	case NodeServerRole, NodeDatabaseRole:
		return 'R'
	case NodeAgentJob:
		return 'J'
	case NodeLinkedServer:
		return 'L'
	case NodeTrigger:
		return 't'
	case NodeSequence:
		return 'q'
	case NodeSynonym:
		return 's'
	case NodeForeignKey:
		return 'k'
	case NodeCheck:
		return 'x'
	case NodeLoading:
		return '.'
	case NodeError:
		return '!'
	default:
		return 'o'
	}
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
		NodeLoading, NodeError:
		return false
	}
	return true
}

// nodeData is the applicaton-specific payload attached to each
// controls.TreeNode via its Tag field.
type nodeData struct {
	Type    NodeType
	Schema  string
	DBName  string
	Loaded  bool
	connIdx int
}
