package tui

import (
	"fmt"
	"strings"

	dbconn "github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// detail_browser_ops.go is the Object Explorer Details pane's write path: the
// Delete its grid's context menu offers over whatever the block selection
// covers.
//
// It lives in the pane rather than in the tree because controls.TreeView has a
// single selection, so SSMS's "Delete Object" dialog listing several objects
// can never come from there. The grid already had block selection; what it had
// no route to was an action.
//
// Rename and Move to Schema stay in the tree deliberately: neither means
// anything applied to a set, and both are one click away there.

// selectedRowObjects is the object each selected row describes, in row order,
// or nil when this view's rows are not objects (rowObjs empty) or the selection
// runs past them.
//
// DataGrid.SelectedRows, never SelectionBounds: Ctrl+click builds a selection
// of rows that no rectangle describes, and the bounds of one would name every
// row between two picked ones as well. The rows, never the cells, for the same
// kind of reason — which columns a selection covers says nothing about which
// objects it covers.
func (db *DetailBrowser) selectedRowObjects() []nodeData {
	if len(db.rowObjs) == 0 {
		return nil
	}
	rows := db.grid.SelectedRows()
	objs := make([]nodeData, 0, len(rows))
	for _, r := range rows {
		if r < 0 || r >= len(db.rowObjs) {
			return nil
		}
		objs = append(objs, db.rowObjs[r])
	}
	if len(objs) == 0 {
		return nil
	}
	return objs
}

// detailMenuItems is the pane's contribution to the grid's cell context menu
// (DataGrid.OnMenuItems), rebuilt every time the menu opens so it describes the
// selection in force rather than the one current when the view loaded.
func (a *App) detailMenuItems(db *DetailBrowser) []controls.MenuItem {
	objs := db.selectedRowObjects()
	if len(objs) == 0 || db.currentNode == nil {
		return nil
	}
	sc := resolveConn(db.currentNode)
	if sc == nil {
		return nil
	}
	// Every selected object has to be deletable, not just the first: a batch
	// that dropped what it could and skipped the rest would be a partial delete
	// nobody asked for.
	for _, n := range objs {
		op := objectOpFor(n.Type)
		if op == nil || (op.drop == nil && op.dropWithOption == nil) {
			return nil
		}
	}
	// A type that is deleted on its own — a database, a principal, an encryption
	// key — is offered as a withheld item naming the row to keep, not left out
	// of the menu: a Delete that vanishes when a second row is selected reads as
	// the pane having no Delete at all. confirmDeleteObjects refuses the batch
	// too; this is the half that says so before the click.
	if len(objs) > 1 {
		for _, n := range objs {
			if op := objectOpFor(n.Type); deletedAlone(op) {
				return []controls.MenuItem{{
					Label:   deleteItemLabel(objs),
					Enabled: func() bool { return false },
					Note:    soloDeleteReason(op, n),
				}}
			}
		}
	}
	// The folder, not the rows: the objects are gone, and it is the folder's
	// listing that has to be read again — the same reason the tree refreshes a
	// deleted node's parent.
	node := db.currentNode
	item := controls.MenuItem{
		Label:  deleteItemLabel(objs),
		Action: func() { a.confirmDeleteObjects(sc, objs, func() { refreshExplorerNode(a, node) }) },
	}
	return []controls.MenuItem{gateDeleteSelection(item, sc, objs)}
}

// deleteItemLabel names what the item deletes: the plain verb for one object,
// the count and the shared noun for a selection of one kind, and a bare count
// for a mixed one.
func deleteItemLabel(objs []nodeData) string {
	if len(objs) == 1 {
		return "Delete..."
	}
	noun := objectOpFor(objs[0].Type).noun
	for _, n := range objs[1:] {
		if objectOpFor(n.Type).noun != noun {
			return fmt.Sprintf("Delete %d Objects...", len(objs))
		}
	}
	return fmt.Sprintf("Delete %d %s...", len(objs), pluralNoun(noun))
}

// pluralNoun pluralises an objectOp noun. Two rules cover the whole table, and
// TestEveryObjectOpNounPluralises is what keeps that true as it grows: "Index"
// is the only one taking -es, and "Security Policy" the only one taking -ies
// ("Key" keeps its -s, its y following a vowel).
func pluralNoun(noun string) string {
	switch {
	case strings.HasSuffix(noun, "x") || strings.HasSuffix(noun, "s") ||
		strings.HasSuffix(noun, "ch") || strings.HasSuffix(noun, "sh"):
		return noun + "es"
	case strings.HasSuffix(noun, "y") && !strings.ContainsAny(noun[len(noun)-2:len(noun)-1], "aeiou"):
		return noun[:len(noun)-1] + "ies"
	}
	return noun + "s"
}

// gateDeleteSelection withholds Delete unless every selected object may be
// deleted — the rights objectOpsMenuItems gates the tree's Delete on, asked per
// object, plus the system-object rule the tree applies through nodeData.IsSystem.
//
// Per object rather than once for the folder: rightAlterOnSchema and
// rightAlterOnObject answer about a named securable, so a selection spanning
// two schemas can be permitted in one and refused in the other, and one answer
// for the batch would be right about at most one of them.
//
// The note names the object that is the problem, which gate's does not have to:
// "needs ALTER" on a forty-row selection says nothing about which row to
// deselect.
func gateDeleteSelection(item controls.MenuItem, sc *dbconn.ServerConn, objs []nodeData) controls.MenuItem {
	firstDenied := func() (nodeData, bool) {
		for _, n := range objs {
			if n.IsSystem {
				return n, true
			}
			if !allowsActionOn(sc, n.DBName, objectDataSchema(n), objectDataObject(n), objectOpRights(n.Type)...) {
				return n, true
			}
		}
		return nodeData{}, false
	}
	item.Enabled = func() bool { _, denied := firstDenied(); return !denied }
	if n, denied := firstDenied(); denied {
		if n.IsSystem {
			item.Note = objectDataName(n) + " is a system object"
		} else {
			item.Note = objectDataName(n) + " needs " + objectOpRights(n.Type)[0].name
		}
	}
	return item
}

// objectDataSchema and objectDataObject are objectOpSchema/objectOpName for a
// nodeData on its own — see those two for why a schema node answers "" to both.
func objectDataSchema(n nodeData) string {
	if n.Type == NodeSchema {
		return ""
	}
	return n.Schema
}

func objectDataObject(n nodeData) string {
	if n.Type == NodeSchema {
		return ""
	}
	return n.Name
}
