package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
)

// detail_browser_service_broker.go is the Detail Browser's view of the seven
// Service Broker families: the folders and their leaves.
//
// Each leaf reuses the finder its Properties page uses, so the pane and the
// dialog can never disagree about which object a node names. Each folder
// reads the same gosmo listing its explorer_service_broker.go loader reads and
// applies the folder's own filter through filterObjects, on the collection
// rather than on the rows — see docs/db-rules.md.

// serviceBrokerFolderDetail lists one Service Broker folder. The caller has
// already narrowed node.data.Type to one this switch handles.
func serviceBrokerFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	n := node.data
	d, err := sc.Server.DatabaseByName(ctx, n.DBName)
	if err != nil {
		return nil, nil, err
	}

	switch n.Type {
	case NodeMessageTypes:
		types, err := d.MessageTypes(ctx)
		if err != nil {
			return nil, nil, err
		}
		types = filterObjects(n.Filter, types, func(mt *gosmo.MessageType) nodeData {
			return nodeData{Name: mt.Name}
		})
		rows := make([][]string, 0, len(types))
		for _, mt := range types {
			rows = append(rows, []string{mt.Name, string(mt.Validation),
				boundOrNone(dottedName(mt.SchemaCollectionSchema, mt.SchemaCollectionName)), mt.Owner})
			*objs = append(*objs, nodeData{Type: NodeMessageType, DBName: n.DBName,
				Name: mt.Name, IsSystem: mt.IsSystemObject})
		}
		return []string{"Name", "Validation", "Schema collection", "Owner"}, rows, nil

	case NodeContracts:
		contracts, err := d.Contracts(ctx)
		if err != nil {
			return nil, nil, err
		}
		contracts = filterObjects(n.Filter, contracts, func(c *gosmo.ServiceContract) nodeData {
			return nodeData{Name: c.Name}
		})
		rows := make([][]string, 0, len(contracts))
		for _, c := range contracts {
			rows = append(rows, []string{c.Name, strconv.Itoa(len(c.Messages)), c.Owner})
			*objs = append(*objs, nodeData{Type: NodeContract, DBName: n.DBName,
				Name: c.Name, IsSystem: c.IsSystemObject})
		}
		return []string{"Name", "Message types", "Owner"}, rows, nil

	case NodeBrokerQueues:
		return brokerQueuesFolderDetail(ctx, d, n, objs)

	case NodeBrokerServices:
		services, err := d.BrokerServices(ctx)
		if err != nil {
			return nil, nil, err
		}
		services = filterObjects(n.Filter, services, func(s *gosmo.BrokerService) nodeData {
			return nodeData{Name: s.Name}
		})
		rows := make([][]string, 0, len(services))
		for _, s := range services {
			rows = append(rows, []string{s.Name, dottedName(s.QueueSchema, s.QueueName),
				strconv.Itoa(len(s.Contracts)), s.Owner})
			*objs = append(*objs, nodeData{Type: NodeBrokerService, DBName: n.DBName,
				Name: s.Name, IsSystem: s.IsSystemObject})
		}
		return []string{"Name", "Queue", "Contracts", "Owner"}, rows, nil

	case NodeRoutes:
		routes, err := d.Routes(ctx)
		if err != nil {
			return nil, nil, err
		}
		routes = filterObjects(n.Filter, routes, func(r *gosmo.Route) nodeData {
			return nodeData{Name: r.Name}
		})
		rows := make([][]string, 0, len(routes))
		for _, r := range routes {
			rows = append(rows, []string{r.Name, boundOrNone(r.RemoteService),
				boundOrNone(r.Address), routeExpiryText(r)})
			// No route is marked system: sys.routes has neither an
			// is_ms_shipped nor a system id range, and AutoCreatedLocal sits
			// inside the user range — see loadRoutesChildren.
			*objs = append(*objs, nodeData{Type: NodeRoute, DBName: n.DBName, Name: r.Name})
		}
		return []string{"Name", "Remote service", "Address", "Expires"}, rows, nil

	case NodeRemoteServiceBindings:
		bindings, err := d.RemoteServiceBindings(ctx)
		if err != nil {
			return nil, nil, err
		}
		bindings = filterObjects(n.Filter, bindings, func(b *gosmo.RemoteServiceBinding) nodeData {
			return nodeData{Name: b.Name}
		})
		rows := make([][]string, 0, len(bindings))
		for _, b := range bindings {
			rows = append(rows, []string{b.Name, boundOrNone(b.RemoteService),
				boundOrNone(b.User), boolStr(b.IsAnonymous)})
			*objs = append(*objs, nodeData{Type: NodeRemoteServiceBinding, DBName: n.DBName, Name: b.Name})
		}
		return []string{"Name", "Remote service", "User", "Anonymous"}, rows, nil

	default: // NodeBrokerPriorities
		priorities, err := d.BrokerPriorities(ctx)
		if err != nil {
			return nil, nil, err
		}
		priorities = filterObjects(n.Filter, priorities, func(p *gosmo.BrokerPriority) nodeData {
			return nodeData{Name: p.Name}
		})
		rows := make([][]string, 0, len(priorities))
		for _, p := range priorities {
			rows = append(rows, []string{p.Name, strconv.Itoa(p.Level),
				anyOrName(p.Contract), anyOrName(p.LocalService), anyOrName(p.RemoteService)})
			*objs = append(*objs, nodeData{Type: NodeBrokerPriority, DBName: n.DBName, Name: p.Name})
		}
		return []string{"Name", "Level", "Contract", "Local service", "Remote service"}, rows, nil
	}
}

// brokerQueuesFolderDetail is the Queues folder, separate from the switch
// above because it is the one family whose listing is two reads: the queues
// themselves, and the message counts from sys.dm_db_partition_stats.
//
// The counts need VIEW DATABASE STATE, which the listing does not, so a
// failure there leaves the column blank rather than failing the folder — and
// blank, never "0": a queue whose internal table has no statistics row is
// not an empty queue, it is a queue nothing is known about.
func brokerQueuesFolderDetail(ctx context.Context, d *gosmo.Database, n nodeData, objs *[]nodeData) ([]string, [][]string, error) {
	queues, err := d.BrokerQueues(ctx)
	if err != nil {
		return nil, nil, err
	}
	queues = filterObjects(n.Filter, queues, func(q *gosmo.BrokerQueue) nodeData {
		return nodeData{Name: q.Name, Schema: q.Schema, CreateDate: q.CreateDate}
	})
	counts, err := d.QueueMessageCounts(ctx)
	if err != nil {
		counts = nil
	}
	rows := make([][]string, 0, len(queues))
	for _, q := range queues {
		rows = append(rows, []string{dottedName(q.Schema, q.Name), queueStatusText(q),
			boolStr(q.IsRetentionEnabled), queueActivationText(q),
			strconv.Itoa(q.MaxReaders), queueMessageCountText(counts, q.ObjectID)})
		*objs = append(*objs, nodeData{Type: NodeBrokerQueue, DBName: n.DBName,
			Schema: q.Schema, Name: q.Name, IsSystem: q.IsSystemObject,
			IsEnabled: q.IsEnqueueEnabled || q.IsReceiveEnabled})
	}
	return []string{"Name", "Status", "Retention", "Activation", "Readers", "Messages"}, rows, nil
}

// serviceBrokerDetail is the Property/Value view of one Service Broker leaf.
// Every arm reuses the finder the object's Properties page uses.
func serviceBrokerDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	n := node.data
	switch n.Type {
	case NodeMessageType:
		mt, err := findMessageType(ctx, sc, n.DBName, n.Name)
		if err != nil {
			return nil, nil, err
		}
		return propertyRows(
			"Name", mt.Name,
			"Owner", mt.Owner,
			"Validation", string(mt.Validation),
			"Schema collection", boundOrNone(dottedName(mt.SchemaCollectionSchema, mt.SchemaCollectionName)),
			"System", boolStr(mt.IsSystemObject),
		)

	case NodeContract:
		c, err := findContract(ctx, sc, n.DBName, n.Name)
		if err != nil {
			return nil, nil, err
		}
		rows := [][]string{
			{"Name", c.Name},
			{"Owner", c.Owner},
			{"System", boolStr(c.IsSystemObject)},
		}
		// The message types and the end that may send each are the contract —
		// a contract with none is not a thing the catalog can hold — so they
		// are listed here rather than left to the Properties dialog.
		for _, m := range c.Messages {
			rows = append(rows, []string{"Message type", m.MessageType + ", sent by " + string(m.SentBy)})
		}
		return propertyValueColumns, rows, nil

	case NodeBrokerQueue:
		q, err := findBrokerQueue(ctx, sc, n.DBName, n.Schema, n.Name)
		if err != nil {
			return nil, nil, err
		}
		rows := [][]string{
			{"Name", dottedName(q.Schema, q.Name)},
			{"Owner", q.Owner},
			{"Status", queueStatusText(q)},
			{"Retention", boolStr(q.IsRetentionEnabled)},
			{"Poison message handling", boolStr(q.IsPoisonMessageHandlingEnabled)},
			{"Activation", queueActivationText(q)},
			{"Activation procedure", boundOrNone(q.ActivationProcedure)},
			{"Max queue readers", strconv.Itoa(q.MaxReaders)},
			{"Activation execute as", boundOrNone(q.ActivationExecuteAs)},
			{"Filegroup", boundOrNone(q.FileGroup)},
			{"System", boolStr(q.IsSystemObject)},
			{"Created", formatSQLDate(q.CreateDate)},
			{"Modified", formatSQLDate(q.ModifyDate)},
		}
		// Both DMV reads need VIEW DATABASE STATE, which the queue's own read
		// does not, and a queue the broker has never monitored has no monitor
		// row at all — the normal case, not an error. Neither failure may cost
		// the rows above.
		if count, err := q.MessageCount(ctx); err == nil {
			rows = append(rows, []string{"Messages", strconv.FormatInt(count, 10)})
		}
		if m := findQueueMonitor(ctx, sc, n.DBName, q.ObjectID); m != nil {
			rows = append(rows,
				[]string{"Monitor state", m.State},
				[]string{"Tasks waiting", strconv.Itoa(m.TasksWaiting)},
				[]string{"Last activated", formatSQLDate(m.LastActivatedTime)})
		}
		return propertyValueColumns, rows, nil

	case NodeBrokerService:
		s, err := findBrokerService(ctx, sc, n.DBName, n.Name)
		if err != nil {
			return nil, nil, err
		}
		rows := [][]string{
			{"Name", s.Name},
			{"Owner", s.Owner},
			{"Queue", dottedName(s.QueueSchema, s.QueueName)},
			{"System", boolStr(s.IsSystemObject)},
		}
		for _, c := range s.Contracts {
			rows = append(rows, []string{"Contract", c})
		}
		return propertyValueColumns, rows, nil

	case NodeRoute:
		r, err := findRoute(ctx, sc, n.DBName, n.Name)
		if err != nil {
			return nil, nil, err
		}
		return propertyRows(
			"Name", r.Name,
			"Owner", r.Owner,
			"Remote service", boundOrNone(r.RemoteService),
			"Broker instance", boundOrNone(r.BrokerInstance),
			"Address", boundOrNone(r.Address),
			"Mirror address", boundOrNone(r.MirrorAddress),
			"Expires", routeExpiryText(r),
		)

	case NodeRemoteServiceBinding:
		b, err := findRemoteServiceBinding(ctx, sc, n.DBName, n.Name)
		if err != nil {
			return nil, nil, err
		}
		return propertyRows(
			"Name", b.Name,
			"Owner", b.Owner,
			"Remote service", boundOrNone(b.RemoteService),
			"User", boundOrNone(b.User),
			"Anonymous", boolStr(b.IsAnonymous),
		)

	default: // NodeBrokerPriority
		p, err := findBrokerPriority(ctx, sc, n.DBName, n.Name)
		if err != nil {
			return nil, nil, err
		}
		return propertyRows(
			"Name", p.Name,
			"Priority level", strconv.Itoa(p.Level),
			"Contract", anyOrName(p.Contract),
			"Local service", anyOrName(p.LocalService),
			"Remote service", anyOrName(p.RemoteService),
		)
	}
}

// queueStatusText renders a queue's STATUS. ALTER QUEUE ... WITH STATUS moves
// the enqueue and receive halves together, so a queue is out of service when
// neither is on — the same reading loadBrokerQueuesChildren's label uses.
func queueStatusText(q *gosmo.BrokerQueue) string {
	return enabledText(q.IsEnqueueEnabled || q.IsReceiveEnabled)
}

// queueActivationText renders a queue's activation as the one phrase that
// says whether anything will run: a queue with activation switched off keeps
// its procedure, and a queue with no procedure at all is a different thing
// again.
func queueActivationText(q *gosmo.BrokerQueue) string {
	if q.ActivationProcedure == "" {
		return "(none)"
	}
	return enabledText(q.IsActivationEnabled)
}

// queueMessageCountText renders one queue's message count, blank when the
// count is unknown — an unreadable DMV or a queue with no statistics row.
// Blank rather than 0, which claims the queue is empty.
func queueMessageCountText(counts map[int]int64, objectID int) string {
	if counts == nil {
		return ""
	}
	n, ok := counts[objectID]
	if !ok {
		return ""
	}
	return strconv.FormatInt(n, 10)
}

// routeExpiryText renders a route's LIFETIME. sys.routes stores the expiry
// instant and NULL for a route that never expires, which is the common case.
func routeExpiryText(r *gosmo.Route) string {
	if r.Expires.IsZero() {
		return "(never)"
	}
	return formatSQLDate(r.Expires)
}

// anyOrName renders a broker priority's criterion. An empty one means ANY —
// how CREATE BROKER PRIORITY spells a criterion it was not given — and a blank
// cell would read as a value the page failed to load.
func anyOrName(s string) string {
	if s == "" {
		return "ANY"
	}
	return s
}
