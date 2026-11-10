package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/fileutil"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
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
		{Label: "Save As...", Action: func() { pp.app.savePicturePanel(pp, true) }},
	}
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
// picture except the mode switch, which is a preference.
func (pp *PicturePanel) toolDisabled(i int) bool {
	return i != picToolMode && !pp.ready()
}

// toolReason says why a cell is withheld.
func (pp *PicturePanel) toolReason(i int) string {
	if !pp.toolDisabled(i) || pp.loading {
		return ""
	}
	return "no picture decoded"
}

// runTool invokes toolbar cell i through the gate the draw dims on.
func (pp *PicturePanel) runTool(i int) {
	pp.tools.Run(i, pp.toolDisabled, pp.toolReason, pp.app.setStatus)
}

// showOverflowMenu pops the cells the row was too narrow for.
func (pp *PicturePanel) showOverflowMenu() {
	r := pp.tools.More.Rect
	pp.app.contextMenu.Show(r.X, r.Y+1, pp.tools.OverflowItems(pp.toolDisabled, pp.toolReason, pp.runTool))
}

// HandleKey: + / - zoom, 0 or f fits, 1 shows actual size, m switches mode,
// arrows / PgUp / PgDn / Home / End pan a zoomed picture.
func (pp *PicturePanel) HandleKey(ev *tcell.EventKey) bool {
	area := pp.artArea()
	switch ev.Key() {
	case tcell.KeyUp:
		pp.scrollBy(0, -1)
	case tcell.KeyDown:
		pp.scrollBy(0, 1)
	case tcell.KeyLeft:
		pp.scrollBy(-2, 0)
	case tcell.KeyRight:
		pp.scrollBy(2, 0)
	case tcell.KeyPgUp:
		pp.scrollBy(0, -max(area.H-1, 1))
	case tcell.KeyPgDn:
		pp.scrollBy(0, max(area.H-1, 1))
	case tcell.KeyHome:
		pp.scrollX, pp.scrollY = 0, 0
	case tcell.KeyEnd:
		pp.scrollY = 1 << 30
		pp.clampScroll()
	case tcell.KeyRune:
		if ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0 {
			return false
		}
		switch ev.Str() {
		case "+", "=":
			pp.runTool(picToolZoomIn)
		case "-", "_":
			pp.runTool(picToolZoomOut)
		case "0", "f", "F":
			pp.runTool(picToolFit)
		case "1":
			pp.runTool(picToolActual)
		case "m", "M":
			pp.runTool(picToolMode)
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
		held := pp.dragging || pp.vbarDrag || pp.hbarDrag
		pp.dragging, pp.vbarDrag, pp.hbarDrag = false, false, false
		pp.dragZone.Release()
		return held
	}
	if pp.dragZone.Route(ev, pp.routeDrag) {
		return true
	}
	if !pp.rect.Contains(mx, my) {
		return false
	}
	switch btn := ev.Buttons(); {
	case btn&tcell.WheelUp != 0:
		pp.wheel(ev, 0, -3)
		return true
	case btn&tcell.WheelDown != 0:
		pp.wheel(ev, 0, 3)
		return true
	case btn&tcell.WheelLeft != 0:
		pp.scrollBy(-6, 0)
		return true
	case btn&tcell.WheelRight != 0:
		pp.scrollBy(6, 0)
		return true
	case btn != tcell.Button1:
		return false
	}
	if pp.toolRect.Contains(mx, my) {
		pp.dragZone.Arm(ev, pzToolbar)
		// The action runs on the press; pzToolbar swallows the repeats.
		if i := pp.tools.CellAt(mx, my); i >= 0 {
			pp.runTool(i)
		} else if pp.tools.More.Rect.Contains(mx, my) {
			pp.showOverflowMenu()
		}
		return true
	}
	if pp.zoomed() && pp.art != nil {
		area := pp.artArea()
		if mx == area.Right() && my >= area.Y && my < area.Bottom() {
			pp.dragZone.Arm(ev, pzVBar)
			pp.routeDrag(ev, pzVBar)
			return true
		}
		if my == area.Bottom() && mx >= area.X && mx < area.Right() {
			pp.dragZone.Arm(ev, pzHBar)
			pp.routeDrag(ev, pzHBar)
			return true
		}
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

// wheel scrolls vertically, or sideways with Shift held.
func (pp *PicturePanel) wheel(ev *tcell.EventMouse, dx, dy int) {
	if ev.Modifiers()&tcell.ModShift != 0 {
		dx, dy = 2*dy, 0
	}
	pp.scrollBy(dx, dy)
}

// routeDrag delivers a held Button1 to the zone that claimed the press.
func (pp *PicturePanel) routeDrag(ev *tcell.EventMouse, z pictureZone) {
	art := pp.art
	if art == nil {
		return
	}
	area := pp.artArea()
	mx, my := ev.Position()
	switch z {
	case pzView:
		if !pp.dragging {
			return
		}
		// Grab-and-drag: the picture follows the pointer.
		pp.scrollBy(pp.dragFrom[0]-mx, pp.dragFrom[1]-my)
		pp.dragFrom = [2]int{mx, my}
	case pzVBar:
		pp.vbarDrag = true
		pp.scrollY = core.ScrollOffsetForDrag(my-area.Y, area.H, art.Rows, area.H)
		pp.clampScroll()
	case pzHBar:
		pp.hbarDrag = true
		pp.scrollX = core.ScrollOffsetForDrag(mx-area.X, area.W, art.Cols, area.W)
		pp.clampScroll()
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
	a.fileDialog.ShowSave("Save Picture", initial, write)
}
