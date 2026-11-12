package tuikittest

import "github.com/gdamore/tcell/v3"

// Key is a press of named key k with the given modifiers (none if omitted).
func Key(k tcell.Key, mod ...tcell.ModMask) *tcell.EventKey {
	return tcell.NewEventKey(k, "", mods(mod))
}

// Rune is a press of the character r with the given modifiers.
func Rune(r rune, mod ...tcell.ModMask) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, string(r), mods(mod))
}

// Mouse is a mouse event at (x,y) with buttons btn held.
func Mouse(x, y int, btn tcell.ButtonMask, mod ...tcell.ModMask) *tcell.EventMouse {
	return tcell.NewEventMouse(x, y, btn, mods(mod))
}

// Press is Button1 down at (x,y). With all-motion tracking a held drag is
// reported the same way at each new position, so it doubles as a drag step.
func Press(x, y int) *tcell.EventMouse { return Mouse(x, y, tcell.Button1) }

// Release is the all-buttons-up event at (x,y).
func Release(x, y int) *tcell.EventMouse { return Mouse(x, y, tcell.ButtonNone) }

func mods(m []tcell.ModMask) tcell.ModMask {
	var out tcell.ModMask
	for _, v := range m {
		out |= v
	}
	return out
}
