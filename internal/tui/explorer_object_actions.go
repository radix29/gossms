package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
)

// explorer_object_actions.go is the Delete, Rename and Move-to-schema flows:
// how each names the object, confirms, scripts or runs, and refreshes the tree
// afterwards. What each action means per node type is the table in
// explorer_object_ops.go.

// objectDisplayName is the object's name as the dialogs should say it:
// schema-qualified where it has a schema, bare otherwise. Not node.label, which
// carries type/state decoration ("IX_x (Nonclustered, Unique)").
//
// A schema node is the exception, tested by type rather than Schema == Name:
// loadSchemasChildren puts the schema's name in both fields, so "Sales.Sales"
// would be nonsense, but so would dropping the qualifier from the table
// Sales.Sales, which a value comparison also matched.
func objectDisplayName(n *explorerNode) string { return objectDataName(n.data) }

// objectDataName is objectDisplayName for a nodeData on its own (the Details
// pane holds no node).
func objectDataName(n nodeData) string {
	// A column's own Schema/Name are the table's schema and the column's name, so
	// the meaningful qualifier is the table's.
	if n.Type == NodeColumn {
		return n.Schema + "." + n.TableName + "." + n.Name
	}
	if n.Schema != "" && n.Type != NodeSchema {
		return n.Schema + "." + n.Name
	}
	return n.Name
}

// deleteObject confirms, drops, and refreshes the parent folder so the node
// disappears. The parent is refreshed rather than the node: the node is gone,
// and its own refresh would re-query a dropped object.
//
// The drop runs off the UI goroutine but names the object from nodeData, which
// the UI goroutine writes (applyNodeFilter sets data.Filter). Copying it here
// keeps the two apart; the ops take a nodeData by value for this reason.
func (a *App) deleteObject(node *explorerNode) {
	sc := resolveConn(node)
	a.confirmDeleteObjects(sc, []nodeData{node.data}, func() {
		a.explorer.ReloadFolders(sc, sameNodeAs(node.parent))
	})
}

// confirmDeleteObjects is the whole of Delete for one or more objects: the
// confirmation, the drops, and after() once done. Object Explorer reaches it
// with a single node; the Details pane with everything the grid's block
// selection covers.
//
// One path, not two: a second copy is where a warning, a typed confirmation or
// the foreign-key option would quietly stop applying. Deleting from the tree
// and from the pane must differ only by where the click landed.
func (a *App) confirmDeleteObjects(sc *db.ServerConn, objs []nodeData, after func()) {
	// IsSystem and the op are re-checked here rather than trusted from the menu:
	// this function issues the DROP, so the guarantee belongs here.
	if len(objs) == 0 || !a.requireConn(sc) {
		return
	}
	ops := make([]*objectOp, len(objs))
	for i, n := range objs {
		op := objectOpFor(n.Type)
		if op == nil || (op.drop == nil && op.dropWithOption == nil) || n.IsSystem {
			return
		}
		ops[i] = op
	}
	run := func(option bool) { a.runDeletes(sc, objs, ops, option, after) }
	// answered routes the two buttons that do something. Script is neither a Yes
	// nor a No: nothing is dropped, and the statements the Yes would have run open
	// in a query window for the user to read, edit or run.
	answered := func(ans dialogs.ConfirmAnswer, option bool) {
		switch ans {
		case dialogs.ConfirmYes:
			run(option)
		case dialogs.ConfirmScript:
			a.scriptDeletes(sc, objs, ops, option)
		}
	}

	if len(objs) == 1 {
		op, n := ops[0], objs[0]
		name := objectDataName(n)
		msg := fmt.Sprintf("Delete %s %q? This cannot be undone.", strings.ToLower(op.noun), name)
		if op.warning != "" {
			msg += " " + op.warning
		}
		title := "Delete " + op.noun
		if typedDelete(op, n) {
			if op.typedWarning != "" {
				msg += " " + op.typedWarning
			}
			a.confirmTypedDialog.ShowTypedConfirmScript(title, msg, n.Name, func(ans dialogs.ConfirmAnswer) {
				answered(ans, false)
			})
			return
		}
		// The checkbox is unticked on every showing: the option widens what the drop
		// touches, so it is asked each time, not remembered. An op without one passes
		// "", showing the question with no checkbox.
		a.confirmDialog.ShowConfirmScript(title, msg, op.dropOption, false, answered)
		return
	}

	// Some types are deleted one at a time, from either surface: a typed
	// confirmation asks for one object's name and cannot stand for a selection, and
	// a principal or database is dropped with a deliberation the batch
	// confirmation's one shared warning cannot carry.
	for i, op := range ops {
		if deletedAlone(op, objs[i]) {
			a.setStatus(soloDeleteReason(op, objs[i]))
			return
		}
	}
	// One flowing sentence: ConfirmDialog wraps and centres its message, so a list
	// laid out with newlines arrives as prose with the names run into it.
	msg := fmt.Sprintf("Delete these %d objects — %s? This cannot be undone.",
		len(objs), deleteListText(objs))
	if w := sharedDeleteWarning(ops); w != "" {
		// The op's warning is written about one object ("All of its data is deleted
		// with it"), so it is introduced rather than repeated as-is.
		msg += " Each object: " + w
	}
	// The checkbox is offered only when every selected object answers to it, since
	// one tick drives every drop in the batch.
	a.confirmDialog.ShowConfirmScript("Delete Objects", msg, sharedDropOption(ops), false, answered)
}

// scriptDeletes opens the DROP statements this delete would have run in a new
// query window without running any: the same drop closures under
// gosmo.WithScript, so what the user reads is what the Yes would execute, not a
// second rendering that could drift.
//
// The option is passed through as it is to run: the checkbox changes the
// statement, and a script ignoring it would answer a different question from the
// one on screen.
func (a *App) scriptDeletes(sc *db.ServerConn, objs []nodeData, ops []*objectOp, option bool) {
	a.safego("scripting deletes", func() {
		ctx, cancel := serverWriteContext(sc)
		defer cancel()
		scriptCtx, script := gosmo.WithScript(ctx)
		var err error
		for i, n := range objs {
			if ops[i].dropWithOption != nil {
				err = ops[i].dropWithOption(scriptCtx, sc, n, option)
			} else {
				err = ops[i].drop(scriptCtx, sc, n)
			}
			if err != nil {
				break
			}
		}
		if s := settlingOp(ops); err == nil && s != nil {
			err = s.settle(scriptCtx, sc)
		}
		text := script.String()
		// The database the first object lives in: a selection comes from one folder,
		// and a server-level principal's is "", which opens the panel on the
		// connection's own default.
		database := objs[0].DBName
		a.postAndWake(func() {
			switch {
			case err != nil:
				a.setStatus(fmt.Sprintf("Script failed: %v", withPermissionAdvice(err)))
			case text == "":
				a.setStatus("Nothing to script.")
			default:
				a.openQueryWithText(sc, database, text)
			}
		})
	})
}

// maxDeleteListNames caps how many object names the multi-object confirmation
// spells out; the rest are counted. A selection can be the whole folder, and a
// dialog listing four hundred names says less than ten and a count.
const maxDeleteListNames = 10

// deleteListText is the object list the multi-object confirmation shows.
func deleteListText(objs []nodeData) string {
	var b strings.Builder
	for i, n := range objs {
		if i > 0 {
			b.WriteString(", ")
		}
		if i == maxDeleteListNames {
			fmt.Fprintf(&b, "and %d more", len(objs)-i)
			break
		}
		b.WriteString(objectDataName(n))
	}
	return b.String()
}

// sharedDropOption is the drop checkbox the whole selection answers to, or ""
// when they do not all carry the same one.
func sharedDropOption(ops []*objectOp) string {
	opt := ops[0].dropOption
	for _, op := range ops[1:] {
		if op.dropOption != opt {
			return ""
		}
	}
	return opt
}

// sharedDeleteWarning is the extra sentence the confirmation carries when every
// selected object has the same one ("All of its data is deleted with it." for a
// set of tables). A mixed selection drops it rather than show a warning true of
// only some of what is about to go.
func sharedDeleteWarning(ops []*objectOp) string {
	w := ops[0].warning
	for _, op := range ops[1:] {
		if op.warning != w {
			return ""
		}
	}
	return w
}

// runDeletes drops each object in turn on one background goroutine and reports
// what happened. Sequential, not concurrent: it is DDL, and a batch that half
// succeeded has to say which half (a fan-out would report whichever error
// arrived first as if it were the only one).
//
// The drops run behind the progress dialog. Cancel stops the drop in flight and
// the batch with it (the context is checked before every object as well as
// handed to each drop), and what already went stays gone, which is why the
// count leads the status line.
func (a *App) runDeletes(sc *db.ServerConn, objs []nodeData, ops []*objectOp, option bool, after func()) {
	done := 0
	var failed nodeData
	// settleErr is the settle step's failure, kept apart from the drops': those
	// stand either way, and the count still leads the status line.
	var settleErr error
	settling := settlingOp(ops)
	job := progressJob{
		title:   "Delete Objects",
		message: fmt.Sprintf("Deleting %d objects...", len(objs)),
		what:    "deleting objects",
		sc:      sc,
	}
	if len(objs) == 1 {
		job.title = "Delete " + ops[0].noun
		job.message = fmt.Sprintf("Deleting %s %q...", strings.ToLower(ops[0].noun), objectDataName(objs[0]))
	}
	a.runWithProgress(job, func(ctx context.Context, report progressReport) error {
		for i, n := range objs {
			if err := ctx.Err(); err != nil {
				failed = n
				return err
			}
			if len(objs) > 1 {
				report(fmt.Sprintf("Deleting %d of %d: %s", i+1, len(objs), objectDataName(n)))
			}
			var err error
			if ops[i].dropWithOption != nil {
				err = ops[i].dropWithOption(ctx, sc, n, option)
			} else {
				err = ops[i].drop(ctx, sc, n)
			}
			if err != nil {
				failed = n
				if done > 0 && settling != nil && ctx.Err() == nil {
					settleErr = settling.settle(ctx, sc)
				}
				return err
			}
			done++
		}
		if settling != nil {
			settleErr = settling.settle(ctx, sc)
		}
		return nil
	}, func(failedErr error, cancelled bool) {
		if settling != nil && settling.settled != nil {
			defer settling.settled(a, sc)
		}
		switch {
		case cancelled && len(objs) == 1:
			a.setStatus(fmt.Sprintf("Delete of %s %q cancelled", strings.ToLower(ops[0].noun), objectDataName(objs[0])))
		case cancelled:
			a.setStatus(fmt.Sprintf("Delete cancelled — %d of %d deleted", done, len(objs)))
		case failedErr != nil && done == 0 && len(objs) == 1:
			a.setStatus(fmt.Sprintf("Delete failed: %v", withPermissionAdvice(failedErr)))
		case failedErr != nil:
			// The count first: the drops already ran, and which landed is what the user
			// must know before retrying.
			a.setStatus(fmt.Sprintf("Deleted %d of %d — %s failed: %v",
				done, len(objs), objectDataName(failed), withPermissionAdvice(failedErr)))
		case settleErr != nil && len(objs) == 1:
			a.setStatus(fmt.Sprintf("%s %q deleted, but applying the change failed: %v",
				ops[0].noun, objectDataName(objs[0]), withPermissionAdvice(settleErr)))
		case settleErr != nil:
			a.setStatus(fmt.Sprintf("Deleted %d objects, but applying the change failed: %v", done, withPermissionAdvice(settleErr)))
		case len(objs) == 1:
			a.setStatus(fmt.Sprintf("%s %q deleted", ops[0].noun, objectDataName(objs[0])))
		default:
			a.setStatus(fmt.Sprintf("Deleted %d objects", done))
		}
		// Even a partial batch refreshes: some objects are gone, and a folder still
		// listing them is worse than the failure. A cancelled one does too, whatever the
		// count: the cancel can reach the server after the interrupted drop committed.
		if done > 0 || failedErr == nil || cancelled {
			after()
		}
	})
}

// settlingOp is the op whose settle a delete of ops runs, or nil when none
// has one.
func settlingOp(ops []*objectOp) *objectOp {
	for _, op := range ops {
		if op.settle != nil {
			return op
		}
	}
	return nil
}

// renameObject prompts for a new name and applies it. The new name is a bare
// name even for a schema-scoped object: sp_rename refuses a qualified one, and
// renaming never moves an object between schemas.
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
	// Copied on the UI goroutine, the only one that writes it (see deleteObject).
	// node is still needed, but only inside postAndWake.
	data := node.data
	a.runWithProgress(progressJob{
		title:   "Rename " + op.noun,
		message: fmt.Sprintf("Renaming %s %q to %q...", strings.ToLower(op.noun), oldName, newName),
		what:    "renaming an object",
		sc:      sc,
	}, func(ctx context.Context, _ progressReport) error {
		return op.rename(ctx, sc, data, newName)
	}, func(err error, cancelled bool) {
		// The parent, not the node: the node's label is built by the folder's loader,
		// so only a reload of the folder shows the new name. After a cancel too, since
		// it may have reached the server after the rename did.
		switch {
		case cancelled:
			a.setStatus(fmt.Sprintf("Rename of %s %q cancelled", strings.ToLower(op.noun), oldName))
			a.explorer.ReloadFolders(sc, sameNodeAs(node.parent))
		case err != nil:
			a.setStatus(fmt.Sprintf("Rename failed: %v", withPermissionAdvice(err)))
		default:
			a.setStatus(fmt.Sprintf("%s %q renamed to %q", op.noun, oldName, newName))
			a.explorer.ReloadFolders(sc, sameNodeAs(node.parent))
		}
	})
}

// moveObjectToSchema offers the database's other schemas for node's object and
// runs ALTER SCHEMA ... TRANSFER on the one picked: what Rename deliberately
// cannot do (sp_rename takes a bare name and never crosses schemas).
//
// The list is fetched before the menu opens rather than typed into a prompt: a
// mistyped schema reaches the server as "Cannot find the schema", and the set
// of legal answers is short and known.
func (a *App) moveObjectToSchema(node *explorerNode) {
	op := objectOpFor(node.data.Type)
	sc := resolveConn(node)
	// Re-checked here, not trusted from the menu (as in deleteObject): this
	// function issues the statement.
	if op == nil || op.transfer == nil || node.data.IsSystem || !a.requireConn(sc) {
		return
	}
	data := node.data
	a.setStatus(fmt.Sprintf("Reading schemas in %q...", data.DBName))
	a.safego("listing schemas", func() {
		ctx, cancel := context.WithTimeout(sc.Server.Context(), childFetchTimeout)
		defer cancel()
		d, err := sc.Server.DatabaseByName(ctx, data.DBName)
		var names []string
		if err == nil {
			var schemas []*gosmo.Schema
			if schemas, err = d.Schemas(ctx); err == nil {
				for _, s := range schemas {
					if !gosmo.SameName(databaseCollation(d), s.Name, data.Schema) {
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

// confirmMoveToSchema asks before the transfer, because it is not only a move:
// SQL Server drops every permission granted directly on the object as part of
// ALTER SCHEMA ... TRANSFER, and nothing afterwards says so.
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
			a.runWithProgress(progressJob{
				title:   "Move to Schema",
				message: fmt.Sprintf("Moving %s %q into schema %q...", strings.ToLower(op.noun), name, target),
				what:    "moving an object between schemas",
				sc:      sc,
			}, func(ctx context.Context, _ progressReport) error {
				return op.transfer(ctx, sc, data, target)
			}, func(err error, cancelled bool) {
				// The parent folder, not the node: its label is built by the folder's loader
				// from the schema the object was in.
				switch {
				case cancelled:
					a.setStatus(fmt.Sprintf("Move of %s %q cancelled", strings.ToLower(op.noun), name))
					a.explorer.ReloadFolders(sc, sameNodeAs(node.parent))
				case err != nil:
					a.setStatus(fmt.Sprintf("Move failed: %v", withPermissionAdvice(err)))
				default:
					a.setStatus(fmt.Sprintf("%s %q moved to schema %q", op.noun, name, target))
					a.explorer.ReloadFolders(sc, sameNodeAs(node.parent))
				}
			})
		})
}
