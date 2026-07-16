package planview

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// propsState holds the bottom Properties section's own scroll position.
type propsState struct {
	scroll int
}

// detailsHeaderText builds a details-style header's title row: title plus a
// right-aligned scroll indicator — shared by the Tree tab's "Operator
// Details" pane and the Plan tab's "Properties" block.
func detailsHeaderText(title string, w int, canUp, canDown bool) string {
	var indicator string
	switch {
	case canUp && canDown:
		indicator = "Scroll ▲▼"
	case canUp:
		indicator = "Scroll ▲"
	case canDown:
		indicator = "Scroll ▼"
	default:
		return title
	}
	pad := w - core.DisplayWidth(title) - core.DisplayWidth(indicator)
	if pad < 1 {
		return title
	}
	return title + strings.Repeat(" ", pad) + indicator
}

// drawDetailsHeader renders a details-style pane's title row.
func drawDetailsHeader(s tcell.Screen, rect core.Rect, title string, canUp, canDown bool) {
	if rect.H == 0 {
		return
	}
	hs := theme.StyleMenuBar()
	core.FillRect(s, rect, ' ', hs)
	core.DrawTextClipped(s, rect.X+1, rect.Y, rect.W-2, hs, detailsHeaderText(title, rect.W-2, canUp, canDown))
}

// scrollDetails shifts the Operator Details pane's scroll offset by delta
// rows, clamped to the current selection's line count.
func (v *PlanView) scrollDetails(delta int) {
	total := len(detailLines(v.selectedNode(), v.currentStatement()))
	max := core.Max(0, total-v.detailsContentRect.H)
	v.detailsScroll = core.Clamp(v.detailsScroll+delta, 0, max)
}

// drawDetails renders the Operator Details pane's aligned key/value lines
// for n, scrolled by scroll rows — shared by the Tree tab's right-hand pane
// and the Plan tab's "Selected Operator" detail strip.
func drawDetails(s tcell.Screen, rect core.Rect, n *showplan.Node, st *showplan.Statement, scroll int) {
	pal := theme.Active()
	bg := theme.StylePanel()
	core.FillRect(s, rect, ' ', bg)
	if rect.H <= 0 || rect.W <= 0 {
		return
	}
	if n == nil {
		core.DrawTextClipped(s, rect.X+1, rect.Y, rect.W-2, bg, "(no operator selected)")
		return
	}
	lines := detailLines(n, st)
	for row := 0; row < rect.H; row++ {
		idx := scroll + row
		if idx >= len(lines) {
			break
		}
		style := bg
		if strings.HasPrefix(lines[idx], "Warnings") && len(n.Warnings) > 0 {
			style = tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Warning)
		}
		core.DrawTextClipped(s, rect.X+1, rect.Y+row, rect.W-2, style, lines[idx])
	}
}

// detailKVs builds the Operator Details pane's ordered key/value pairs for
// n, matching the SSMS Properties-grid field set. st supplies the
// statement-level Memory Grant, shown only on the plan's root operator —
// same as real SSMS, which reports memory grant once per statement rather
// than per operator.
func detailKVs(n *showplan.Node, st *showplan.Statement) []showplan.KV {
	var kvs []showplan.KV
	add := func(k, v string) { kvs = append(kvs, showplan.KV{Key: k, Value: v}) }

	add("Physical Operator", n.PhysicalOp)
	if n.LogicalOp != "" {
		add("Logical Operator", n.LogicalOp)
	}
	if st != nil {
		add("Cost %", fmt.Sprintf("%.1f%%", n.Cost(st.SubTreeCost)*100))
	}
	if !n.Object.IsZero() {
		add("Object", n.Object.Short())
	}
	if len(n.OutputColumns) > 0 {
		add("Output Columns", strings.Join(n.OutputColumns, ", "))
	}
	if n.Predicate != "" {
		add("Predicate", n.Predicate)
	}
	if n.SeekPredicate != "" {
		add("Seek Predicate", n.SeekPredicate)
	}
	add("Estimated Rows", formatCount(n.EstRows))
	if n.Runtime != nil {
		add("Actual Rows", fmt.Sprintf("%d", n.Runtime.Rows))
	}
	add("Estimated I/O", fmt.Sprintf("%.2f", n.EstIO))
	add("Estimated CPU", fmt.Sprintf("%.2f", n.EstCPU))
	if n.Runtime != nil {
		add("Actual CPU", fmt.Sprintf("%d ms", n.Runtime.CPUMS))
		add("Actual Duration", fmt.Sprintf("%d ms", n.Runtime.ElapsedMS))
	}
	if st != nil && st.Root == n && st.MemoryGrant != nil {
		add("Memory Grant", fmt.Sprintf("%d KB", st.MemoryGrant.GrantedKB))
	}
	if n.Parallel {
		threads := ""
		if n.Runtime != nil && n.Runtime.Threads > 0 {
			threads = fmt.Sprintf(" (%d threads)", n.Runtime.Threads)
		}
		add("Parallel", "Yes"+threads)
	} else {
		add("Parallel", "No")
	}
	warn := "—"
	if len(n.Warnings) > 0 {
		warn = strings.Join(n.Warnings, ", ")
	}
	add("Warnings", warn)
	return kvs
}

// detailLines renders detailKVs as "Label : Value" lines with every label
// padded to the widest one, so the colons line up in a column — shared by
// drawDetails and formatDetailsText (Copy).
func detailLines(n *showplan.Node, st *showplan.Statement) []string {
	if n == nil {
		return nil
	}
	kvs := detailKVs(n, st)
	width := 0
	for _, kv := range kvs {
		if w := core.DisplayWidth(kv.Key); w > width {
			width = w
		}
	}
	lines := make([]string, len(kvs))
	for i, kv := range kvs {
		lines[i] = core.PadRight(kv.Key, width) + " : " + kv.Value
	}
	return lines
}

// formatDetailsText renders detailLines as Copy-ready plain text.
func formatDetailsText(n *showplan.Node, st *showplan.Statement) string {
	return strings.Join(detailLines(n, st), "\n")
}

// formatCount renders an estimate (a float64 in the source XML, even
// though it's always integral in practice) without a decimal point.
func formatCount(f float64) string {
	return fmt.Sprintf("%.0f", f)
}

// drawProperties renders n's full ordered attribute list — the bottom
// section's "Properties" mode — starting at row scroll.
func drawProperties(s tcell.Screen, rect core.Rect, n *showplan.Node, scroll int) {
	bg := theme.StylePanel()
	core.FillRect(s, rect, ' ', bg)
	if n == nil || rect.H <= 0 || rect.W <= 0 {
		return
	}
	for row := 0; row < rect.H; row++ {
		idx := scroll + row
		if idx >= len(n.Props) {
			break
		}
		kv := n.Props[idx]
		core.DrawTextClipped(s, rect.X+1, rect.Y+row, rect.W-2, bg, kv.Key+" : "+kv.Value)
	}
}
