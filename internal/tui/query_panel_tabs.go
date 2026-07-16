package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// messagesHighlighter colors an entire line in the Messages tab red when it
// belongs to an error message (see query.Message.IsError and the parallel
// messageErrorLines slice built in renderActiveTab).
func (p *QueryPanel) messagesHighlighter(lines [][]rune, idx int) []controls.ColorRun {
	if idx >= len(p.messageErrorLines) || !p.messageErrorLines[idx] {
		return nil
	}
	pal := theme.Active()
	errStyle := tcell.StyleDefault.Background(pal.EditorBg).Foreground(pal.Error)
	return []controls.ColorRun{{Start: 0, Len: len(lines[idx]), Style: errStyle}}
}

// onMessagesTab reports whether the active tab is Messages rather than a
// result-set grid or execution plan — results, messages, resultsText, and
// planView occupy the same rect (see layoutChildren), so exactly one of
// them is drawn, and routed keys/mouse, at any given time.
//
// Built on tabCount/messagesTabIndex rather than resultTabs — this runs
// several times per key/mouse event and every Draw (see its call sites in
// query_panel.go), and resultTabs formats a label string per tab just to
// have its length counted.
func (p *QueryPanel) onMessagesTab() bool {
	idx := p.messagesTabIndex()
	return idx >= 0 && p.activeTab == idx
}

// tabCount returns how many result tabs there currently are — the same
// count resultTabs' returned slice would have, without allocating or
// formatting any label. See resultTabs for what each index means.
func (p *QueryPanel) tabCount() int {
	if p.planView != nil && p.result == nil {
		return 2 // Execution Plan, Messages
	}
	if p.result == nil {
		return 0
	}
	n := len(p.result.Sets) + 1 // result set(s) + Messages
	if p.planView != nil {
		n++ // Execution Plan, inserted before Messages
	}
	return n
}

// messagesTabIndex returns the Messages tab's index — always the last tab,
// per resultTabs' ordering — or -1 when there are no tabs at all.
func (p *QueryPanel) messagesTabIndex() int {
	if n := p.tabCount(); n > 0 {
		return n - 1
	}
	return -1
}

// textTabActive reports whether the active tab is a result set being
// rendered as plain text (Query > Results To Text) rather than the grid —
// results, messages, resultsText, and planView occupy the same rect (see
// layoutChildren), so exactly one of them is drawn, and routed keys/mouse,
// at any given time.
func (p *QueryPanel) textTabActive() bool {
	return !p.onMessagesTab() && !p.planTabActive() && p.resultsMode == ResultsModeText && p.result != nil
}

// planTabActive reports whether the active tab is the graphical Execution
// Plan view rather than Messages or a Results tab — see onMessagesTab and
// resultTabs. Estimated mode (p.result == nil) puts it first; Actual mode
// (both p.result and p.planView set — see setResultPlan) puts it right
// after the Results tab(s), matching resultTabs' own ordering.
func (p *QueryPanel) planTabActive() bool {
	if p.planView == nil {
		return false
	}
	if p.result == nil {
		return p.activeTab == 0
	}
	return p.activeTab == len(p.result.Sets)
}

// tabSegments computes each result-tab's on-screen extent for labels. Draw
// and hit-test both build their column math from this same call so hits
// line up with what's actually on screen.
func (p *QueryPanel) tabSegments(labels []string) [][]controls.TabSegment {
	widths := make([][]int, len(labels))
	for i, label := range labels {
		widths[i] = []int{controls.TabLabelWidth(label)}
	}
	return controls.TabStripSegments(p.tabRect.X+1, widths, p.tabRect.Right())
}

// drawTabBar renders the result-set/Messages tabs, styled like the
// PanelManager's panel tabs.
func (p *QueryPanel) drawTabBar(s tcell.Screen) {
	if p.tabRect.H != 1 {
		return
	}
	pal := theme.Active()
	barStyle := theme.StyleMenuBar()
	core.FillRect(s, p.tabRect, ' ', barStyle)
	labels := p.resultTabs()
	for i, seg := range p.tabSegments(labels) {
		tabStyle := barStyle
		if i == p.activeTab {
			tabStyle = tcell.StyleDefault.Background(pal.BorderActive).Foreground(tcell.ColorWhite).Bold(true)
		}
		core.DrawText(s, seg[0].X, p.tabRect.Y, tabStyle, " "+labels[i]+" ")
	}
}

// resultTabs returns the tab labels for the last result: one per result
// set ("Results" alone when there's only one), plus Messages — with
// "Execution Plan" inserted depending on how planView got populated (see
// setEstimatedPlan and setResultPlan): alone with Messages when there's no
// real result (Estimated mode, which never runs the query), or between the
// Results tab(s) and Messages when there is one (Actual mode, "Include
// Actual Execution Plan" — see planTabActive, which mirrors this ordering).
func (p *QueryPanel) resultTabs() []string {
	if p.planView != nil && p.result == nil {
		return []string{"Execution Plan", "Messages"}
	}
	if p.result == nil {
		return nil
	}
	tabs := make([]string, 0, len(p.result.Sets)+2)
	if len(p.result.Sets) == 1 {
		tabs = append(tabs, "Results")
	} else {
		for i := range p.result.Sets {
			tabs = append(tabs, fmt.Sprintf("Results %d", i+1))
		}
	}
	if p.planView != nil {
		tabs = append(tabs, "Execution Plan")
	}
	return append(tabs, "Messages")
}

// setActiveTab switches the results area to tab i, if it exists.
func (p *QueryPanel) setActiveTab(i int) {
	if i < 0 || i >= p.tabCount() || i == p.activeTab {
		return
	}
	p.activeTab = i
	p.renderActiveTab()
}

// tabAt returns the tab index at screen column mx on the tab bar, or -1.
func (p *QueryPanel) tabAt(mx int) int {
	for i, seg := range p.tabSegments(p.resultTabs()) {
		if mx >= seg[0].X && mx < seg[0].X+seg[0].W {
			return i
		}
	}
	return -1
}

// renderActiveTab loads the active tab's content into the results grid or
// resultsText editor, honouring the panel's Grid/Text results mode.
func (p *QueryPanel) renderActiveTab() {
	res := p.result
	if res == nil {
		return
	}
	// +2 to convert the Options dialog's "max cell length" (a character
	// count) into a column-width clamp, matching computeColWidths's own
	// header-width convention of content width + 1 column of padding on
	// each side.
	p.results.SetMaxCellWidth(p.app.cfg.MaxCellLength + 2)
	if p.onMessagesTab() {
		p.setMessages(res.Messages)
		return
	}
	if p.planTabActive() {
		return // planView draws itself; nothing to push into it
	}
	set := res.Sets[p.activeTab]
	if p.resultsMode == ResultsModeText {
		p.resultsText.SetText(formatResultsAsText(set))
		return
	}
	p.results.SetData(set.Columns, set.Rows)
}

// setMessages installs msgs into the Messages tab's read-only editor — a
// message's Text may itself span multiple lines (a detailed SQL Server
// error, say), so each one is split first, keeping messageErrorLines a
// per-rendered-line slice in lockstep with what SetText below actually
// produces (Editor.SetText splits on "\n" the same way). Shared by a normal
// query's Messages tab (renderActiveTab) and the execution-plan paths,
// which report a compile failure the same way.
func (p *QueryPanel) setMessages(msgs []query.Message) {
	var textLines []string
	var errLines []bool
	for _, m := range msgs {
		for _, l := range strings.Split(m.Text, "\n") {
			textLines = append(textLines, l)
			errLines = append(errLines, m.IsError)
		}
	}
	p.messageErrorLines = errLines
	p.messages.SetText(strings.Join(textLines, "\n"))
}

// formatResultsAsText renders set as SSMS's Results To Text look: a header
// row, a dashed separator, then one line per data row, each column padded
// to its widest value so columns visually line up like a real table.
func formatResultsAsText(set query.ResultSet) string {
	widths := make([]int, len(set.Columns))
	for i, c := range set.Columns {
		widths[i] = core.DisplayWidth(c)
	}
	for _, row := range set.Rows {
		for i, cell := range row {
			if w := core.DisplayWidth(cell); w > widths[i] {
				widths[i] = w
			}
		}
	}
	var sb strings.Builder
	writeRow := func(cells []string) {
		for i, cell := range cells {
			if i > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(core.PadRight(cell, widths[i]))
		}
		sb.WriteByte('\n')
	}
	writeRow(set.Columns)
	seps := make([]string, len(widths))
	for i, w := range widths {
		seps[i] = strings.Repeat("-", w)
	}
	writeRow(seps)
	for _, row := range set.Rows {
		writeRow(row)
	}
	return strings.TrimSuffix(sb.String(), "\n")
}
