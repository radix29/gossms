package controls

import (
	"fmt"
	"testing"
)

// Object Explorer hands SetNodes the whole flattened forest on every expand,
// collapse, load and refresh, and SetNodes measures every label. A 50,000-table
// folder open is the worst case it has to stay under a frame (16 ms) for.
func BenchmarkTreeViewSetNodes50k(b *testing.B) {
	nodes := make([]TreeNode, 50_000)
	for i := range nodes {
		nodes[i] = TreeNode{ID: i + 1, Label: fmt.Sprintf("dbo.SalesOrderDetail_%05d", i), Icon: '▦', Depth: 4}
	}
	tv := NewTreeView()
	tv.SetBounds(0, 0, 60, 40)
	tv.SetNodes(nodes)
	tv.SelectID(nodes[len(nodes)-1].ID) // indexOf's worst case: the last node
	for b.Loop() {
		tv.SetNodes(nodes)
	}
}
