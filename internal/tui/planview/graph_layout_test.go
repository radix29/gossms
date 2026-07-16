package planview

import (
	"testing"

	"github.com/radix29/gossms/internal/showplan"
)

// buildTestTree constructs a small synthetic operator tree (no XML
// parsing needed) shaped like the real fixture's shallow subtrees:
//
//	root
//	├── a
//	│   ├── a1
//	│   └── a2
//	└── b
func buildTestTree() *showplan.Node {
	a1 := &showplan.Node{ID: 3}
	a2 := &showplan.Node{ID: 4}
	a := &showplan.Node{ID: 1, Children: []*showplan.Node{a1, a2}}
	b := &showplan.Node{ID: 2}
	return &showplan.Node{ID: 0, Children: []*showplan.Node{a, b}}
}

func TestLayoutGraph_NilRoot(t *testing.T) {
	g := layoutGraph(nil)
	if len(g.tiles) != 0 || len(g.edges) != 0 {
		t.Fatalf("layoutGraph(nil) produced tiles/edges, want none")
	}
}

func TestLayoutGraph_AllNodesPlaced(t *testing.T) {
	g := layoutGraph(buildTestTree())
	if len(g.tiles) != 5 {
		t.Fatalf("len(tiles) = %d, want 5", len(g.tiles))
	}
	if len(g.edges) != 4 {
		t.Fatalf("len(edges) = %d, want 4 (one per parent-child pair: root-a, root-b, a-a1, a-a2)", len(g.edges))
	}
}

func TestLayoutGraph_ParentLeftOfChildren(t *testing.T) {
	g := layoutGraph(buildTestTree())
	root, a, b, a1, a2 := g.rects[0], g.rects[1], g.rects[2], g.rects[3], g.rects[4]
	for name, pair := range map[string][2]int{
		"root<a": {root.X, a.X},
		"root<b": {root.X, b.X},
		"a<a1":   {a.X, a1.X},
		"a<a2":   {a.X, a2.X},
	} {
		if pair[0] >= pair[1] {
			t.Errorf("%s: expected parent X < child X, got %d >= %d", name, pair[0], pair[1])
		}
	}
}

func TestLayoutGraph_TilesNonOverlapping(t *testing.T) {
	g := layoutGraph(buildTestTree())
	for i := range g.tiles {
		for j := range g.tiles {
			if i == j {
				continue
			}
			a, b := g.tiles[i].rect, g.tiles[j].rect
			if a.X != b.X {
				continue // different columns never overlap in this layout
			}
			overlapY := a.Y < b.Bottom() && b.Y < a.Bottom()
			if overlapY {
				t.Errorf("tiles for node %d and %d overlap: %+v vs %+v",
					g.tiles[i].node.ID, g.tiles[j].node.ID, a, b)
			}
		}
	}
}

func TestLayoutGraph_EdgesConnectTileMidpoints(t *testing.T) {
	g := layoutGraph(buildTestTree())
	rootRect, aRect := g.rects[0], g.rects[1]
	var found bool
	for _, e := range g.edges {
		if e.x1 == rootRect.Right() && e.y1 == rootRect.Y+graphTileH/2 &&
			e.x2 == aRect.X && e.y2 == aRect.Y+graphTileH/2 {
			found = true
		}
	}
	if !found {
		t.Error("no edge found connecting root's right-mid to node a's left-mid")
	}
}

func TestLayoutGraph_CanvasSize(t *testing.T) {
	g := layoutGraph(buildTestTree())
	wantW := 0
	wantH := 0
	for _, t := range g.tiles {
		if r := t.rect.Right(); r > wantW {
			wantW = r
		}
		if b := t.rect.Bottom(); b > wantH {
			wantH = b
		}
	}
	if g.canvasW != wantW {
		t.Errorf("canvasW = %d, want %d", g.canvasW, wantW)
	}
	if g.canvasH != wantH {
		t.Errorf("canvasH = %d, want %d", g.canvasH, wantH)
	}
}

func TestLayoutGraph_ParentCenteredBetweenFirstAndLastChild(t *testing.T) {
	g := layoutGraph(buildTestTree())
	a, a1, a2 := g.rects[1], g.rects[3], g.rects[4]
	wantCenter := (a1.Y + a2.Y) / 2
	if a.Y != wantCenter {
		t.Errorf("node a's Y = %d, want %d (centered between a1.Y=%d and a2.Y=%d)", a.Y, wantCenter, a1.Y, a2.Y)
	}
}
