package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/fileutil"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// app_panel_actions.go opens what the File menu opens: a query panel, a query
// file, a .sqlplan, and the saving of a plan back out again. Closing and
// quitting are in app_panel_close.go, the query actions themselves in
// app_query_actions.go, the other panels in app_show_panels.go and the
// property dialogs in app_show_properties.go.

func (a *App) newQueryPanel() {
	a.queryPanelCnt++
	qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
	a.panels.SetActive(a.panels.AddPanel(qp))
	a.focusPanels()
	if sc, database := a.selectedConnTarget(); sc != nil {
		a.connectForQueryPanel(qp, sc, database, nil)
	}
}

// newQueryPanelForConn opens a query panel with its own connection cloned from
// sc, in the given database context ("" = the connection's default).
func (a *App) newQueryPanelForConn(sc *db.ServerConn, database string) {
	a.queryPanelCnt++
	qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
	a.panels.SetActive(a.panels.AddPanel(qp))
	a.focusPanels()
	if sc != nil {
		a.connectForQueryPanel(qp, sc, database, nil)
	}
}

func (a *App) openQueryWithText(sc *db.ServerConn, database, text string) {
	a.queryPanelCnt++
	qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
	qp.editor.SetText(text)
	a.panels.SetActive(a.panels.AddPanel(qp))
	a.focusPanels()
	if sc != nil {
		a.connectForQueryPanel(qp, sc, database, nil)
	}
}

// openQueryWithTextAndExecute is openQueryWithText plus an immediate run, for
// actions like "View Backup History" that hand the user a live result set
// rather than a query to review. A nil sc opens the panel disconnected.
func (a *App) openQueryWithTextAndExecute(sc *db.ServerConn, database, text string) {
	a.queryPanelCnt++
	qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
	qp.editor.SetText(text)
	a.panels.SetActive(a.panels.AddPanel(qp))
	a.focusPanels()
	if sc != nil {
		a.connectForQueryPanel(qp, sc, database, func() { qp.Execute() })
	}
}

// openQueryFile runs File > Open: prompts for a path and loads its content into
// a new query panel, never the active one, as SSMS does. The panel's title then
// tracks the file's name.
func (a *App) openQueryFile() {
	a.fileDialog.ShowOpen("Open Query File", "", func(path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			a.setStatus(fmt.Sprintf("Open failed: %v", err))
			return
		}
		if strings.EqualFold(filepath.Ext(path), sqlPlanExt) {
			a.openPlanFile(path, data)
			return
		}
		a.queryPanelCnt++
		qp := NewQueryPanel(a, fmt.Sprintf("Query %d", a.queryPanelCnt))
		text, enc, crlf, lossy := decodeTextFile(data)
		qp.editor.SetText(text)
		// Read back from the editor, never from text: SetText expands tabs, so
		// seeding savedText from the source marks a file with one tab in it
		// dirty the moment it opens, and closing it then prompts to save,
		// rewriting a file the user never touched.
		qp.savedText = qp.editor.Text()
		qp.filePath = path
		qp.fileEnc, qp.fileCRLF = enc, crlf
		if strings.EqualFold(filepath.Ext(path), ".xml") {
			qp.editor.SetHighlighter(controls.XMLHighlighter(theme.Active()))
		}
		a.panels.SetActive(a.panels.AddPanel(qp))
		a.focusPanels()
		if lossy {
			a.setStatus("Opened " + path + " — not valid UTF-8; undecodable bytes shown as � and saving will replace them")
		} else {
			a.setStatus("Opened " + path)
		}
		if sc, database := a.selectedConnTarget(); sc != nil {
			a.connectForQueryPanel(qp, sc, database, nil)
		}
	})
}

// sqlPlanExt is the extension SSMS gives a saved execution plan, and what
// File > Open keys off to show one rather than treating it as a script.
const sqlPlanExt = ".sqlplan"

// openPlanFile shows an already-read .sqlplan in its own PlanPanel, through
// decodeTextFile like every other file gossms reads: SSMS writes .sqlplan as
// UTF-16, identified by its BOM (see text_encoding.go).
func (a *App) openPlanFile(path string, data []byte) {
	text, _, _, _ := decodeTextFile(data)
	plan, err := showplan.Parse([]byte(text))
	if err != nil {
		a.setStatus(fmt.Sprintf("Could not parse %s: %v", filepath.Base(path), err))
		return
	}
	pp := NewPlanPanel(a, filepath.Base(path), plan)
	pp.filePath = path
	a.panels.SetActive(a.panels.AddPanel(pp))
	a.focusPanels()
	a.setStatus("Opened " + path)
}

// savePlanPanel runs File > Save / Save As... for a plan panel, writing the
// plan's source XML back out. A panel opened from a file saves straight back to
// it; anything else prompts.
//
// The XML is written as UTF-8 rather than the UTF-16 SSMS emits: it is the
// decoded showplan document, and re-encoding it would need the declaration
// rewritten to match or the file would announce an encoding it isn't in.
func (a *App) savePlanPanel(pp *PlanPanel, saveAs bool) {
	xml := pp.PlanXML()
	if xml == "" {
		a.setStatus("No execution plan to save")
		return
	}
	write := func(path string) {
		if a.writePlanFile(path, xml) {
			pp.filePath = path
		}
	}
	if !saveAs && pp.filePath != "" {
		write(pp.filePath)
		return
	}
	initial := pp.filePath
	if initial == "" {
		initial = "plan" + sqlPlanExt
	}
	a.fileDialog.ShowSave("Save Execution Plan", initial, write)
}

// saveExecutionPlanAs runs File > Save Execution Plan As..., which works from a
// detached plan panel and from a query panel's Execution Plan tab alike —
// unlike Save, which in a query panel means the script.
func (a *App) saveExecutionPlanAs() {
	switch p := a.panels.ActivePanel().(type) {
	case *PlanPanel:
		a.savePlanPanel(p, true)
	case *QueryPanel:
		plan := a.activePlan()
		if plan == nil {
			a.setStatus("No execution plan to save")
			return
		}
		a.fileDialog.ShowSave("Save Execution Plan", p.Title()+sqlPlanExt, func(path string) {
			a.writePlanFile(path, plan.XML)
		})
	default:
		a.setStatus("No execution plan to save")
	}
}

// activePlan returns the plan the active panel is showing — a detached plan
// panel's, or a query panel's Execution Plan tab — or nil when there is none.
func (a *App) activePlan() *showplan.Plan {
	switch p := a.panels.ActivePanel().(type) {
	case *PlanPanel:
		return p.planView.Plan()
	case *QueryPanel:
		if p.planView != nil {
			return p.planView.Plan()
		}
	}
	return nil
}

// writePlanFile writes one plan document to path, reporting success.
func (a *App) writePlanFile(path, xml string) bool {
	if err := fileutil.WriteAtomic(path, []byte(xml), 0o644); err != nil {
		a.setStatus(fmt.Sprintf("Save failed: %v", err))
		return false
	}
	a.setStatus("Saved to " + path)
	return true
}
