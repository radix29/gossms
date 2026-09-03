// Package controls provides higher-level, reusable TUI controls:
//
//   - MenuBar / ContextMenu — application menu bar (menu_bar.go) and
//     floating right-click popup menu (context_menu.go); their shared
//     MenuItem/Menu types live in menu_item.go, and the nested submenu
//     machinery in menu_cascade.go
//   - Toolbar — row of icon-only buttons with hover tooltips (toolbar.go)
//   - TreeView — collapsible/expandable tree with generic node data
//     (treeview.go)
//   - ListBox — scrollable single-column list of strings (listbox.go)
//   - DataGrid — scrollable, column-aligned tabular data display, split
//     across datagrid.go, datagrid_draw.go, datagrid_input.go and
//     datagrid_overlay.go
//   - Editor — multi-line text editor with optional syntax highlighting,
//     split across editor*.go over its buffer type Document (document.go);
//     the built-in SQL, XML and JSON highlighters and the statement
//     splitter live in sql_highlighter.go, xml_highlighter.go,
//     json_highlighter.go and sql_statement.go
//   - TabStripSegments — shared column-layout math for horizontal tab bars,
//     used by every tab strip in the app so draw and hit-test code can't
//     drift apart (tabstrip.go)
//
// Controls depend on core, theme, and widgets but not on any application
// types.  The application layer passes data in and reads state out;
// controls never call back into the application directly — instead they
// fire callbacks (func values) that the caller wires up.
package controls
