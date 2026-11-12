package layout

import "github.com/radix29/gossms/internal/tuikit/core"

// Which of core's control interfaces each layout type implements; see the
// widgets package's control_test.go for why these are pinned. PanelManager is
// not a Focusable: its panels are, through Activatable.
var (
	_ interface {
		core.Focusable
		core.Bounded
		core.KeyHandler
		core.MouseHandler
	} = (*Splitter)(nil)
	_ interface {
		core.Bounded
		core.KeyHandler
		core.MouseHandler
	} = (*PanelManager)(nil)
)
