package planview

import (
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// Tile geometry, in virtual canvas cells: a fixed-width "operator card".
const (
	graphTileW = 20
	// graphTileH leaves room for 3 interior text lines (PhysicalOp,
	// object/LogicalOp, cost%+rows): 5 rows, since Rect.Inner(1) on 4 yields
	// 2 interior rows and the third line would overwrite the border.
	graphTileH = 5
	// graphLiveTileH adds a fourth interior line in live mode (see live.go):
	// the "rows of estimate (pct)" figure, without giving up the object name.
	graphLiveTileH = 6
	graphHGap      = 4 // horizontal gap between a tile and its children's column
	graphVGap      = 1 // vertical gap between sibling tiles
)

// tile is one operator's placed position on the virtual canvas.
type tile struct {
	node *showplan.Node
	rect core.Rect
}

// edge is one parent→child connector in three straight segments: horizontal
// from the parent's right edge to midX, vertical at midX between the two rows,
// horizontal from midX to the child's left edge (virtual-canvas cells).
type edge struct {
	x1, y1 int // parent tile's right-edge midpoint
	x2, y2 int // child tile's left-edge midpoint
	midX   int // the connector's vertical trunk column
}

// graphLayout is one statement's operator tree laid out left to right, root at
// the smallest X, as SSMS does.
type graphLayout struct {
	tiles   []tile
	edges   []edge
	rects   map[int]core.Rect // NodeID -> placed rect, for hit-testing/navigation
	canvasW int
	canvasH int
}

// layoutGraph places root's operator tree on a virtual canvas. A pure function
// of the tree shape (no tcell/theme), so testable without a screen. Each tile
// is top-aligned with its first child's, as SSMS does, so the root lands on
// the first row. A childless node occupies one band of tileH rows.
func layoutGraph(root *showplan.Node, tileH int) *graphLayout {
	g := &graphLayout{rects: make(map[int]core.Rect)}
	if root == nil {
		return g
	}

	var place func(n *showplan.Node, depth, top int) (bandBottom, tileY int)
	place = func(n *showplan.Node, depth, top int) (int, int) {
		x := depth * (graphTileW + graphHGap)
		if len(n.Children) == 0 {
			r := core.Rect{X: x, Y: top, W: graphTileW, H: tileH}
			g.tiles = append(g.tiles, tile{node: n, rect: r})
			g.rects[n.ID] = r
			return top + tileH, top
		}
		cursor := top
		selfY := 0
		for i, c := range n.Children {
			cBottom, cTileY := place(c, depth+1, cursor)
			if i == 0 {
				selfY = cTileY
			}
			cursor = cBottom + graphVGap
		}
		r := core.Rect{X: x, Y: selfY, W: graphTileW, H: tileH}
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
				x1: t.rect.Right(), y1: t.rect.Y + tileH/2,
				x2: cr.X, y2: cr.Y + tileH/2,
				midX: midX,
			})
		}
	}
	return g
}
