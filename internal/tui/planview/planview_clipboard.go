package planview

// HasSelection and the SelectedText, Cut, Paste and SelectAll beside it let a
// host wire PlanView into its App-level Copy/Cut/Paste (see
// internal/tui/clipboard.go's clipboardTarget interface), as DetailBrowser
// does for its grid. The XML tab forwards to its editor's selection; the Plan
// and Tree tabs report the selected operator's details (details.go's
// formatDetailsText) as their "selection". The summary grid's "Show Value"
// popup takes precedence while open: DataGrid is then a clipboard target backed
// by the popup's read-only editor, so Ctrl+C copies its selected text. Its
// HasSelection is false when the popup is closed, so this falls through (the
// fall-back contract DataGrid documents for propsheet.GridRow).
func (v *PlanView) HasSelection() bool {
	if v.summaryVisible() && v.summarySt.grid.HasSelection() {
		return true
	}
	switch {
	case v.activeTab == TabXML:
		return v.xml.HasSelection()
	case v.activeTab == TabTree, v.activeTab == TabPlan:
		return v.selectedNode() != nil
	}
	return false
}
func (v *PlanView) SelectedText() string {
	if v.summaryVisible() && v.summarySt.grid.HasSelection() {
		return v.summarySt.grid.SelectedText()
	}
	switch {
	case v.activeTab == TabXML:
		return v.xml.SelectedText()
	case v.activeTab == TabTree, v.activeTab == TabPlan:
		if n := v.selectedNode(); n != nil {
			return formatDetailsText(n, v.currentStatement(), v.liveCountersPtr(n))
		}
	}
	return ""
}
func (v *PlanView) Cut() string       { return v.SelectedText() }
func (v *PlanView) Paste(text string) {}

// SelectAll gates on OverlayActive, not the HasSelection its siblings use:
// nothing is selected yet when Select All is invoked, so HasSelection is false
// exactly when this must route to the popup. DataGrid.SelectAll is a no-op
// unless its viewer is open, so the wider gate costs nothing.
func (v *PlanView) SelectAll() {
	if v.summaryVisible() && v.summarySt.grid.OverlayActive() {
		v.summarySt.grid.SelectAll()
		return
	}
	if v.activeTab == TabXML {
		v.xml.SelectAll()
	}
}
