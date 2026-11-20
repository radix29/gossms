package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/tuikit/core"
	"github.com/radix29/tuikit/dialogs"
	"github.com/radix29/tuikit/theme"
	"github.com/radix29/tuikit/widgets"
)

// FilterDialog is Object Explorer's "Filter Settings" dialog: one row per
// property the folder offers (see filterProps), each an operator dropdown and a
// value field. Which rows it shows depends on the folder it was opened on, so
// the widgets are rebuilt on every showing.
//
// Applying is the folder's business: OK hands the assembled filter to
// App.applyNodeFilter, which reloads the folder.
type FilterDialog struct {
	dialogs.ModalDialog
	app *App

	// node is the folder this showing filters, captured at Show time. The
	// dialog is modal, so the tree can't change underneath it.
	node *explorerNode

	rows []filterDialogRow

	// Column widths for this showing, from columnWidths. Fields rather than the
	// constants because a dialog clamped to a narrow terminal lays its rows out
	// inside the width it got.
	propColW int
	opW      int
	valueW   int

	serverName string
	dbName     string

	// focusIdx walks each row's operator dropdown then its value field, then
	// the button row at widgetCount(), one Tab stop crossed with the arrows
	// (dialogs.RingStep, ButtonRowKey). btnFocus is the button the row would
	// press: OK while focus is in the rows, so Enter from a value field applies
	// the filter.
	focusIdx int
	btnFocus int

	// drag is the text-selection gesture a click in a row's value field starts
	// — see dialogs.FieldGesture for the ordering its three calls depend on.
	drag dialogs.FieldGesture

	// message is the validation readout above the button row; messageY is
	// the row it's drawn on, recorded by layout for Draw.
	message  string
	messageY int
}

// filterDialogRow is one property's widgets plus the operators its kind offers:
// the dropdown shows their names, so the selected index maps back through this
// slice to the operator.
type filterDialogRow struct {
	prop  filterProp
	ops   []filterOp
	op    *widgets.DropDown
	value *widgets.InputField
}

// Column geometry, in columns from the dialog's left text margin: property
// name, operator dropdown, value field. Widths fit the longest property and
// operator names; they apply on a terminal wide enough for the whole dialog,
// and columnWidths shrinks from them on a narrower one.
const (
	filterPropColW  = 21
	filterOpW       = 18
	filterValueW    = 24
	filterDialogW   = 74
	filterButtonsOK = 1 // index of OK in buttons(), the Enter default
)

// Column floors for a dialog clamped narrower than filterDialogW: a property
// name and an operator clip to something still recognisable, and the value field
// keeps enough room to see what was typed.
const (
	filterPropMinW  = 10
	filterOpMinW    = 8
	filterValueMinW = 8
)

// filterRowFixedW is what layout puts around the three columns on a row: the
// dropdown's two brackets, the value field's two, and the gap between them.
const filterRowFixedW = 5

func NewFilterDialog(app *App) *FilterDialog {
	d := &FilterDialog{app: app}
	d.InitModal("Filter Settings", filterDialogW, 16)
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
	a.filterDialog().show(node, props, sc.Opts.Server)
}

func (d *FilterDialog) show(node *explorerNode, props []filterProp, server string) {
	d.node = node
	d.serverName = server
	d.dbName = node.data.DBName
	d.message = ""
	// A latch must not survive into the next showing: a dialog dismissed mid-drag
	// would reopen still routing every click to that field.
	d.drag.Clear()

	// Sized before the widgets are built: recentre clamps the dialog to the
	// terminal and a widget's width is fixed at construction, so rows built at full
	// width would draw value fields past a clamped dialog's right border.
	d.SetSize(filterDialogW, d.heightFor(len(props)))
	d.propColW, d.opW, d.valueW = d.columnWidths()

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
			op:    widgets.NewDropDown("", names, d.opW),
			value: widgets.NewInputField("", d.valueW, false),
		})
	}
	d.seedFrom(node.data.Filter)

	d.focusIdx, d.btnFocus = 0, filterButtonsOK
	d.syncFocus()
	d.Show()
}

// columnWidths divides the row's usable width between the three columns: the
// full constants when they fit, otherwise shrinking in the order a narrow dialog
// can best afford: property name first (the header still says what it is), then
// the operator, then the value field. Every column stops at its floor, so a
// terminal too narrow even for those overflows; that is ModalDialog's limit.
func (d *FilterDialog) columnWidths() (propColW, opW, valueW int) {
	propColW, opW, valueW = filterPropColW, filterOpW, filterValueW
	// -1 for the left text margin (layout's lx = inner.X+1).
	short := propColW + opW + valueW + filterRowFixedW - (d.InnerRect().W - 1)
	for _, c := range []struct {
		w   *int
		min int
	}{{&propColW, filterPropMinW}, {&opW, filterOpMinW}, {&valueW, filterValueMinW}} {
		if short <= 0 {
			break
		}
		give := min(short, *c.w-c.min)
		*c.w -= give
		short -= give
	}
	return propColW, opW, valueW
}

// seedFrom fills the rows from the folder's current filter, so reopening the
// dialog offers back what is in force. A criterion whose property the folder no
// longer offers is dropped.
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

// heightFor sizes the dialog to a row count: the header, a blank row, the
// "Filter Criteria:" caption and its column header, one row per property, a
// blank row and the message line, plus the 5 rows ModalDialog reserves below.
// Takes the count rather than len(d.rows) since show sizes the dialog before
// building the rows.
func (d *FilterDialog) heightFor(rows int) int {
	return d.headerLines() + 4 + rows + 1 + 5
}

func (d *FilterDialog) buttons() []string { return []string{"Clear Filter", "OK", "Cancel"} }

// widgetCount is the number of Tab stops before the buttons: an operator
// dropdown and a value field per row.
func (d *FilterDialog) widgetCount() int { return len(d.rows) * 2 }

func (d *FilterDialog) onButtons() bool { return d.focusIdx == d.widgetCount() }

// fields is the ring's widgets in Tab order, widgetAt over every row.
func (d *FilterDialog) fields() []focusable {
	out := make([]focusable, d.widgetCount())
	for i := range out {
		out[i] = d.widgetAt(i)
	}
	return out
}

// widgetAt maps a focus index onto its widget: even indexes are operator
// dropdowns, odd ones value fields. nil once the index is past the rows.
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

// openDropDown returns the row's dropdown whose list is open, or nil. An open
// list is drawn last and gets first refusal of every event.
func (d *FilterDialog) openDropDown() *widgets.DropDown {
	for i := range d.rows {
		if d.rows[i].op.OverlayActive() {
			return d.rows[i].op
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Actions
// ---------------------------------------------------------------------------

// buildFilter assembles the rows into a filter, or reports the first row whose
// value the criterion's kind can't parse. A nil filter with no error means
// every row was empty: "no filter", which OK applies as a removal.
//
// Values are trimmed here, making a whitespace-only row count as empty.
// Matching trims too, so an untrimmed " " would reach matchText as an empty
// needle, making `Contains` true of every row: the folder would get the
// "(filtered)" label and filter nothing.
func (d *FilterDialog) buildFilter() (*nodeFilter, int, error) {
	f := &nodeFilter{}
	for i := range d.rows {
		r := &d.rows[i]
		val := strings.TrimSpace(r.value.Value())
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

// clearRows empties every value field. The filter itself isn't touched until OK,
// as in SSMS.
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

	label := theme.StyleDialog()
	dim := theme.StyleDialogDim()

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
	core.DrawTextClipped(s, lx+d.propColW, y, d.opW+2, dim, "Operator")
	core.DrawTextClipped(s, lx+d.propColW+d.opW+3, y, d.valueW+2, dim, "Value")
	y++

	for i := range d.rows {
		r := &d.rows[i]
		core.DrawTextClipped(s, lx, y+i, d.propColW-1, label, r.prop.name)
		r.op.Draw(s)
		r.value.Draw(s)
	}

	msgStyle := theme.StyleDialogWarning()
	core.DrawTextClipped(s, lx, d.messageY, inner.W-2, msgStyle, d.message)

	d.DrawSeparator(s)
	d.DrawButtons(s, d.buttons(), d.btnFocus)

	// Last, over everything else — an open list belongs on top of the rows and
	// buttons below it.
	if dd := d.openDropDown(); dd != nil {
		dd.DrawOverlay(s)
	}
}

// layout positions the row widgets and records messageY for Draw.
func (d *FilterDialog) layout() {
	inner := d.InnerRect()
	lx := inner.X + 1
	opX := lx + d.propColW
	valX := opX + d.opW + 3
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
	// An open dropdown list is the focused widget (DropDown.Focus(false) closes
	// it), so FieldRingKey's widget-first order hands it Up/Down/Enter/Escape.
	press := func() { d.pressButton(d.btnFocus) }
	if d.onButtons() {
		return dialogs.ButtonRowKey(ev, &d.btnFocus, len(d.buttons()), nil, press, d.Hide, d.moveFocus)
	}
	return dialogs.FieldRingKey(ev, d.widgetAt(d.focusIdx), press, d.Hide, d.moveFocus)
}

// moveFocus steps the ring by dir. Leaving the button row resets it to OK, the
// button Enter presses from a field.
func (d *FilterDialog) moveFocus(dir int) {
	d.focusIdx = dialogs.RingStep(d.fields(), d.focusIdx, dir)
	if !d.onButtons() {
		d.btnFocus = filterButtonsOK
	}
	d.syncFocus()
}

// HandleMouse is dialogs.RingMouse over the focus ring: an operator dropdown
// and a value field per row, then the button row.
func (d *FilterDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	return dialogs.RingMouse(ev, &d.ModalDialog, &d.drag, d.fields(), d.focusIdx, d.buttons(),
		func(i int) {
			d.focusIdx = i
			d.syncFocus()
		},
		func(btn int) {
			d.btnFocus = btn
			d.pressButton(btn)
		})
}

// FocusClipboardTargetAt implements core.ClipboardFocuser: a right-click on a
// row's value field focuses it; an operator dropdown is not a text field, and
// a disabled value field is skipped, as dialogs.ClipboardFieldAt skips one.
func (d *FilterDialog) FocusClipboardTargetAt(x, y int) bool {
	if d.openDropDown() != nil {
		return false // its list covers the value fields of the rows below
	}
	for i := range d.rows {
		if v := d.rows[i].value; v.Enabled() && v.HitTest(x, y) {
			d.focusIdx = i*2 + 1
			d.syncFocus()
			return true
		}
	}
	return false
}

// FocusedClipboardTarget implements core.ClipboardHost: the focused row's value
// field. widgetAt answers nil past the rows, and an operator dropdown is not a
// clipboard target, so both fall through to nil.
func (d *FilterDialog) FocusedClipboardTarget() core.ClipboardTarget {
	if t, ok := d.widgetAt(d.focusIdx).(core.ClipboardTarget); ok {
		return t
	}
	return nil
}
