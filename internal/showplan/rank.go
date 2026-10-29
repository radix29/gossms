package showplan

import (
	"cmp"
	"slices"
)

// RankBy selects the measure Statement.Rank orders operators by. The TUI's
// operator summary and the CLI's plan digest both rank through it, so the two
// agree on which operators are the expensive ones.
type RankBy int

const (
	RankByCost RankBy = iota // own cost as a share of the statement's (Node.Cost)
	RankByRows               // Node.Rows
	RankByTime               // Node.ElapsedMS
)

// Rows returns the operator's actual rows when it ran, else its estimate per
// execution.
func (n *Node) Rows() int64 {
	if n.Runtime != nil {
		return n.Runtime.Rows
	}
	return int64(n.EstRows)
}

// ElapsedMS returns the operator's actual elapsed time, or 0 on an estimated
// plan.
func (n *Node) ElapsedMS() int64 {
	if n.Runtime != nil {
		return n.Runtime.ElapsedMS
	}
	return 0
}

// Rank returns the statement's operators sorted descending by by; ties keep
// preorder (Nodes).
func (s *Statement) Rank(by RankBy) []*Node {
	nodes := s.Nodes()
	slices.SortStableFunc(nodes, func(a, b *Node) int {
		switch by {
		case RankByRows:
			return cmp.Compare(b.Rows(), a.Rows())
		case RankByTime:
			return cmp.Compare(b.ElapsedMS(), a.ElapsedMS())
		default:
			return cmp.Compare(b.Cost(s.SubTreeCost), a.Cost(s.SubTreeCost))
		}
	})
	return nodes
}

// Ranked is one operator of a Plan, with the index of its statement in
// Plan.Statements.
type Ranked struct {
	Stmt int
	Node *Node
}

// Rank returns every operator of every statement sorted descending by by.
// Cost ranks by own estimated cost (Node.OwnCost), not by share of the
// statement: a trivial statement's lone operator is 100% of its statement
// and would otherwise outrank the expensive scan of another. Within one
// statement that is Statement.Rank's order; ties keep statement order, then
// preorder.
func (p *Plan) Rank(by RankBy) []Ranked {
	var out []Ranked
	for i, st := range p.Statements {
		for _, n := range st.Nodes() {
			out = append(out, Ranked{Stmt: i, Node: n})
		}
	}
	slices.SortStableFunc(out, func(a, b Ranked) int {
		switch by {
		case RankByRows:
			return cmp.Compare(b.Node.Rows(), a.Node.Rows())
		case RankByTime:
			return cmp.Compare(b.Node.ElapsedMS(), a.Node.ElapsedMS())
		default:
			return cmp.Compare(b.Node.OwnCost(), a.Node.OwnCost())
		}
	})
	return out
}
