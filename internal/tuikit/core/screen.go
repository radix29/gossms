package core

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// Screen conveniences
// ---------------------------------------------------------------------------

// Init creates, initialises, and returns a new tcell.Screen with mouse enabled.
func Init() (tcell.Screen, error) {
	s, err := tcell.NewScreen()
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
