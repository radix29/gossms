package showplan

import "testing"

// Rank orders descending by each measure and keeps every operator; on an
// estimated plan Rows falls back to the estimate and ElapsedMS is 0.
func TestStatementRank(t *testing.T) {
	for _, file := range []string{"testdata/actual_plan.sqlplan", "testdata/estimated_plan.xml"} {
		plan := mustParseFile(t, file)
		for si, st := range plan.Statements {
			if st.Root == nil {
				continue
			}
			all := len(st.Nodes())
			measures := map[RankBy]func(*Node) float64{
				RankByCost: func(n *Node) float64 { return n.Cost(st.SubTreeCost) },
				RankByRows: func(n *Node) float64 { return float64(n.Rows()) },
				RankByTime: func(n *Node) float64 { return float64(n.ElapsedMS()) },
			}
			for by, m := range measures {
				ranked := st.Rank(by)
				if len(ranked) != all {
					t.Fatalf("%s stmt %d by %d: %d nodes, want %d", file, si, by, len(ranked), all)
				}
				for i := 1; i < len(ranked); i++ {
					if m(ranked[i-1]) < m(ranked[i]) {
						t.Errorf("%s stmt %d by %d: not descending at %d", file, si, by, i)
					}
				}
			}
		}
	}
}

func TestNodeRowsAndElapsed(t *testing.T) {
	est := &Node{EstRows: 12.7}
	if est.Rows() != 12 || est.ElapsedMS() != 0 {
		t.Errorf("estimated node: Rows %d, ElapsedMS %d; want 12, 0", est.Rows(), est.ElapsedMS())
	}
	act := &Node{EstRows: 12.7, Runtime: &Runtime{Rows: 40, ElapsedMS: 7}}
	if act.Rows() != 40 || act.ElapsedMS() != 7 {
		t.Errorf("actual node: Rows %d, ElapsedMS %d; want 40, 7", act.Rows(), act.ElapsedMS())
	}
}

// Plan.Rank keeps every operator of every statement, descends by each
// measure, and by cost orders on own cost, so a trivial statement's 100%
// operator does not outrank a costlier one elsewhere.
func TestPlanRank(t *testing.T) {
	for _, file := range []string{"testdata/actual_plan.sqlplan", "testdata/estimated_plan.xml"} {
		plan := mustParseFile(t, file)
		all := 0
		for _, st := range plan.Statements {
			all += len(st.Nodes())
		}
		measures := map[RankBy]func(*Node) float64{
			RankByCost: (*Node).OwnCost,
			RankByRows: func(n *Node) float64 { return float64(n.Rows()) },
			RankByTime: func(n *Node) float64 { return float64(n.ElapsedMS()) },
		}
		for by, m := range measures {
			ranked := plan.Rank(by)
			if len(ranked) != all {
				t.Fatalf("%s by %d: %d nodes, want %d", file, by, len(ranked), all)
			}
			for i := 1; i < len(ranked); i++ {
				if m(ranked[i-1].Node) < m(ranked[i].Node) {
					t.Errorf("%s by %d: not descending at %d", file, by, i)
				}
			}
		}
	}

	cheap := &Node{ID: 0, EstSubtreeCost: 0.001}
	scan := &Node{ID: 1, EstSubtreeCost: 2}
	seek := &Node{ID: 0, EstSubtreeCost: 3, Children: []*Node{scan}}
	p := &Plan{Statements: []*Statement{
		{SubTreeCost: 0.001, Root: cheap},
		{SubTreeCost: 3, Root: seek},
	}}
	got := p.Rank(RankByCost)
	if got[0].Node != scan || got[0].Stmt != 1 || got[1].Node != seek || got[2].Node != cheap || got[2].Stmt != 0 {
		t.Errorf("Rank by cost: %+v", got)
	}
}
