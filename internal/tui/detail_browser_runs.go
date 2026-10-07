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
// Reselecting a node mid-fetch dispatches a second fetch for the same pointer,
// and the first, though cancelled, can still land its final stage; without the
// token whichever finished last wins the cache write. An entry lives only while
// its fetch is in flight (the write that caches the result deletes it), so the
// map cannot accumulate one entry per node ever selected.
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
			db.postFinalObjects(app, node, seq, cols, rows, objs, err)
		})
	}
}

// errDetailFetchPanicked is what the panel shows when a detail loader panicked.
// The stack is already logged (see reportPanic).
var errDetailFetchPanicked = errors.New("loading failed unexpectedly — see the log for details")

// panicRepair builds the safegoRepair step every loader in fetch shares: the
// panel is latched at "Loading..." (or a progressive loader's placeholder rows)
// and only postFinal clears it, which a panic never reaches.
//
// Nothing is cached: a panic says nothing about this node's details, so dropping
// the pending entry lets the next selection retry (unlike postFinal, which
// caches an ordinary error because the server answered). postFinal's two guards
// are kept: pending is cleared only if this fetch is still the newest for the
// node, and the grid touched only if its node is still selected.
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
	db.postFinalObjects(app, node, seq, cols, rows, nil, err)
}

// postFinalObjects is postFinal for a view whose rows are objects.
func (db *DetailBrowser) postFinalObjects(app *App, node *explorerNode, seq int, cols []string, rows [][]string, objs []nodeData, err error) {
	result := &detailResult{cols: cols, rows: rows, objs: objs, err: err}
	app.postAndWake(func() {
		db.cacheIfCurrent(node, seq, result)
		if !db.run.Current(seq) {
			return
		}
		db.applyResult(result)
	})
}

// postFinalCharts is postFinal for a view that draws a chart strip under its
// grid.
func (db *DetailBrowser) postFinalCharts(app *App, node *explorerNode, seq int, cols []string, rows [][]string, cs []detailChart, err error) {
	result := &detailResult{cols: cols, rows: rows, charts: cs, err: err}
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

// fetchNodeDetails runs the gosmo queries for a node's detail grid. Called from
// a background goroutine, so it must not touch DetailBrowser or other UI state,
// only return data for the caller to apply via postAndWake. ctx bounds the
// whole call.
//
// objs is an out-parameter rather than a fourth result because only the arms
// whose rows are objects use it: they append one nodeData per row, and every
// other arm leaves it nil, which withholds the pane's Delete. See
// detailResult.objs.
func fetchNodeDetails(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	switch node.data.Type {
	case NodeAgentJobs:
		return agentServerDetail(ctx, sc)
	case NodeAgentJob:
		return agentJobDetail(ctx, sc, node)
	case NodeAgentSchedule:
		return agentScheduleDetail(ctx, sc, node)
	case NodeAgentAlert:
		return agentAlertDetail(ctx, sc, node)
	case NodeAgentOperator:
		return agentOperatorDetail(ctx, sc, node)
	case NodeAgentJobActivity:
		return agentJobActivityDetail(ctx, sc)
	case NodeAgentJobHistory:
		return agentJobHistoryDetail(ctx, sc)
	case NodeAgentJobCategories:
		return agentJobCategoriesDetail(ctx, sc)
	case NodeAgentAlertCategories:
		return agentAlertCategoriesDetail(ctx, sc)
	case NodeAgentReport:
		return agentReportDetail(ctx, sc, node.data.Name)
	case NodeQueryStore:
		return queryStoreFolderDetail(ctx, sc, node.data.DBName)
	case NodeQueryStoreReport:
		return queryStoreReportDetail(ctx, sc, node.data.DBName, node.data.Name)
	case NodeSQLServerLogs:
		return errorLogFilesDetail(ctx, sc, gosmo.ErrorLogSQLServer)
	case NodeAgentErrorLogs:
		return errorLogFilesDetail(ctx, sc, gosmo.ErrorLogAgent)
	case NodeSQLServerLog, NodeAgentErrorLog:
		return errorLogFileDetail(ctx, sc, node)
	case NodeSystemDatabases:
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

	case NodeViews:
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

	case NodeStoredProcedures:
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

	case NodePartitionFunction, NodePartitionScheme, NodeSecurityPolicy,
		NodeColumnMasterKey, NodeColumnEncryptionKey:
		return storageSecurityDetail(ctx, sc, node)

	case NodeSystemDataTypes, NodeUserDefinedDataTypes, NodeUserDefinedTableTypes,
		NodeUserDefinedTypes, NodeXMLSchemaCollections,
		NodeAssemblies, NodeRules, NodeDefaults:
		return programmabilityFolderDetail(ctx, sc, node, objs)
	case NodePlanGuides:
		return planGuidesFolderDetail(ctx, sc, node, objs)
	case NodeSystemDataType, NodeUserDefinedDataType, NodeUserDefinedTableType,
		NodeUserDefinedType, NodeXMLSchemaCollection,
		NodeAssembly, NodeRule, NodeDefault, NodePlanGuide:
		return programmabilityDetail(ctx, sc, node)

	case NodeMessageTypes, NodeContracts, NodeBrokerQueues, NodeBrokerServices,
		NodeRoutes, NodeRemoteServiceBindings, NodeBrokerPriorities:
		return serviceBrokerFolderDetail(ctx, sc, node, objs)
	case NodeMessageType, NodeContract, NodeBrokerQueue, NodeBrokerService,
		NodeRoute, NodeRemoteServiceBinding, NodeBrokerPriority:
		return serviceBrokerDetail(ctx, sc, node)

	case NodeExternalDataSources, NodeExternalFileFormats, NodeExternalLibraries:
		return externalFolderDetail(ctx, sc, node, objs)
	case NodeExternalDataSource, NodeExternalFileFormat, NodeExternalLibrary:
		return externalDetail(ctx, sc, node)

	case NodeDatabaseSnapshots:
		return databaseSnapshotsFolderDetail(ctx, sc, node, objs)
	case NodeDatabaseSnapshot:
		return databaseSnapshotDetail(ctx, sc, node)

	case NodeCredentials:
		return credentialsFolderDetail(ctx, sc, node, objs)
	case NodeCredential:
		return credentialDetail(ctx, sc, node)

	case NodeCryptographicProviders:
		return cryptographicProvidersFolderDetail(ctx, sc, node, objs)
	case NodeCryptographicProvider:
		return cryptographicProviderDetail(ctx, sc, node)

	case NodeAudits:
		return auditsFolderDetail(ctx, sc, node, objs)
	case NodeAudit:
		return auditDetail(ctx, sc, node)

	case NodeServerAuditSpecifications:
		return serverAuditSpecificationsFolderDetail(ctx, sc, node, objs)
	case NodeServerAuditSpecification:
		return serverAuditSpecificationDetail(ctx, sc, node)

	case NodeDatabaseAuditSpecifications:
		return databaseAuditSpecificationsFolderDetail(ctx, sc, node, objs)
	case NodeDatabaseAuditSpecification:
		return databaseAuditSpecificationDetail(ctx, sc, node)

	case NodeDatabaseScopedCredentials:
		return databaseScopedCredentialsFolderDetail(ctx, sc, node, objs)
	case NodeDatabaseScopedCredential:
		return databaseScopedCredentialDetail(ctx, sc, node)

	case NodeAsymmetricKeys:
		return asymmetricKeysFolderDetail(ctx, sc, node, objs)
	case NodeAsymmetricKey:
		return asymmetricKeyDetail(ctx, sc, node)

	case NodeCertificates:
		return certificatesFolderDetail(ctx, sc, node, objs)
	case NodeCertificate:
		return certificateDetail(ctx, sc, node)

	case NodeSymmetricKeys:
		return symmetricKeysFolderDetail(ctx, sc, node, objs)
	case NodeSymmetricKey:
		return symmetricKeyDetail(ctx, sc, node)
	case NodeMasterKey:
		return masterKeyDetail(ctx, sc, node)

	case NodeBackupDevices:
		return backupDevicesFolderDetail(ctx, sc, node, objs)
	case NodeBackupDevice:
		return backupDeviceDetail(ctx, sc, node)

	case NodeServerTriggers:
		return serverTriggersFolderDetail(ctx, sc, node, objs)
	case NodeServerTrigger:
		return serverTriggerDetail(ctx, sc, node)

	case NodeDatabaseTriggers:
		return databaseTriggersFolderDetail(ctx, sc, node, objs)
	case NodeDatabaseTrigger:
		return databaseTriggerDetail(ctx, sc, node)

	case NodeEndpoints:
		return endpointsFolderDetail(ctx, sc, node, objs)
	case NodeEndpoint:
		return endpointDetail(ctx, sc, node)

	case NodeEventSessions:
		return eventSessionsFolderDetail(ctx, sc, node, objs)
	case NodeEventSession:
		return eventSessionDetail(ctx, sc, node)
	case NodeEventTarget:
		return eventTargetDetail(ctx, sc, node)

	case NodeResourceGovernor:
		return resourceGovernorDetail(ctx, sc)
	case NodeResourcePools:
		return resourcePoolsFolderDetail(ctx, sc, node, objs)
	case NodeResourcePool:
		return resourcePoolDetail(ctx, sc, node)
	case NodeWorkloadGroups:
		return workloadGroupsFolderDetail(ctx, sc, node, objs)
	case NodeWorkloadGroup:
		return workloadGroupDetail(ctx, sc, node)
	case NodeExternalResourcePools:
		return externalResourcePoolsFolderDetail(ctx, sc, node, objs)
	case NodeExternalResourcePool:
		return externalResourcePoolDetail(ctx, sc, node)

	case NodeDatabaseMail:
		return databaseMailDetail(ctx, sc)

	case NodeReplication:
		return replicationDetail(ctx, sc)
	case NodeLocalPublications:
		return localPublicationsDetail(ctx, sc)
	case NodeLocalSubscriptions:
		return localSubscriptionsDetail(ctx, sc)
	case NodePublication:
		return publicationDetail(ctx, sc, node)
	case NodeLocalSubscription:
		return localSubscriptionDetail(ctx, sc, node)

	case NodeStoredProcedure, NodeFunction, NodeTrigger:
		return moduleDetail(ctx, sc, node)

	default:
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
