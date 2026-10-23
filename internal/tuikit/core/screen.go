package core

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// Screen conveniences
// ---------------------------------------------------------------------------

// controlStringLimit caps inbound OSC/XDA payloads. tcell's 64 KiB default
// silently drops an OSC 52 clipboard reply (base64) for any paste over ~48 KiB
// of text — the terminal-clipboard fallback used over SSH. A cap, not 0, so an
// unterminated string from a misbehaving terminal can't grow without bound.
const controlStringLimit = 16 << 20

// Init creates, initialises, and returns a new tcell.Screen with mouse enabled.
func Init() (tcell.Screen, error) {
	s, err := tcell.NewScreen(tcell.OptControlStringLimit(controlStringLimit))
	if err != nil {
		return nil, err
	}
	if err := s.Init(); err != nil {
		return nil, err
	}
	s.EnableMouse()
	s.EnablePaste()
	s.SetStyle(theme.StyleDefault())
	return s, nil
}
