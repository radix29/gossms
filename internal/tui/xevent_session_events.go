package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// xevent_session_events.go is the Events page of New Session and Session
// Properties (see xevent_session_dialog.go): the event library with its
// filters, the session's events, and per event SSMS's Configure — the global
// fields (actions) it collects, its filter (predicate), and its customizable
// event fields.
//
// The filter is text, as the catalog stores it, and the text is what is
// written. The clause builder below it only appends to that text — a field or
// predicate source, a comparison and a value — so what SSMS's Filter grid
// cannot express (parentheses, a pred_compare it does not list) is still
// typed, and nothing is lost converting between a grid and the text.

var (
	xeLibraryColumns  = []string{"Selected", "Event", "Package", "Category", "Channel", "Description"}
	xeSelectedColumns = []string{"Event", "Global fields", "Event fields", "Filter"}
	xeActionColumns   = []string{"Collect", "Global field", "Description"}
	xeFieldColumns    = []string{"Field", "Value", "Default", "Type", "Description"}

	xeAllItem = "(all)"

	// xeOperators are the builder's comparisons. like and not like are the
	// sqlserver package's case-insensitive LIKE comparators, the only
	// pred_compare functions SSMS's Filter grid offers by name.
	xeOperators = []string{"=", "<>", ">", ">=", "<", "<=", "like", "not like"}
	xeJoins     = []string{"AND", "OR"}
)

// xePredSource is one entry of the builder's field list: one of the event's
// own data fields, written [name], or a predicate source (a global field the
// predicate can test), written [package].[name].
type xePredSource struct {
	label, ref, typeName string
}

// xeClause builds one comparison for the filter, quoting value as the
// source's type wants: N'…' for a string, (n) for anything else. A value
// already quoted is taken as typed.
func xeClause(src xePredSource, op, value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", errors.New("type a value to compare with")
	}
	quoted := strings.HasPrefix(v, "'") || strings.HasPrefix(v, "N'")
	switch op {
	case "like", "not like":
		fn, lit := "[sqlserver].[like_i_sql_unicode_string]", gosmo.QuoteLiteral(v)
		if strings.HasPrefix(src.typeName, "ansi") {
			fn, lit = "[sqlserver].[like_i_sql_ansi_string]", "'"+strings.ReplaceAll(v, "'", "''")+"'"
		}
		if quoted {
			lit = v
		}
		c := fn + "(" + src.ref + "," + lit + ")"
		if op == "not like" {
			c = "NOT " + c
		}
		return c, nil
	}
	lit := "(" + v + ")"
	switch {
	case quoted:
		lit = v
	case xeIsStringType(src.typeName):
		lit = gosmo.QuoteLiteral(v)
	}
	return src.ref + op + lit, nil
}

// xeJoinPredicate appends clause to pred with join, or starts a filter.
func xeJoinPredicate(pred, join, clause string) string {
	if strings.TrimSpace(pred) == "" {
		return clause
	}
	return strings.TrimSpace(pred) + " " + join + " " + clause
}

// xeEventsEditor is the Events page's state and rows.
type xeEventsEditor struct {
	h   xeHost
	cat *xeCatalog
	m   *xeSessionModel

	search, category, channel string // the library filter; "" is everything
	library                   []int  // cat.events indices the library grid shows
	sel                       int    // m.events index being configured, -1 for none

	cols   map[string][]gosmo.XEObjectColumn // an event's columns, by qualified name
	colRun latest

	actions  []string               // actGrid's rows: qualified action names
	fields   []gosmo.XEObjectColumn // fieldGrid's rows: the event's customizable fields
	sources  []xePredSource         // the builder's field list
	fieldSel int                    // fieldGrid's selected row

	libGrid, selGrid, actGrid, fieldGrid *controls.DataGrid
	selRow                               *validatedGridRow

	searchRow, pred, value, fieldValue      *propsheet.TextRow
	categoryRow, channelRow, source, op, jn *propsheet.SelectRow
	configure                               *propsheet.SectionRow
	typeHint, hint                          *propsheet.HintRow
}

// newXEEventsEditor builds the page. cols holds event columns already read,
// by lower-cased qualified name; a page built off the UI goroutine must hold
// every event of m there, since reading one later is the UI goroutine's.
func newXEEventsEditor(h xeHost, cat *xeCatalog, m *xeSessionModel, cols map[string][]gosmo.XEObjectColumn) *xeEventsEditor {
	if cols == nil {
		cols = map[string][]gosmo.XEObjectColumn{}
	}
	e := &xeEventsEditor{h: h, cat: cat, m: m, sel: -1, fieldSel: -1, cols: cols}
	if len(m.events) > 0 {
		e.sel = 0
	}

	var keywords, channels []string
	for _, o := range cat.events {
		if o.Keyword != "" && !slices.Contains(keywords, o.Keyword) {
			keywords = append(keywords, o.Keyword)
		}
		if o.Channel != "" && !slices.Contains(channels, o.Channel) {
			channels = append(channels, o.Channel)
		}
	}
	slices.Sort(keywords)
	slices.Sort(channels)

	e.searchRow = propsheet.Text("Search events", "", 30)
	e.searchRow.SetDirtyTracked(false)
	e.searchRow.SetOnChange(func(v string) { e.search = strings.ToLower(strings.TrimSpace(v)); e.refilter() })
	e.categoryRow = propsheet.Select("Category", append([]string{xeAllItem}, keywords...), 0)
	e.categoryRow.SetDirtyTracked(false)
	e.categoryRow.SetOnChange(func(v string) { e.category = allToEmpty(v); e.refilter() })
	e.channelRow = propsheet.Select("Channel", append([]string{xeAllItem}, channels...), 0)
	e.channelRow.SetDirtyTracked(false)
	e.channelRow.SetOnChange(func(v string) { e.channel = allToEmpty(v); e.refilter() })

	e.libGrid = controls.NewDataGrid()
	e.libGrid.SetCellCursor(true)
	e.filter()
	e.libGrid.SetData(xeLibraryColumns, e.libraryRows())
	e.libGrid.OnActivateCell = func(row, _ int) { e.toggleLibrary(row) }

	e.selGrid = controls.NewDataGrid()
	e.selGrid.SetCellCursor(true)
	e.selGrid.SetData(xeSelectedColumns, e.selectedRows())
	e.selGrid.OnSelectRow = func(row int) {
		if row >= 0 && row < len(e.m.events) {
			e.sel = row
			e.syncConfigure()
		}
	}
	e.selRow = &validatedGridRow{GridRow: propsheet.NewGridRow(e.selGrid, 7), validate: func() error {
		if len(e.m.events) == 0 {
			return errors.New("a session needs at least one event — add one from the event library")
		}
		return nil
	}}
	e.selRow.DirtyFn = m.eventsDirty
	e.selRow.RevertFn = func() {
		m.events = cloneXEEvents(m.origEvents)
		e.reset()
	}

	e.actGrid = controls.NewDataGrid()
	e.actGrid.SetCellCursor(true)
	e.actGrid.OnActivateCell = func(row, _ int) { e.toggleAction(row) }

	e.fieldGrid = controls.NewDataGrid()
	e.fieldGrid.SetCellCursor(true)
	e.fieldGrid.OnSelectRow = func(row int) {
		e.fieldSel = row
		e.syncFieldValue()
	}
	e.fieldGrid.OnActivateCell = func(row, _ int) { e.flipField(row) }

	e.configure = propsheet.Section("Configure")
	e.pred = propsheet.Text("Filter (predicate)", "", 44)
	e.pred.SetDirtyTracked(false)
	e.pred.SetOnChange(func(v string) {
		if ev := e.current(); ev != nil {
			ev.Predicate = strings.TrimSpace(v)
			e.redrawSelected()
		}
	})
	e.source = propsheet.Select("Field to compare", nil, 0)
	e.source.SetDirtyTracked(false)
	e.source.SetOnChange(func(string) { e.syncTypeHint() })
	e.op = propsheet.Select("Operator", xeOperators, 0)
	e.op.SetDirtyTracked(false)
	e.value = propsheet.Text("Value", "", 40)
	e.value.SetDirtyTracked(false)
	e.jn = propsheet.Select("Join with", xeJoins, 0)
	e.jn.SetDirtyTracked(false)
	e.typeHint = propsheet.Hint()
	e.hint = propsheet.Hint()
	e.fieldValue = propsheet.Text("Selected field's value", "", 20)
	e.fieldValue.SetDirtyTracked(false)
	e.fieldValue.SetOnChange(func(v string) { e.setField(strings.TrimSpace(v)) })

	m.listeners = append(m.listeners, e.reset)
	e.syncConfigure()
	return e
}

func allToEmpty(v string) string {
	if v == xeAllItem {
		return ""
	}
	return v
}

func (e *xeEventsEditor) form() *propsheet.Form {
	addClause := widgets.NewButton("Add Clause", e.addClause)
	clearFilter := widgets.NewButton("Clear Filter", func() { e.pred.Edit("") })
	remove := widgets.NewButton("Remove Event", e.removeSelected)
	return propsheet.NewForm(
		propsheet.Section("Event library"),
		e.searchRow,
		e.categoryRow,
		e.channelRow,
		propsheet.NewGridRow(e.libGrid, 10),
		propsheet.Section("Selected events"),
		e.selRow,
		propsheet.Buttons(remove),
		e.hint,
		e.configure,
		propsheet.NewGridRow(e.actGrid, 8),
		propsheet.Section("Filter"),
		e.pred,
		e.source,
		e.typeHint,
		e.op,
		e.value,
		e.jn,
		propsheet.Buttons(addClause, clearFilter),
		propsheet.Section("Event fields"),
		propsheet.NewGridRow(e.fieldGrid, 6),
		e.fieldValue,
		propsheet.Note("Enter or a click on a library row adds the event, or removes it; on a global field it collects it or stops; on a true/false event field it flips it (any field takes a typed value below the grid, and an empty value is the server's default). The filter is the WHERE clause without the keyword — type it, or build it a clause at a time: pick a field, an operator and a value, then Add Clause."),
	)
}

// filter recomputes which catalog events the library shows.
func (e *xeEventsEditor) filter() {
	e.library = e.library[:0]
	for i, o := range e.cat.events {
		if e.category != "" && o.Keyword != e.category {
			continue
		}
		if e.channel != "" && o.Channel != e.channel {
			continue
		}
		if e.search != "" && !strings.Contains(strings.ToLower(o.QualifiedName()), e.search) {
			continue
		}
		e.library = append(e.library, i)
	}
}

// refilter is a filter row's edit: the row set changed, so the cursor goes
// back to the top.
func (e *xeEventsEditor) refilter() {
	e.filter()
	resetGrid(e.libGrid, xeLibraryColumns, e.libraryRows(), 0)
}

func (e *xeEventsEditor) libraryRows() [][]string {
	rows := make([][]string, len(e.library))
	for i, ci := range e.library {
		o := e.cat.events[ci]
		in := ""
		if e.m.eventIndex(o.QualifiedName()) >= 0 {
			in = "Yes"
		}
		rows[i] = []string{in, o.Name, o.Package, o.Keyword, o.Channel, o.Description}
	}
	return rows
}

func (e *xeEventsEditor) selectedRows() [][]string {
	rows := make([][]string, len(e.m.events))
	for i, ev := range e.m.events {
		rows[i] = []string{ev.QualifiedName(), strconv.Itoa(len(ev.Actions)), xeFieldsSummary(ev.Fields), ev.Predicate}
	}
	return rows
}

// current is the event being configured, or nil.
func (e *xeEventsEditor) current() *gosmo.SessionEvent {
	if e.sel < 0 || e.sel >= len(e.m.events) {
		return nil
	}
	return &e.m.events[e.sel]
}

// redrawSelected re-renders the session's events in place, the cursor kept.
func (e *xeEventsEditor) redrawSelected() {
	redrawGrid(e.selGrid, xeSelectedColumns, e.selectedRows())
}

// toggleLibrary adds the library's row to the session, or takes it out.
func (e *xeEventsEditor) toggleLibrary(row int) {
	if row < 0 || row >= len(e.library) {
		return
	}
	o := e.cat.events[e.library[row]]
	if i := e.m.eventIndex(o.QualifiedName()); i >= 0 {
		e.m.events = slices.Delete(e.m.events, i, i+1)
		if e.sel > i {
			e.sel--
		}
		e.sel = min(e.sel, len(e.m.events)-1)
	} else {
		e.m.events = append(e.m.events, gosmo.SessionEvent{Package: o.Package, Name: o.Name})
		e.sel = len(e.m.events) - 1
	}
	redrawGrid(e.libGrid, xeLibraryColumns, e.libraryRows())
	resetGrid(e.selGrid, xeSelectedColumns, e.selectedRows(), max(e.sel, 0))
	e.syncConfigure()
}

// removeSelected is Remove Event.
func (e *xeEventsEditor) removeSelected() {
	if e.current() == nil {
		e.hint.SetError("Select an event under Selected events to remove it.")
		return
	}
	e.m.events = slices.Delete(e.m.events, e.sel, e.sel+1)
	e.sel = min(e.sel, len(e.m.events)-1)
	redrawGrid(e.libGrid, xeLibraryColumns, e.libraryRows())
	resetGrid(e.selGrid, xeSelectedColumns, e.selectedRows(), max(e.sel, 0))
	e.syncConfigure()
}

// reset re-renders everything from the model after it was replaced wholesale
// — a template, or a Revert.
func (e *xeEventsEditor) reset() {
	e.sel = -1
	if len(e.m.events) > 0 {
		e.sel = 0
	}
	redrawGrid(e.libGrid, xeLibraryColumns, e.libraryRows())
	resetGrid(e.selGrid, xeSelectedColumns, e.selectedRows(), 0)
	e.syncConfigure()
}

// syncConfigure shows the selected event's configuration, reading its columns
// first if they are not in hand.
func (e *xeEventsEditor) syncConfigure() {
	e.hint.Clear()
	ev := e.current()
	if ev == nil {
		e.configure.SetTitle("Configure (no event selected)")
		e.pred.SetValue("")
		e.pred.SetEnabled(false)
		e.value.SetEnabled(false)
		e.fieldValue.SetEnabled(false)
		e.actions, e.fields, e.sources = nil, nil, nil
		e.source.SetItems(nil)
		e.typeHint.Clear()
		resetGrid(e.actGrid, xeActionColumns, nil, 0)
		resetGrid(e.fieldGrid, xeFieldColumns, nil, 0)
		return
	}
	e.configure.SetTitle("Configure " + ev.QualifiedName() + ": global fields")
	e.pred.SetEnabled(true)
	e.value.SetEnabled(true)
	e.pred.SetValue(ev.Predicate)
	e.pred.ShowFromStart()

	// What the event collects comes first — among ~80 global fields the
	// eight it has are otherwise pages apart — then the rest of the catalog.
	// The order is fixed until another event is selected, so a row does not
	// move under the cursor as it is toggled. An action the catalog does not
	// list (a private one, or one this version dropped) is among the first,
	// so it can be taken off.
	e.actions = append(e.actions[:0], ev.Actions...)
	for _, o := range e.cat.actions {
		if !slices.ContainsFunc(e.actions, func(x string) bool { return strings.EqualFold(x, o.QualifiedName()) }) {
			e.actions = append(e.actions, o.QualifiedName())
		}
	}
	resetGrid(e.actGrid, xeActionColumns, e.actionRows(), 0)

	key := ev.QualifiedName()
	if cols, ok := e.cols[strings.ToLower(key)]; ok {
		e.showColumns(cols)
		return
	}
	e.fields, e.sources = nil, nil
	e.source.SetItems(nil)
	e.fieldValue.SetEnabled(false)
	resetGrid(e.fieldGrid, xeFieldColumns, nil, 0)
	e.typeHint.Set("Reading " + key + "'s fields...")
	e.h.columns(&e.colRun, ev.Package, ev.Name, func(cols []gosmo.XEObjectColumn, err error) {
		if err != nil {
			e.typeHint.SetError("Could not read " + key + "'s fields: " + err.Error())
			return
		}
		e.cols[strings.ToLower(key)] = cols
		if cur := e.current(); cur != nil && strings.EqualFold(cur.QualifiedName(), key) {
			e.showColumns(cols)
		}
	})
}

// showColumns fills the builder's field list and the event-field grid from the
// selected event's columns.
func (e *xeEventsEditor) showColumns(cols []gosmo.XEObjectColumn) {
	e.fields, e.sources = nil, nil
	for _, c := range cols {
		switch c.ColumnType {
		case gosmo.XEColumnCustomizable:
			e.fields = append(e.fields, c)
		case gosmo.XEColumnData:
			e.sources = append(e.sources, xePredSource{label: c.Name, ref: "[" + c.Name + "]", typeName: c.TypeName})
		}
	}
	for _, o := range e.cat.predSources {
		e.sources = append(e.sources, xePredSource{label: o.QualifiedName(),
			ref: "[" + o.Package + "].[" + o.Name + "]", typeName: o.TypeName})
	}
	labels := make([]string, len(e.sources))
	for i, s := range e.sources {
		labels[i] = s.label
	}
	e.source.SetItems(labels)
	e.syncTypeHint()
	e.fieldSel = -1
	if len(e.fields) > 0 {
		e.fieldSel = 0
	}
	resetGrid(e.fieldGrid, xeFieldColumns, e.fieldRows(), 0)
	e.syncFieldValue()
}

func (e *xeEventsEditor) syncTypeHint() {
	i := e.source.Selected()
	if i < 0 || i >= len(e.sources) {
		e.typeHint.Clear()
		return
	}
	e.typeHint.Set(xeTypeHint(e.sources[i].typeName, e.cat.isMap(e.sources[i].typeName)))
}

// xeTypeHint says how the builder writes a value for a field of an XE type.
func xeTypeHint(t string, isMap bool) string {
	switch {
	case xeIsStringType(t):
		return "Type " + t + " — the value is quoted as N'…'."
	case isMap:
		return "Type " + t + ", a map — compare with the key number of the value wanted."
	case t == "boolean":
		return "Type boolean — compare with 0 or 1."
	case strings.Contains(t, "int") || strings.HasPrefix(t, "float"):
		return "Type " + t + " — compare with a number."
	}
	return "Type " + t + " — written as typed; quote a text value yourself."
}

func (e *xeEventsEditor) actionRows() [][]string {
	ev := e.current()
	rows := make([][]string, len(e.actions))
	for i, a := range e.actions {
		on := ""
		if ev != nil && slices.ContainsFunc(ev.Actions, func(x string) bool { return strings.EqualFold(x, a) }) {
			on = "Yes"
		}
		desc := ""
		if o, ok := xeFind(e.cat.actions, a); ok {
			desc = o.Description
		}
		rows[i] = []string{on, a, desc}
	}
	return rows
}

// toggleAction collects the row's global field on the selected event, or
// stops collecting it.
func (e *xeEventsEditor) toggleAction(row int) {
	ev := e.current()
	if ev == nil || row < 0 || row >= len(e.actions) {
		return
	}
	a := e.actions[row]
	if i := slices.IndexFunc(ev.Actions, func(x string) bool { return strings.EqualFold(x, a) }); i >= 0 {
		ev.Actions = slices.Delete(slices.Clone(ev.Actions), i, i+1)
	} else {
		ev.Actions = append(slices.Clone(ev.Actions), a)
	}
	redrawGrid(e.actGrid, xeActionColumns, e.actionRows())
	e.redrawSelected()
}

func (e *xeEventsEditor) fieldRows() [][]string {
	ev := e.current()
	rows := make([][]string, len(e.fields))
	for i, c := range e.fields {
		v := ""
		if ev != nil {
			v = xeFieldValue(ev.Fields, c.Name)
		}
		rows[i] = []string{c.Name, v, c.Value, c.TypeName, c.Description}
	}
	return rows
}

func (e *xeEventsEditor) syncFieldValue() {
	ev := e.current()
	if ev == nil || e.fieldSel < 0 || e.fieldSel >= len(e.fields) {
		e.fieldValue.SetValue("")
		e.fieldValue.SetEnabled(false)
		return
	}
	e.fieldValue.SetEnabled(true)
	e.fieldValue.SetValue(xeFieldValue(ev.Fields, e.fields[e.fieldSel].Name))
	e.fieldValue.ShowFromStart()
}

// flipField turns a true/false event field over. Setting it back to the
// server's default removes the SET, which is how SSMS writes an untouched
// field.
func (e *xeEventsEditor) flipField(row int) {
	if row < 0 || row >= len(e.fields) || e.fields[row].TypeName != "boolean" {
		return
	}
	ev := e.current()
	if ev == nil {
		return
	}
	c := e.fields[row]
	def := xeBoolText(c.Value)
	now := xeBoolText(orDefault(xeFieldValue(ev.Fields, c.Name), def))
	next := "1"
	if now == "1" {
		next = "0"
	}
	if next == def {
		next = ""
	}
	ev.Fields = xeSetField(ev.Fields, c.Name, next, false)
	e.fieldSel = row
	redrawGrid(e.fieldGrid, xeFieldColumns, e.fieldRows())
	e.syncFieldValue()
	e.redrawSelected()
}

// setField is the typed value of the selected event field.
func (e *xeEventsEditor) setField(v string) {
	ev := e.current()
	if ev == nil || e.fieldSel < 0 || e.fieldSel >= len(e.fields) {
		return
	}
	c := e.fields[e.fieldSel]
	if c.TypeName == "boolean" {
		v = xeBoolText(v)
	}
	ev.Fields = xeSetField(ev.Fields, c.Name, v, xeIsStringType(c.TypeName))
	redrawGrid(e.fieldGrid, xeFieldColumns, e.fieldRows())
	e.redrawSelected()
}

// addClause is Add Clause: the builder's comparison appended to the filter.
func (e *xeEventsEditor) addClause() {
	if e.current() == nil {
		e.hint.SetError("Select an event under Selected events first.")
		return
	}
	i := e.source.Selected()
	if i < 0 || i >= len(e.sources) {
		e.hint.SetError("Pick a field to compare first — the list fills once the event's fields are read.")
		return
	}
	clause, err := xeClause(e.sources[i], e.op.Value(), e.value.Value())
	if err != nil {
		e.hint.SetError(err.Error())
		return
	}
	e.hint.Clear()
	e.pred.Edit(xeJoinPredicate(e.pred.Value(), e.jn.Value(), clause))
	e.value.SetValue("")
}

// pageXESessionEvents is Session Properties > Events.
func pageXESessionEvents(h xeHost, name string) propPage {
	return propPage{
		title: "Events",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			es, err := h.scope.byName(ctx, h.sc, name)
			if err != nil {
				return nil, nil, err
			}
			cat, err := loadXECatalog(ctx, h.sc)
			if err != nil {
				return nil, nil, err
			}
			// Every event's columns are read with the page — see
			// newXEEventsEditor — one query each, cached per server after.
			cols, err := xeReadColumns(ctx, h, es.Events, func(e gosmo.SessionEvent) (string, string) { return e.Package, e.Name })
			if err != nil {
				return nil, nil, err
			}
			m := newXESessionModel(es.Spec())
			e := newXEEventsEditor(h, cat, m, cols)
			apply := func(ctx context.Context) error {
				if !m.eventsDirty() {
					return nil
				}
				if len(m.events) == 0 {
					return fmt.Errorf("a session needs at least one event")
				}
				events := cloneXEEvents(m.events)
				return alterXESession(ctx, h.sc, h.scope, name, func(s *gosmo.EventSessionSpec) { s.Events = events })
			}
			return e.form(), apply, nil
		},
	}
}
