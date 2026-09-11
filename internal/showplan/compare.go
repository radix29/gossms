package showplan

import (
	"fmt"
	"math"
	"strings"
)

// compare.go pairs two plans of the same query for side-by-side comparison
// (SSMS's Compare Showplan over the operator tree). Pure data; presentation is
// the caller's.

// ChangeKind classifies one line of a comparison.
type ChangeKind int

const (
	ChangeSame      ChangeKind = iota // matched, and nothing compared differs
	ChangeDifferent                   // matched, but the estimates or counters moved
	ChangeOnlyLeft                    // present in the left plan only
	ChangeOnlyRight                   // present in the right plan only
)

// String names the kind for a grid cell. "Only in A/B" rather than left/right,
// since the caller labels its columns.
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

// NodeDiff is one comparison line: a matched operator pair, or an operator in
// one plan only. Depth is the depth in the left tree (right for right-only
// lines), for indentation.
type NodeDiff struct {
	Depth int
	Kind  ChangeKind
	Left  *Node
	Right *Node

	// Changes names each differing property; empty for identical pairs and
	// one-sided lines.
	Changes []string
}

// Node returns whichever side exists, preferring the left.
func (d NodeDiff) Node() *Node {
	if d.Left != nil {
		return d.Left
	}
	return d.Right
}

// PropDiff is one statement-level property compared across the plans.
type PropDiff struct {
	Name        string
	Left, Right string
	Different   bool
}

// CompareStatements pairs two statements' operators in preorder and reports
// what differs.
//
// Operators match by physical operator and object, in sibling order: two Index
// Seeks on one table pair even if the index changed (the comparison users
// want). A changed physical operator (seek → scan) doesn't pair and shows as
// two one-sided lines.
func CompareStatements(a, b *Statement) []NodeDiff {
	var out []NodeDiff
	if a == nil || b == nil {
		return out
	}
	var walk func(l, r *Node, depth int)

	// only emits a subtree as one-sided lines.
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

		// Pair children greedily in order: each left child takes the first
		// unclaimed right child with the same signature. Siblings are two or
		// three wide, so an LCS pass wouldn't differ where it matters.
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
		// Emit in left order, each unmatched right child after the matched
		// sibling it follows, so an added subtree shows where it was added.
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

// signature identifies an operator across plans: physical operator and object,
// without the index (see CompareStatements).
func signature(n *Node) string {
	o := n.Object
	return strings.Join([]string{n.PhysicalOp, o.Database, o.Schema, o.Table, o.Alias}, "|")
}

// nodeChanges names what differs between two matched operators.
//
// Estimates use a relative tolerance, since a re-estimate against slightly
// different statistics nudges every number. The two runtime numbers are
// compared exactly: they're measurements, and hiding a reads delta hides what
// the user is tuning.
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
		if l.Runtime.Rows != r.Runtime.Rows {
			out = append(out, fmt.Sprintf("Actual rows %d → %d", l.Runtime.Rows, r.Runtime.Rows))
		}
		if l.Runtime.LogicalReads != r.Runtime.LogicalReads {
			out = append(out, fmt.Sprintf("Logical reads %d → %d", l.Runtime.LogicalReads, r.Runtime.LogicalReads))
		}
	}
	return out
}

// changeTolerance is 1%: absorbs a re-estimate, catches real differences.
const changeTolerance = 0.01

// moved reports whether two numbers differ beyond the tolerance. Any move away
// from zero counts. The equality check above also keeps scale non-zero.
func moved(a, b float64) bool {
	if a == b {
		return false
	}
	scale := math.Max(math.Abs(a), math.Abs(b))
	return math.Abs(a-b)/scale > changeTolerance
}

// CompareProperties compares the statement-level numbers SSMS shows above the
// trees. Every property is listed, matching or not, so the table has a fixed
// shape.
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

// num and cost render as a plan does: rows to one decimal, costs to four.
func num(v float64) string  { return fmt.Sprintf("%.1f", v) }
func cost(v float64) string { return fmt.Sprintf("%.4f", v) }
