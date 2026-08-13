package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// FilterDialog is Object Explorer's "Filter Settings" dialog — SSMS's
// per-folder filter, one row per property the folder offers (see
// filterProps), each an operator dropdown and a value field. Which rows it
// shows depends entirely on the folder it was opened on, so the widgets are
// rebuilt on every showing rather than kept across them.
//
// Applying is the folder's business, not the dialog's: OK hands the
// assembled filter to App.applyNodeFilter, which reloads the folder.
type FilterDialog struct {
	dialogs.ModalDialog
	app *App

	// node is the folder this showing filters, captured at Show time. The
	// dialog is modal, so the tree can't change underneath it.
	node *explorerNode

	rows []filterDialogRow

	serverName string
	dbName     string

	// focusIdx walks each row's operator dropdown then its value field, then
	// the buttons — the same one-cycle-reaches-everything arrangement
	// FindReplaceDialog uses.
	focusIdx int

	// dragField is the value field that claimed the current Button1 gesture,
	// nil between gestures, so a selection drag that leaves the field's rect
	// keeps extending instead of stopping at the boundary.
	dragField *widgets.InputField

	// message is the validation readout above the button row; messageY is
	// the row it's drawn on, recorded by layout for Draw.
	message  string
	messageY int
}

// filterDialogRow is one property's widgets, plus the operators its kind
// offers — the dropdown shows their names, so the selected index has to be
// mapped back through this slice to get the operator itself.
type filterDialogRow struct {
	prop  filterProp
	ops   []filterOp
	op    *widgets.DropDown
	value *widgets.InputField
}

// Column geometry, in columns from the dialog's left text margin: the
// property name, then the operator dropdown, then the value field. Widths
// fit the longest property name ("Is Memory Optimized") and the longest
// operator name ("Does not contain") without clipping.
const (
	filterPropColW  = 21
	filterOpW       = 17
	filterValueW    = 24
	filterDialogW   = 74
	filterButtonsOK = 1 // index of OK in buttons(), the Enter default
)

func NewFilterDialog(app *App) *FilterDialog {
	d := &FilterDialog{app: app}
	d.InitModal(app.screen, "Filter Settings", filterDialogW, 16)
	return d
}

// showFilterDialog opens Filter Settings on a folder node. Folders with no
// filterable properties never reach here — the menu item isn't offered.
func (a *App) showFilterDialog(node *explorerNode) {
	props := filterProps(node.data.Type)
	if len(props) == 0 {
		a.setStatus("This folder can't be filtered")
		return
	}
	sc := resolveConn(node)
	if !a.requireConn(sc) {
		return
	}
	a.filterDialog.show(node, props, sc.Opts.Server)
}

func (d *FilterDialog) show(node *explorerNode, props []filterProp, server string) {
	d.node = node
	d.serverName = server
	d.dbName = node.data.DBName
	d.message = ""
	// A latch must not survive into the next showing: a dialog dismissed
	// mid-drag would otherwise reopen still routing every click to that field.
	d.dragField = nil

	d.rows = make([]filterDialogRow, 0, len(props))
	for _, p := range props {
		ops := filterOps(p.kind)
		names := make([]string, 0, len(ops))
		for _, op := range ops {
			names = append(names, op.String())
		}
		d.rows = append(d.rows, filterDialogRow{
			prop:  p,
			ops:   ops,
			op:    widgets.NewDropDown("", names, filterOpW),
			value: widgets.NewInputField("", filterValueW, false),
		})
	}
	d.seedFrom(node.data.Filter)

	d.focusIdx = 0
	d.syncFocus()
	d.SetSize(filterDialogW, d.height())
	d.Show()
}

// seedFrom fills the rows from the folder's current filter, so reopening
// the dialog offers back what's actually in force. A criterion whose
// property the folder no longer offers is simply dropped.
func (d *FilterDialog) seedFrom(f *nodeFilter) {
	if !f.active() {
		return
	}
	for _, c := range f.criteria {
		for i := range d.rows {
			r := &d.rows[i]
			if r.prop.id != c.prop.id {
				continue
			}
			for j, op := range r.ops {
				if op == c.op {
					r.op.SetSelected(j)
					break
				}
			}
			r.value.SetValue(c.value)
		}
	}
}

// headerLines is 2 for a folder inside a database (Server + Database), 1 for
// a server-level one (Logins, Databases) that has no database to name.
func (d *FilterDialog) headerLines() int {
	if d.dbName == "" {
		return 1
	}
	return 2
}

// height sizes the dialog to its row count: the header, a blank row, the
// "Filter Criteria:" caption and its column header, one row per property, a
// blank row and the message line — then the 5 rows ModalDialog reserves
// below the content (separator, button row, borders).
func (d *FilterDialog) height() int {
	return d.headerLines() + 4 + len(d.rows) + 1 + 5
}

func (d *FilterDialog) buttons() []string { return []string{"Clear Filter", "OK", "Cancel"} }

// btnFocus is the index of the focused button, or OK's while focus is still
// in the rows — so Enter from a value field applies the filter.
func (d *FilterDialog) btnFocus() int {
	if i := d.focusIdx - d.widgetCount(); i >= 0 {
		return i
	}
	return filterButtonsOK
}

// widgetCount is the number of Tab stops before the buttons: an operator
// dropdown and a value field per row.
func (d *FilterDialog) widgetCount() int { return len(d.rows) * 2 }

func (d *FilterDialog) focusCount() int { return d.widgetCount() + len(d.buttons()) }

// widgetAt maps a focus index onto its widget — even indexes are operator
// dropdowns, odd ones value fields. Returns nil once the index is past the
// rows and into the button row.
func (d *FilterDialog) widgetAt(i int) focusable {
	if i < 0 || i >= d.widgetCount() {
		return nil
	}
	r := &d.rows[i/2]
	if i%2 == 0 {
		return r.op
	}
	return r.value
}

func (d *FilterDialog) syncFocus() {
	for i := range d.widgetCount() {
		d.widgetAt(i).Focus(i == d.focusIdx)
	}
}

// openDropDown returns the row's dropdown whose list is currently open, or
// nil. An open list is drawn last and gets first refusal of every event.
func (d *FilterDialog) openDropDown() *widgets.DropDown {
	for i := range d.rows {
		if d.rows[i].op.IsOpen() {
			return d.rows[i].op
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Actions
// ---------------------------------------------------------------------------

// buildFilter assembles the rows into a filter, or reports the first row
// whose value the criterion's kind can't parse. A nil filter with no error
// means every row was left empty — "no filter", which OK applies as a
// removal.
func (d *FilterDialog) buildFilter() (*nodeFilter, int, error) {
	f := &nodeFilter{}
	for i := range d.rows {
		r := &d.rows[i]
		val := r.value.Value()
		if val == "" {
			continue
		}
		op := r.ops[r.op.Selected()]
		switch r.prop.kind {
		case filterDate:
			if _, err := parseFilterDate(val); err != nil {
				return nil, i, fmt.Errorf("%s: enter a date as %s", r.prop.name, filterDateLayout)
			}
		case filterBool:
			if _, err := parseFilterBool(val); err != nil {
				return nil, i, fmt.Errorf("%s: enter True or False", r.prop.name)
			}
		}
		f.criteria = append(f.criteria, filterCriterion{prop: r.prop, op: op, value: val})
	}
	if !f.active() {
		return nil, -1, nil
	}
	return f, -1, nil
}

func (d *FilterDialog) applyAndClose() {
	f, badRow, err := d.buildFilter()
	if err != nil {
		d.message = err.Error()
		d.focusIdx = badRow*2 + 1 // the offending value field
		d.syncFocus()
		return
	}
	d.app.applyNodeFilter(d.node, f)
	d.Hide()
}

// clearRows empties every value field. The filter itself isn't touched
// until OK — same as SSMS, where Clear Filter clears the grid and OK is
// what removes the folder's filter.
func (d *FilterDialog) clearRows() {
	for i := range d.rows {
		d.rows[i].value.SetValue("")
		d.rows[i].op.SetSelected(0)
	}
	d.message = ""
}

func (d *FilterDialog) pressButton(i int) {
	switch i {
	case 0:
		d.clearRows()
	case filterButtonsOK:
		d.applyAndClose()
	default:
		d.Hide()
	}
}

// ---------------------------------------------------------------------------
// Drawing
// ---------------------------------------------------------------------------

func (d *FilterDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	d.layout()

	p := theme.Active()
	label := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	dim := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)

	inner := d.InnerRect()
	lx := inner.X + 1
	y := inner.Y

	core.DrawText(s, lx, y, dim, "Server:")
	core.DrawTextClipped(s, lx+11, y, inner.W-12, label, d.serverName)
	y++
	if d.dbName != "" {
		core.DrawText(s, lx, y, dim, "Database:")
		core.DrawTextClipped(s, lx+11, y, inner.W-12, label, d.dbName)
		y++
	}
	y++
	core.DrawText(s, lx, y, dim, "Filter Criteria:")
	y++
	core.DrawText(s, lx, y, dim, "Property")
	core.DrawText(s, lx+filterPropColW, y, dim, "Operator")
	core.DrawText(s, lx+filterPropColW+filterOpW+3, y, dim, "Value")
	y++

	for i := range d.rows {
		r := &d.rows[i]
		core.DrawTextClipped(s, lx, y+i, filterPropColW-1, label, r.prop.name)
		r.op.Draw(s)
		r.value.Draw(s)
	}

	msgStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Warning)
	core.DrawTextClipped(s, lx, d.messageY, inner.W-2, msgStyle, d.message)

	d.DrawSeparator(s)
	d.DrawButtons(s, d.buttons(), d.btnFocus())

	// Last, over everything else in the dialog — an open list belongs on top
	// of the rows and buttons below it.
	if dd := d.openDropDown(); dd != nil {
		dd.DrawOverlay(s)
	}
}

// layout positions the row widgets and records messageY for Draw.
func (d *FilterDialog) layout() {
	inner := d.InnerRect()
	lx := inner.X + 1
	opX := lx + filterPropColW
	valX := opX + filterOpW + 3
	rowY := inner.Y + d.headerLines() + 3

	for i := range d.rows {
		d.rows[i].op.SetBounds(opX, rowY+i)
		d.rows[i].value.SetBounds(valX, rowY+i)
	}
	d.messageY = rowY + len(d.rows) + 1
}

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

func (d *FilterDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	// An open dropdown list gets first refusal: Up/Down/Enter/Escape belong
	// to it, not to the dialog's focus cycling.
	if dd := d.openDropDown(); dd != nil && dd.HandleKey(ev) {
		return true
	}
	switch ev.Key() {
	case tcell.KeyTab:
		d.moveFocus(1)
		return true
	case tcell.KeyBacktab:
		d.moveFocus(-1)
		return true
	case tcell.KeyEscape:
		d.Hide()
		return true
	}
	if w, ok := d.widgetAt(d.focusIdx).(interface {
		HandleKey(*tcell.EventKey) bool
	}); ok && w.HandleKey(ev) {
		return true
	}
	switch ev.Key() {
	case tcell.KeyEnter:
		d.pressButton(d.btnFocus())
	case tcell.KeyDown:
		d.moveFocus(1)
	case tcell.KeyUp:
		d.moveFocus(-1)
	}
	return true
}

func (d *FilterDialog) moveFocus(dir int) {
	n := d.focusCount()
	d.focusIdx = (d.focusIdx + dir + n) % n
	d.syncFocus()
}

func (d *FilterDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	// A release has to reach every latch-bearing widget even when it lands
	// outside the dialog, or the widget's next press is swallowed as the
	// continuation of a stale drag.
	if ev.Buttons() == tcell.ButtonNone {
		for i := range d.rows {
			d.rows[i].op.HandleMouse(ev)
		}
		if d.dragField != nil {
			d.dragField.HandleMouse(ev)
			d.dragField = nil
		}
	}
	// An open list may extend below the dialog's own rows, so it is offered
	// the press before ConsumeOutsideClick can discard it as "outside".
	if dd := d.openDropDown(); dd != nil && dd.HandleMouse(ev) {
		return true
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if ev.Buttons() == tcell.ButtonNone {
		return true
	}
	if ev.Buttons() != tcell.Button1 {
		return false
	}

	// The gesture belongs to whichever field claimed its press, so motion is
	// replayed there without hit-testing — that test is what stops a
	// selection extending the moment the pointer leaves the field's rect.
	if d.dragField != nil {
		d.dragField.HandleMouse(ev)
		return true
	}

	if i := d.ButtonClicked(ev, d.buttons()); i >= 0 {
		d.focusIdx = d.widgetCount() + i
		d.syncFocus()
		d.pressButton(i)
		return true
	}
	for i := range d.rows {
		if d.rows[i].op.HandleMouse(ev) {
			d.focusIdx = i * 2
			d.syncFocus()
			return true
		}
	}
	mx, my := ev.Position()
	for i := range d.rows {
		f := d.rows[i].value
		if f.HitTest(mx, my) {
			d.focusIdx = i*2 + 1
			d.syncFocus()
			d.dragField = f
			f.HandleMouse(ev)
			return true
		}
	}
	return true
}
