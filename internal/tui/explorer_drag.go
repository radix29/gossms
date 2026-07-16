package tui

import "fmt"

// isDraggableNode reports whether t is a concrete SQL Server object that can
// be dragged from Object Explorer into a query editor — every node except
// grouping ("folder") nodes, the server root (no name of its own to drop),
// and the synthetic Loading/Error placeholder rows.
func isDraggableNode(t NodeType) bool {
	if isContainerNode(t) {
		return false
	}
	switch t {
	case NodeServer, NodeLoading, NodeError:
		return false
	}
	return true
}

// explorerDragText returns the quoted T-SQL identifier to insert into a
// query editor when n is dropped there. Tables, views, stored procedures,
// functions, sequences, synonyms, and triggers are addressable as
// schema.name in T-SQL (e.g. "DROP TRIGGER dbo.MyTrigger"), so those get the
// full two-part fqn; everything else (databases, schemas, columns, keys,
// indexes, statistics, foreign keys, checks, logins, users, roles, agent
// jobs, linked servers) is quoted as a single bare name. In particular, for
// the table-nested detail types (column, key, index, statistic, foreign
// key, check), n.data.Schema holds the *owning table's* schema, not a
// schema this object itself is addressed by — qualifying with it would
// produce a misleading (and invalid) two-part name.
func explorerDragText(n *explorerNode) string {
	switch n.data.Type {
	case NodeTable, NodeView, NodeStoredProcedure, NodeFunction, NodeSequence, NodeSynonym, NodeTrigger:
		return fqn(n.data.Schema, n.data.Name)
	default:
		return fqn("", n.data.Name)
	}
}

// dropExplorerNode finishes a drag armed by a Button1 press over a
// draggable Object Explorer node (see handleMouse in app_events.go): if the
// release lands inside the active query panel's editor, insert the node's
// quoted identifier at that screen position. A release back over Object
// Explorer itself is treated as a cancelled drag (silent, like releasing a
// mouse button without moving); a release anywhere else that isn't a valid
// drop target gets a status message explaining why nothing happened.
func (a *App) dropExplorerNode(mx, my int) {
	n := a.dragNode
	if n == nil {
		return
	}
	if mx < a.explorerSplit.FirstRect().Right() {
		return
	}
	qp := a.activeQueryPanel()
	if qp == nil {
		a.setStatus("Drop target must be a query window")
		return
	}
	text := explorerDragText(n)
	if !qp.editor.Bounds().Contains(mx, my) {
		a.setStatus("Drop into the query editor to insert " + text)
		return
	}
	qp.editor.SetCursorFromScreen(mx, my)
	qp.editor.Paste(text)
	a.focusPanels()
	a.setStatus(fmt.Sprintf("Inserted %s", text))
}
