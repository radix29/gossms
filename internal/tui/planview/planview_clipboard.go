package planview

// HasSelection, SelectedText, Cut, Paste, and SelectAll let a host wire
// PlanView into its own App-level Copy/Cut/Paste (see
// internal/tui/clipboard.go's clipboardTarget interface) exactly like
// DetailBrowser does for its grid — the XML tab forwards to its editor's
// text selection; the Plan and Tree tabs report the selected operator's
// details (see details.go's formatDetailsText) as their "selection"
// instead, since there's no free-form text selection to speak of there.
// The summary grid's "Show Value" popup takes precedence wherever it's
// open: while it is, DataGrid is itself a clipboard target backed by the
// popup's read-only editor, so Ctrl+C copies the text selected in there
// rather than the operator details the Tree tab would otherwise offer. Its
// HasSelection is false whenever the popup is closed, so this falls straight
// through the rest of the time — the same fall-back contract DataGrid
// documents for propsheet.GridRow.
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
			return formatDetailsText(n, v.currentStatement())
		}
	}
	return ""
}
func (v *PlanView) Cut() string       { return v.SelectedText() }
func (v *PlanView) Paste(text string) {}

// SelectAll gates on OverlayActive rather than the grid's HasSelection its
// two siblings above use — deliberately. There is nothing selected yet at
// the moment Select All is invoked, so HasSelection is false exactly when
// this needs to route to the popup. DataGrid.SelectAll is itself a no-op
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
