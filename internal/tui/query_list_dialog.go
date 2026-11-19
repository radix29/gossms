package tui

import "github.com/radix29/tuikit/dialogs"

// QueryListDialog lists every currently open query panel and lets the
// user jump straight to one (Tools > Query List). The list is a snapshot
// taken in Show; a row click switches to it.
type QueryListDialog struct {
	*dialogs.ListDialog
	app     *App
	indices []int // a.panels index for each listed row
	titles  []string
}

// NewQueryListDialog creates the dialog.
func NewQueryListDialog(app *App) *QueryListDialog {
	d := &QueryListDialog{app: app}
	d.ListDialog = dialogs.NewListDialog(dialogs.ListDialogSpec{
		Title: "Query List", W: 56, H: 16,
		Buttons:         []string{"Switch To", "Close"},
		Empty:           "(no open queries)",
		Count:           func() int { return len(d.titles) },
		Item:            func(i int) string { return d.titles[i] },
		OnButton:        func(_, sel int) bool { return d.activate(sel) },
		ActivateOnClick: true,
	})
	return d
}

// Show rebuilds the list from the current panels and displays the dialog.
func (d *QueryListDialog) Show() {
	d.indices = d.indices[:0]
	d.titles = d.titles[:0]
	for i := range d.app.panels.Count() {
		qp, ok := d.app.panels.PanelAt(i).(*QueryPanel)
		if !ok {
			continue
		}
		label := qp.Title()
		if qp.conn != nil {
			label += "  [" + qp.conn.Opts.Server + "]"
		}
		if qp.filePath != "" {
			label += "  — " + qp.filePath
		}
		d.indices = append(d.indices, i)
		d.titles = append(d.titles, label)
	}
	d.ListDialog.Show()
}

// activate switches to the query on row sel and reports whether it did (the
// dialog then closes).
func (d *QueryListDialog) activate(sel int) bool {
	if sel < 0 || sel >= len(d.indices) {
		return false
	}
	d.app.panels.SetActiveIndex(d.indices[sel])
	d.app.focusPanels()
	return true
}
