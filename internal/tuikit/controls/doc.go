// Package controls provides higher-level, reusable TUI controls:
//
//   - MenuBar / ContextMenu — application menu bar (menu_bar.go) and
//     floating right-click popup menu (context_menu.go); their shared
//     MenuItem/Menu types live in menu_item.go
//   - Toolbar — row of icon-only buttons with hover tooltips (toolbar.go)
//   - TreeView — collapsible/expandable tree with generic node data
//     (treeview.go)
//   - DataGrid — scrollable, column-aligned tabular data display
//     (datagrid.go)
//   - Editor — multi-line text editor with optional syntax highlighting,
//     including the built-in SQLHighlighter (editor.go)
//   - TabStripSegments — shared column-layout math for horizontal tab bars,
//     used by every tab strip in the app so draw and hit-test code can't
//     drift apart (tabstrip.go)
//
// Controls depend on core, theme, and widgets but not on any application
// types.  The application layer passes data in and reads state out;
// controls never call back into the application directly — instead they
// fire callbacks (func values) that the caller wires up.
package controls
