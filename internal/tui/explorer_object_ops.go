package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// explorer_object_ops.go is Object Explorer's general Delete and Rename: one
// table of what the two mean per node type, plus the shared actions that
// confirm, run and refresh around them. A node type absent from the table
// offers neither, which is how a folder or anything gosmo can't drop stays out
// of the menu instead of failing when clicked.
//
// SQL Server Agent objects are renameable here but keep their own Delete (see
// agent_menu.go).

// objectOp is what Delete and Rename do for one node type. A nil drop or rename
// means the node doesn't offer that action.
//
// Both take a nodeData by value, not the *explorerNode it came off: they run on
// a background goroutine while the UI goroutine writes node.data.
// deleteObject/runRename make the copy before the safego.
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
	// dropOption labels a checkbox on the delete confirmation and dropWithOption
	// is the drop it feeds. A type setting these sets both and leaves drop nil:
	// deleteObject picks the path from dropOption, objectOpsMenuItems from either
	// drop being present.
	dropOption     string
	dropWithOption func(ctx context.Context, sc *db.ServerConn, n nodeData, opt bool) error
	// transfer moves the object into another schema (ALTER SCHEMA ... TRANSFER),
	// which a rename cannot do. Only the sp_rename 'OBJECT' families and tables
	// have one.
	transfer func(ctx context.Context, sc *db.ServerConn, n nodeData, targetSchema string) error
	// renameWarning is a question asked between the new-name prompt and the
	// rename itself, for a rename that costs more than the name change.
	renameWarning string
}

// dbOf is the database a node's object lives in.
//
// Server.Database, not DatabaseByName: every statement below names its object in
// the text and reads nothing off the *gosmo.Database but its name, so the
// sys.databases round trip buys nothing — and DatabaseByName cannot run under a
// WithScript-derived context, which Script Changes on Delete needs.
func dbOf(sc *db.ServerConn, n nodeData) *gosmo.Database {
	return sc.Server.Database(n.DBName)
}

// tableOf is the table a table-scoped node (index, statistic, key, constraint)
// belongs to — nodeData.TableName, since Schema/Name there name the index or
// constraint itself. A name-only handle, for the same reason as dbOf.
func tableOf(sc *db.ServerConn, n nodeData) *gosmo.Table {
	return sc.Server.Database(n.DBName).Table(n.Schema, n.TableName)
}

// objectOps is the per-type table. Every rename going through
// Database.RenameObjectContext is sp_rename's 'OBJECT' class — view, procedure,
// function, sequence, synonym, trigger, constraint. Indexes and statistics have
// their own sp_rename object types and gosmo methods.
var objectOps = map[NodeType]objectOp{
	NodeDatabase: {
		noun:    "Database",
		warning: "Existing connections to it will be closed.",
		typed:   true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return sc.Server.DropDatabaseContext(ctx, n.Name, true)
		},
		// MODIFY NAME needs exclusive access, which the tree's own metadata
		// connections deny — so the rename always closes connections, and always
		// asks first.
		renameWarning: "Renaming a database needs exclusive access to it. Existing connections will be closed and their transactions rolled back. Continue?",
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			return sc.Server.RenameDatabaseContext(ctx, n.Name, newName, true)
		},
	},
	NodeTable: {
		noun:    "Table",
		warning: "All of its data is deleted with it.",
		// Unticked by default, so the plain gesture is SSMS's: a referenced
		// table is refused until the foreign key is dealt with. Ticking it drops
		// those foreign keys on the *other* tables, which is why it is a
		// decision and not a retry.
		dropOption: "Also drop the foreign keys that reference it",
		dropWithOption: func(ctx context.Context, sc *db.ServerConn, n nodeData, cascade bool) error {
			return dbOf(sc, n).DropTableContext(ctx, n.Schema, n.Name, cascade)
		},
		transfer: transferObjectIn,
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			return dbOf(sc, n).RenameTableContext(ctx, n.Schema, n.Name, newName)
		},
	},
	NodeView:            {noun: "View", drop: dropIn((*gosmo.Database).DropViewContext), rename: renameObjectIn, transfer: transferObjectIn},
	NodeStoredProcedure: {noun: "Stored Procedure", drop: dropIn((*gosmo.Database).DropStoredProcedureContext), rename: renameObjectIn, transfer: transferObjectIn},
	NodeFunction:        {noun: "Function", drop: dropIn((*gosmo.Database).DropFunctionContext), rename: renameObjectIn, transfer: transferObjectIn},
	// A trigger belongs to its table and moves with it; ALTER SCHEMA TRANSFER
	// refuses one.
	NodeTrigger:  {noun: "Trigger", drop: dropIn((*gosmo.Database).DropTriggerContext), rename: renameObjectIn},
	NodeSequence: {noun: "Sequence", drop: dropIn((*gosmo.Database).DropSequenceContext), rename: renameObjectIn, transfer: transferObjectIn},
	NodeSynonym:  {noun: "Synonym", drop: dropIn((*gosmo.Database).DropSynonymContext), rename: renameObjectIn, transfer: transferObjectIn},

	NodeColumn: {
		noun: "Column",
		// The server refuses a column anything depends on — a default or check
		// constraint, an index, a statistic — and its message doesn't name what
		// ("one or more objects access this column"), so the warning names the
		// classes itself.
		warning: "Its data goes with it, and the server refuses the drop while a constraint, index or statistic depends on the column — without naming which.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return tableOf(sc, n).DropColumnContext(ctx, n.Name)
		},
		// sp_rename updates the column and nothing that names it, and SQL
		// Server's caution ("may break scripts and stored procedures") is a
		// notice on a rename that already succeeded — so ask first.
		renameWarning: "Renaming a column does not update anything that names it. Views, procedures, functions, computed columns, check constraints and filtered indexes keep the old name and break at their next use. Continue?",
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			return tableOf(sc, n).RenameColumnContext(ctx, n.Name, newName)
		},
	},

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
		// A primary key's or unique constraint's name is its backing index's name
		// in sys.indexes, so it renames as an index, not as an object.
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
		// Not recoverable: the key material exists only encrypted here, so every
		// column encrypted with it becomes unreadable ciphertext.
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
		// ALTER SCHEMA ... TRANSFER — so Rename is deliberately absent.
		noun: "Schema",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			d := dbOf(sc, n)
			return d.DropSchemaContext(ctx, n.Name)
		},
	},

	// Agent objects: Rename only. Their Delete lives in agent_menu.go.
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
// methods into a drop function.
func dropIn(fn func(*gosmo.Database, context.Context, string, string) error) func(context.Context, *db.ServerConn, nodeData) error {
	return func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
		return fn(dbOf(sc, n), ctx, n.Schema, n.Name)
	}
}

// renameObjectIn is sp_rename's 'OBJECT' class, shared by every schema-scoped
// object that isn't a table, index, or statistic.
func renameObjectIn(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
	return dbOf(sc, n).RenameObjectContext(ctx, n.Schema, n.Name, newName)
}

// transferObjectIn moves a schema-scoped object into another schema. Shared
// by every family ALTER SCHEMA ... TRANSFER's default OBJECT class covers.
func transferObjectIn(ctx context.Context, sc *db.ServerConn, n nodeData, targetSchema string) error {
	return dbOf(sc, n).TransferObjectContext(ctx, targetSchema, n.Schema, n.Name)
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
	if op.transfer != nil {
		items = append(items, controls.MenuItem{Label: "Move to Schema...", Action: func() { a.moveObjectToSchema(node) }})
	}
	if op.drop != nil || op.dropWithOption != nil {
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
	// A column's own Schema/Name are the table's schema and the column's
	// name, so the qualifier that means anything is the table's.
	if n.data.Type == NodeColumn {
		return n.data.Schema + "." + n.data.TableName + "." + n.data.Name
	}
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
	if op == nil || (op.drop == nil && op.dropWithOption == nil) || node.data.IsSystem || !a.requireConn(sc) {
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
	run := func(option bool) {
		a.safego("deleting an object", func() {
			ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
			defer cancel()
			var err error
			if op.dropWithOption != nil {
				err = op.dropWithOption(ctx, sc, data, option)
			} else {
				err = op.drop(ctx, sc, data)
			}
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
				run(false)
			}
		})
		return
	}
	if op.dropOption != "" {
		// Unticked on every showing: the option widens what the drop touches,
		// so it is asked for each time rather than remembered.
		a.confirmDialog.ShowConfirmOption(title, msg, op.dropOption, false, func(confirmed, option bool) {
			if confirmed {
				run(option)
			}
		})
		return
	}
	a.confirmDialog.ShowConfirm(title, msg, func(confirmed bool) {
		if confirmed {
			run(false)
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

// moveObjectToSchema offers the database's other schemas for node's object
// and runs ALTER SCHEMA ... TRANSFER on the one picked — Object Explorer's
// answer to the thing Rename deliberately cannot do (sp_rename takes a bare
// name and never crosses schemas).
//
// The list is fetched before the menu opens rather than typed into a prompt:
// a mistyped schema reaches the server as "Cannot find the schema", and the
// set of legal answers is short and already known.
func (a *App) moveObjectToSchema(node *explorerNode) {
	op := objectOpFor(node.data.Type)
	sc := resolveConn(node)
	// Re-checked here, not trusted from the menu — the same rule deleteObject
	// follows, since this is the function that issues the statement.
	if op == nil || op.transfer == nil || node.data.IsSystem || !a.requireConn(sc) {
		return
	}
	data := node.data
	a.setStatus(fmt.Sprintf("Reading schemas in %q...", data.DBName))
	a.safego("listing schemas", func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
		defer cancel()
		d, err := sc.Server.DatabaseByNameContext(ctx, data.DBName)
		var names []string
		if err == nil {
			var schemas []*gosmo.Schema
			if schemas, err = d.SchemasContext(ctx); err == nil {
				for _, s := range schemas {
					if !strings.EqualFold(s.Name, data.Schema) {
						names = append(names, s.Name)
					}
				}
			}
		}
		a.postAndWake(func() {
			if err != nil {
				a.setStatus(fmt.Sprintf("Could not list schemas: %v", err))
				return
			}
			if len(names) == 0 {
				a.setStatus(fmt.Sprintf("%q is the only schema in %q — nowhere to move it", data.Schema, data.DBName))
				return
			}
			items := make([]controls.MenuItem, 0, len(names))
			for _, name := range names {
				items = append(items, controls.MenuItem{Label: name, Action: func() {
					a.confirmMoveToSchema(sc, node, op, name)
				}})
			}
			x, y, ok := a.explorer.SelectionAnchor()
			if !ok {
				x, y = 0, 0
			}
			a.setStatus("")
			a.contextMenu.Show(x, y+1, items)
		})
	})
}

// confirmMoveToSchema asks before the transfer, because it is not only a
// move: SQL Server drops every permission granted directly on the object as
// part of ALTER SCHEMA ... TRANSFER, and nothing afterwards says so.
func (a *App) confirmMoveToSchema(sc *db.ServerConn, node *explorerNode, op *objectOp, target string) {
	name := objectDisplayName(node)
	a.confirmDialog.ShowConfirm("Move to Schema",
		fmt.Sprintf("Move %s %q into schema %q? Permissions granted directly on it are dropped by the move.",
			strings.ToLower(op.noun), name, target),
		func(confirmed bool) {
			if !confirmed {
				return
			}
			data := node.data
			a.safego("moving an object between schemas", func() {
				ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
				defer cancel()
				err := op.transfer(ctx, sc, data, target)
				a.postAndWake(func() {
					if err != nil {
						a.setStatus(fmt.Sprintf("Move failed: %v", err))
						return
					}
					a.setStatus(fmt.Sprintf("%s %q moved to schema %q", op.noun, name, target))
					// The parent folder, not the node: its label is built by
					// the folder's loader from the schema the object was in.
					refreshExplorerNode(a, node.parent)
				})
			})
		})
}
