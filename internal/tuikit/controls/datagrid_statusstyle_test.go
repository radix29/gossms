package controls

import (
	"testing"

	"github.com/radix29/gossms/internal/tuikit/theme"
)

// A status style override follows a live theme switch. It used to be a style
// value captured when the grid was built — at package init for the results
// grids, before the configured theme was even applied — so every results grid
// kept the Default palette's status line.
func TestDataGridStatusStyleFollowsThePalette(t *testing.T) {
	saved := *theme.Active()
	defer theme.SetPalette(saved)

	g := NewDataGrid()
	g.SetBounds(0, 0, 40, 6)
	g.SetData([]string{"n"}, [][]string{{"1"}})
	g.SetStatus("1 row")
	g.SetStatusStyle(theme.StyleGridStatus)
	s := mockScreen(t, 40, 6)

	for _, p := range []theme.Palette{theme.Default, theme.MC} {
		theme.SetPalette(p)
		g.Draw(s)
		_, st, _ := s.Get(0, 5)
		if got := st.GetBackground(); got != p.GridStatusBg {
			t.Errorf("status line background = %v, want the palette's GridStatusBg %v", got, p.GridStatusBg)
		}
		if got := st.GetForeground(); got != p.GridStatusText {
			t.Errorf("status line foreground = %v, want the palette's GridStatusText %v", got, p.GridStatusText)
		}
	}
}
