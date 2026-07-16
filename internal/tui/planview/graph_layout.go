package planview

import (
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// Tile geometry, in virtual canvas cells — fixed-width "operator card"
// per the SSMS-classic graph mockup.
const (
	graphTileW = 20
	// graphTileH must leave room for 3 interior text lines (PhysicalOp,
	// object/LogicalOp, cost%+rows) between the top and bottom borders —
	// 5 total, not 4: an earlier version used 4 and the third line
	// silently overwrote the bottom border row instead of the border
	// clipping it, since Rect.Inner(1) only had 2 interior rows to give.
	graphTileH = 5
	graphHGap  = 4 // horizontal gap between a tile and its children's column
	graphVGap  = 1 // vertical gap between sibling tiles
)

// tile is one operator's placed position on the virtual canvas.
type tile struct {
	node *showplan.Node
	rect core.Rect
}

// edge is one parent→child connector, as three straight segments: a
// horizontal run from the parent's right edge to midX, a vertical run at
// midX between the parent's and child's row, and a horizontal run from
// midX to the child's left edge. All coordinates are virtual-canvas cells.
type edge struct {
	x1, y1 int // parent tile's right-edge midpoint
	x2, y2 int // child tile's left-edge midpoint
	midX   int // the connector's vertical trunk column
}

// graphLayout is the result of laying out one statement's operator tree
// as a left-to-right node-and-edge graph — root at the smallest X,
// children extending rightward, matching real SSMS (exec.png).
type graphLayout struct {
	tiles   []tile
	edges   []edge
	rects   map[int]core.Rect // NodeID -> placed rect, for hit-testing/navigation
	canvasW int
	canvasH int
}

// layoutGraph places root's operator tree on a virtual canvas. Pure
// function of the tree shape — no tcell/theme dependency — so it's
// testable without a screen. Each node's tile is centered vertically
// between its first and last child's tile (recursive, not globally
// balanced, but stable and simple); a childless node occupies exactly one
// tile-height band.
func layoutGraph(root *showplan.Node) *graphLayout {
	g := &graphLayout{rects: make(map[int]core.Rect)}
	if root == nil {
		return g
	}

	var place func(n *showplan.Node, depth, top int) (bandBottom, tileY int)
	place = func(n *showplan.Node, depth, top int) (int, int) {
		x := depth * (graphTileW + graphHGap)
		if len(n.Children) == 0 {
			r := core.Rect{X: x, Y: top, W: graphTileW, H: graphTileH}
			g.tiles = append(g.tiles, tile{node: n, rect: r})
			g.rects[n.ID] = r
			return top + graphTileH, top
		}
		cursor := top
		firstY, lastY := 0, 0
		for i, c := range n.Children {
			cBottom, cTileY := place(c, depth+1, cursor)
			if i == 0 {
				firstY = cTileY
			}
			lastY = cTileY
			cursor = cBottom + graphVGap
		}
		selfY := (firstY + lastY) / 2
		r := core.Rect{X: x, Y: selfY, W: graphTileW, H: graphTileH}
		g.tiles = append(g.tiles, tile{node: n, rect: r})
		g.rects[n.ID] = r
		return cursor - graphVGap, selfY
	}
	bottom, _ := place(root, 0, 0)
	g.canvasH = bottom

	for _, t := range g.tiles {
		if right := t.rect.Right(); right > g.canvasW {
			g.canvasW = right
		}
		for _, c := range t.node.Children {
			cr := g.rects[c.ID]
			midX := t.rect.Right() + (cr.X-t.rect.Right())/2
			g.edges = append(g.edges, edge{
				x1: t.rect.Right(), y1: t.rect.Y + graphTileH/2,
				x2: cr.X, y2: cr.Y + graphTileH/2,
				midX: midX,
			})
		}
	}
	return g
}
