package planview

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// searchState holds the Tree/Plan tabs' shared operator search: '/' starts a
// query, Enter confirms and jumps to the first match, Escape cancels; then n/N
// cycle matches.
type searchState struct {
	active  bool
	query   string
	matches []int // NodeIDs, in statement preorder
	idx     int
}

// searchEligibleTab reports whether operator search/warning-jump apply to the
// active tab (not the XML tab, which browses raw text).
func (v *PlanView) searchEligibleTab() bool {
	return v.activeTab == TabTree || v.activeTab == TabPlan
}

// handleSearchKey handles '/' typing, Enter/Escape, and the n/N/w/p keys.
// Returns false for anything else, so the caller falls through to the tab.
func (v *PlanView) handleSearchKey(ev *tcell.EventKey) bool {
	if v.searchSt.active {
		switch ev.Key() {
		case tcell.KeyEnter:
			v.confirmSearch()
			return true
		case tcell.KeyEscape:
			v.searchSt.active = false
			return true
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			v.searchSt.query = core.TrimLastGrapheme(v.searchSt.query)
			return true
		}
		// Swallow everything else while typing (digits and letters would
		// switch tabs or fire other keys) so a query can contain any character.
		if r := core.EvRune(ev); r != 0 && ev.Modifiers()&tcell.ModCtrl == 0 {
			v.searchSt.query += ev.Str()
		}
		return true
	}
	if !v.searchEligibleTab() {
		return false
	}
	switch core.EvRune(ev) {
	case '/':
		v.searchSt.active = true
		v.searchSt.query = ""
		return true
	case 'n':
		v.jumpToMatch(1)
		return true
	case 'N':
		v.jumpToMatch(-1)
		return true
	case 'w':
		v.jumpToWarning(1)
		return true
	case 'p':
		v.showEstimated = !v.showEstimated
		return true
	}
	return false
}

// confirmSearch finds every operator matching the query (case-insensitive
// substring of PhysicalOp/LogicalOp/Object.Table) and jumps to the first.
func (v *PlanView) confirmSearch() {
	v.searchSt.active = false
	st := v.currentStatement()
	q := strings.ToLower(strings.TrimSpace(v.searchSt.query))
	if st == nil || q == "" {
		v.searchSt.matches = nil
		return
	}
	var matches []int
	for _, n := range st.Nodes() {
		if nodeMatchesQuery(n, q) {
			matches = append(matches, n.ID)
		}
	}
	v.searchSt.matches = matches
	v.searchSt.idx = -1
	v.jumpToMatch(1)
}

// nodeMatchesQuery reports whether n's operator name or object contains the
// (lowercased) query.
func nodeMatchesQuery(n *showplan.Node, q string) bool {
	if strings.Contains(strings.ToLower(n.PhysicalOp), q) {
		return true
	}
	if strings.Contains(strings.ToLower(n.LogicalOp), q) {
		return true
	}
	return !n.Object.IsZero() && strings.Contains(strings.ToLower(n.Object.Table), q)
}

// jumpToMatch selects the next/previous match, wrapping; reports "no matches"
// via OnStatus.
func (v *PlanView) jumpToMatch(delta int) {
	n := len(v.searchSt.matches)
	if n == 0 {
		if v.OnStatus != nil {
			v.OnStatus(`No matches for "` + v.searchSt.query + `"`)
		}
		return
	}
	v.searchSt.idx = ((v.searchSt.idx+delta)%n + n) % n
	v.revealAndSelect(v.searchSt.matches[v.searchSt.idx])
}

// revealAndSelect expands any collapsed ancestor between the statement root
// and id, so the flattened row list (rebuildTreeRows) contains it before
// selectNode scrolls. Otherwise ensureTreeRowVisible silently no-ops for a
// match hidden under a collapsed node: details update but the Tree pane
// doesn't move.
func (v *PlanView) revealAndSelect(id int) {
	if st := v.currentStatement(); st != nil && v.expandAncestorsOf(st.Root, id) {
		v.rebuildTreeRows()
	}
	v.selectNode(id)
}

// jumpToWarning selects the next/previous operator with a warning, wrapping.
func (v *PlanView) jumpToWarning(delta int) {
	st := v.currentStatement()
	if st == nil {
		return
	}
	nodes := st.Nodes()
	if len(nodes) == 0 {
		return
	}
	start := 0
	for i, n := range nodes {
		if n.ID == v.selectedID {
			start = i
			break
		}
	}
	for step := 1; step <= len(nodes); step++ {
		i := ((start+step*delta)%len(nodes) + len(nodes)) % len(nodes)
		if len(nodes[i].Warnings) > 0 {
			v.revealAndSelect(nodes[i].ID)
			return
		}
	}
	if v.OnStatus != nil {
		v.OnStatus("No operators with warnings")
	}
}
