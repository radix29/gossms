package tui

import (
	"fmt"

	"github.com/radix29/gossms/internal/fileutil"
	"github.com/radix29/gossms/internal/showplan"
)

// app_query_actions.go is what the toolbar and the Query menu do to the active
// query panel: execute, plan, cancel, reconnect, and save its text to a file.
// Which panel is active is answered in app_panel_close.go.

// showEstimatedExecutionPlan runs the toolbar's "Show Estimated Execution
// Plan" button.
func (a *App) showEstimatedExecutionPlan() {
	a.withQueryPanel(func(qp *QueryPanel) { qp.ShowEstimatedPlan() })
}

// toggleActualExecutionPlan flips whether Execute captures the actual
// (post-run) execution plan alongside a query's results, rebuilding the toolbar
// and Query menu to match.
func (a *App) toggleActualExecutionPlan() {
	a.actualPlanEnabled = !a.actualPlanEnabled
	a.toolbar.SetButtons(a.buildToolbar())
	a.menuBar.SetMenus(a.buildMenus())
	a.layoutAll()
	state := "off"
	if a.actualPlanEnabled {
		state = "on"
	}
	a.setStatus("Include Actual Execution Plan: " + state)
}

// toggleOutputColumnMeta flips "Show Output Column Metadata", which lists each
// result set's columns and declared types in the Messages tab. Applies to the
// next execution; results on screen are not re-rendered.
func (a *App) toggleOutputColumnMeta() {
	a.metaEnabled = !a.metaEnabled
	a.toolbar.SetButtons(a.buildToolbar())
	a.menuBar.SetMenus(a.buildMenus())
	a.layoutAll()
	state := "off"
	if a.metaEnabled {
		state = "on"
	}
	a.setStatus("Show Output Column Metadata: " + state)
}

// openPlanPanel opens a new detached panel showing plan — the Execution Plan
// tab's "[ Expand ]" action. Every call adds a new panel.
func (a *App) openPlanPanel(title string, plan *showplan.Plan) {
	a.panels.SetActive(a.panels.AddPanel(NewPlanPanel(a, title, plan)))
	a.focusPanels()
}

// openPlanComparePanel opens a plan comparison in its own panel, the way
// openPlanPanel opens a single plan.
func (a *App) openPlanComparePanel(title string, left, right *showplan.Plan) {
	a.panels.SetActive(a.panels.AddPanel(NewPlanComparePanel(a, title, left, right)))
	a.focusPanels()
}

// cancelExecutingQuery runs Query > Cancel Executing Query.
func (a *App) cancelExecutingQuery() {
	a.withQueryPanel(func(qp *QueryPanel) { qp.CancelExecution() })
}

// reconnectActiveQuery runs Query > Reconnect.
func (a *App) reconnectActiveQuery() {
	a.withQueryPanel(func(qp *QueryPanel) { qp.Reconnect() })
}

// refreshCompletionCache runs Query > Refresh IntelliSense Cache.
func (a *App) refreshCompletionCache() {
	a.withQueryPanel(func(qp *QueryPanel) { qp.refreshCompletionCache() })
}

// setResultsMode runs Query > Results To Grid/Text/File.
func (a *App) setResultsMode(mode ResultsMode) {
	a.withQueryPanel(func(qp *QueryPanel) { qp.SetResultsMode(mode) })
}

// saveQuery runs File > Save (saveAs=false) or File > Save As... (saveAs=true).
func (a *App) saveQuery(saveAs bool) {
	// A plan panel holds no editable text — its Save is the .sqlplan.
	if pp, ok := a.panels.ActivePanel().(*PlanPanel); ok {
		a.savePlanPanel(pp, saveAs)
		return
	}
	qp := a.activeQueryPanel()
	if qp == nil {
		a.setStatus("No active query to save")
		return
	}
	a.saveQueryPanel(qp, saveAs, nil)
}

// saveQueryPanel saves qp — straight to qp.filePath when it has one and saveAs
// is false, otherwise via a path prompt — calling then only once the write
// succeeds, so requestClosePanel's panel closes only after the changes land.
func (a *App) saveQueryPanel(qp *QueryPanel, saveAs bool, then func()) {
	if !saveAs && qp.filePath != "" {
		if a.writeQueryFile(qp, qp.filePath) && then != nil {
			then()
		}
		return
	}
	initial := qp.filePath
	if initial == "" {
		initial = "query.sql"
	}
	title := "Save Query"
	if saveAs {
		title = "Save Query As"
	}
	a.fileDialog.ShowSave(title, initial, func(path string) {
		if a.writeQueryFile(qp, path) && then != nil {
			then()
		}
	})
}

// writeQueryFile writes qp's editor content to path, reporting whether it
// succeeded so callers can proceed only then.
//
// The bytes are re-encoded in whatever shape the panel's file was opened in; a
// panel never opened from a file writes LF-separated UTF-8, fileEnc/fileCRLF's
// zero value. WriteAtomic for the same reason config.Save uses it: a plain
// os.WriteFile truncates first, so a full disk mid-write leaves the user with
// neither their script nor the file it replaced.
func (a *App) writeQueryFile(qp *QueryPanel, path string) bool {
	if err := fileutil.WriteAtomic(path, encodeTextFile(qp.editor.Text(), qp.fileEnc, qp.fileCRLF), 0o644); err != nil {
		a.setStatus(fmt.Sprintf("Save failed: %v", err))
		return false
	}
	qp.filePath = path
	qp.savedText = qp.editor.Text()
	a.setStatus("Saved to " + path)
	return true
}
