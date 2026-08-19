package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// explorer_object_ops.go is Object Explorer's general Delete and Rename:
// one table of what the two mean per node type, and the two shared actions
// that confirm, run, and refresh around them. A node type absent from the
// table offers neither — that is how a folder, a system object family, or
// anything gosmo can't drop stays out of the menu, instead of a menu item
// that fails when clicked.
//
// SQL Server Agent objects are renameable here but keep their own Delete
// (see agent_menu.go), whose wording explains what blocks each one.

// objectOp is what Delete and Rename do for one node type. A nil drop or
// rename means the node doesn't offer that action.
//
// Both take a nodeData by value, not the *explorerNode it came off: they run
// on a background goroutine, and the UI goroutine writes node.data (see
// applyNodeFilter). The copy is made by deleteObject/runRename before the
// safego.
type objectOp struct {
	// noun names the object in dialog titles and messages ("Table").
	noun   string
	drop   func(ctx context.Context, sc *db.ServerConn, n nodeData) error
	rename func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error
	// warning is appended to the delete confirmation when the drop does
	// something beyond removing the object itself.
	warning string
	// typed gates the delete behind retyping the object's name, for a drop
	// whose blast radius is bigger than one object.
	typed bool
	// renameWarning is a question asked between the new-name prompt and the
	// rename itself, for a rename that costs more than the name change.
	renameWarning string
}

// dbOf is the database a node's object lives in.
//
// Server.Database, not DatabaseByName: every statement below names its object
// in the text and reads nothing off the *gosmo.Database but its name, so the
// sys.databases round trip DatabaseByName costs bought nothing. It also could
// not run under a WithScript-derived context, which is what a Script Changes
// on Delete would need — see gosmo's Server.Database.
func dbOf(sc *db.ServerConn, n nodeData) *gosmo.Database {
	return sc.Server.Database(n.DBName)
}

// tableOf is the table a table-scoped node (index, statistic, key,
// constraint) belongs to — nodeData.TableName, since Schema/Name on those
// point at the index's or constraint's own name.
//
// A name-only handle, for the same reason as dbOf. Only DropConstraint uses
// it; the index and statistic ops need the real object and query for it.
func tableOf(sc *db.ServerConn, n nodeData) *gosmo.Table {
	return sc.Server.Database(n.DBName).Table(n.Schema, n.TableName)
}

// objectOps is the per-type table. Every rename that goes through
// Database.RenameObjectContext is sp_rename's 'OBJECT' class — a view,
// procedure, function, sequence, synonym, trigger, or constraint; indexes
// and statistics have their own sp_rename object types and so their own
// gosmo methods.
var objectOps = map[NodeType]objectOp{
	NodeDatabase: {
		noun:    "Database",
		warning: "Existing connections to it will be closed.",
		typed:   true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return sc.Server.DropDatabaseContext(ctx, n.Name, true)
		},
		// MODIFY NAME needs exclusive access, which the tree's own metadata
		// connections are enough to deny — so the rename always closes
		// connections, and always asks first.
		renameWarning: "Renaming a database needs exclusive access to it. Existing connections will be closed and their transactions rolled back. Continue?",
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			return sc.Server.RenameDatabaseContext(ctx, n.Name, newName, true)
		},
	},
	NodeTable: {
		noun:    "Table",
		warning: "All of its data is deleted with it.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			d := dbOf(sc, n)
			// cascade=false: a table another table references must have that
			// foreign key dealt with first, the same refusal SSMS gives.
			return d.DropTableContext(ctx, n.Schema, n.Name, false)
		},
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			return dbOf(sc, n).RenameTableContext(ctx, n.Schema, n.Name, newName)
		},
	},
	NodeView:            {noun: "View", drop: dropIn((*gosmo.Database).DropViewContext), rename: renameObjectIn},
	NodeStoredProcedure: {noun: "Stored Procedure", drop: dropIn((*gosmo.Database).DropStoredProcedureContext), rename: renameObjectIn},
	NodeFunction:        {noun: "Function", drop: dropIn((*gosmo.Database).DropFunctionContext), rename: renameObjectIn},
	NodeTrigger:         {noun: "Trigger", drop: dropIn((*gosmo.Database).DropTriggerContext), rename: renameObjectIn},
	NodeSequence:        {noun: "Sequence", drop: dropIn((*gosmo.Database).DropSequenceContext), rename: renameObjectIn},
	NodeSynonym:         {noun: "Synonym", drop: dropIn((*gosmo.Database).DropSynonymContext), rename: renameObjectIn},

	NodeIndex: {
		noun: "Index",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			t, idx, err := findIndex(ctx, sc, n.DBName, n.Schema, n.TableName, n.Name)
			if err != nil {
				return err
			}
			return idx.DropContext(ctx, t)
		},
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			t, idx, err := findIndex(ctx, sc, n.DBName, n.Schema, n.TableName, n.Name)
			if err != nil {
				return err
			}
			return idx.RenameContext(ctx, t, newName)
		},
	},
	NodeStatistic: {
		noun: "Statistic",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			_, st, err := findStatistic(ctx, sc, n.DBName, n.Schema, n.TableName, n.Name)
			if err != nil {
				return err
			}
			return st.DropContext(ctx)
		},
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			_, st, err := findStatistic(ctx, sc, n.DBName, n.Schema, n.TableName, n.Name)
			if err != nil {
				return err
			}
			return st.RenameContext(ctx, newName)
		},
	},
	NodeKey: {
		noun: "Key",
		drop: dropConstraint,
		// A primary key's or unique constraint's name is its backing index's
		// name in sys.indexes, so it renames as an index, not as an object.
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			t, idx, err := findIndex(ctx, sc, n.DBName, n.Schema, n.TableName, n.Name)
			if err != nil {
				return err
			}
			return idx.RenameContext(ctx, t, newName)
		},
	},
	NodeForeignKey: {noun: "Foreign Key", drop: dropConstraint, rename: renameObjectIn},
	NodeCheck:      {noun: "Constraint", drop: dropConstraint, rename: renameObjectIn},

	NodePartitionFunction: {
		noun:    "Partition Function",
		warning: "Every partition scheme built on it must be dropped first.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			pf, err := findPartitionFunction(ctx, sc, n.DBName, n.Name)
			if err != nil {
				return err
			}
			return pf.DropContext(ctx)
		},
	},
	NodePartitionScheme: {
		noun:    "Partition Scheme",
		warning: "Every table and index partitioned by it must be moved off it first.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			ps, err := findPartitionScheme(ctx, sc, n.DBName, n.Name)
			if err != nil {
				return err
			}
			return ps.DropContext(ctx)
		},
	},
	NodeSecurityPolicy: {
		noun:    "Security Policy",
		warning: "The tables it protects stop being filtered — every row becomes visible.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			p, err := findSecurityPolicy(ctx, sc, n.DBName, n.Schema, n.Name)
			if err != nil {
				return err
			}
			return p.DropContext(ctx)
		},
	},
	NodeColumnMasterKey: {
		noun:    "Column Master Key",
		warning: "Every column encryption key protected by it must be dropped first.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			k, err := findColumnMasterKey(ctx, sc, n.DBName, n.Name)
			if err != nil {
				return err
			}
			return k.DropContext(ctx)
		},
	},
	NodeColumnEncryptionKey: {
		noun: "Column Encryption Key",
		// Not recoverable: the key material only exists encrypted here, so
		// every column encrypted with it becomes unreadable ciphertext.
		warning: "Data in every column encrypted with it becomes permanently unreadable.",
		typed:   true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			k, err := findColumnEncryptionKey(ctx, sc, n.DBName, n.Name)
			if err != nil {
				return err
			}
			return k.DropContext(ctx)
		},
	},

	NodeLogin: {
		noun:    "Login",
		warning: "Database users mapped to it are left orphaned.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return sc.Server.DropLoginContext(ctx, n.Name)
		},
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			return sc.Server.Login(n.Name).RenameContext(ctx, newName)
		},
	},
	NodeServerRole: {
		noun: "Server Role",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return sc.Server.DropServerRoleContext(ctx, n.Name)
		},
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			r, err := sc.Server.ServerRoleByNameContext(ctx, n.Name)
			if err != nil {
				return err
			}
			return r.RenameContext(ctx, newName)
		},
	},
	NodeUser: {
		noun: "User",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			d := dbOf(sc, n)
			return d.DropUserContext(ctx, n.Name)
		},
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			d := dbOf(sc, n)
			u, err := d.UserByNameContext(ctx, n.Name)
			if err != nil {
				return err
			}
			return u.RenameContext(ctx, newName)
		},
	},
	NodeDatabaseRole: {
		noun: "Database Role",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			d := dbOf(sc, n)
			return d.DropDatabaseRoleContext(ctx, n.Name)
		},
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			r, err := findRole(ctx, sc, n.DBName, n.Name)
			if err != nil {
				return err
			}
			return r.RenameContext(ctx, newName)
		},
	},
	NodeSchema: {
		// SQL Server has no schema rename — moving a schema's contents is
		// ALTER SCHEMA ... TRANSFER, a different operation — so Rename is
		// deliberately absent here rather than offered and failing.
		noun: "Schema",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			d := dbOf(sc, n)
			return d.DropSchemaContext(ctx, n.Name)
		},
	},

	// Agent objects: Rename only. Their Delete lives in agent_menu.go, with
	// per-type wording about what blocks it.
	NodeAgentJob: {
		noun: "Job",
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			j, err := sc.Server.JobByNameContext(ctx, n.Name)
			if err != nil {
				return err
			}
			return j.RenameContext(ctx, newName)
		},
	},
	NodeAgentSchedule: {
		noun: "Schedule",
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			s, err := sc.Server.ScheduleByNameContext(ctx, n.Name)
			if err != nil {
				return err
			}
			return s.RenameContext(ctx, newName)
		},
	},
	NodeAgentAlert: {
		noun: "Alert",
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			al, err := sc.Server.AlertByNameContext(ctx, n.Name)
			if err != nil {
				return err
			}
			return al.RenameContext(ctx, newName)
		},
	},
	NodeAgentOperator: {
		noun: "Operator",
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			o, err := sc.Server.OperatorByNameContext(ctx, n.Name)
			if err != nil {
				return err
			}
			return o.RenameContext(ctx, newName)
		},
	},
}

// dropIn adapts one of gosmo's Database.DropXxxContext(ctx, schema, name)
// methods — the shape every schema-scoped object family shares — into a
// drop function.
func dropIn(fn func(*gosmo.Database, context.Context, string, string) error) func(context.Context, *db.ServerConn, nodeData) error {
	return func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
		return fn(dbOf(sc, n), ctx, n.Schema, n.Name)
	}
}

// renameObjectIn is sp_rename's 'OBJECT' class, shared by every schema-
// scoped object that isn't a table, index, or statistic.
func renameObjectIn(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
	return dbOf(sc, n).RenameObjectContext(ctx, n.Schema, n.Name, newName)
}

// dropConstraint removes a primary key, unique constraint, foreign key, or
// CHECK constraint — one ALTER TABLE ... DROP CONSTRAINT for all four.
func dropConstraint(ctx context.Context, sc *db.ServerConn, n nodeData) error {
	return tableOf(sc, n).DropConstraintContext(ctx, n.Name)
}

// objectOpFor returns the Delete/Rename behaviour for a node type, or nil
// when it has none.
func objectOpFor(t NodeType) *objectOp {
	if op, ok := objectOps[t]; ok {
		return &op
	}
	return nil
}

// objectOpsMenuItems is the Rename/Delete pair a node offers, or nil.
//
// A system object offers neither, the same way a node type absent from
// objectOps does. The System * folders emit the same node types as the user
// ones, so the type alone put Delete and Rename on master, on sys.objects and
// on the SQL-Server-created Agent jobs: renaming a system database runs
// SET SINGLE_USER WITH ROLLBACK IMMEDIATE — kicking every connection to msdb
// or model — before the server refuses the rename itself, and renaming a
// system Agent job is not refused at all.
func (a *App) objectOpsMenuItems(node *explorerNode) []controls.MenuItem {
	op := objectOpFor(node.data.Type)
	if op == nil || node.data.IsSystem {
		return nil
	}
	var items []controls.MenuItem
	if op.rename != nil {
		items = append(items, controls.MenuItem{Label: "Rename...", Action: func() { a.renameObject(node) }})
	}
	if op.drop != nil {
		items = append(items, controls.MenuItem{Label: "Delete...", Action: func() { a.deleteObject(node) }})
	}
	return items
}

// objectDisplayName is the object's name as the dialogs should say it:
// schema-qualified where it has a schema, bare otherwise. Not node.label,
// which carries type/state decoration ("IX_x (Nonclustered, Unique)").
//
// A schema node is the exception, and the test is its type rather than
// Schema == Name: loadSchemasChildren puts the schema's name in both fields,
// so "Sales.Sales" would be nonsense — but so would dropping the qualifier
// from the table Sales.Sales, which a value comparison also matched.
func objectDisplayName(n *explorerNode) string {
	if n.data.Schema != "" && n.data.Type != NodeSchema {
		return n.data.Schema + "." + n.data.Name
	}
	return n.data.Name
}

// deleteObject confirms, drops, and refreshes the parent folder so the node
// disappears. The parent is refreshed rather than the node itself: the node
// is gone, and its own refresh would just re-query a dropped object.
func (a *App) deleteObject(node *explorerNode) {
	op := objectOpFor(node.data.Type)
	sc := resolveConn(node)
	// IsSystem is re-checked here rather than trusted from the menu: this is
	// the function that issues the DROP, so it is where the guarantee belongs.
	if op == nil || op.drop == nil || node.data.IsSystem || !a.requireConn(sc) {
		return
	}
	name := objectDisplayName(node)
	msg := fmt.Sprintf("Delete %s %q? This cannot be undone.", strings.ToLower(op.noun), name)
	if op.warning != "" {
		msg += " " + op.warning
	}
	// The drop runs off the UI goroutine but names the object from nodeData,
	// which the UI goroutine writes (applyNodeFilter sets data.Filter). Copying
	// it here is what keeps the two apart — the ops take a nodeData by value for
	// exactly this reason.
	data := node.data
	run := func() {
		a.safego("deleting an object", func() {
			ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
			defer cancel()
			err := op.drop(ctx, sc, data)
			a.postAndWake(func() {
				if err != nil {
					a.setStatus(fmt.Sprintf("Delete failed: %v", err))
					return
				}
				a.setStatus(fmt.Sprintf("%s %q deleted", op.noun, name))
				refreshExplorerNode(a, node.parent)
			})
		})
	}
	title := "Delete " + op.noun
	if op.typed {
		a.confirmTypedDialog.ShowTypedConfirm(title, msg, node.data.Name, func(confirmed bool) {
			if confirmed {
				run()
			}
		})
		return
	}
	a.confirmDialog.ShowConfirm(title, msg, func(confirmed bool) {
		if confirmed {
			run()
		}
	})
}

// renameObject prompts for a new name and applies it. The new name is a
// bare name even for a schema-scoped object — sp_rename refuses a qualified
// one, and renaming never moves an object between schemas.
func (a *App) renameObject(node *explorerNode) {
	op := objectOpFor(node.data.Type)
	sc := resolveConn(node)
	if op == nil || op.rename == nil || node.data.IsSystem || !a.requireConn(sc) {
		return
	}
	oldName := node.data.Name
	a.promptDialog.ShowPrompt("Rename "+op.noun,
		fmt.Sprintf("New name for %s %q:", strings.ToLower(op.noun), objectDisplayName(node)),
		"Name:", oldName,
		func(newName string) {
			if newName == oldName {
				return
			}
			if op.renameWarning != "" {
				a.confirmDialog.ShowConfirm("Rename "+op.noun, op.renameWarning, func(confirmed bool) {
					if confirmed {
						a.runRename(sc, node, op, oldName, newName)
					}
				})
				return
			}
			a.runRename(sc, node, op, oldName, newName)
		})
}

// runRename performs the rename itself, once the new name is known and any
// warning has been accepted.
func (a *App) runRename(sc *db.ServerConn, node *explorerNode, op *objectOp, oldName, newName string) {
	// Copied on the UI goroutine, which is the only one that writes it — see
	// deleteObject. node itself is still needed, but only inside postAndWake.
	data := node.data
	a.safego("renaming an object", func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		err := op.rename(ctx, sc, data, newName)
		a.postAndWake(func() {
			if err != nil {
				a.setStatus(fmt.Sprintf("Rename failed: %v", err))
				return
			}
			a.setStatus(fmt.Sprintf("%s %q renamed to %q", op.noun, oldName, newName))
			// The parent, not the node: the node's label is built by the
			// folder's loader, so only a reload of the folder shows the new
			// name.
			refreshExplorerNode(a, node.parent)
		})
	})
}
