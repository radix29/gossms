package planview

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// searchState holds the Tree/Plan tabs' shared operator search: '/'
// starts typing a query, Enter confirms it and jumps to the first match,
// Escape cancels; once confirmed, n/N cycle to the next/previous match.
type searchState struct {
	active  bool
	query   string
	matches []int // NodeIDs, in statement preorder
	idx     int
}

// searchEligibleTab reports whether operator search/warning-jump apply
// to the active tab — the XML tab has its own browsing model (raw
// text), not per-operator navigation.
func (v *PlanView) searchEligibleTab() bool {
	return v.activeTab == TabTree || v.activeTab == TabPlan
}

// handleSearchKey handles '/' typing, Enter/Escape, and the n/N/w/p
// single-key actions. Returns false for anything it doesn't own, so the
// caller falls through to the active tab's own key handling.
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
			if n := len(v.searchSt.query); n > 0 {
				v.searchSt.query = v.searchSt.query[:n-1]
			}
			return true
		}
		// Swallow everything else while typing — including digits and
		// letters that would otherwise switch tabs or trigger other
		// single-key actions — so a query can contain any character.
		if r := core.EvRune(ev); r != 0 && ev.Modifiers()&tcell.ModCtrl == 0 {
			v.searchSt.query += string(r)
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

// confirmSearch computes every operator matching the typed query
// (case-insensitive substring against PhysicalOp/LogicalOp/Object.Table)
// and jumps to the first one.
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

// nodeMatchesQuery reports whether n's operator name or object matches
// the (already-lowercased) query as a substring.
func nodeMatchesQuery(n *showplan.Node, q string) bool {
	if strings.Contains(strings.ToLower(n.PhysicalOp), q) {
		return true
	}
	if strings.Contains(strings.ToLower(n.LogicalOp), q) {
		return true
	}
	return !n.Object.IsZero() && strings.Contains(strings.ToLower(n.Object.Table), q)
}

// jumpToMatch selects the next/previous search match, wrapping around;
// reports "no matches" via OnStatus if the query found nothing.
func (v *PlanView) jumpToMatch(delta int) {
	n := len(v.searchSt.matches)
	if n == 0 {
		if v.OnStatus != nil {
			v.OnStatus(`No matches for "` + v.searchSt.query + `"`)
		}
		return
	}
	v.searchSt.idx = ((v.searchSt.idx+delta)%n + n) % n
	v.selectNode(v.searchSt.matches[v.searchSt.idx])
}

// jumpToWarning selects the next/previous operator with a warning,
// starting from the current selection and wrapping around.
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
			v.selectNode(nodes[i].ID)
			return
		}
	}
	if v.OnStatus != nil {
		v.OnStatus("No operators with warnings")
	}
}
