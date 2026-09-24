package tui

import (
	"context"
	"errors"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// queue_props.go is Queue Properties, one of the two writable pages in the
// Service Broker tree.
//
// A queue is here and a contract is not because a queue's settings are the
// ones that change in operation rather than at design time: a queue taken out
// of service, an activation procedure that has to be stopped, a reader count
// raised under load. Every one of them is an ALTER QUEUE clause, and none of
// them changes what the application's messages mean.
//
// The rights are the queue's own and are *not* the ones its Delete takes —
// ALTER ON OBJECT::<queue> writes this page and is refused the drop, which
// needs CONTROL on the queue or ALTER on its schema. See gate.QueueAlterRights.

func findBrokerQueue(ctx context.Context, sc *db.ServerConn, dbName, schema, name string) (*gosmo.BrokerQueue, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.BrokerQueueByName(ctx, schema, name)
}

// findQueueMonitor returns the broker's activation state for one queue, or
// nil. A queue with no monitor row is the normal case — the broker creates one
// when it first has reason to look at the queue — and so is a login without
// VIEW DATABASE STATE, so neither is an error the caller has to handle.
func findQueueMonitor(ctx context.Context, sc *db.ServerConn, dbName string, objectID int) *gosmo.QueueMonitor {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil
	}
	monitors, err := d.QueueMonitors(ctx)
	if err != nil {
		return nil
	}
	for _, m := range monitors {
		if m.QueueID == objectID {
			return m
		}
	}
	return nil
}

func brokerQueuePropPages(sc *db.ServerConn, dbName, schema, name string) []propPage {
	return []propPage{
		withRequiresOn(pageBrokerQueueGeneral(sc, dbName, schema, name), dbName, schema, name,
			gate.QueueAlterRights()...),
	}
}

func pageBrokerQueueGeneral(sc *db.ServerConn, dbName, schema, name string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			q, err := findBrokerQueue(ctx, sc, dbName, schema, name)
			if err != nil {
				return nil, nil, err
			}

			// One STATUS row, not two: ALTER QUEUE ... WITH STATUS moves the
			// enqueue and receive halves together and the catalog's two
			// columns always agree, so a pair of checkboxes would offer a
			// combination no statement can produce.
			status := propsheet.Check("Queue enabled", q.IsEnqueueEnabled || q.IsReceiveEnabled)
			retention := propsheet.Check("Retention", q.IsRetentionEnabled)
			poison := propsheet.Check("Poison message handling", q.IsPoisonMessageHandlingEnabled)

			activation := propsheet.Check("Activation enabled", q.IsActivationEnabled)
			// A queue with no activation has no procedure schema to show.
			// gosmo refuses an empty one rather than guessing, so the row
			// offers dbo — visibly, where it can be changed — instead.
			procSchemaValue := q.ActivationProcedureSchema
			if procSchemaValue == "" {
				procSchemaValue = "dbo"
			}
			procSchema := propsheet.Text("Activation procedure schema", procSchemaValue, 30)
			procName := propsheet.Text("Activation procedure", q.ActivationProcedureName, 30)
			readers := propsheet.Int("Max queue readers", int64(q.MaxReaders), 0, 32767, "")
			executeAs := propsheet.Text("Activation execute as", q.ActivationExecuteAs, 30)

			f := propsheet.NewForm(
				propsheet.Section("Queue"),
				propsheet.Static("Name", dottedName(q.Schema, q.Name)),
				propsheet.Static("Owner", q.Owner),
				propsheet.Static("Filegroup", boundOrNone(q.FileGroup)),
				status, retention, poison,
				propsheet.Section("Activation"),
				activation, procSchema, procName, readers, executeAs,
				propsheet.Note("EXECUTE AS takes OWNER, SELF, or a database user's name. SELF does not survive a round trip: the server resolves it at ALTER time and the queue reads back as the user it resolved to."),
				propsheet.Section("Dates"),
				propsheet.Static("Created", formatSQLDate(q.CreateDate)),
				propsheet.Static("Last modified", formatSQLDate(q.ModifyDate)),
			)

			apply := func(ctx context.Context) error {
				s := gosmo.QueueSettings{}
				dirty := false
				if status.Dirty() {
					v := status.Checked()
					s.Status, dirty = &v, true
				}
				if retention.Dirty() {
					v := retention.Checked()
					s.Retention, dirty = &v, true
				}
				if poison.Dirty() {
					v := poison.Checked()
					s.PoisonMessageHandling, dirty = &v, true
				}
				// The ACTIVATION block is restated in full whenever any part
				// of it changed: the server refuses a partial one on a queue
				// that has no activation ("the activation user is not
				// specified"), so the four rows move together.
				if activation.Dirty() || procSchema.Dirty() || procName.Dirty() ||
					readers.Dirty() || executeAs.Dirty() {
					proc := strings.TrimSpace(procName.Value())
					switch {
					case proc == "" && q.ActivationProcedure != "":
						s.DropActivation, dirty = true, true
					case proc == "":
						return errors.New("name an activation procedure, or leave the activation rows alone: " +
							"a queue with no procedure has no ACTIVATION block to change")
					default:
						n, err := readers.IntValue()
						if err != nil {
							return err
						}
						s.Activation = &gosmo.QueueActivation{
							Enabled:         activation.Checked(),
							ProcedureSchema: strings.TrimSpace(procSchema.Value()),
							ProcedureName:   proc,
							MaxQueueReaders: int(n),
							ExecuteAs:       strings.TrimSpace(executeAs.Value()),
						}
						dirty = true
					}
				}
				if !dirty {
					return nil
				}
				// Re-read rather than reusing q: the apply runs after the form
				// was built, and a queue dropped in between must fail here
				// rather than have an ALTER sent for it.
				queue, err := findBrokerQueue(ctx, sc, dbName, schema, name)
				if err != nil {
					return err
				}
				return queue.Alter(ctx, s)
			}
			return f, apply, nil
		},
	}
}
