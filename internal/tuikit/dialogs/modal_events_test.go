package dialogs

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/tuikittest"
)

// A visible modal returns true for every mouse event, whatever the button: it
// is a focus trap, and a host that routes a refused event onward would let a
// wheel or right-click inside the box reach the screen beneath it. PromptDialog
// and TypedConfirmDialog returned false for anything but Button1 over their
// form area.
func TestAVisibleModalConsumesEveryMouseEvent(t *testing.T) {
	scr := func() tcell.Screen { return tuikittest.NewScreen(100, 30) }
	type modal interface {
		HandleMouse(*tcell.EventMouse) bool
		InnerRect() core.Rect
	}
	cases := map[string]func(t *testing.T) modal{
		"alert": func(*testing.T) modal {
			d := NewAlertDialog(scr())
			d.ShowAlert("Error", "Something went wrong.")
			return d
		},
		"confirm": func(*testing.T) modal {
			d := NewConfirmDialog(scr())
			d.ShowConfirm("Delete Table", "Delete dbo.Orders?", func(bool) {})
			return d
		},
		"prompt": func(*testing.T) modal {
			d := NewPromptDialog(scr())
			d.ShowPrompt("Rename", "New name:", "Name", "orders", func(string) {})
			return d
		},
		"typed confirm": func(*testing.T) modal {
			d := NewTypedConfirmDialog(scr())
			d.ShowTypedConfirm("Drop Database", "Type the name to confirm.", "sales", func(bool) {})
			return d
		},
		"progress": func(*testing.T) modal {
			d := NewProgressDialog(scr())
			d.RevealDelay = 0
			d.ShowProgress("Delete Table", "Deleting...", func() {})
			return d
		},
		"properties": func(*testing.T) modal {
			d := NewPropertiesDialog(scr())
			d.ShowProperties("Properties", []PropertyRow{{Key: "k", Value: "v"}})
			return d
		},
		"file dialog": func(t *testing.T) modal {
			d, dir := newTestFileDialog(t)
			d.ShowOpen("Open File", dir+"/", func(string) {})
			d.rect = core.Rect{X: 0, Y: 0, W: 60, H: 20} // nil screen: placed by hand
			return d
		},
	}
	buttons := map[string]tcell.ButtonMask{
		"wheel up": tcell.WheelUp, "wheel down": tcell.WheelDown,
		"right": tcell.Button2, "middle": tcell.Button3,
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			d := build(t)
			in := d.InnerRect()
			// Every row of the box's interior, so a form area (the prompt's field
			// row) is covered as well as the message and button rows.
			for y := in.Y; y < in.Bottom(); y++ {
				for bname, b := range buttons {
					if !d.HandleMouse(tcell.NewEventMouse(in.X+1, y, b, tcell.ModNone)) {
						t.Fatalf("%s at row %d was refused by a visible modal", bname, y-in.Y)
					}
					d.HandleMouse(tcell.NewEventMouse(in.X+1, y, tcell.ButtonNone, tcell.ModNone))
				}
			}
		})
	}
}
