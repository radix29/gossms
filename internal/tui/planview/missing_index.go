package planview

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// missingIndexes returns the suggestions the optimizer attached to the
// statement now on screen, or nil.
func (v *PlanView) missingIndexes() []showplan.MissingIndex {
	st := v.currentStatement()
	if st == nil {
		return nil
	}
	return st.MissingIndexes
}

// bannerText is the one line the banner shows: the highest-impact
// suggestion's CREATE statement, with a count when there are more.
func (v *PlanView) bannerText() string {
	mi := v.missingIndexes()
	if len(mi) == 0 {
		return ""
	}
	best := 0
	for i, m := range mi {
		if m.Impact > mi[best].Impact {
			best = i
		}
	}
	text := fmt.Sprintf("Missing Index (Impact %.1f%%): %s", mi[best].Impact, mi[best].CreateStatement())
	if len(mi) > 1 {
		text += fmt.Sprintf("  (+%d more)", len(mi)-1)
	}
	return text + "  —  m / click for details"
}

// drawMissingIndexBanner renders the banner row, in the green SSMS uses for
// it. A no-op when layout reserved no row for it.
func (v *PlanView) drawMissingIndexBanner(s tcell.Screen) {
	if v.bannerRect.H != 1 {
		return
	}
	pal := theme.Active()
	st := tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Success)
	core.FillRect(s, v.bannerRect, ' ', st)
	if v.bannerRect.W > 2 {
		core.DrawTextClipped(s, v.bannerRect.X+1, v.bannerRect.Y, v.bannerRect.W-2, st, v.bannerText())
	}
}

// openMissingIndexDetails hands the current statement's suggestions to the
// host as a ready-to-review script — SSMS's "Missing Index Details...".
// Reports whether there was anything to hand over.
func (v *PlanView) openMissingIndexDetails() bool {
	mi := v.missingIndexes()
	if len(mi) == 0 || v.OnMissingIndex == nil {
		return false
	}
	v.OnMissingIndex(showplan.MissingIndexScript(mi))
	return true
}
