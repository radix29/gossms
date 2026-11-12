package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// explorer_object_op_adapters.go holds the constructors and gosmo adapters the
// objectOps table (explorer_object_ops.go) is built from: the Resource Governor
// and schema-object op shapes, the dropIn/renameIn/transferIn handle adapters,
// and the constraint drop/rename.

// rgDropApplied ends every Resource Governor delete's warning.
const rgDropApplied = "Resource Governor is then reconfigured, so the drop takes effect at once; a disabled governor stays disabled."

// rgDroppable is a gosmo Resource Governor handle — pool, group or external
// pool.
type rgDroppable interface{ Drop(context.Context) error }

// rgObjectOp is the op the three Resource Governor families share: a
// lookup-free handle's Drop, settled by applyResourceGovernor and the node's
// label re-read.
func rgObjectOp(noun, warning string, ref func(*gosmo.Server, string) rgDroppable) objectOp {
	return objectOp{
		noun:    noun,
		warning: warning,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return ref(sc.Server, n.Name).Drop(ctx)
		},
		settle:  applyResourceGovernor,
		settled: (*App).refreshResourceGovernorLabel,
	}
}

// schemaObjectHandle is a gosmo handle for a schema-scoped object that can be
// dropped, renamed and moved to another schema.
type schemaObjectHandle interface {
	Drop(context.Context) error
	Rename(context.Context, string) error
	Transfer(context.Context, string) error
}

// schemaObjectOp is the whole op for a family whose handle has all three —
// view, procedure, function, sequence, synonym.
func schemaObjectOp[T schemaObjectHandle](noun string, ref func(*gosmo.Database, string, string) T) objectOp {
	return objectOp{noun: noun, drop: dropIn(ref), rename: renameIn(ref), transfer: transferIn(ref)}
}

// brokerQueueRef is ServiceBroker.QueueRef in the Database-first shape dropIn
// and transferIn take: queues are the one schema-scoped broker family, and
// their handle sits on the d.ServiceBroker() facet, not on Database.
func brokerQueueRef(d *gosmo.Database, schema, name string) *gosmo.BrokerQueue {
	return d.ServiceBroker().QueueRef(schema, name)
}

// dropIn adapts one of gosmo's schema-scoped Database.XxxRef(schema, name)
// handles into a drop function: the handle is lookup-free, and its Drop
// addresses the object by the two names alone.
func dropIn[T interface{ Drop(context.Context) error }](ref func(*gosmo.Database, string, string) T) func(context.Context, *db.ServerConn, nodeData) error {
	return func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
		return ref(dbOf(sc, n), n.Schema, n.Name).Drop(ctx)
	}
}

// renameIn is dropIn for a rename.
func renameIn[T interface {
	Rename(context.Context, string) error
}](ref func(*gosmo.Database, string, string) T) func(context.Context, *db.ServerConn, nodeData, string) error {
	return func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
		return ref(dbOf(sc, n), n.Schema, n.Name).Rename(ctx, newName)
	}
}

// transferIn is dropIn for a move to another schema (ALTER SCHEMA ... TRANSFER).
// The handle's Transfer picks the securable class (TYPE::, XML SCHEMA
// COLLECTION::).
func transferIn[T interface {
	Transfer(context.Context, string) error
}](ref func(*gosmo.Database, string, string) T) func(context.Context, *db.ServerConn, nodeData, string) error {
	return func(ctx context.Context, sc *db.ServerConn, n nodeData, targetSchema string) error {
		return ref(dbOf(sc, n), n.Schema, n.Name).Transfer(ctx, targetSchema)
	}
}

// typeInUseWarning is the delete warning the three type families share; like
// NodeColumn's, it leaves naming the blocker to the server.
const typeInUseWarning = "The drop is refused while a column, parameter, variable or routine is typed on it — the server's error names what blocks it."

// dropConstraint removes a primary key, unique constraint, foreign key, or
// CHECK constraint — one ALTER TABLE ... DROP CONSTRAINT for all four.
func dropConstraint(ctx context.Context, sc *db.ServerConn, n nodeData) error {
	return tableOf(sc, n).DropConstraint(ctx, n.Name)
}

// renameConstraint renames a foreign key or CHECK constraint. A primary key
// or unique constraint renames through its backing index instead — see
// NodeKey.
func renameConstraint(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
	return tableOf(sc, n).RenameConstraint(ctx, n.Name, newName)
}
