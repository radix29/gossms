package tui

import (
	"context"
	"errors"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
)

// detailRuns is the panel's latest plus the per-node bookkeeping latest does not
// model: pending holds, per node, the token of the most recent fetch dispatched
// for it.
//
// Reselecting a node mid-fetch dispatches a second fetch for the same pointer, and
// the first, though cancelled, can still land its final stage; without the token
// whichever finished last wins the cache write. An entry lives only while its
// fetch is in flight (the write that caches the result deletes it), so the map
// cannot accumulate one entry per node ever selected.
//
// Call stop and supersede, never the embedded Cancel and Abandon: those stop the
// run without evicting its pending entry, which the eviction exists to prevent
// (see stop).
type detailRuns struct {
	latest

	// node and token are the run Begin last started, until it is stopped;
	// node is nil when nothing is in flight.
	node  *explorerNode
	token int

	pending map[*explorerNode]int
}

// begin supersedes whatever run is in flight (stopping it and evicting its
// pending entry) and starts a new one for node under parent, the owning
// connection's context.
func (r *detailRuns) begin(parent context.Context, node *explorerNode) (context.Context, int) {
	r.stop()
	ctx, token := r.latest.Begin(parent)
	r.node, r.token = node, token
	r.pending[node] = token
	return ctx, token
}

// stop cancels the run in flight, if any, so its reads stop taking pool
// connections from the one that replaces it.
//
// Its pending entry goes with it, which keeps a cancelled fetch out of the
// cache: its reads fail with "context canceled" and its rows are part-filled,
// yet the final stage would still find pending[node] == token and cache that
// for good, making reselecting the node a permanent cache hit. The display half
// needs nothing: whoever stops a run supersedes it, or is about to.
func (r *detailRuns) stop() {
	if r.Idle() {
		return
	}
	r.evict(r.node, r.token)
	r.latest.Cancel()
	r.node = nil
}

// supersede is stop, plus discarding the stopped run's result: for a selection
// that ends without a new fetch to replace it.
func (r *detailRuns) supersede() {
	r.stop()
	r.latest.Abandon()
}

// end releases the context of the run dispatched at token once its final stage
// has landed, keeping its pending entry for the caller to claim. Without it the
// context stays registered under the connection's until the next selection
// cancels it. UI goroutine only.
func (r *detailRuns) end(token int) {
	if r.Idle() || r.token != token {
		return
	}
	r.latest.Cancel()
	r.node = nil
}

// evict drops node's pending entry if it still belongs to token, so a
// superseded fetch cannot drop a newer one's entry.
func (r *detailRuns) evict(node *explorerNode, token int) {
	if node != nil && r.pending[node] == token {
		delete(r.pending, node)
	}
}

// forget drops node's pending entry whatever run it belongs to: for a node
// leaving the tree or whose cache entry is being invalidated.
func (r *detailRuns) forget(node *explorerNode) { delete(r.pending, node) }

// fetch dispatches to a per-node-type loader. Types worth more than one round
// trip (NodeServer, NodeDatabases, NodeLogins) show their fast fields first and
// backfill progressively; the rest go through fetchNodeDetails.
//
// fetchCtx is the fetch's own, cancelled by detailRuns.stop on supersede; every
// loader derives each read's timeout from it, never from sc.Server.Context(),
// or cancelling it would stop nothing.
func (db *DetailBrowser) fetch(fetchCtx context.Context, app *App, sc *dbconn.ServerConn, node *explorerNode, seq int) {
	switch node.data.Type {
	case NodeServer:
		db.loadServerDetails(fetchCtx, app, sc, node, seq)
	case NodeDatabases:
		db.loadDatabasesFolderDetails(fetchCtx, app, sc, node, seq)
	case NodeLogins:
		db.loadLoginsDetails(fetchCtx, app, sc, node, seq)
	case NodeDatabase:
		db.loadDatabaseDetails(fetchCtx, app, sc, node, seq)
	case NodeTables, NodeSystemTables, NodeFileTables, NodeExternalTables, NodeGraphTables:
		db.loadTablesFolderDetails(fetchCtx, app, sc, node, seq)
	default:
		// The fetch reads a snapshot, never the live node (see explorerNode.snapshot).
		// node stays as the identity postFinal and panicRepair key off, both on the UI
		// goroutine.
		snap := node.snapshot()
		app.safegoRepair("loading Object Explorer details", db.panicRepair(node, seq), func() {
			ctx, cancel := context.WithTimeout(fetchCtx, childFetchTimeout)
			defer cancel()
			var objs []nodeData
			cols, rows, err := fetchNodeDetails(ctx, sc, snap, &objs)
			db.postFinalResult(app, node, seq, &detailResult{cols: cols, rows: rows, objs: objs, err: err})
		})
	}
}

// errDetailFetchPanicked is what the panel shows when a detail loader panicked.
// The stack is already logged (see reportPanic).
var errDetailFetchPanicked = errors.New("loading failed unexpectedly — see the log for details")

// panicRepair builds the safegoRepair step every loader in fetch shares: the panel
// is latched at "Loading..." (or a progressive loader's placeholder rows) and only
// postFinal clears it, which a panic never reaches.
//
// Nothing is cached: a panic says nothing about this node's details, so dropping
// the pending entry lets the next selection retry (unlike postFinal, which caches
// an ordinary error because the server answered). postFinal's two guards are kept:
// pending is cleared only if this fetch is still the newest for the node, and the
// grid touched only if its node is still selected.
func (db *DetailBrowser) panicRepair(node *explorerNode, seq int) func() {
	return func() {
		db.run.end(seq)
		db.run.evict(node, seq)
		if !db.run.Current(seq) {
			return
		}
		db.grid.SetError(errDetailFetchPanicked)
	}
}

// postPartial displays cols/rows immediately if node and seq are still current,
// without caching: a progressive loader's fast first stage.
func (db *DetailBrowser) postPartial(app *App, seq int, cols []string, rows [][]string) {
	db.postPartialObjects(app, seq, cols, rows, nil)
}

// postPartialObjects is postPartial for a view whose rows are objects.
func (db *DetailBrowser) postPartialObjects(app *App, seq int, cols []string, rows [][]string, objs []nodeData) {
	app.postAndWake(func() {
		if !db.run.Current(seq) {
			return
		}
		db.grid.SetFillLastColumn(isPropertyValueColumns(cols))
		db.setCharts(nil)
		db.grid.SetData(cols, rows)
		db.setRowObjects(rows, objs)
	})
}

// postFinal caches the completed result for node, unless a newer fetch has been
// dispatched since, and displays it if still current. Called once per fetch.
func (db *DetailBrowser) postFinal(app *App, node *explorerNode, seq int, cols []string, rows [][]string, err error) {
	db.postFinalResult(app, node, seq, &detailResult{cols: cols, rows: rows, err: err})
}

// postFinalResult is postFinal for a result carrying more than a grid: the
// row objects of a view whose rows are objects, or a chart strip.
func (db *DetailBrowser) postFinalResult(app *App, node *explorerNode, seq int, result *detailResult) {
	app.postAndWake(func() {
		db.cacheIfCurrent(node, seq, result)
		if !db.run.Current(seq) {
			return
		}
		db.applyResult(result)
	})
}

// cacheOnlyObjects caches the completed result without touching the grid: a
// progressive loader's last stage, where every row is already updated in place
// and postFinal's SetData would reset the scroll position. Gated by pending like
// postFinal. The objs mapping is cached with the rows so a reselect, served
// from the cache, still offers Delete; pass nil for a view whose rows are not
// objects.
func (db *DetailBrowser) cacheOnlyObjects(app *App, node *explorerNode, seq int, cols []string, rows [][]string, objs []nodeData, err error) {
	app.postAndWake(func() {
		db.cacheIfCurrent(node, seq, &detailResult{cols: cols, rows: rows, objs: objs, err: err})
	})
}

// cacheIfCurrent caches result for node if seq is still the newest fetch
// dispatched for it, and ends that fetch's pending entry. The one cache write
// every final stage shares; UI goroutine only. A fetch stopped by
// detailRuns.stop has no pending entry left, so nothing it produced is cached.
func (db *DetailBrowser) cacheIfCurrent(node *explorerNode, seq int, result *detailResult) {
	db.run.end(seq)
	if db.run.pending[node] != seq {
		return
	}
	db.cache[node] = result
	db.run.forget(node)
}

// maxRowFetchConcurrency bounds how many per-row backfill goroutines one
// progressive loader runs at once. Unbounded, a folder with hundreds of entries
// opens hundreds of connections against a pool whose MaxOpenConns is 20 and
// queues a redraw for each.
//
// The bound is per loader, not per connection: two folders loading at once fan
// out to 16, inside the pool, but the headroom is two loaders, not more.
const maxRowFetchConcurrency = 8

// fetchNodeDetails runs the gosmo queries for a node's detail grid. Called from a
// background goroutine, so it must not touch DetailBrowser or other UI state, only
// return data for the caller to apply via postAndWake. ctx bounds the whole call.
//
// objs is an out-parameter rather than a fourth result because only the loaders
// whose rows are objects use it: they append one nodeData per row, and every other
// loader leaves it nil, which withholds the pane's Delete. See detailResult.objs.
func fetchNodeDetails(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	if load, ok := detailLoaders[node.data.Type]; ok {
		return load(ctx, sc, node, objs)
	}
	if hasChildren(node.data.Type) {
		return fetchChildObjectsDetail(ctx, sc, node, objs)
	}
	return []string{"Property", "Value"}, [][]string{
		{"Name", node.label},
		{"Type", nodeTypeName(node.data.Type)},
		{"Database", node.data.DBName},
		{"Schema", node.data.Schema},
	}, nil
}

// detailLoader builds the Details grid for one node type. objs is the
// out-parameter fetchNodeDetails documents; a loader whose rows are not
// objects ignores it.
type detailLoader func(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error)

// serverDetail adapts a loader that needs only the connection.
func serverDetail(f func(context.Context, *dbconn.ServerConn) ([]string, [][]string, error)) detailLoader {
	return func(ctx context.Context, sc *dbconn.ServerConn, _ *explorerNode, _ *[]nodeData) ([]string, [][]string, error) {
		return f(ctx, sc)
	}
}

// nodeDetail adapts a loader whose rows are not objects.
func nodeDetail(f func(context.Context, *dbconn.ServerConn, *explorerNode) ([]string, [][]string, error)) detailLoader {
	return func(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, _ *[]nodeData) ([]string, [][]string, error) {
		return f(ctx, sc, node)
	}
}

// detailLoaders maps a NodeType to its purpose-built Details view. A type
// with no entry falls to fetchNodeDetails' fallback: a folder lists its
// children (fetchChildObjectsDetail), a leaf gets a Property/Value grid. The
// progressive types fetch dispatches itself never reach this table
// (TestDetailLoadersCoverage).
var detailLoaders = map[NodeType]detailLoader{
	NodeAgentJobs:            serverDetail(agentServerDetail),
	NodeAgentJob:             nodeDetail(agentJobDetail),
	NodeAgentSchedule:        nodeDetail(agentScheduleDetail),
	NodeAgentAlert:           nodeDetail(agentAlertDetail),
	NodeAgentOperator:        nodeDetail(agentOperatorDetail),
	NodeAgentJobActivity:     serverDetail(agentJobActivityDetail),
	NodeAgentJobHistory:      serverDetail(agentJobHistoryDetail),
	NodeAgentJobCategories:   serverDetail(agentJobCategoriesDetail),
	NodeAgentAlertCategories: serverDetail(agentAlertCategoriesDetail),
	NodeAgentReport: func(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, _ *[]nodeData) ([]string, [][]string, error) {
		return agentReportDetail(ctx, sc, node.data.Name)
	},
	NodeQueryStore: func(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, _ *[]nodeData) ([]string, [][]string, error) {
		return queryStoreFolderDetail(ctx, sc, node.data.DBName)
	},
	NodeQueryStoreReport: func(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, _ *[]nodeData) ([]string, [][]string, error) {
		return queryStoreReportDetail(ctx, sc, node.data.DBName, node.data.Name)
	},
	NodeSQLServerLogs: func(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, _ *[]nodeData) ([]string, [][]string, error) {
		return errorLogFilesDetail(ctx, sc, gosmo.ErrorLogSQLServer)
	},
	NodeAgentErrorLogs: func(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, _ *[]nodeData) ([]string, [][]string, error) {
		return errorLogFilesDetail(ctx, sc, gosmo.ErrorLogAgent)
	},

	NodeSQLServerLog:  nodeDetail(errorLogFileDetail),
	NodeAgentErrorLog: nodeDetail(errorLogFileDetail),

	NodeSystemDatabases:  systemDatabasesDetail,
	NodeViews:            viewsFolderDetail,
	NodeStoredProcedures: storedProceduresFolderDetail,

	NodePartitionFunction:   nodeDetail(storageSecurityDetail),
	NodePartitionScheme:     nodeDetail(storageSecurityDetail),
	NodeSecurityPolicy:      nodeDetail(storageSecurityDetail),
	NodeColumnMasterKey:     nodeDetail(storageSecurityDetail),
	NodeColumnEncryptionKey: nodeDetail(storageSecurityDetail),

	NodeSystemDataTypes:       programmabilityFolderDetail,
	NodeUserDefinedDataTypes:  programmabilityFolderDetail,
	NodeUserDefinedTableTypes: programmabilityFolderDetail,
	NodeUserDefinedTypes:      programmabilityFolderDetail,
	NodeXMLSchemaCollections:  programmabilityFolderDetail,
	NodeAssemblies:            programmabilityFolderDetail,
	NodeRules:                 programmabilityFolderDetail,
	NodeDefaults:              programmabilityFolderDetail,

	NodePlanGuides: planGuidesFolderDetail,

	NodeSystemDataType:       nodeDetail(programmabilityDetail),
	NodeUserDefinedDataType:  nodeDetail(programmabilityDetail),
	NodeUserDefinedTableType: nodeDetail(programmabilityDetail),
	NodeUserDefinedType:      nodeDetail(programmabilityDetail),
	NodeXMLSchemaCollection:  nodeDetail(programmabilityDetail),
	NodeAssembly:             nodeDetail(programmabilityDetail),
	NodeRule:                 nodeDetail(programmabilityDetail),
	NodeDefault:              nodeDetail(programmabilityDetail),
	NodePlanGuide:            nodeDetail(programmabilityDetail),

	NodeMessageTypes:          serviceBrokerFolderDetail,
	NodeContracts:             serviceBrokerFolderDetail,
	NodeBrokerQueues:          serviceBrokerFolderDetail,
	NodeBrokerServices:        serviceBrokerFolderDetail,
	NodeRoutes:                serviceBrokerFolderDetail,
	NodeRemoteServiceBindings: serviceBrokerFolderDetail,
	NodeBrokerPriorities:      serviceBrokerFolderDetail,

	NodeMessageType:          nodeDetail(serviceBrokerDetail),
	NodeContract:             nodeDetail(serviceBrokerDetail),
	NodeBrokerQueue:          nodeDetail(serviceBrokerDetail),
	NodeBrokerService:        nodeDetail(serviceBrokerDetail),
	NodeRoute:                nodeDetail(serviceBrokerDetail),
	NodeRemoteServiceBinding: nodeDetail(serviceBrokerDetail),
	NodeBrokerPriority:       nodeDetail(serviceBrokerDetail),

	NodeExternalDataSources: externalFolderDetail,
	NodeExternalFileFormats: externalFolderDetail,
	NodeExternalLibraries:   externalFolderDetail,

	NodeExternalDataSource: nodeDetail(externalDetail),
	NodeExternalFileFormat: nodeDetail(externalDetail),
	NodeExternalLibrary:    nodeDetail(externalDetail),

	NodeDatabaseSnapshots:           databaseSnapshotsFolderDetail,
	NodeDatabaseSnapshot:            nodeDetail(databaseSnapshotDetail),
	NodeCredentials:                 credentialsFolderDetail,
	NodeCredential:                  nodeDetail(credentialDetail),
	NodeCryptographicProviders:      cryptographicProvidersFolderDetail,
	NodeCryptographicProvider:       nodeDetail(cryptographicProviderDetail),
	NodeAudits:                      auditsFolderDetail,
	NodeAudit:                       nodeDetail(auditDetail),
	NodeServerAuditSpecifications:   serverAuditSpecificationsFolderDetail,
	NodeServerAuditSpecification:    nodeDetail(serverAuditSpecificationDetail),
	NodeDatabaseAuditSpecifications: databaseAuditSpecificationsFolderDetail,
	NodeDatabaseAuditSpecification:  nodeDetail(databaseAuditSpecificationDetail),
	NodeDatabaseScopedCredentials:   databaseScopedCredentialsFolderDetail,
	NodeDatabaseScopedCredential:    nodeDetail(databaseScopedCredentialDetail),
	NodeAsymmetricKeys:              asymmetricKeysFolderDetail,
	NodeAsymmetricKey:               nodeDetail(asymmetricKeyDetail),
	NodeCertificates:                certificatesFolderDetail,
	NodeCertificate:                 nodeDetail(certificateDetail),
	NodeSymmetricKeys:               symmetricKeysFolderDetail,
	NodeSymmetricKey:                nodeDetail(symmetricKeyDetail),
	NodeMasterKey:                   nodeDetail(masterKeyDetail),
	NodeBackupDevices:               backupDevicesFolderDetail,
	NodeBackupDevice:                nodeDetail(backupDeviceDetail),
	NodeServerTriggers:              serverTriggersFolderDetail,
	NodeServerTrigger:               nodeDetail(serverTriggerDetail),
	NodeDatabaseTriggers:            databaseTriggersFolderDetail,
	NodeDatabaseTrigger:             nodeDetail(databaseTriggerDetail),
	NodeEndpoints:                   endpointsFolderDetail,
	NodeEndpoint:                    nodeDetail(endpointDetail),
	NodeEventSessions:               eventSessionsFolderDetail,
	NodeEventSession:                nodeDetail(eventSessionDetail),
	NodeEventTarget:                 nodeDetail(eventTargetDetail),
	NodeResourceGovernor:            serverDetail(resourceGovernorDetail),
	NodeResourcePools:               resourcePoolsFolderDetail,
	NodeResourcePool:                nodeDetail(resourcePoolDetail),
	NodeWorkloadGroups:              workloadGroupsFolderDetail,
	NodeWorkloadGroup:               nodeDetail(workloadGroupDetail),
	NodeExternalResourcePools:       externalResourcePoolsFolderDetail,
	NodeExternalResourcePool:        nodeDetail(externalResourcePoolDetail),
	NodeDatabaseMail:                serverDetail(databaseMailDetail),
	NodeReplication:                 serverDetail(replicationDetail),
	NodeLocalPublications:           serverDetail(localPublicationsDetail),
	NodeLocalSubscriptions:          serverDetail(localSubscriptionsDetail),
	NodePublication:                 nodeDetail(publicationDetail),
	NodeLocalSubscription:           nodeDetail(localSubscriptionDetail),
	NodeFullTextCatalogs:            fullTextCatalogsFolderDetail,
	NodeFullTextCatalog:             nodeDetail(fullTextCatalogDetail),
	NodeFullTextStoplists:           fullTextStoplistsFolderDetail,
	NodeFullTextStoplist:            nodeDetail(fullTextStoplistDetail),
	NodeSearchPropertyLists:         searchPropertyListsFolderDetail,
	NodeSearchPropertyList:          nodeDetail(searchPropertyListDetail),

	NodeStoredProcedure: nodeDetail(moduleDetail),
	NodeFunction:        nodeDetail(moduleDetail),
	NodeTrigger:         nodeDetail(moduleDetail),
}

func systemDatabasesDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	dbs, err := sc.Server.Databases(ctx)
	if err != nil {
		return nil, nil, err
	}
	dbs = filterObjects(node.data.Filter, dbs, func(d *gosmo.Database) nodeData {
		return nodeData{Name: d.Name, CreateDate: d.CreateDate}
	})
	rows := make([][]string, 0, 4)
	for _, d := range dbs {
		if d.IsSystem() {
			rows = append(rows, []string{d.Name, d.State, string(d.RecoveryModel)})
		}
	}
	return []string{"Name", "State", "Recovery"}, rows, nil
}

func viewsFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	dbObj, err := sc.Server.DatabaseByName(ctx, node.data.DBName)
	if err != nil {
		return nil, nil, err
	}
	views, err := dbObj.ViewsFiltered(ctx, serverFilter(node.data.Filter))
	if err != nil {
		return nil, nil, err
	}
	views = filterObjects(node.data.Filter, views, func(v *gosmo.View) nodeData {
		return nodeData{Name: v.Name, Schema: v.Schema, CreateDate: v.CreateDate}
	})
	rows := make([][]string, 0, len(views))
	for _, v := range views {
		rows = append(rows, []string{v.Schema + "." + v.Name, formatSQLDate(v.CreateDate)})
		*objs = append(*objs, nodeData{Type: NodeView, DBName: node.data.DBName, Schema: v.Schema, Name: v.Name})
	}
	return []string{"Name", "Created"}, rows, nil
}

func storedProceduresFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	dbObj, err := sc.Server.DatabaseByName(ctx, node.data.DBName)
	if err != nil {
		return nil, nil, err
	}
	procs, err := dbObj.StoredProceduresFiltered(ctx, serverFilter(node.data.Filter))
	if err != nil {
		return nil, nil, err
	}
	procs = filterObjects(node.data.Filter, procs, func(p *gosmo.StoredProcedure) nodeData {
		return nodeData{Name: p.Name, Schema: p.Schema, CreateDate: p.CreateDate}
	})
	rows := make([][]string, 0, len(procs))
	for _, p := range procs {
		rows = append(rows, []string{p.Schema + "." + p.Name, formatSQLDate(p.CreateDate), formatSQLDate(p.ModifyDate)})
		*objs = append(*objs, nodeData{Type: NodeStoredProcedure, DBName: node.data.DBName, Schema: p.Schema, Name: p.Name})
	}
	return []string{"Name", "Created", "Modified"}, rows, nil
}

// fetchChildObjectsDetail is the fallback detail view for a node type with
// children but no purpose-built view: it lists the child objects, reusing the
// childLoaders entry the tree expands with, so a newly wired NodeType gets a
// folder-shaped detail view rather than a Property/Value grid.
func fetchChildObjectsDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	loader, ok := childLoaders[node.data.Type]
	if !ok {
		return []string{"Name"}, nil, nil
	}
	children, err := loader(loaderCtx{ctx: ctx, sc: sc}, node)
	if err != nil {
		return nil, nil, err
	}
	children = filterChildren(node.data.Filter, children)
	rows := make([][]string, 0, len(children))
	for _, c := range children {
		rows = append(rows, []string{c.label})
		// The child's own nodeData, not one rebuilt from the label: the label carries
		// type and state decoration ("IX_x (Nonclustered, Unique)") and nothing in it
		// identifies the table an index belongs to.
		*objs = append(*objs, c.data)
	}
	return []string{"Name"}, rows, nil
}
