package theme

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// SetPalette is the extension point doc.go advertises to a host application:
// swap the palette at start-up and every Style* helper follows. Active returns
// a pointer into the package's own variable, so what this pins is that the
// pointer keeps reading the palette that was set — an implementation that
// handed back a copy would leave a caller reading the old colours.
func TestSetPaletteReplacesWhatActiveReads(t *testing.T) {
	saved := *Active()
	defer SetPalette(saved)

	before := Active()
	custom := saved
	custom.Background = tcell.NewRGBColor(1, 2, 3)
	custom.MenuBar = tcell.NewRGBColor(4, 5, 6)
	SetPalette(custom)

	if got := Active().Background; got != custom.Background {
		t.Errorf("Active().Background = %v, want %v", got, custom.Background)
	}
	if got := before.MenuBar; got != custom.MenuBar {
		t.Errorf("a pointer taken before SetPalette reads MenuBar = %v, want %v", got, custom.MenuBar)
	}
	if got := StyleMenuBar(); got != tcell.StyleDefault.Background(custom.MenuBar).Foreground(custom.Text) {
		t.Errorf("StyleMenuBar did not follow the new palette: %v", got)
	}

	SetPalette(saved)
	if got := Active().Background; got != saved.Background {
		t.Errorf("after restoring: Background = %v, want %v", got, saved.Background)
	}
}
