package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// messagesHighlighter colors an entire line in the Messages tab red when it
// belongs to an error message (query.Message.IsError and the parallel
// messageErrorLines slice built in renderActiveTab).
func (p *QueryPanel) messagesHighlighter(doc *controls.Document, idx int) []controls.ColorRun {
	if idx >= len(p.messageErrorLines) || !p.messageErrorLines[idx] {
		return nil
	}
	pal := theme.Active()
	errStyle := tcell.StyleDefault.Background(pal.EditorBg).Foreground(pal.Error)
	return []controls.ColorRun{{Start: 0, Len: len(doc.Line(idx)), Style: errStyle}}
}

// onMessagesTab reports whether the active tab is Messages rather than a
// result-set grid or execution plan. results, messages, resultsText and planView
// occupy the same rect (layoutChildren), so exactly one is drawn and routed
// keys/mouse at a time.
//
// Built on tabCount/messagesTabIndex rather than resultTabs: this runs several
// times per key/mouse event and every Draw, and resultTabs formats a label string
// per tab just to count them.
func (p *QueryPanel) onMessagesTab() bool {
	idx := p.messagesTabIndex()
	return idx >= 0 && p.activeTab == idx
}

// tabCount returns how many result tabs there are (the count resultTabs would
// return), without allocating or formatting labels. See resultTabs for what each
// index means.
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

// messagesTabIndex returns the Messages tab's index (always the last, per
// resultTabs' ordering), or -1 when there are no tabs at all.
func (p *QueryPanel) messagesTabIndex() int {
	if n := p.tabCount(); n > 0 {
		return n - 1
	}
	return -1
}

// textTabActive reports whether the active tab is a result set rendered as plain
// text (Query > Results To Text) rather than the grid. See onMessagesTab for why
// only one panel is live.
func (p *QueryPanel) textTabActive() bool {
	return !p.onMessagesTab() && !p.planTabActive() && p.resultsMode == ResultsModeText && p.result != nil
}

// planTabActive reports whether the active tab is the graphical Execution Plan
// view rather than Messages or a Results tab (onMessagesTab, resultTabs).
// Estimated mode (p.result == nil) puts it first; Actual mode (p.result and
// p.planView both set, see setResultPlan) puts it right after the Results tab(s).
func (p *QueryPanel) planTabActive() bool {
	if p.planView == nil {
		return false
	}
	if p.result == nil {
		return p.activeTab == 0
	}
	return p.activeTab == len(p.result.Sets)
}

// tabSegments computes each result-tab's on-screen extent for labels. Draw and
// hit-test both build their column math from this call so hits line up with
// what's on screen.
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
			tabStyle = tcell.StyleDefault.Background(pal.BorderActive).Foreground(color.White).Bold(true)
		}
		core.DrawText(s, seg[0].X, p.tabRect.Y, tabStyle, " "+labels[i]+" ")
	}
}

// resultTabs returns the tab labels for the last result: one per result set
// ("Results" alone when there's only one), plus Messages, with "Execution Plan"
// inserted depending on how planView got populated (setEstimatedPlan and
// setResultPlan): alone with Messages when there's no real result (Estimated mode,
// which never runs the query, and a Live Query Statistics run still executing,
// whose tab is labelled for it, planTabLabel), or between the Results tab(s) and
// Messages in Actual mode ("Include Actual Execution Plan"; planTabActive mirrors
// this ordering).
func (p *QueryPanel) resultTabs() []string {
	if p.planView != nil && p.result == nil {
		return []string{p.planTabLabel(), "Messages"}
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
		tabs = append(tabs, p.planTabLabel())
	}
	return append(tabs, "Messages")
}

// planTabLabel is the plan tab's label: "Live Query Statistics" while a run with
// that toggle on is executing (startLiveStats), "Execution Plan" once the actual
// plan replaces it, and for every other plan.
func (p *QueryPanel) planTabLabel() string {
	if p.planView != nil && p.planView.Live() {
		return "Live Query Statistics"
	}
	return "Execution Plan"
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
	// +2 to convert the Options dialog's "max default cell length" (a character
	// count) into a column-width clamp, matching computeColWidths's header-width
	// convention of content width + 1 column of padding on each side.
	p.results.SetMaxCellWidth(p.app.cfg.MaxCellLength + 2)
	if p.onMessagesTab() {
		p.setMessages(res.Messages)
		return
	}
	if p.planTabActive() {
		return // planView draws itself; nothing to push into it
	}
	set, ok := p.activeResultSet()
	if !ok {
		return
	}
	if p.resultsMode == ResultsModeText {
		p.showResultsText(set)
		return
	}
	// The set itself, not its Rows: it is the grid's NullSource.
	p.results.SetSource(set.Columns, set)
}

// setMessages installs msgs into the Messages tab's read-only editor. A message's
// Text may itself span lines (a detailed SQL Server error), so each is split
// first, keeping messageErrorLines per rendered line in step with what SetText
// produces (it splits on "\n" the same way). Shared by a normal query's Messages
// tab (renderActiveTab) and the execution-plan paths, which report a compile
// failure the same way.
func (p *QueryPanel) setMessages(msgs []query.Message) {
	var textLines []string
	var errLines []bool
	for _, m := range msgs {
		for l := range strings.SplitSeq(m.Text, "\n") {
			textLines = append(textLines, l)
			errLines = append(errLines, m.IsError)
		}
	}
	p.messageErrorLines = errLines
	p.messages.SetText(strings.Join(textLines, "\n"))
}

// textKey is what one Results to Text rendering depends on: the set (a result and
// a tab in it), the column cap, and the tab width a cell's own tabs expand to.
type textKey struct {
	result         *query.Result
	tab, max, tabW int
}

// textKeyNow is the key of the rendering the active tab wants now.
func (p *QueryPanel) textKeyNow() textKey {
	maxW := p.app.cfg.MaxTextColumnLength
	if maxW <= 0 { // a Config not from config.Load
		maxW = config.DefaultMaxTextColumnLength
	}
	return textKey{result: p.result, tab: p.activeTab, max: maxW, tabW: p.resultsText.IndentWidth()}
}

// textSyncCells is the largest set, in cells, that Results to Text formats on the
// UI goroutine. A million rows of eight columns took 2.1 s to format, 1.1 s to
// install through SetText and 370 ms more in the first Draw, with input frozen;
// below this it is a few tens of milliseconds, and a "Formatting..." flash would
// cost more than it saves.
const textSyncCells = 100_000

// showResultsText puts set into the resultsText editor: from the memo when it was
// the last one rendered, formatted in place when small, otherwise formatted off
// the UI goroutine behind a placeholder and installed when it lands if the tab
// still wants it. A run for the same rendering already in flight is left to
// finish, so switching away and back mid-format does not throw the work away.
func (p *QueryPanel) showResultsText(set query.ResultSet) {
	key := p.textKeyNow()
	if m := &p.textMemo; m.lines != nil && m.key == key {
		p.resultsText.SetLineBuffer(m.lines)
		return
	}
	if len(set.Rows)*max(len(set.Columns), 1) <= textSyncCells {
		lines, _ := formatResultsAsText(context.Background(), set, key.max, key.tabW)
		p.textMemo.key, p.textMemo.lines = key, lines
		p.resultsText.SetLineBuffer(lines)
		return
	}
	p.resultsText.SetText(fmt.Sprintf("Formatting %d rows as text...", len(set.Rows)))
	if p.textFormatting() {
		return
	}
	// Rooted at Background because formatting reads no connection: the rows are
	// already in memory, and a disconnect leaves them worth showing. The latest still
	// cancels it on a newer rendering, a new run, or a close.
	ctx, token := p.textRun.Begin(context.Background())
	p.textRunKey = key
	repair := func() { p.textRun.Done(token) }
	p.app.safegoRepair("formatting results as text", repair, func() {
		lines, err := formatResultsAsText(ctx, set, key.max, key.tabW)
		p.app.postAndWake(func() {
			if !p.textRun.Done(token) || err != nil {
				return
			}
			p.textMemo.key, p.textMemo.lines = key, lines
			if p.textTabActive() && p.textKeyNow() == key {
				p.resultsText.SetLineBuffer(lines)
			}
		})
	})
}

// textFormatting reports whether the rendering the active tab wants is being
// formatted off the UI goroutine right now.
func (p *QueryPanel) textFormatting() bool {
	return !p.textRun.Idle() && p.textRunKey == p.textKeyNow()
}

// textCancelEvery is how many rows formatResultsAsText formats between checks of
// its context: often enough that a superseded run stops within a few
// milliseconds, rarely enough that the check costs nothing.
const textCancelEvery = 4096

// formatResultsAsText renders set as SSMS's Results To Text look: a header row, a
// dashed separator, then one line per data row, each column padded to its widest
// value.
//
// No column is wider than maxW; a longer value or header is cut to it with no
// ellipsis, as SSMS does. Uncapped, one megabyte-long cell padded every row of its
// result to a megabyte.
//
// It builds a LineBuffer rather than a string so a large set can be formatted,
// split and measured entirely off the UI goroutine (showResultsText). A cancelled
// ctx stops it with ctx's error.
func formatResultsAsText(ctx context.Context, set query.ResultSet, maxW, tabW int) (*controls.LineBuffer, error) {
	widths := make([]int, len(set.Columns))
	for i, c := range set.Columns {
		widths[i] = core.DisplayWidthAtMost(c, maxW)
	}
	for r, row := range set.Rows {
		if r%textCancelEvery == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		for i, cell := range row {
			if w := core.DisplayWidthAtMost(cell, maxW); w > widths[i] {
				widths[i] = w
			}
		}
	}
	lines := controls.NewLineBuffer(tabW)
	var sb strings.Builder
	writeRow := func(cells []string) {
		sb.Reset()
		for i, cell := range cells {
			if i > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(core.PadRight(cell, widths[i]))
		}
		lines.AppendText(sb.String())
	}
	writeRow(set.Columns)
	seps := make([]string, len(widths))
	for i, w := range widths {
		seps[i] = strings.Repeat("-", w)
	}
	writeRow(seps)
	for r, row := range set.Rows {
		if r%textCancelEvery == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		writeRow(row)
	}
	return lines, nil
}
