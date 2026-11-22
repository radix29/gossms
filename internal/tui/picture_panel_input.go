package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/fileutil"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/core"
)

// picture_panel_input.go is PicturePanel's toolbar, keys, mouse and Save.

// buildTools defines the toolbar, in picTool* order.
func (pp *PicturePanel) buildTools() {
	pp.tools.Cells = []controls.ToolCell{
		{Label: "Fit", Action: func() { pp.setZoom(0) }},
		{Label: "1:1", Action: pp.zoomActual},
		{Label: "Zoom +", Action: func() { pp.zoomBy(1) }},
		{Label: "Zoom -", Action: func() { pp.zoomBy(-1) }},
		{Action: pp.toggleMode},
		{Label: "Bright +", Action: func() { pp.adjustTone(1, 0) }},
		{Label: "Bright -", Action: func() { pp.adjustTone(-1, 0) }},
		{Label: "Contrast +", Action: func() { pp.adjustTone(0, 1) }},
		{Label: "Contrast -", Action: func() { pp.adjustTone(0, -1) }},
		{Label: "Reset", Action: pp.resetTone},
		{Label: "Save As...", Action: func() { pp.app.savePicturePanel(pp, true) }},
	}
	pp.tools.Disabled, pp.tools.Reason, pp.tools.Status = pp.toolDisabled, pp.toolReason, pp.app.setStatus
	pp.refreshToolLabels()
}

// refreshToolLabels names the mode the Mode cell is in, and marks Fit while
// fitting.
func (pp *PicturePanel) refreshToolLabels() {
	label := "Mode: Sextant"
	if pp.ascii {
		label = "Mode: ASCII"
	}
	pp.tools.Cells[picToolMode].Label = label
	pp.tools.Cells[picToolFit].Selected = !pp.zoomed()
}

// toolDisabled reports whether cell i is inert: every cell needs a decoded
// picture except the mode switch, which is a preference, and Reset needs a
// tone to undo.
func (pp *PicturePanel) toolDisabled(i int) bool {
	switch {
	case i == picToolMode:
		return false
	case !pp.ready():
		return true
	}
	return i == picToolResetTone && !pp.toned()
}

// toolReason says why a cell is withheld.
func (pp *PicturePanel) toolReason(i int) string {
	switch {
	case !pp.toolDisabled(i) || pp.loading:
		return ""
	case !pp.ready():
		return "no picture decoded"
	}
	return "brightness and contrast are the picture's own"
}

// HandleKey: + / - zoom, b / B brighter / darker, c / C more / less contrast,
// r resets both, 0 or f fits, 1 shows actual size, m switches mode,
// arrows / PgUp / PgDn / Home / End pan a zoomed picture.
func (pp *PicturePanel) HandleKey(ev *tcell.EventKey) bool {
	// The scroll keys are claimed even when nothing moves (the view keeps
	// them, as it always has).
	if bound, _ := pp.viewport().ScrollKey(ev, 2); bound {
		return true
	}
	switch ev.Key() {
	case tcell.KeyRune:
		if ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0 {
			return false
		}
		switch ev.Str() {
		case "+", "=":
			pp.tools.RunCell(picToolZoomIn)
		case "-", "_":
			pp.tools.RunCell(picToolZoomOut)
		case "0", "f", "F":
			pp.tools.RunCell(picToolFit)
		case "1":
			pp.tools.RunCell(picToolActual)
		case "m", "M":
			pp.tools.RunCell(picToolMode)
		case "b":
			pp.tools.RunCell(picToolBrighter)
		case "B":
			pp.tools.RunCell(picToolDarker)
		case "c":
			pp.tools.RunCell(picToolMoreContrast)
		case "C":
			pp.tools.RunCell(picToolLessContrast)
		case "r", "R":
			pp.tools.RunCell(picToolResetTone)
		default:
			return false
		}
	default:
		return false
	}
	return true
}

// HandleMouse: toolbar clicks, a Button1 drag in the view pans, the
// scrollbars drag, and the wheel scrolls (Shift or a horizontal wheel
// sideways).
func (pp *PicturePanel) HandleMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	if ev.Buttons() == tcell.ButtonNone {
		bars := pp.vp.Release()
		held := pp.dragging || bars
		pp.dragging = false
		pp.dragZone.Release()
		return held
	}
	if pp.dragZone.Route(ev, pp.routeDrag) {
		return true
	}
	if !pp.rect.Contains(mx, my) {
		return false
	}
	// Three rows a tick, six columns sideways (Shift turns the wheel too).
	if wheel, _ := pp.viewport().Wheel(ev, 6, 3); wheel {
		return true
	}
	if ev.Buttons() != tcell.Button1 {
		return false
	}
	// The action runs on the press; pzToolbar swallows the repeats.
	if pp.tools.Press(pp.toolRect, mx, my, pp.app.contextMenu, pp.rect) {
		pp.dragZone.Arm(ev, pzToolbar)
		return true
	}
	// The bars are drawn only while zoomed (picture_panel_draw.go).
	if pp.zoomed() && pp.art != nil && pp.viewport().HandleScrollbars(ev) {
		pp.dragZone.Arm(ev, pzBar)
		return true
	}
	if pp.viewRect.Contains(mx, my) {
		pp.dragZone.Arm(ev, pzView)
		pp.dragging = true
		pp.dragFrom = [2]int{mx, my}
		return true
	}
	pp.dragZone.Arm(ev, pzNone)
	return true
}

// routeDrag delivers a held Button1 to the zone that claimed the press.
func (pp *PicturePanel) routeDrag(ev *tcell.EventMouse, z pictureZone) {
	if pp.art == nil {
		return
	}
	mx, my := ev.Position()
	switch z {
	case pzView:
		if !pp.dragging {
			return
		}
		// Grab-and-drag: the picture follows the pointer.
		pp.scrollBy(pp.dragFrom[0]-mx, pp.dragFrom[1]-my)
		pp.dragFrom = [2]int{mx, my}
	case pzBar:
		// The bar latched on the press keeps the drag (core.Viewport).
		pp.viewport().HandleScrollbars(ev)
	}
}

// savePicturePanel runs File > Save / Save As... for a picture panel, writing
// the cell's original bytes — not the rendering — under the format's
// extension. A second Save goes back to the file the first one wrote.
func (a *App) savePicturePanel(pp *PicturePanel, saveAs bool) {
	if len(pp.data) == 0 {
		a.setStatus("No picture to save")
		return
	}
	data := pp.data
	write := func(path string) {
		if err := fileutil.WriteAtomic(path, data, 0o644); err != nil {
			a.setStatus(fmt.Sprintf("Save failed: %v", err))
			return
		}
		pp.filePath = path
		a.setStatus("Saved " + path)
	}
	if !saveAs && pp.filePath != "" {
		write(pp.filePath)
		return
	}
	initial := pp.filePath
	if initial == "" {
		initial = core.SafeFileName(pp.title)
		if ext := pictureExt(pp.format); len(initial) < len(ext) || initial[len(initial)-len(ext):] != ext {
			initial += ext
		}
	}
	a.fileDialog().ShowSave("Save Picture", initial, write)
}
