package tui

import (
	"context"
	"errors"
	"strings"

	"github.com/radix29/gossms/internal/db"
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
	NodeServer:           loadServerChildren,
	NodeDatabases:        loadDatabasesChildren,
	NodeSystemDatabases:  loadSystemDatabasesChildren,
	NodeDatabase:         loadDatabaseChildren,
	NodeDatabaseSecurity: loadDatabaseSecurityChildren,
	NodeUsers:            loadUsersChildren,
	NodeDatabaseRoles:    loadDatabaseRolesChildren,
	NodeSchemas:          loadSchemasChildren,

	NodeTables:           loadTablesChildren,
	NodeTable:            loadTableChildren,
	NodeColumns:          loadColumnsChildren,
	NodeKeys:             loadKeysChildren,
	NodeChecks:           loadConstraintsChildren,
	NodeIndexes:          loadIndexesChildren,
	NodeStatistics:       loadStatisticsChildren,
	NodeViews:            loadViewsChildren,
	NodeSystemViews:      loadSystemViewsChildren,
	NodeStoredProcedures: loadStoredProceduresChildren,
	NodeSystemProcedures: loadSystemProceduresChildren,
	NodeFunctions:        loadFunctionsChildren,
	NodeSystemFunctions:  loadSystemFunctionsChildren,
	NodeTriggers:         loadTriggersChildren,
	NodeSequences:        loadSequencesChildren,
	NodeSynonyms:         loadSynonymsChildren,

	NodeSecurity:    loadSecurityChildren,
	NodeLogins:      loadLoginsChildren,
	NodeServerRoles: loadServerRolesChildren,

	NodeManagement:    loadManagementChildren,
	NodeLinkedServers: loadLinkedServersChildren,

	NodeAgentJobs:        loadAgentRootChildren,
	NodeAgentJobsFolder:  loadAgentJobsFolderChildren,
	NodeAgentUserJobs:    loadAgentUserJobsChildren,
	NodeAgentSystemJobs:  loadAgentSystemJobsChildren,
	NodeAgentSchedules:   loadAgentSchedulesChildren,
	NodeAgentAlerts:      loadAgentAlertsChildren,
	NodeAgentEventAlerts: loadAgentEventAlertsChildren,
	NodeAgentOperators:   loadAgentOperatorsChildren,
	NodeAgentAdmin:       loadAgentAdminChildren,
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
	return children
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
func errExplorerNode(err error) *explorerNode {
	return &explorerNode{label: err.Error(), data: nodeData{Type: NodeError}}
}

// fqn brackets a schema-qualified SQL Server identifier for use in
// generated T-SQL ("SELECT TOP 1000 * FROM "+fqn(schema, name)). schema=""
// (e.g. a server-level object) omits the schema part entirely rather than
// emitting an empty "[]." prefix. Embedded "]" characters are doubled,
// same escaping rule SQL Server itself uses for bracketed identifiers.
func fqn(schema, name string) string {
	if schema == "" {
		return "[" + bracketEscape(name) + "]"
	}
	return "[" + bracketEscape(schema) + "].[" + bracketEscape(name) + "]"
}

func bracketEscape(s string) string { return strings.ReplaceAll(s, "]", "]]") }
