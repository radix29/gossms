// Package layout provides Panel interface, PanelManager (tab/combo switcher),
// and Splitter (draggable/keyboard-resizable divider between two regions).
// These types are pure layout infrastructure; they have no dependency on any
// application-level code.
//
//   - panel.go         — Panel and Activatable interfaces
//   - panel_manager.go — PanelManager
//   - splitter.go      — SplitterDir, Splitter
package layout
