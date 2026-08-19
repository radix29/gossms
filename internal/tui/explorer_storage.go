package tui

import (
	"fmt"

	gosmo "github.com/radix29/gosmo"
)

// explorer_storage.go backs a database's Storage folder — the partition
// functions and schemes SSMS files there.

// loadStorageChildren returns the Storage folder's own subfolders.
func loadStorageChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Partition Functions", NodePartitionFunctions, "", "", node.data.DBName),
		l.node("Partition Schemes", NodePartitionSchemes, "", "", node.data.DBName),
	}, nil
}

// loadPartitionFunctionsChildren lists a database's partition functions,
// labelled the way SSMS does: the name plus its boundary count.
func loadPartitionFunctionsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.PartitionFunction, error) { return dbObj.PartitionFunctionsContext(l.ctx) },
		func(pf *gosmo.PartitionFunction) *explorerNode {
			label := fmt.Sprintf("%s (%s, %d boundaries)", pf.Name, pf.InputType, pf.BoundaryCount)
			return l.node(label, NodePartitionFunction, "", pf.Name, node.data.DBName)
		})
}

// loadPartitionSchemesChildren lists a database's partition schemes, each
// labelled with the function it partitions by.
func loadPartitionSchemesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.PartitionScheme, error) { return dbObj.PartitionSchemesContext(l.ctx) },
		func(ps *gosmo.PartitionScheme) *explorerNode {
			return l.node(fmt.Sprintf("%s (%s)", ps.Name, ps.FunctionName), NodePartitionScheme, "", ps.Name, node.data.DBName)
		})
}
