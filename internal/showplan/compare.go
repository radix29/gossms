package showplan

import (
	"fmt"
	"math"
	"strings"
)

// compare.go pairs two plans of the same query so the differences between them
// can be shown side by side — SSMS's Compare Showplan, over the operator tree.
//
// Pure data, like the rest of the package: it produces the paired rows and
// says what differs about each, and leaves every question of presentation to
// the caller.

// ChangeKind classifies one line of a comparison.
type ChangeKind int

const (
	ChangeSame      ChangeKind = iota // matched, and nothing compared differs
	ChangeDifferent                   // matched, but the estimates or counters moved
	ChangeOnlyLeft                    // present in the left plan only
	ChangeOnlyRight                   // present in the right plan only
)

// String names the kind for a grid cell. "Only in A"/"Only in B" rather than
// left/right: the caller labels its two columns, and a row that says "left"
// beside a column headed "Plan 42" reads as a third thing.
func (k ChangeKind) String() string {
	switch k {
	case ChangeSame:
		return "Same"
	case ChangeDifferent:
		return "Changed"
	case ChangeOnlyLeft:
		return "Only in A"
	case ChangeOnlyRight:
		return "Only in B"
	}
	return ""
}

// NodeDiff is one line of the comparison: a pair of matched operators, or one
// operator present in a single plan. Depth is the operator's depth in the left
// tree, or in the right one for a right-only line, so the caller can indent the
// comparison the way it would indent either tree.
type NodeDiff struct {
	Depth int
	Kind  ChangeKind
	Left  *Node
	Right *Node

	// Changes names what moved, one phrase per property, empty for a matched
	// pair that is identical and for a one-sided line — there is nothing to
	// compare an absent operator against.
	Changes []string
}

// Node returns whichever side of the line exists, preferring the left. Every
// line has at least one.
func (d NodeDiff) Node() *Node {
	if d.Left != nil {
		return d.Left
	}
	return d.Right
}

// PropDiff is one statement-level property compared across the two plans.
type PropDiff struct {
	Name        string
	Left, Right string
	Different   bool
}

// CompareStatements pairs the operators of two statements, preorder, and
// reports what differs about each pair.
//
// Matching is by physical operator and the object it reads, in sibling order:
// two Index Seeks of the same table pair up even when the index changed —
// which is the comparison a user opens this for, and would be lost if the
// index were part of what makes two operators "the same". A physical operator
// that changed (a seek that became a scan) deliberately does not pair: the two
// show as one-sided lines, which is what happened.
func CompareStatements(a, b *Statement) []NodeDiff {
	var out []NodeDiff
	if a == nil || b == nil {
		return out
	}
	var walk func(l, r *Node, depth int)

	// only emits one side's subtree as one-sided lines.
	var only func(n *Node, depth int, kind ChangeKind)
	only = func(n *Node, depth int, kind ChangeKind) {
		if n == nil {
			return
		}
		d := NodeDiff{Depth: depth, Kind: kind}
		if kind == ChangeOnlyLeft {
			d.Left = n
		} else {
			d.Right = n
		}
		out = append(out, d)
		for _, c := range n.Children {
			only(c, depth+1, kind)
		}
	}

	walk = func(l, r *Node, depth int) {
		changes := nodeChanges(l, r)
		kind := ChangeSame
		if len(changes) > 0 {
			kind = ChangeDifferent
		}
		out = append(out, NodeDiff{Depth: depth, Kind: kind, Left: l, Right: r, Changes: changes})

		// Pair the children greedily in order: each left child takes the first
		// unclaimed right child with the same signature. Greedy rather than a
		// longest-common-subsequence pass because an operator tree's siblings
		// are two or three wide — the two agree wherever it matters, and the
		// difference is invisible beside "this operator is only in one plan".
		taken := make([]bool, len(r.Children))
		match := make([]int, len(l.Children))
		for i, lc := range l.Children {
			match[i] = -1
			for j, rc := range r.Children {
				if !taken[j] && signature(lc) == signature(rc) {
					taken[j], match[i] = true, j
					break
				}
			}
		}
		// Emit in left order, with each unmatched right child following the
		// matched sibling it sits after — so a subtree added on the right shows
		// where it was added rather than at the end.
		next := 0
		emitRightUpTo := func(limit int) {
			for ; next < limit; next++ {
				if !taken[next] {
					only(r.Children[next], depth+1, ChangeOnlyRight)
					taken[next] = true
				}
			}
		}
		for i, lc := range l.Children {
			if j := match[i]; j >= 0 {
				emitRightUpTo(j)
				walk(lc, r.Children[j], depth+1)
				next = j + 1
				continue
			}
			only(lc, depth+1, ChangeOnlyLeft)
		}
		emitRightUpTo(len(r.Children))
	}

	if a.Root == nil || b.Root == nil {
		only(a.Root, 0, ChangeOnlyLeft)
		only(b.Root, 0, ChangeOnlyRight)
		return out
	}
	walk(a.Root, b.Root, 0)
	return out
}

// signature is what makes two operators the same operator across two plans:
// the physical operator and the object it touches, without the index — see
// CompareStatements.
func signature(n *Node) string {
	o := n.Object
	return strings.Join([]string{n.PhysicalOp, o.Database, o.Schema, o.Table, o.Alias}, "|")
}

// nodeChanges names what differs between two matched operators, or nothing.
//
// Estimates are compared with a relative tolerance: a plan re-estimated
// against slightly different statistics moves every row count in the tree by a
// hair, and a comparison where every operator says "Changed" says nothing.
func nodeChanges(l, r *Node) []string {
	var out []string
	if l.Object.Index != r.Object.Index && (l.Object.Index != "" || r.Object.Index != "") {
		out = append(out, "Index "+dashIfEmpty(l.Object.Index)+" → "+dashIfEmpty(r.Object.Index))
	}
	if l.Parallel != r.Parallel {
		out = append(out, "Parallel "+yesNo(l.Parallel)+" → "+yesNo(r.Parallel))
	}
	if l.ExecMode != r.ExecMode && (l.ExecMode != "" || r.ExecMode != "") {
		out = append(out, "Execution mode "+dashIfEmpty(l.ExecMode)+" → "+dashIfEmpty(r.ExecMode))
	}
	if moved(l.EstRows, r.EstRows) {
		out = append(out, fmt.Sprintf("Est rows %s → %s", num(l.EstRows), num(r.EstRows)))
	}
	if moved(l.EstSubtreeCost, r.EstSubtreeCost) {
		out = append(out, fmt.Sprintf("Est subtree cost %s → %s", cost(l.EstSubtreeCost), cost(r.EstSubtreeCost)))
	}
	if l.Runtime != nil && r.Runtime != nil {
		if moved(float64(l.Runtime.Rows), float64(r.Runtime.Rows)) {
			out = append(out, fmt.Sprintf("Actual rows %d → %d", l.Runtime.Rows, r.Runtime.Rows))
		}
		if moved(float64(l.Runtime.LogicalReads), float64(r.Runtime.LogicalReads)) {
			out = append(out, fmt.Sprintf("Logical reads %d → %d", l.Runtime.LogicalReads, r.Runtime.LogicalReads))
		}
	}
	return out
}

// changeTolerance is how far a number may move before it counts as a change:
// one per cent, which absorbs a re-estimate against refreshed statistics and
// still catches every difference worth opening a comparison for.
const changeTolerance = 0.01

// moved reports whether two numbers differ by more than the tolerance. A move
// away from zero always counts — the tolerance is relative, and relative to
// zero everything is infinite.
func moved(a, b float64) bool {
	if a == b {
		return false
	}
	scale := math.Max(math.Abs(a), math.Abs(b))
	if scale == 0 {
		return false
	}
	return math.Abs(a-b)/scale > changeTolerance
}

// CompareProperties compares the statement-level numbers SSMS puts above the
// two trees. Every property is listed, matching or not: the point of the pane
// is to read the two plans against each other, and a row that vanishes because
// it happens to agree makes the list a different length for every comparison.
func CompareProperties(a, b *Statement) []PropDiff {
	if a == nil || b == nil {
		return nil
	}
	var out []PropDiff
	add := func(name, l, r string) {
		out = append(out, PropDiff{Name: name, Left: l, Right: r, Different: l != r})
	}
	add("Statement type", a.Type, b.Type)
	add("Estimated subtree cost", cost(a.SubTreeCost), cost(b.SubTreeCost))
	add("Estimated rows", num(a.EstRows), num(b.EstRows))
	add("Degree of parallelism", fmt.Sprint(a.DOP), fmt.Sprint(b.DOP))
	add("Operators", fmt.Sprint(len(a.Nodes())), fmt.Sprint(len(b.Nodes())))
	add("Missing indexes", fmt.Sprint(len(a.MissingIndexes)), fmt.Sprint(len(b.MissingIndexes)))
	add("Warnings", fmt.Sprint(len(a.Warnings)), fmt.Sprint(len(b.Warnings)))
	add("Query hash", dashIfEmpty(a.QueryHash), dashIfEmpty(b.QueryHash))
	add("CPU time (ms)", timeStat(a.TimeStats, func(t *TimeStats) int64 { return t.CPUMS }),
		timeStat(b.TimeStats, func(t *TimeStats) int64 { return t.CPUMS }))
	add("Elapsed time (ms)", timeStat(a.TimeStats, func(t *TimeStats) int64 { return t.ElapsedMS }),
		timeStat(b.TimeStats, func(t *TimeStats) int64 { return t.ElapsedMS }))
	add("Memory granted (KB)", grant(a.MemoryGrant), grant(b.MemoryGrant))
	return out
}

func timeStat(t *TimeStats, get func(*TimeStats) int64) string {
	if t == nil {
		return "-"
	}
	return fmt.Sprint(get(t))
}

func grant(g *MemoryGrant) string {
	if g == nil {
		return "-"
	}
	return fmt.Sprint(g.GrantedKB)
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// num and cost render an estimate the way a plan does: row counts to one
// decimal (the optimizer's estimates are fractional), costs to four, which is
// the precision a plan's own cost numbers carry.
func num(v float64) string  { return fmt.Sprintf("%.1f", v) }
func cost(v float64) string { return fmt.Sprintf("%.4f", v) }
