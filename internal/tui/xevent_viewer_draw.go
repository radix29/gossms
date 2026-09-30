package tui

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/xevent"
)

// xevent_viewer_draw.go renders the Extended Events viewer: the toolbar row,
// the event grid, the splitter, and the details pane listing the selected
// event's every field and action.

// xeDetailMaxLines caps how many lines one value takes in the details pane. A
// showplan wraps to thousands; the pane says how much is left and Show Value
// (or the cell menu) opens the whole of it.
const xeDetailMaxLines = 40

// The details pane's two section headings.
const (
	xeFieldsHeading  = "Fields"
	xeActionsHeading = "Actions"
)

// Draw renders the panel (Panel interface).
func (v *XEventViewer) Draw(s tcell.Screen) {
	// The labels carry state (Stop/Start, Pause/Resume), so the cells are
	// relaid out every frame: a rect sized for the old label would leave the
	// next cell overpainting this one's tail.
	v.refreshToolLabels()
	v.layoutTools()
	v.drawToolbar(s)
	v.grid.Draw(s)
	v.splitter.Draw(s)
	v.drawDetails(s)
	// Last: the grid's cell menu and value popup draw outside its rect.
	v.grid.DrawOverlay(s)
}

// drawToolbar paints the toolbar row in Activity Monitor's tooltip scheme.
func (v *XEventViewer) drawToolbar(s tcell.Screen) {
	if v.toolRect.H != 1 {
		return
	}
	pal := theme.Active()
	core.FillRect(s, v.toolRect, ' ', theme.StyleMenuBar())
	for i, t := range v.tools {
		if t.rect.IsZero() {
			continue
		}
		style := theme.StyleTooltip()
		if v.toolDisabled(i) {
			style = style.Foreground(pal.TextDim)
		}
		core.FillRect(s, t.rect, ' ', style)
		core.DrawText(s, t.rect.X+1, t.rect.Y, style, t.label)
	}
	if !v.more.rect.IsZero() {
		style := theme.StyleTooltip()
		core.FillRect(s, v.more.rect, ' ', style)
		core.DrawText(s, v.more.rect.X+1, v.more.rect.Y, style, v.more.label)
	}
}

// drawDetails paints the selected event below the splitter.
func (v *XEventViewer) drawDetails(s tcell.Screen) {
	r := v.detailRect
	if r.W <= 0 || r.H <= 0 {
		return
	}
	pal := theme.Active()
	style := theme.StyleDefault()
	dim := style.Foreground(pal.TextDim)
	core.FillRect(s, r, ' ', style)

	e, ok := v.selectedEvent()
	if !ok {
		core.DrawTextClipped(s, r.X+1, r.Y, r.W-2, dim, "No event selected")
		return
	}
	lines := v.detailLines(e, r.W-2)
	for i := v.detailScroll; i < len(lines); i++ {
		y := r.Y + i - v.detailScroll
		if y >= r.Y+r.H {
			break
		}
		st := style
		// Section headings ("Fields", "Actions") stand out from the rows.
		if lines[i] == xeFieldsHeading || lines[i] == xeActionsHeading {
			st = dim
		}
		core.DrawTextClipped(s, r.X+1, y, r.W-2, st, lines[i])
	}
	if len(lines)-v.detailScroll-r.H > 0 {
		core.DrawTextRight(s, r.X, r.Y+r.H-1, r.W-1, dim, core.Truncate("▾ more (Alt+↓)", r.W-1))
	}
}

// detailLines is the details pane's text for e at width w, cached per
// (event, width) — the wrap of a big value is the pane's only real cost, and
// the draw and every scroll step ask for it.
func (v *XEventViewer) detailLines(e *xevent.Event, w int) []string {
	if w < 12 {
		return nil
	}
	if e == v.detailCacheEvent && w == v.detailCacheWidth {
		return v.detailCache
	}
	lines := v.eventLines(e, w)
	v.detailCacheEvent, v.detailCacheWidth, v.detailCache = e, w, lines
	return lines
}

// invalidateDetailCache forces the next detailLines to rebuild.
func (v *XEventViewer) invalidateDetailCache() {
	v.detailCacheEvent, v.detailCache = nil, nil
}

// eventLines renders e as the details pane shows it — the event, then its
// fields and its actions as name/value rows, each value wrapped under its
// name. w <= 0 renders unwrapped and uncut, for Copy Event Details.
func (v *XEventViewer) eventLines(e *xevent.Event, w int) []string {
	lines := []string{
		"Event      " + e.Package + "." + e.Name,
		"Timestamp  " + xevent.FormatTimestamp(e.Timestamp),
	}
	section := func(title string, vals []xevent.Value) {
		if len(vals) == 0 {
			return
		}
		lines = append(lines, "", title)
		nameW := 0
		for _, val := range vals {
			nameW = max(nameW, core.DisplayWidth(val.Name))
		}
		nameW = min(nameW, 28)
		for _, val := range vals {
			lines = append(lines, xeValueLines(val, nameW, w)...)
		}
	}
	section(xeFieldsHeading, e.Fields)
	section(xeActionsHeading, e.Actions)
	return lines
}

// xeValueLines renders one name/value row: the name padded to nameW, the
// value beside it and wrapped beneath it, at most xeDetailMaxLines lines. A
// map field shows its text and its key.
func xeValueLines(val xevent.Value, nameW, w int) []string {
	text := val.Display()
	if val.Text != "" && val.Text != val.Value {
		text += " (" + val.Value + ")"
	}
	name := val.Name
	if core.DisplayWidth(name) > nameW {
		name = core.Truncate(name, nameW)
	}
	prefix := "  " + name + strings.Repeat(" ", nameW-core.DisplayWidth(name)) + "  "
	indent := strings.Repeat(" ", core.DisplayWidth(prefix))
	valueW := w - core.DisplayWidth(prefix)

	var body []string
	for _, para := range splitLogLines(text) {
		if w <= 0 {
			body = append(body, para)
			continue
		}
		if para == "" {
			body = append(body, "")
			continue
		}
		body = append(body, core.WrapText(para, max(8, valueW))...)
	}
	if len(body) == 0 {
		body = []string{""}
	}
	var out []string
	for i, l := range body {
		if w > 0 && i == xeDetailMaxLines {
			out = append(out, indent+"… "+strconv.Itoa(len(body)-i)+" more lines — Show Value or the cell menu opens it whole")
			break
		}
		if i == 0 {
			out = append(out, prefix+l)
		} else {
			out = append(out, indent+l)
		}
	}
	return out
}
