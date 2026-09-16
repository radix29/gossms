package tui

import (
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// The seven Service Broker families SSMS files under a database's Service
// Broker folder: Message Types, Contracts, Queues, Services, Routes, Remote
// Service Bindings and Broker Priorities.
//
// The folder is listed whether or not the broker is enabled on the database.
// Every one of these objects can be created, listed and dropped with
// is_broker_enabled off — disabling the broker stops message delivery, not
// DDL — and Database Properties ▸ Options already reports the flag.
//
// Queues are the one schema-scoped family here (a sys.objects row of type
// 'SQ'); the other six are database-scoped with an owner and no schema, which
// is why only the queue loader fills nodeData.Schema.

// loadServiceBrokerChildren returns the seven family folders, in SSMS's
// order. A static loader: each folder reads for itself, so expanding Service
// Broker costs no query.
func loadServiceBrokerChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbName := node.data.DBName
	return []*explorerNode{
		l.node("Message Types", NodeMessageTypes, "", "", dbName),
		l.node("Contracts", NodeContracts, "", "", dbName),
		l.node("Queues", NodeBrokerQueues, "", "", dbName),
		l.node("Services", NodeBrokerServices, "", "", dbName),
		l.node("Routes", NodeRoutes, "", "", dbName),
		l.node("Remote Service Bindings", NodeRemoteServiceBindings, "", "", dbName),
		l.node("Broker Priorities", NodeBrokerPriorities, "", "", dbName),
	}, nil
}

// loadMessageTypesChildren lists the database's message types. The ones SQL
// Server ships (the thirteen schemas.microsoft.com/… types and DEFAULT) are
// listed too, as SSMS lists them, but marked IsSystem so Delete and Rename
// stay off their menu — the same shape loadAssembliesChildren uses.
func loadMessageTypesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.MessageType, error) { return dbObj.MessageTypesContext(l.ctx) },
		func(mt *gosmo.MessageType) *explorerNode {
			n := l.node(mt.Name, NodeMessageType, "", mt.Name, node.data.DBName)
			n.data.IsSystem = mt.IsSystemObject
			return n
		})
}

func loadContractsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.ServiceContract, error) { return dbObj.ContractsContext(l.ctx) },
		func(c *gosmo.ServiceContract) *explorerNode {
			n := l.node(c.Name, NodeContract, "", c.Name, node.data.DBName)
			n.data.IsSystem = c.IsSystemObject
			return n
		})
}

// loadBrokerQueuesChildren lists the database's queues — the one family here
// that is schema-scoped, so the label carries the schema the way every other
// schema-scoped folder's does.
//
// A queue taken out of service is labelled "(Disabled)", the same suffix the
// trigger, policy and plan-guide folders use and for the same reason: a queue
// with STATUS = OFF neither receives nor enqueues anything, and nothing else
// in the row says so. ALTER QUEUE … WITH STATUS sets both halves together, so
// a queue is disabled when neither is on.
func loadBrokerQueuesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.BrokerQueue, error) { return dbObj.BrokerQueuesContext(l.ctx) },
		func(q *gosmo.BrokerQueue) *explorerNode {
			label := q.Schema + "." + q.Name
			enabled := q.IsEnqueueEnabled || q.IsReceiveEnabled
			if !enabled {
				label += " (Disabled)"
			}
			n := l.node(label, NodeBrokerQueue, q.Schema, q.Name, node.data.DBName)
			n.data.CreateDate = q.CreateDate
			n.data.IsEnabled = enabled
			n.data.IsSystem = q.IsSystemObject
			return n
		})
}

// loadBrokerServicesChildren lists the database's services. IsSystemObject is
// the id-range test, which is why msdb shows Database Mail's services as user
// objects while the same feature's queues come back system — see gosmo's
// service_broker.go; the two classifications cannot be made to agree and the
// asymmetry is the catalog's, not a bug here.
func loadBrokerServicesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.BrokerService, error) { return dbObj.BrokerServicesContext(l.ctx) },
		func(s *gosmo.BrokerService) *explorerNode {
			n := l.node(s.Name, NodeBrokerService, "", s.Name, node.data.DBName)
			n.data.IsSystem = s.IsSystemObject
			return n
		})
}

// loadRoutesChildren lists the database's routes. No route is marked system,
// deliberately: sys.routes has no is_ms_shipped and no system id range —
// AutoCreatedLocal, which SQL Server puts in every database, has route_id
// 65536, inside the user range. SSMS lists it plainly and a user may
// legitimately drop it.
//
// The label carries the address, since a route's name says nothing about
// where it sends and "LOCAL" vs. a TCP address is the whole of what it is.
func loadRoutesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.Route, error) { return dbObj.RoutesContext(l.ctx) },
		func(r *gosmo.Route) *explorerNode {
			label := r.Name
			if r.Address != "" {
				label += " (" + r.Address + ")"
			}
			return l.node(label, NodeRoute, "", r.Name, node.data.DBName)
		})
}

// loadRemoteServiceBindingsChildren lists the database's remote service
// bindings. The family exists on Managed Instance and is listed there:
// sys.remote_service_bindings reads fine and ALTER/DROP run — only CREATE is
// refused, which is the edition gate's business, not the tree's.
func loadRemoteServiceBindingsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(
		func() ([]*gosmo.RemoteServiceBinding, error) { return dbObj.RemoteServiceBindingsContext(l.ctx) },
		func(b *gosmo.RemoteServiceBinding) *explorerNode {
			return l.node(b.Name, NodeRemoteServiceBinding, "", b.Name, node.data.DBName)
		})
}

// loadBrokerPrioritiesChildren lists the database's conversation priorities,
// each labelled with its level — the one number that says what a priority
// does, and 1..10 is not recoverable from the name.
//
// sys.conversation_priorities has no system members at all, so nothing here
// is ever marked IsSystem.
func loadBrokerPrioritiesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.BrokerPriority, error) { return dbObj.BrokerPrioritiesContext(l.ctx) },
		func(p *gosmo.BrokerPriority) *explorerNode {
			label := p.Name + " (" + strconv.Itoa(p.Level) + ")"
			return l.node(label, NodeBrokerPriority, "", p.Name, node.data.DBName)
		})
}

// The context menus for the seven leaves, looked up through nodeMenus
// (explorer_loaders.go). Each opens its family's Properties; five of them are
// read-only pages, and the queue's and the route's can write.
//
// Each is Properties only here because the rest of the menu is spliced in
// from the shared tables rather than named per family: Delete comes from
// objectOps, Script as from scriptables, and Move to Schema — queues only,
// the one schema-scoped family — from the same objectOp's transfer. There are
// no renames at all in this subtree: no sp_rename class exists for any of the
// seven.
//
// There is no Enable/Disable item for a queue, deliberately: a queue's status
// is a row on its Properties page, which is one place rather than two. A plan
// guide has the item because it has no other page that writes.

func messageTypeMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showMessageTypePropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

func contractMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showContractPropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

func brokerQueueMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showBrokerQueuePropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.Name)
	})
}

func brokerServiceMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showBrokerServicePropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

func routeMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showRoutePropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

func remoteServiceBindingMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showRemoteServiceBindingPropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

func brokerPriorityMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showBrokerPriorityPropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}
